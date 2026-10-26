package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"sync"
)

// Logger пише в stdout (для journald) і, за потреби, у файл з ротацією.
type Logger struct {
	l *log.Logger
}

func newLogger(cfg *Config) (*Logger, error) {
	writers := []io.Writer{os.Stdout}
	if cfg.LogFile != "" {
		rw, err := newRotatingWriter(cfg.LogFile, int64(cfg.LogMaxMB)*1024*1024, cfg.LogBackups)
		if err != nil {
			return nil, fmt.Errorf("log file: %w", err)
		}
		writers = append(writers, rw)
	}
	return &Logger{l: log.New(io.MultiWriter(writers...), "", log.LstdFlags)}, nil
}

func (lg *Logger) Infof(format string, args ...interface{})  { lg.l.Printf("INFO  "+format, args...) }
func (lg *Logger) Warnf(format string, args ...interface{})  { lg.l.Printf("WARN  "+format, args...) }
func (lg *Logger) Errorf(format string, args ...interface{}) { lg.l.Printf("ERROR "+format, args...) }

// rotatingWriter — простий файл-логер з ротацією за розміром:
// svitlobot.log -> svitlobot.log.1 -> svitlobot.log.2 ...
type rotatingWriter struct {
	mu      sync.Mutex
	path    string
	max     int64
	backups int
	f       *os.File
}

func newRotatingWriter(path string, max int64, backups int) (*rotatingWriter, error) {
	if max <= 0 {
		max = 10 * 1024 * 1024
	}
	if backups < 0 {
		backups = 0
	}
	w := &rotatingWriter{path: path, max: max, backups: backups}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *rotatingWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	w.f = f
	return nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		// з rotten-файлу не падаємо: лог це не критичний шлях
		return 0, fmt.Errorf("log file %q unavailable", w.path)
	}
	if st, err := w.f.Stat(); err == nil && st.Size()+int64(len(p)) > w.max {
		w.rotate()
	}
	return w.f.Write(p)
}

func (w *rotatingWriter) rotate() {
	if w.f != nil {
		w.f.Close()
		w.f = nil
	}
	if w.backups <= 0 {
		os.Remove(w.path)
	} else {
		for i := w.backups - 1; i >= 1; i-- {
			os.Rename(fmt.Sprintf("%s.%d", w.path, i), fmt.Sprintf("%s.%d", w.path, i+1))
		}
		os.Rename(w.path, w.path+".1")
	}
	if err := w.open(); err != nil {
		// останній шанс — не впасти через лог
		f, oerr := os.OpenFile(os.DevNull, os.O_WRONLY, 0644)
		if oerr == nil {
			w.f = f
		}
	}
}
