package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// ManagerConfig captures the local process settings for the shared runtime manager.
type ManagerConfig struct {
	MetricsBindAddress     string `yaml:"metricsBindAddress"`
	HealthProbeBindAddress string `yaml:"healthProbeBindAddress"`
	HTTPAddr               string `yaml:"httpAddr"`
	ReportInterval         string `yaml:"reportInterval"`
	LeaderElect            bool   `yaml:"leaderElect"`
	MetricsSecure          bool   `yaml:"metricsSecure"`
	EnableHTTP2            bool   `yaml:"enableHTTP2"`
}

// DefaultManagerConfig returns the baseline local development settings.
func DefaultManagerConfig() ManagerConfig {
	return ManagerConfig{
		MetricsBindAddress:     "0",
		HealthProbeBindAddress: ":8081",
		HTTPAddr:               ":8080",
		ReportInterval:         "30s",
		LeaderElect:            false,
		MetricsSecure:          true,
		EnableHTTP2:            false,
	}
}

// LoadManagerConfig loads a YAML file on top of the built-in defaults.
func LoadManagerConfig(path string) (ManagerConfig, error) {
	cfg := DefaultManagerConfig()
	if path == "" {
		return cfg, nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return ManagerConfig{}, fmt.Errorf("read manager config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		return ManagerConfig{}, fmt.Errorf("unmarshal manager config %s: %w", path, err)
	}
	return cfg, nil
}

// ReportIntervalDuration parses the configured report interval string.
func (c ManagerConfig) ReportIntervalDuration() (time.Duration, error) {
	interval, err := time.ParseDuration(c.ReportInterval)
	if err != nil {
		return 0, fmt.Errorf("parse reportInterval %q: %w", c.ReportInterval, err)
	}
	return interval, nil
}
