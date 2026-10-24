package main

import (
	"context"
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
	client    *http.Client
	runners   map[string]*runner
	cancels   map[string]context.CancelFunc
	wg        sync.WaitGroup
	stopping  bool
	startedAt time.Time
}

func newManager(cfg *Config, log *Logger) *Manager {
	m := &Manager{
		log:       log,
		runners:   map[string]*runner{},
		cancels:   map[string]context.CancelFunc{},
		startedAt: time.Now(),
	}
	m.httpTO = cfg.HTTPTimeoutSeconds
	m.client = m.newClient(cfg.HTTPTimeoutSeconds)
	return m
}

func (m *Manager) newClient(to int) *http.Client {
	return &http.Client{Timeout: time.Duration(to) * time.Second}
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
