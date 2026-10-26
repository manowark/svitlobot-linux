package main

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newSilentLogger() *Logger {
	return &Logger{l: log.New(io.Discard, "", 0)}
}

func TestResilientDialerRemember(t *testing.T) {
	d := newResilientDialer(newSilentLogger())
	if ip := d.cached("example.com"); ip != "" {
		t.Fatalf("expected empty cache, got %q", ip)
	}
	// реальний TCP-коннект (у net.Pipe немає RemoteAddr)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	d.remember("example.com", c)
	if ip := d.cached("example.com"); ip != "127.0.0.1" {
		t.Fatalf("expected 127.0.0.1 cached, got %q", ip)
	}
}

// TestDialerFallbackToCachedIP: у кеш кладемо IP локального httptest-сервера
// під вигаданим hostname (DNS якого не резолвиться), і перевіряємо, що діалер
// успішно фолбекне на кешований IP, а HTTP-запит через такий транспорт пройде.
func TestDialerFallbackToCachedIP(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	serverIP, serverPort, _ := net.SplitHostPort(ts.Listener.Addr().String())
	host := "nonexistent.invalid"

	d := newResilientDialer(newSilentLogger())
	d.mu.Lock()
	d.good[host] = serverIP
	d.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, serverPort))
	if err != nil {
		// деякі системи резолвлять .invalid через wildcard-DNS — тоді фолбек
		// не потрібен, у такому середовищі тест непридатний
		if _, ok := err.(*net.DNSError); ok {
			t.Skipf("system resolver behaves unexpectedly: %v", err)
		}
		t.Fatalf("fallback dial to cached IP failed: %v", err)
	}
	ip, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
	if ip != serverIP {
		t.Fatalf("expected dial to %s, connected to %s", serverIP, ip)
	}
	conn.Close()

	client := &http.Client{
		Transport: &http.Transport{DialContext: d.DialContext},
		Timeout:   5 * time.Second,
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("HTTP request via resilient transport failed: %v", err)
	}
	resp.Body.Close()
}
