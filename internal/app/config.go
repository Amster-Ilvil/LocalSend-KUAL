package app

import (
	"fmt"
	"os"
	"path/filepath"
)

func DefaultConfig(root string) Config {
	return Config{
		Alias:           "Kindle Voyage KUAL",
		Port:            DefaultPort,
		Encryption:      false,
		AutoAccept:      true,
		VerifyChecksums: true,
		ReceiveDir:      "/mnt/us/documents",
		OutboxDir:       "/mnt/us/LocalSend/Outbox",
		Interface:       "wlan0",
		AnnounceSeconds: 30,
		MaxPeers:        12,
	}
}

func LoadConfig(root string) (Config, error) {
	cfg := DefaultConfig(root)
	path := filepath.Join(root, "config", "settings.json")
	if err := readJSON(path, &cfg); err != nil {
		if !os.IsNotExist(err) {
			return cfg, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return cfg, err
		}
		if err := atomicWriteJSON(path, cfg, 0o644); err != nil {
			return cfg, err
		}
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		return cfg, fmt.Errorf("invalid port: %d", cfg.Port)
	}
	if cfg.Alias == "" {
		cfg.Alias = "Kindle Voyage KUAL"
	}
	if cfg.AnnounceSeconds < 5 {
		cfg.AnnounceSeconds = 30
	}
	if cfg.MaxPeers < 1 {
		cfg.MaxPeers = 12
	}
	if err := os.MkdirAll(cfg.ReceiveDir, 0o755); err != nil {
		// During host-side tests /mnt/us may not exist or be writable; callers
		// can override receive_dir in settings.json. Return the error because a
		// daemon that cannot write its destination should not pretend to run.
		return cfg, fmt.Errorf("create receive_dir: %w", err)
	}
	if err := os.MkdirAll(cfg.OutboxDir, 0o755); err != nil {
		return cfg, fmt.Errorf("create outbox_dir: %w", err)
	}
	return cfg, nil
}
