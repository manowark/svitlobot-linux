package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
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
	if len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse config %s: %w", path, err)
		}
	}
	if err := resolveConfig(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// resolveConfig застосовує значення за замовчуванням і валідує конфіг.
func resolveConfig(cfg *Config) error {
	if cfg.IntervalSeconds <= 0 {
		// ESP32-прошивка шле серцебиття кожні 50 с — та сама логіка тут
		cfg.IntervalSeconds = 50
	}
	if cfg.Attempts <= 0 {
		cfg.Attempts = 4
	}
	if cfg.AttemptIntervalMs <= 0 {
		cfg.AttemptIntervalMs = 700
	}
	if cfg.PingTimeoutMs <= 0 {
		cfg.PingTimeoutMs = 1000
	}
	if cfg.MinSuccesses <= 0 {
		cfg.MinSuccesses = 2
	}
	if cfg.DownThreshold <= 0 {
		// скільки циклів поспіль без жодного пінга вважаємо таргет ще живим
		// (анти-флап: мережеві короткострокові "планові" збої не обривають
		// серцебиття миттєво)
		cfg.DownThreshold = 3
	}
	if cfg.WebListen == "" {
		cfg.WebListen = ":8080"
	}
	if cfg.HTTPTimeoutSeconds <= 0 {
		cfg.HTTPTimeoutSeconds = 15
	}
	if cfg.StatusLogSeconds <= 0 {
		cfg.StatusLogSeconds = 600
	}
	if cfg.LogMaxMB <= 0 {
		cfg.LogMaxMB = 10
	}
	if cfg.LogBackups < 0 {
		cfg.LogBackups = 0
	}

	if len(cfg.Hosts) == 0 {
		return fmt.Errorf("config error: no hosts configured")
	}

	seen := map[string]bool{}
	for i := range cfg.Hosts {
		h := &cfg.Hosts[i]
		if h.Name == "" {
			h.Name = fmt.Sprintf("host-%d", i+1)
		}
		if seen[h.Name] {
			return fmt.Errorf("config error: duplicate host name %q", h.Name)
		}
		seen[h.Name] = true
		if strings.TrimSpace(h.Target) == "" {
			return fmt.Errorf("config error: host %q: missing target", h.Name)
		}
		switch h.Type {
		case "":
			h.Type = "icmp"
		case "icmp", "tcp":
			// ok
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
			// ok
		default:
			return fmt.Errorf("config error: host %q: unknown mode %q (allowed: edge|always)", h.Name, h.Mode)
		}
		if h.IntervalSeconds <= 0 {
			h.IntervalSeconds = cfg.IntervalSeconds
		}
		if h.Attempts <= 0 {
			h.Attempts = cfg.Attempts
		}
		if h.Attempts < 1 || h.Attempts > 20 {
			return fmt.Errorf("config error: host %q: attempts must be 1..20", h.Name)
		}
		if h.AttemptIntervalMs <= 0 {
			h.AttemptIntervalMs = cfg.AttemptIntervalMs
		}
		if h.PingTimeoutMs <= 0 {
			h.PingTimeoutMs = cfg.PingTimeoutMs
		}
		if h.MinSuccesses <= 0 {
			h.MinSuccesses = cfg.MinSuccesses
		}
		if h.MinSuccesses < 1 || h.MinSuccesses > h.Attempts {
			return fmt.Errorf("config error: host %q: min_successes must be 1..attempts (%d)", h.Name, h.Attempts)
		}
		if h.DownThreshold <= 0 {
			h.DownThreshold = cfg.DownThreshold
		}
		if h.DownThreshold < 1 || h.DownThreshold > 60 {
			return fmt.Errorf("config error: host %q: down_threshold must be 1..60", h.Name)
		}
		if h.HTTPTimeoutSeconds <= 0 {
			h.HTTPTimeoutSeconds = cfg.HTTPTimeoutSeconds
		}
		if h.StatusLogSeconds <= 0 {
			h.StatusLogSeconds = cfg.StatusLogSeconds
		}
		if h.URL != "" {
			u, err := url.Parse(h.URL)
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
				return fmt.Errorf("config error: host %q: url must be a valid http(s) URL", h.Name)
			}
		}
	}
	return nil
}
