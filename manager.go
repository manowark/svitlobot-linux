package main

import (
	"context"
	"net"
	"net/http"
	"reflect"
	"sort"
	"sync"
	"time"
)

// Manager володіє setом гонок-ранерів по одному на хост і спільним
// HTTP-клієнтом. Він же реалізує hot-reload конфіга.
type Manager struct {
	log *Logger

	mu        sync.Mutex
	ctx       context.Context
	httpTO    int
	dialer    *resilientDialer
	client    *http.Client
	runners   map[string]*runner
	cancels   map[string]context.CancelFunc
	wg        sync.WaitGroup
	stopping  bool
	startedAt time.Time
}

func newManager(cfg *Config, log *Logger) *Manager {
	// dialer спільний на все життя процесу: кеш IP переживає hot-reload
	// клієнта при зміні http_timeout_seconds.
	dl := newResilientDialer(log)
	m := &Manager{
		log:       log,
		dialer:    dl,
		runners:   map[string]*runner{},
		cancels:   map[string]context.CancelFunc{},
		startedAt: time.Now(),
	}
	m.httpTO = cfg.HTTPTimeoutSeconds
	m.client = m.newClient(cfg.HTTPTimeoutSeconds)
	return m
}

// resilientDialer — обгортка навколо net.Dialer, стійка до зависання
// системного/Docker-резолвера (127.0.0.11 у контейнерах вміє заклинювати,
// коли зовнішній DNS недоступний — ретраї тоді не допомагають, бо кожен
// знову проходить змоглий резолвер).
//
// Стратегія: успішний dial запам'ятовує останній робочий IP хоста; коли
// звичайний dial провалився (DNS або конект), робиться друга спроба
// напряму на запам'ятований IP. TLS SNI і валідація сертифіката лишаються
// по hostname незалежно від того, куди йде TCP — це поведінка http.Transport.
type resilientDialer struct {
	log  *Logger
	mu   sync.Mutex
	good map[string]string // hostname -> ip
}

func newResilientDialer(log *Logger) *resilientDialer {
	return &resilientDialer{log: log, good: map[string]string{}}
}

func (d *resilientDialer) remember(host string, conn net.Conn) {
	if ra := conn.RemoteAddr(); ra != nil {
		if host_, _, err := net.SplitHostPort(ra.String()); err == nil && net.ParseIP(host_) != nil {
			d.mu.Lock()
			d.good[host] = host_
			d.mu.Unlock()
		}
	}
}

func (d *resilientDialer) cached(host string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.good[host]
}

func (d *resilientDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) != nil {
		return (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, network, address)
	}
	nd := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	conn, derr := nd.DialContext(ctx, network, address)
	if derr == nil {
		d.remember(host, conn)
		return conn, nil
	}
	if ctx.Err() != nil {
		return nil, derr // процес стопається — не обходимо проблему
	}
	if ip := d.cached(host); ip != "" {
		d.log.Warnf("dial: DNS/connect to %q failed (%v), trying cached IP %s", host, derr, ip)
		if conn2, err2 := nd.DialContext(ctx, network, net.JoinHostPort(ip, port)); err2 == nil {
			return conn2, nil
		}
	}
	return nil, derr
}

func (m *Manager) newClient(to int) *http.Client {
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           m.dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          20,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       70 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{Timeout: time.Duration(to) * time.Second, Transport: tr}
}

func (m *Manager) getHTTPClient() *http.Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.client
}

func (m *Manager) Start(ctx context.Context, cfg *Config) {
	m.mu.Lock()
	m.ctx = ctx
	m.mu.Unlock()
	m.Apply(cfg)
}

// Apply застосовує новий конфіг: додає хости, знімає видалені,
// оновлює конфігурацію trajалих (стан і пінгер ранера зберігаються).
func (m *Manager) Apply(cfg *Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopping || m.ctx == nil {
		return
	}

	if cfg.HTTPTimeoutSeconds != m.httpTO {
		m.httpTO = cfg.HTTPTimeoutSeconds
		old := m.client
		m.client = m.newClient(m.httpTO)
		go old.CloseIdleConnections()
	}

	desired := map[string]*HostConfig{}
	for i := range cfg.Hosts {
		desired[cfg.Hosts[i].Name] = &cfg.Hosts[i]
	}

	for name := range m.runners {
		if _, ok := desired[name]; !ok {
			m.log.Infof("stopping host %q", name)
			if c := m.cancels[name]; c != nil {
				c()
			}
			delete(m.runners, name)
			delete(m.cancels, name)
		}
	}

	for name, h := range desired {
		if r, ok := m.runners[name]; ok {
			if !reflect.DeepEqual(r.current(), h) {
				r.setConfig(h)
				m.log.Infof("updated host %q (target %s, type %s, every %ds)", name, h.Target, h.Type, h.IntervalSeconds)
			}
			continue
		}
		rctx, cancel := context.WithCancel(m.ctx)
		r := newRunner(m, h)
		m.runners[name] = r
		m.cancels[name] = cancel
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			defer cancel()
			r.run(rctx)
		}()
		notify := "disabled"
		if h.URL != "" {
			notify = "enabled"
		}
		m.log.Infof("started host %q (target %s, type %s, every %ds, %d pings/burst, need %d ok, notify %s)",
			name, h.Target, h.Type, h.IntervalSeconds, h.Attempts, h.MinSuccesses, notify)
	}
}

// Snapshot — стан усіх хостів для веб-сторінки статуса.
func (m *Manager) Snapshot() GlobalState {
	m.mu.Lock()
	names := make([]string, 0, len(m.runners))
	for n := range m.runners {
		names = append(names, n)
	}
	started := m.startedAt
	runnersByID := make(map[string]*runner, len(m.runners))
	for n, r := range m.runners {
		runnersByID[n] = r
	}
	m.mu.Unlock()

	sort.Strings(names)
	g := GlobalState{
		Version:       version,
		UptimeSeconds: int64(time.Since(started).Seconds()),
		StartedAt:     fmtTime(started),
		Hosts:         make([]HostState, 0, len(names)),
	}
	for _, n := range names {
		g.Hosts = append(g.Hosts, runnersByID[n].snapshot())
	}
	return g
}

type GlobalState struct {
	Version       string      `json:"version"`
	StartedAt     string      `json:"started_at"`
	UptimeSeconds int64       `json:"uptime_seconds"`
	Hosts         []HostState `json:"hosts"`
}

func (m *Manager) Close() {
	m.mu.Lock()
	if m.stopping {
		m.mu.Unlock()
		return
	}
	m.stopping = true
	for _, c := range m.cancels {
		c()
	}
	client := m.client
	wg := &m.wg
	m.mu.Unlock()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
	}
	if client != nil {
		client.CloseIdleConnections()
	}
}
