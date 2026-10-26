package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
)

// Config — кореневий конфіг.
type Config struct {
	IntervalSeconds    int          `json:"interval_seconds"`
	Attempts           int          `json:"attempts"`
	AttemptIntervalMs  int          `json:"attempt_interval_ms"`
	PingTimeoutMs      int          `json:"ping_timeout_ms"`
	MinSuccesses       int          `json:"min_successes"`
	DownThreshold      int          `json:"down_threshold"`
	HTTPTimeoutSeconds int          `json:"http_timeout_seconds"`
	StatusLogSeconds   int          `json:"status_log_seconds"`
	WebListen          string       `json:"web_listen"`
	LogFile            string       `json:"log_file"`
	LogMaxMB           int          `json:"log_max_size_mb"`
	LogBackups         int          `json:"log_max_backups"`
	Hosts              []HostConfig `json:"hosts"`
}

// HostConfig — конфіг одного хоста. Порожні (нульові) поля
// успадковуються від глобальних значень.
type HostConfig struct {
	Name               string `json:"name"`
	Target             string `json:"target"`
	Type               string `json:"type"`
	Port               int    `json:"port"`
	URL                string `json:"url"`
	Mode               string `json:"mode"`
	IntervalSeconds    int    `json:"interval_seconds"`
	Attempts           int    `json:"attempts"`
	AttemptIntervalMs  int    `json:"attempt_interval_ms"`
	PingTimeoutMs      int    `json:"ping_timeout_ms"`
	MinSuccesses       int    `json:"min_successes"`
	DownThreshold      int    `json:"down_threshold"`
	HTTPTimeoutSeconds int    `json:"http_timeout_seconds"`
	StatusLogSeconds   int    `json:"status_log_seconds"`
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := &Config{}
	if len(data) > 0 {
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse config %s: %w", path, err)
		}
	}
	if err := resolveConfig(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// defaultIfZero записує v у p, якщо p ще не задана (<=0).
func defaultIfZero(p *int, v int) {
	if *p <= 0 {
		*p = v
	}
}

// resolveConfig застосовує значення за замовчуванням і валідує конфіг.
func resolveConfig(cfg *Config) error {
	defaultIfZero(&cfg.IntervalSeconds, 50) // серцебиття кожні 50 с
	defaultIfZero(&cfg.Attempts, 4)
	defaultIfZero(&cfg.AttemptIntervalMs, 700)
	defaultIfZero(&cfg.PingTimeoutMs, 1000)
	defaultIfZero(&cfg.MinSuccesses, 2)
	defaultIfZero(&cfg.DownThreshold, 3) // анти-флап: не обривати серцебиття за один зрив мережі
	defaultIfZero(&cfg.HTTPTimeoutSeconds, 15)
	defaultIfZero(&cfg.StatusLogSeconds, 600)
	defaultIfZero(&cfg.LogMaxMB, 10)
	if cfg.LogBackups < 0 {
		cfg.LogBackups = 0
	}
	if cfg.WebListen == "" {
		cfg.WebListen = ":8080"
	}

	if len(cfg.Hosts) == 0 {
		return fmt.Errorf("config error: no hosts configured")
	}

	seen := map[string]bool{}
	for i := range cfg.Hosts {
		if err := resolveHost(&cfg.Hosts[i], i, cfg, seen); err != nil {
			return err
		}
	}
	return nil
}

// resolveHost застосовує дефолти і валідує один хост.
// Глобальні значення використані як дефолти для порожніх полів.
func resolveHost(h *HostConfig, idx int, cfg *Config, seen map[string]bool) error {
	defaultIfZero(&h.IntervalSeconds, cfg.IntervalSeconds)
	defaultIfZero(&h.Attempts, cfg.Attempts)
	defaultIfZero(&h.AttemptIntervalMs, cfg.AttemptIntervalMs)
	defaultIfZero(&h.PingTimeoutMs, cfg.PingTimeoutMs)
	defaultIfZero(&h.MinSuccesses, cfg.MinSuccesses)
	defaultIfZero(&h.DownThreshold, cfg.DownThreshold)
	defaultIfZero(&h.HTTPTimeoutSeconds, cfg.HTTPTimeoutSeconds)
	defaultIfZero(&h.StatusLogSeconds, cfg.StatusLogSeconds)

	if h.Name == "" {
		h.Name = fmt.Sprintf("host-%d", idx+1)
	}
	if seen[h.Name] {
		return fmt.Errorf("config error: duplicate host name %q", h.Name)
	}
	seen[h.Name] = true

	if h.Target == "" {
		return fmt.Errorf("config error: host %q: missing target", h.Name)
	}
	switch h.Type {
	case "":
		h.Type = "icmp"
	case "icmp", "tcp":
	default:
		return fmt.Errorf("config error: host %q: unknown type %q (allowed: icmp|tcp)", h.Name, h.Type)
	}
	if h.Type == "tcp" && (h.Port < 1 || h.Port > 65535) {
		return fmt.Errorf("config error: host %q: tcp type requires a valid port 1-65535", h.Name)
	}
	switch h.Mode {
	case "":
		// дефолт для SvitloBot: періодичне серцебиття, поки хост UP
		h.Mode = "always"
	case "edge", "always":
	default:
		return fmt.Errorf("config error: host %q: unknown mode %q (allowed: edge|always)", h.Name, h.Mode)
	}
	if h.Attempts < 1 || h.Attempts > 20 {
		return fmt.Errorf("config error: host %q: attempts must be 1..20", h.Name)
	}
	if h.MinSuccesses < 1 || h.MinSuccesses > h.Attempts {
		return fmt.Errorf("config error: host %q: min_successes must be 1..attempts (%d)", h.Name, h.Attempts)
	}
	if h.DownThreshold < 1 || h.DownThreshold > 60 {
		return fmt.Errorf("config error: host %q: down_threshold must be 1..60", h.Name)
	}
	if h.URL != "" {
		if u, err := url.Parse(h.URL); err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("config error: host %q: url must be a valid http(s) URL", h.Name)
		}
	}
	return nil
}
