package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
)

const existsCacheSize = 100_000

type Storage struct {
	backends    []Backend
	existsCache *lru.Cache[string, struct{}]
	client      *http.Client
}

func newStorage(cfg *Config) (*Storage, error) {
	cache, err := lru.New[string, struct{}](existsCacheSize)
	if err != nil {
		return nil, err
	}
	return &Storage{
		backends:    cfg.StorageBackends,
		existsCache: cache,
		client:      &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func buildURL(b Backend, resourcePath, ext string) string {
	return fmt.Sprintf("%s/%s%s", b.URL, resourcePath, ext)
}

func (st *Storage) checkExists(ctx context.Context, url string) bool {
	if _, ok := st.existsCache.Get(url); ok {
		slog.Debug("cache hit", "url", url)
		return true
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return false
	}
	resp, err := st.client.Do(req)
	if err != nil {
		slog.Warn("backend probe failed", "url", url, "err", err)
		return false
	}
	resp.Body.Close()
	exists := resp.StatusCode == http.StatusOK
	slog.Info("backend probe", "url", url, "exists", exists)
	if exists {
		st.existsCache.Add(url, struct{}{})
	}
	return exists
}

// findURL returns the URL of the first backend that has resourcePath+ext, or "".
func (st *Storage) findURL(ctx context.Context, resourcePath, ext string) string {
	for _, b := range st.backends {
		url := buildURL(b, resourcePath, ext)
		if st.checkExists(ctx, url) {
			return url
		}
	}
	return ""
}
