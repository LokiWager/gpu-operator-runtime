package config

import (
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/loki/gpu-operator-runtime/pkg/contract"
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

// ManagerConfig captures the legacy local process settings for the shared runtime manager.
type ManagerConfig struct {
	MetricsBindAddress     string                         `yaml:"metricsBindAddress"`
	HealthProbeBindAddress string                         `yaml:"healthProbeBindAddress"`
	HTTPAddr               string                         `yaml:"httpAddr"`
	ReportInterval         string                         `yaml:"reportInterval"`
	LeaderElect            bool                           `yaml:"leaderElect"`
	MetricsSecure          bool                           `yaml:"metricsSecure"`
	EnableHTTP2            bool                           `yaml:"enableHTTP2"`
	BlockedEgressCIDRs     []string                       `yaml:"blockedEgressCIDRs"`
	NvidiaMetricsEndpoint  string                         `yaml:"nvidiaMetricsEndpoint"`
	Serverless             serverless.NATSConfig          `yaml:"serverless"`
	ServerlessWorker       serverless.WorkerSidecarConfig `yaml:"serverlessWorker"`
}

// ControllerManagerConfig captures the reconciler process settings.
type ControllerManagerConfig struct {
	MetricsBindAddress     string                         `yaml:"metricsBindAddress"`
	HealthProbeBindAddress string                         `yaml:"healthProbeBindAddress"`
	LeaderElect            bool                           `yaml:"leaderElect"`
	MetricsSecure          bool                           `yaml:"metricsSecure"`
	EnableHTTP2            bool                           `yaml:"enableHTTP2"`
	BlockedEgressCIDRs     []string                       `yaml:"blockedEgressCIDRs"`
	Serverless             serverless.NATSConfig          `yaml:"serverless"`
	ServerlessWorker       serverless.WorkerSidecarConfig `yaml:"serverlessWorker"`
}

// RuntimeAPIConfig captures the HTTP API process settings.
type RuntimeAPIConfig struct {
	HTTPAddr              string                         `yaml:"httpAddr"`
	ReportInterval        string                         `yaml:"reportInterval"`
	NvidiaMetricsEndpoint string                         `yaml:"nvidiaMetricsEndpoint"`
	Serverless            serverless.NATSConfig          `yaml:"serverless"`
	Packages              contract.RuntimePackageCatalog `yaml:"packages"`
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
		ServerlessWorker:       serverless.DefaultWorkerSidecarConfig(),
	}
}

// DefaultControllerManagerConfig returns reconciler-only local development settings.
func DefaultControllerManagerConfig() ControllerManagerConfig {
	cfg := DefaultManagerConfig()
	return ControllerManagerConfig{
		MetricsBindAddress:     cfg.MetricsBindAddress,
		HealthProbeBindAddress: cfg.HealthProbeBindAddress,
		LeaderElect:            cfg.LeaderElect,
		MetricsSecure:          cfg.MetricsSecure,
		EnableHTTP2:            cfg.EnableHTTP2,
		BlockedEgressCIDRs:     append([]string(nil), cfg.BlockedEgressCIDRs...),
		Serverless:             cfg.Serverless,
		ServerlessWorker:       cfg.ServerlessWorker,
	}
}

// DefaultRuntimeAPIConfig returns API-only local development settings.
func DefaultRuntimeAPIConfig() RuntimeAPIConfig {
	cfg := DefaultManagerConfig()
	return RuntimeAPIConfig{
		HTTPAddr:              cfg.HTTPAddr,
		ReportInterval:        cfg.ReportInterval,
		NvidiaMetricsEndpoint: cfg.NvidiaMetricsEndpoint,
		Serverless:            cfg.Serverless,
		Packages:              nil,
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

// LoadControllerManagerConfig loads reconciler process settings on top of defaults.
func LoadControllerManagerConfig(path string) (ControllerManagerConfig, error) {
	cfg := DefaultControllerManagerConfig()
	if path == "" {
		return cfg, nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return ControllerManagerConfig{}, fmt.Errorf("read controller manager config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		return ControllerManagerConfig{}, fmt.Errorf("unmarshal controller manager config %s: %w", path, err)
	}
	return cfg, nil
}

// LoadRuntimeAPIConfig loads HTTP API process settings on top of defaults.
func LoadRuntimeAPIConfig(path string) (RuntimeAPIConfig, error) {
	cfg := DefaultRuntimeAPIConfig()
	if path == "" {
		return cfg, nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return RuntimeAPIConfig{}, fmt.Errorf("read runtime API config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		return RuntimeAPIConfig{}, fmt.Errorf("unmarshal runtime API config %s: %w", path, err)
	}
	return cfg, nil
}

// ReportIntervalDuration parses the configured report interval string.
func (c ManagerConfig) ReportIntervalDuration() (time.Duration, error) {
	return parseReportInterval(c.ReportInterval)
}

// ReportIntervalDuration parses the configured report interval string.
func (c RuntimeAPIConfig) ReportIntervalDuration() (time.Duration, error) {
	return parseReportInterval(c.ReportInterval)
}

// NormalizedBlockedEgressCIDRs validates and de-duplicates the configured egress blocklist.
func (c ManagerConfig) NormalizedBlockedEgressCIDRs() ([]string, error) {
	return normalizeBlockedEgressCIDRs(c.BlockedEgressCIDRs)
}

// NormalizedBlockedEgressCIDRs validates and de-duplicates the configured egress blocklist.
func (c ControllerManagerConfig) NormalizedBlockedEgressCIDRs() ([]string, error) {
	return normalizeBlockedEgressCIDRs(c.BlockedEgressCIDRs)
}

func parseReportInterval(value string) (time.Duration, error) {
	interval, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse reportInterval %q: %w", value, err)
	}
	return interval, nil
}

func normalizeBlockedEgressCIDRs(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}

	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
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
