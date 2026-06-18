package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

var logLevel slog.LevelVar // defaults to INFO

// logWriter manages a log file and can reopen it after rotation.
// Logger() always returns the current logger, which is replaced atomically
// when the file is reopened — callers must call Logger() per use, not cache
// the returned *slog.Logger across rotations.
type logWriter struct {
	path   string
	file   atomic.Pointer[os.File]
	logger atomic.Pointer[slog.Logger]
}

func newLogWriter(path string) (*logWriter, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0640)
	if err != nil {
		return nil, err
	}
	lw := &logWriter{path: path}
	lw.file.Store(f)
	lw.logger.Store(slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: &logLevel})))
	return lw, nil
}

func (lw *logWriter) Logger() *slog.Logger {
	return lw.logger.Load()
}

func (lw *logWriter) reopen() error {
	f, err := os.OpenFile(lw.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0640)
	if err != nil {
		return err
	}
	lw.logger.Store(slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: &logLevel})))
	old := lw.file.Swap(f)
	old.Close()
	return nil
}

func newStderrLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: &logLevel}))
}

func main() {
	cfgPath := flag.String("config", "config.toml", "path to config file")
	debug := flag.Bool("debug", false, "enable debug logging")
	flag.Parse()

	if *debug {
		logLevel.Set(slog.LevelDebug)
	}

	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}

	// Set up server log (default logger).
	var serverLog *logWriter
	if cfg.LogFile != "" {
		serverLog, err = newLogWriter(cfg.LogFile)
		if err != nil {
			slog.Error("open log file", "err", err)
			os.Exit(1)
		}
		slog.SetDefault(serverLog.Logger())
	} else {
		slog.SetDefault(newStderrLogger())
	}

	// Set up access log.
	stderrFallback := newStderrLogger()
	getAccessLog := func() *slog.Logger { return stderrFallback }
	var accessLog *logWriter
	if cfg.AccessLogFile != "" {
		accessLog, err = newLogWriter(cfg.AccessLogFile)
		if err != nil {
			slog.Error("open access log file", "err", err)
			os.Exit(1)
		}
		getAccessLog = accessLog.Logger
	}

	// Reopen both log files on SIGUSR1.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGUSR1)
	go func() {
		for range sigCh {
			if serverLog != nil {
				if err := serverLog.reopen(); err != nil {
					slog.Error("reopen log file", "err", err)
				} else {
					slog.SetDefault(serverLog.Logger())
				}
			}
			if accessLog != nil {
				if err := accessLog.reopen(); err != nil {
					slog.Error("reopen access log file", "err", err)
				}
			}
			slog.Info("log files reopened")
		}
	}()

	storage, err := newStorage(cfg)
	if err != nil {
		slog.Error("storage", "err", err)
		os.Exit(1)
	}

	srv := &server{cfg: cfg, storage: storage, accessLog: getAccessLog}

	httpSrv := &http.Server{
		Addr:              cfg.ServerAddr,
		Handler:           srv.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Shut down cleanly on SIGTERM/SIGINT so systemctl restart drains in-flight
	// requests instead of cutting them.
	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-stopCh
		slog.Info("shutting down", "signal", sig.String())
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(ctx); err != nil {
			slog.Error("shutdown", "err", err)
		}
	}()

	slog.Info("listening", "addr", cfg.ServerAddr)
	if err := httpSrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}
