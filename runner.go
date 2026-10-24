package main

import (
	"context"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

// runner — цикл одного хоста:
//   - кожні interval секунд провести "берст" із attempts пінгів
//   - хост вважається UP, якщо успіхів >= min_successes
//   - анти-флап: якщо берст провалився, хост залишається UP ще
//     down_threshold-1 невдалих циклів (мережеві короткострокові збої
//     не обривають серцебиття миттєво); після порога — DOWN
//   - поки хост UP (mode=always, дефолт для SvitloBot — серцебиття),
//     при кожному вдалому берсті шлється GET на URL;
//     mode=edge — один раз на переході DOWN->UP
//
// Усі змінні стану доступні з горутіни run() і (лише на читання)
// зі web-знімків — тому захищені mu.
// Конфіг оновлюється атомарно (SIGHUP reload).
type runner struct {
	man  *Manager
	log  *Logger
	name string
	cfg  atomic.Value // *HostConfig

	mu   sync.Mutex
	icmp *icmpClient

	up          bool // стан хоста (з урахуванням down_threshold grace)
	failBursts  int  // кількість підряд цілком невдалих берстів
	pending     bool // notify не доставлено, треба ретраїти
	lastErr     string
	lastOkTime  time.Time
	lastBurstOk int // скільки пінгів пройшло в останньому берсті

	lastPingAt     time.Time
	lastNotifyAt   time.Time
	lastNotifyCode int
	lastNotifyErr  string
	lastStatusAt   time.Time
	lastNotifyLog  string
	restartsSoFar  int
}

func newRunner(m *Manager, h *HostConfig) *runner {
	r := &runner{man: m, log: m.log, name: h.Name}
	r.cfg.Store(h)
	return r
}

func (r *runner) current() *HostConfig {
	if v, ok := r.cfg.Load().(*HostConfig); ok {
		return v
	}
	return nil
}

func (r *runner) setConfig(h *HostConfig) { r.cfg.Store(h) }

func (r *runner) run(ctx context.Context) {
	for {
		cfg := r.current()
		if cfg == nil {
			return
		}
		start := time.Now()
		r.cycle(ctx, cfg)
		if ctx.Err() != nil {
			r.closePinger()
			return
		}
		iv := time.Duration(cfg.IntervalSeconds) * time.Second
		if rest := iv - time.Since(start); rest > 0 {
			if !sleepCtx(ctx, rest) {
				r.closePinger()
				return
			}
		}
	}
}

func (r *runner) cycle(ctx context.Context, cfg *HostConfig) {
	succ := r.burst(ctx, cfg)
	now := time.Now()

	r.mu.Lock()
	r.lastPingAt = now
	r.lastBurstOk = succ
	if succ >= cfg.MinSuccesses {
		r.lastOkTime = now
	}
	r.mu.Unlock()

	// анти-флап: хост вважається зниклим тільки після
	// down_threshold цілком невдалих берстів поспіль
	okBurst := succ >= cfg.MinSuccesses
	if okBurst {
		r.mu.Lock()
		r.failBursts = 0
		r.lastErr = ""
		r.mu.Unlock()
	} else {
		r.mu.Lock()
		r.failBursts++
		th := r.failBursts
		r.mu.Unlock()
		if th >= 3 {
			// кілька циклів поспіль безуспішно — скидаємо сокет/DNS-кеш
			r.resetPinger()
		}
	}

	r.mu.Lock()
	grace := !okBurst && r.up && r.failBursts < cfg.DownThreshold
	up := okBurst || grace
	prev := r.up
	r.up = up
	r.mu.Unlock()

	switch {
	case up != prev && up:
		r.log.Infof("host %q is UP (%d/%d pings ok)", r.name, succ, cfg.Attempts)
	case up != prev && !up:
		r.log.Warnf("host %q is DOWN (no successful burst for %d cycles, down_threshold=%d)",
			r.name, r.failSnapshot(), cfg.DownThreshold)
	case grace && prev:
		r.log.Infof("status: host %q %d/%d pings ok, still UP due to down_threshold (failed %d/%d)",
			r.name, succ, cfg.Attempts, r.failSnapshot(), cfg.DownThreshold)
	}

	// статусний рядок, щоб бачити, що процес живий
	r.mu.Lock()
	doStatus := time.Since(r.lastStatusAt) >= time.Duration(cfg.StatusLogSeconds)*time.Second
	if doStatus {
		r.lastStatusAt = now
	}
	r.mu.Unlock()
	if doStatus {
		state := "DOWN"
		if grace {
			state = "UP(grace)"
		} else if up {
			state = "UP"
		}
		r.log.Infof("status: host %q %d/%d pings ok, state=%s", r.name, succ, cfg.Attempts, state)
	}

	if !up {
		return
	}

	need := false
	switch {
	case r.pendingSnap():
		need = true // попередня доставка не пройшла — ретраїмо
	case !prev:
		need = true // щойно став UP — ключова подія
	case cfg.Mode == "always":
		need = true // серцебиття
	}
	if need && cfg.URL != "" {
		r.deliver(ctx, cfg)
	}
}

func (r *runner) failSnapshot() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failBursts
}

func (r *runner) pendingSnap() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pending
}

// burst робить attempts пінгів з паузою attempt_interval_ms.
// Раній вихід, коли вже набрано min_successes.
func (r *runner) burst(ctx context.Context, cfg *HostConfig) int {
	ok := 0
	for i := 0; i < cfg.Attempts; i++ {
		if ctx.Err() != nil {
			return ok
		}
		if err := r.pingOne(ctx, cfg); err != nil {
			r.errOnce(err)
		} else {
			ok++
			if ok >= cfg.MinSuccesses {
				break
			}
		}
		if i < cfg.Attempts-1 {
			if !sleepCtx(ctx, time.Duration(cfg.AttemptIntervalMs)*time.Millisecond) {
				return ok
			}
		}
	}
	return ok
}

