package main

import "testing"

func TestICMPChecksum(t *testing.T) {
	// пакет-приклад із RFC 1071 опису checksum (echo request)
	pkt := []byte{
		0x08, 0x00, 0x00, 0x00, // type 8, code 0, checksum порожній
		0x00, 0x01, 0x00, 0x01, // id, seq
		'a', 'b', 'c', 'd',
	}
	sum := icmpSum(pkt)
	if sum == 0 {
		t.Fatalf("checksum must not be 0 for this packet")
	}
	// перевірка: повторна сума пакету з вже вставленою чек-сумою = 0
	pkt[2], pkt[3] = byte(sum>>8), byte(sum)
	if icmpSum(pkt) != 0 {
		t.Fatalf("checksum verification failed")
	}
}

func TestResolveConfigDefaults(t *testing.T) {
	cfg := &Config{
		Hosts: []HostConfig{{Target: "192.168.1.1", URL: "http://example.com/?key=x"}},
	}
	if err := resolveConfig(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	h := cfg.Hosts[0]
	if h.Type != "icmp" || h.Mode != "always" || h.IntervalSeconds != 50 ||
		h.Attempts != 4 || h.MinSuccesses != 2 || h.PingTimeoutMs != 1000 ||
		h.DownThreshold != 3 || cfg.WebListen != ":8080" {
		t.Fatalf("defaults not applied: %+v global %+v", h, cfg)
	}
}

func TestResolveConfigValidation(t *testing.T) {
	if _, err := loadConfig("testdata/nonexistent.json"); err == nil {
		t.Fatalf("expected error for missing file")
	}
	// min_successes > attempts -> помилка
	cfg := &Config{Hosts: []HostConfig{{Target: "1.2.3.4", Attempts: 2, MinSuccesses: 3}}}
	if err := resolveConfig(cfg); err == nil {
		t.Fatalf("expected validation error")
	}
	// tcp без порту -> помилка
	cfg = &Config{Hosts: []HostConfig{{Target: "1.2.3.4", Type: "tcp"}}}
	if err := resolveConfig(cfg); err == nil {
		t.Fatalf("expected validation error for tcp without port")
	}
	// поганий URL -> помилка
	cfg = &Config{Hosts: []HostConfig{{Target: "1.2.3.4", URL: "notaurl"}}}
	if err := resolveConfig(cfg); err == nil {
		t.Fatalf("expected validation error for bad url")
	}
	// down_threshold поза діапазоном -> помилка
	cfg = &Config{Hosts: []HostConfig{{Target: "1.2.3.4", DownThreshold: 100}}}
	if err := resolveConfig(cfg); err == nil {
		t.Fatalf("expected validation error for down_threshold")
	}
	// дублікати імен -> помилка
	cfg = &Config{Hosts: []HostConfig{{Name: "a", Target: "1.2.3.4"}, {Name: "a", Target: "1.2.3.5"}}}
	if err := resolveConfig(cfg); err == nil {
		t.Fatalf("expected duplicate name error")
	}
}
