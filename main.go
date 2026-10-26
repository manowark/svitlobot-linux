package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

// version — за замовчуванням "dev"; одинира білд-системи (Makefile,
// Dockerfile з ARG VERSION, GitHub Actions) інжектить фактичну версію через
// -ldflags "-X main.version=<тег релізу>".
var version = "dev"

func main() {
	showVer := flag.Bool("version", false, "print version and exit")
	cfgPath := flag.String("config", "", "path to config file (default: /etc/svitlobot/config.json, fallback: config.json next to the binary)")
	flag.Parse()

	if *showVer {
		fmt.Println("svitlobot", version)
		return
	}

	path, err := resolveConfigPath(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}

	cfg, err := loadConfig(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}

	logr, err := newLogger(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}

	logr.Infof("svitlobot %s starting (config: %s)", version, path)

	m := newManager(cfg, logr)
	defer m.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	m.Start(ctx, cfg)

	ws := newWebServer(logr, m, cfg.WebListen)
	ws.Start()
	defer ws.Close()

	for {
		select {
		case s := <-sigs:
			switch s {
			case syscall.SIGHUP:
				nc, err := loadConfig(path)
				if err != nil {
					logr.Errorf("config reload failed: %v (keeping previous config)", err)
					continue
				}
				logr.Infof("config reloaded: %d host(s)", len(nc.Hosts))
				m.Apply(nc)
				ws.Apply(nc.WebListen)
			case syscall.SIGINT, syscall.SIGTERM:
				logr.Infof("shutting down (%s)", s)
				cancel()
				m.Close()
				return
			}
		case <-ctx.Done():
			m.Close()
			return
		}
	}
}

func resolveConfigPath(flagVal string) (string, error) {
	if flagVal != "" {
		return flagVal, nil
	}
	candidates := []string{"/etc/svitlobot/config.json"}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "config.json"))
	}
	candidates = append(candidates, "config.json")
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("config not found: pass -config /path/to/config.json")
}
