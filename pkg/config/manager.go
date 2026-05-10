package config

import (
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/loki/gpu-operator-runtime/pkg/serverless"
	"gopkg.in/yaml.v3"
)

var defaultBlockedEgressCIDRs = []string{
	"10.0.0.0/8",
	"100.64.0.0/10",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.168.0.0/16",
}

// ManagerConfig captures the local process settings for the shared runtime manager.
type ManagerConfig struct {
	MetricsBindAddress     string                `yaml:"metricsBindAddress"`
	HealthProbeBindAddress string                `yaml:"healthProbeBindAddress"`
	HTTPAddr               string                `yaml:"httpAddr"`
	ReportInterval         string                `yaml:"reportInterval"`
	LeaderElect            bool                  `yaml:"leaderElect"`
	MetricsSecure          bool                  `yaml:"metricsSecure"`
	EnableHTTP2            bool                  `yaml:"enableHTTP2"`
	BlockedEgressCIDRs     []string              `yaml:"blockedEgressCIDRs"`
	NvidiaMetricsEndpoint  string                `yaml:"nvidiaMetricsEndpoint"`
	Serverless             serverless.NATSConfig `yaml:"serverless"`
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
		BlockedEgressCIDRs:     append([]string(nil), defaultBlockedEgressCIDRs...),
		Serverless:             serverless.DefaultNATSConfig(),
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

// NormalizedBlockedEgressCIDRs validates and de-duplicates the configured egress blocklist.
func (c ManagerConfig) NormalizedBlockedEgressCIDRs() ([]string, error) {
	if len(c.BlockedEgressCIDRs) == 0 {
		return nil, nil
	}

	out := make([]string, 0, len(c.BlockedEgressCIDRs))
	seen := map[string]struct{}{}
	for _, value := range c.BlockedEgressCIDRs {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(trimmed); err != nil {
			return nil, fmt.Errorf("parse blockedEgressCIDRs entry %q: %w", trimmed, err)
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return out, nil
}