func (r *runner) pingOne(ctx context.Context, cfg *HostConfig) error {
	timeout := time.Duration(cfg.PingTimeoutMs) * time.Millisecond
	switch cfg.Type {
	case "tcp":
		return tcpPing(ctx, cfg.Target, cfg.Port, timeout)
	default:
		return r.icmpPing(ctx, cfg, timeout)
	}
}

func (r *runner) icmpPing(ctx context.Context, cfg *HostConfig, timeout time.Duration) error {
	r.mu.Lock()
	icmp := r.icmp
	r.mu.Unlock()
	if icmp == nil {
		icmp = newICMPClient(r.log, r.name)
		r.mu.Lock()
		r.icmp = icmp
		r.mu.Unlock()
	}
	return icmp.ping(ctx, cfg.Target, timeout)
}

func (r *runner) resetPinger() {
	r.mu.Lock()
	icmp := r.icmp
	r.mu.Unlock()
	if icmp != nil {
		// Reset зберігає вивчене: фолбек на системний ping — "липкий",
		// тому raw-сокет не пробується знову і лог не спамиться.
		icmp.Reset()
	}
}

func (r *runner) closePinger() {
	r.mu.Lock()
	icmp := r.icmp
	r.icmp = nil
	r.mu.Unlock()
	if icmp != nil {
		icmp.Close()
	}
}

// errOnce логує помилку пінгу тільки коли текст помилки змінився —
// без спам-логів при довгому даунтаймі.
func (r *runner) errOnce(err error) {
	s := err.Error()
	r.mu.Lock()
	if s != r.lastErr {
		r.lastErr = s
		r.mu.Unlock()
		r.log.Errorf("host %q: ping failed: %v", r.name, err)
		return
	}
	r.mu.Unlock()
}

func (r *runner) deliver(ctx context.Context, cfg *HostConfig) {
	sctx, cancel := context.WithTimeout(ctx, time.Duration(cfg.HTTPTimeoutSeconds)*time.Second)
	defer cancel()

	code, err := sendNotify(sctx, r.man.getHTTPClient(), cfg.URL)
	if err != nil {
		if ctx.Err() != nil {
			return // зупинка процесу — не логуємо як помилку
		}
		r.mu.Lock()
		r.pending = true
		firstTime := err.Error() != r.lastNotifyLog
		r.lastNotifyAt = time.Now()
		r.lastNotifyCode = 0
		r.lastNotifyErr = err.Error()
		if firstTime {
			r.lastNotifyLog = err.Error()
		}
		r.mu.Unlock()
		if firstTime {
			r.log.Warnf("host %q: notify failed: %v (will retry next cycle)", r.name, err)
		} else {
			r.log.Warnf("host %q: notify failed again: %v", r.name, err)
		}
		return
	}
	dst := ""
	if u, perr := url.Parse(cfg.URL); perr == nil {
		dst = u.Host
	}
	r.mu.Lock()
	r.pending = false
	r.lastNotifyLog = ""
	r.lastNotifyErr = ""
	r.lastNotifyAt = time.Now()
	r.lastNotifyCode = code
	r.mu.Unlock()
	r.log.Infof("host %q: notify delivered (HTTP %d) -> %s", r.name, code, dst)
}

// HostState — знімок стану хоста для веб-сторінки.
type HostState struct {
	Name          string `json:"name"`
	Target        string `json:"target"`
	Type          string `json:"type"`
	Mode          string `json:"mode"`
	URL           bool   `json:"has_url"`
	IntervalSec   int    `json:"interval_seconds"`
	Attempts      int    `json:"attempts"`
	MinSuccesses  int    `json:"min_successes"`
	DownThreshold int    `json:"down_threshold"`

	Up         bool `json:"up"`          // стан з урахуванням grace
	Grace      bool `json:"grace"`       // UP, але останні берсти невдалі
	FailBursts int  `json:"fail_bursts"` // підряд невдалих берстів

	OkInLastBurst  int    `json:"ok_in_last_burst"`
	LastPingAt     string `json:"last_ping_at"`
	LastOkAt       string `json:"last_ok_at"`
	LastNotifyAt   string `json:"last_notify_at"`
	LastNotifyCode int    `json:"last_notify_code"`
	LastNotifyErr  string `json:"last_notify_err"`
	NotifyPending  bool   `json:"notify_pending"`
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// snapshot бере знімок стану хоста.
func (r *runner) snapshot() HostState {
	cfg := r.current()
	if cfg == nil {
		return HostState{Name: r.name}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	grace := r.up && r.failBursts > 0
	return HostState{
		Name:           cfg.Name,
		Target:         cfg.Target,
		Type:           cfg.Type,
		Mode:           cfg.Mode,
		URL:            cfg.URL != "",
		IntervalSec:    cfg.IntervalSeconds,
		Attempts:       cfg.Attempts,
		MinSuccesses:   cfg.MinSuccesses,
		DownThreshold:  cfg.DownThreshold,
		Up:             r.up,
		Grace:          grace,
		FailBursts:     r.failBursts,
		OkInLastBurst:  r.lastBurstOk,
		LastPingAt:     fmtTime(r.lastPingAt),
		LastOkAt:       fmtTime(r.lastOkTime),
		LastNotifyAt:   fmtTime(r.lastNotifyAt),
		LastNotifyCode: r.lastNotifyCode,
		LastNotifyErr:  r.lastNotifyErr,
		NotifyPending:  r.pending,
	}
}

// sleepCtx спить d або до скасування ctx.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer func() {
		if !t.Stop() {
			select {
			case <-t.C:
			default:
			}
		}
	}()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
