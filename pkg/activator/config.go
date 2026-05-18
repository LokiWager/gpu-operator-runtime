package activator

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/loki/gpu-operator-runtime/pkg/serverless"
	"gopkg.in/yaml.v3"
)

const (
	defaultConsumerName    = "runtime-activator"
	defaultWorkerReadyWait = "2m"
	defaultWorkerPoll      = "2s"
)

// Config captures the local process settings for the dedicated serverless activator.
type Config struct {
	Serverless         serverless.NATSConfig `yaml:"serverless"`
	ConsumerName       string                `yaml:"consumerName"`
	WorkerReadyWait    string                `yaml:"workerReadyWait"`
	WorkerPollInterval string                `yaml:"workerPollInterval"`
}

// DefaultConfig returns the baseline activator settings.
func DefaultConfig() Config {
	return Config{
		Serverless:         serverless.DefaultNATSConfig(),
		ConsumerName:       defaultConsumerName,
		WorkerReadyWait:    defaultWorkerReadyWait,
		WorkerPollInterval: defaultWorkerPoll,
	}
}

// LoadConfig loads a YAML file on top of the built-in defaults.
func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	if path == "" {
		return cfg.Normalized()
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read activator config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		return Config{}, fmt.Errorf("unmarshal activator config %s: %w", path, err)
	}
	return cfg.Normalized()
}

// Normalized validates and defaults the activator configuration.
func (c Config) Normalized() (Config, error) {
	cfg := c

	normalizedServerless, err := cfg.Serverless.Normalized()
	if err != nil {
		return Config{}, err
	}
	cfg.Serverless = normalizedServerless
	if !cfg.Serverless.Enabled() {
		return Config{}, fmt.Errorf("serverless.url is required for the activator")
	}

	cfg.ConsumerName = strings.TrimSpace(cfg.ConsumerName)
	if cfg.ConsumerName == "" {
		cfg.ConsumerName = defaultConsumerName
	}
	if strings.ContainsAny(cfg.ConsumerName, " .*>/\\") {
		return Config{}, fmt.Errorf("consumerName %q is invalid", cfg.ConsumerName)
	}

	if cfg.WorkerReadyWait == "" {
		cfg.WorkerReadyWait = defaultWorkerReadyWait
	}
	if _, err := time.ParseDuration(cfg.WorkerReadyWait); err != nil {
		return Config{}, fmt.Errorf("parse workerReadyWait %q: %w", cfg.WorkerReadyWait, err)
	}

	if cfg.WorkerPollInterval == "" {
		cfg.WorkerPollInterval = defaultWorkerPoll
	}
	if _, err := time.ParseDuration(cfg.WorkerPollInterval); err != nil {
		return Config{}, fmt.Errorf("parse workerPollInterval %q: %w", cfg.WorkerPollInterval, err)
	}

	return cfg, nil
}

// WorkerReadyWaitDuration parses the worker ready wait budget.
func (c Config) WorkerReadyWaitDuration() time.Duration {
	d, _ := time.ParseDuration(c.WorkerReadyWait)
	return d
}

// WorkerPollIntervalDuration parses the worker polling interval.
func (c Config) WorkerPollIntervalDuration() time.Duration {
	d, _ := time.ParseDuration(c.WorkerPollInterval)
	return d
}

// AckWaitDuration returns the durable queue ack budget required for worker creation and dispatch publication.
func (c Config) AckWaitDuration() time.Duration {
	return c.WorkerReadyWaitDuration() + 15*time.Second
}
