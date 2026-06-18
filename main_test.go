package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLogWriterReopenIsObservedByCallers verifies that callers fetching the
// logger via logWriter.Logger() after a reopen() see writes land in the new
// file, not in the closed fd. This is the bug surfaced in code review:
// previously the access logger was captured once at startup and never
// refreshed after SIGUSR1.
func TestLogWriterReopenIsObservedByCallers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")

	lw, err := newLogWriter(path)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a long-lived caller that always fetches the current logger.
	getLogger := lw.Logger

	// Pre-rotation write.
	getLogger().Info("before")

	// Rotate: move the original file aside (as logrotate does) and reopen.
	rotated := path + ".1"
	if err := os.Rename(path, rotated); err != nil {
		t.Fatal(err)
	}
	if err := lw.reopen(); err != nil {
		t.Fatal(err)
	}

	// Post-rotation write must land in the NEW file.
	getLogger().Info("after")

	newContents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(newContents), "after") {
		t.Errorf("post-rotation write missing from new log file; contents=%q", newContents)
	}
	if strings.Contains(string(newContents), "before") {
		t.Errorf("pre-rotation write leaked into new log file; contents=%q", newContents)
	}

	oldContents, err := os.ReadFile(rotated)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(oldContents), "before") {
		t.Errorf("pre-rotation write missing from rotated file; contents=%q", oldContents)
	}
}

// TestAccessLogRotationVisibleToServer is the end-to-end version of the bug:
// configure a server with a file-backed access log, serve a request, rotate,
// serve another request, and verify the second access-log entry is in the
// new file.
func TestAccessLogRotationVisibleToServer(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")
	lw, err := newLogWriter(logPath)
	if err != nil {
		t.Fatal(err)
	}

	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(fake.Close)

	cfg := &Config{StorageBackends: []Backend{{URL: fake.URL}}}
	st, err := newStorage(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := &server{cfg: cfg, storage: st, accessLog: lw.Logger}
	h := srv.routes()

	get(t, h, "/artifact/"+testHash, "zstd")

	rotated := logPath + ".1"
	if err := os.Rename(logPath, rotated); err != nil {
		t.Fatal(err)
	}
	if err := lw.reopen(); err != nil {
		t.Fatal(err)
	}

	get(t, h, "/registries", "")

	newContents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(newContents), "/registries") {
		t.Errorf("post-rotation request not in new access log; contents=%q", newContents)
	}
	if strings.Contains(string(newContents), "/artifact/") {
		t.Errorf("pre-rotation request leaked into new access log; contents=%q", newContents)
	}
}
