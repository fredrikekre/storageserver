package main

import (
	"os"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "config*.toml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return f.Name()
}

func TestLoadConfigNoBackends(t *testing.T) {
	path := writeConfig(t, `server_addr = "0.0.0.0:8080"`)
	_, err := loadConfig(path)
	if err == nil {
		t.Error("expected error for config with no backends, got nil")
	}
}

func TestLoadConfigValid(t *testing.T) {
	path := writeConfig(t, `
server_addr = "0.0.0.0:9090"
[[storage_backends]]
url = "https://example.com"
`)
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ServerAddr != "0.0.0.0:9090" {
		t.Errorf("ServerAddr = %q, want %q", cfg.ServerAddr, "0.0.0.0:9090")
	}
	if len(cfg.StorageBackends) != 1 || cfg.StorageBackends[0].URL != "https://example.com" {
		t.Errorf("unexpected backends: %+v", cfg.StorageBackends)
	}
}

func TestLoadConfigTrimsTrailingSlash(t *testing.T) {
	path := writeConfig(t, `
[[storage_backends]]
url = "https://example.com/"
[[storage_backends]]
url = "https://example.org///"
`)
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := cfg.StorageBackends[0].URL, "https://example.com"; got != want {
		t.Errorf("backend[0] URL = %q, want %q", got, want)
	}
	if got, want := cfg.StorageBackends[1].URL, "https://example.org"; got != want {
		t.Errorf("backend[1] URL = %q, want %q", got, want)
	}
}

func TestLoadConfigEmptyBackendURL(t *testing.T) {
	path := writeConfig(t, `
[[storage_backends]]
url = ""
`)
	if _, err := loadConfig(path); err == nil {
		t.Error("expected error for empty backend URL, got nil")
	}
}
