package main

import (
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	ServerAddr      string    `toml:"server_addr"`
	LogFile         string    `toml:"log_file"`
	AccessLogFile   string    `toml:"access_log_file"`
	StorageBackends []Backend `toml:"storage_backends"`
}

type Backend struct {
	URL string `toml:"url"`
}

func loadConfig(path string) (*Config, error) {
	cfg := &Config{ServerAddr: "127.0.0.1:8080"}
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, err
	}
	if len(cfg.StorageBackends) == 0 {
		return nil, fmt.Errorf("no storage backends configured")
	}
	for i, b := range cfg.StorageBackends {
		if b.URL == "" {
			return nil, fmt.Errorf("storage_backends[%d]: url is required", i)
		}
		cfg.StorageBackends[i].URL = strings.TrimRight(b.URL, "/")
	}
	return cfg, nil
}
