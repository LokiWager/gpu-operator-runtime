package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadManagerConfigMergesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manager.yaml")
	if err := os.WriteFile(path, []byte("httpAddr: \":9090\"\nreportInterval: \"45s\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadManagerConfig(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.HTTPAddr != ":9090" {
		t.Fatalf("expected httpAddr :9090, got %s", cfg.HTTPAddr)
	}
	if cfg.MetricsBindAddress != "0" {
		t.Fatalf("expected default metrics bind address, got %s", cfg.MetricsBindAddress)
	}

	interval, err := cfg.ReportIntervalDuration()
	if err != nil {
		t.Fatalf("parse interval: %v", err)
	}
	if interval.String() != "45s" {
		t.Fatalf("expected 45s interval, got %s", interval)
	}
}
