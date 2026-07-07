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
	defaultConsumerName                  = "runtime-activator"
	defaultMetricsConsumerName           = "runtime-activator-metrics"
	defaultWorkerReadyWait               = "2m"
	defaultWorkerPoll                    = "2s"
	defaultLifecycleInterval             = "15s"
	defaultMaxPendingWorkersPerRequestID = 1
)

// Config captures the local process settings for the dedicated serverless activator.
type Config struct {
	Serverless                    serverless.NATSConfig  `yaml:"serverless"`
	ConsumerName                  string                 `yaml:"consumerName"`
	MetricsConsumerName           string                 `yaml:"metricsConsumerName"`
	WorkerReadyWait               string                 `yaml:"workerReadyWait"`
	WorkerPollInterval            string                 `yaml:"workerPollInterval"`
	LifecycleInterval             string                 `yaml:"lifecycleInterval"`
	InvocationRetry               serverless.RetryPolicy `yaml:"invocationRetry"`
	MetricsRetry                  serverless.RetryPolicy `yaml:"metricsRetry"`
	MaxPendingWorkersPerRequestID int                    `yaml:"maxPendingWorkersPerRequestID"`
}

// DefaultConfig returns the baseline activator settings.
func DefaultConfig() Config {
	return Config{
		Serverless:                    serverless.DefaultNATSConfig(),
		ConsumerName:                  defaultConsumerName,
		MetricsConsumerName:           defaultMetricsConsumerName,
		WorkerReadyWait:               defaultWorkerReadyWait,
		WorkerPollInterval:            defaultWorkerPoll,
		LifecycleInterval:             defaultLifecycleInterval,
		InvocationRetry:               serverless.DefaultRetryPolicy(),
		MetricsRetry:                  serverless.DefaultRetryPolicy(),
		MaxPendingWorkersPerRequestID: defaultMaxPendingWorkersPerRequestID,
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

	if cfg.ConsumerName, err = normalizeConsumerName("consumerName", cfg.ConsumerName, defaultConsumerName); err != nil {
		return Config{}, err
	}
	if cfg.MetricsConsumerName, err = normalizeConsumerName("metricsConsumerName", cfg.MetricsConsumerName, defaultMetricsConsumerName); err != nil {
		return Config{}, err
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

	if cfg.LifecycleInterval == "" {
		cfg.LifecycleInterval = defaultLifecycleInterval
	}
	if _, err := time.ParseDuration(cfg.LifecycleInterval); err != nil {
		return Config{}, fmt.Errorf("parse lifecycleInterval %q: %w", cfg.LifecycleInterval, err)
	}

	invocationRetry, err := cfg.InvocationRetry.Normalized()
	if err != nil {
		return Config{}, fmt.Errorf("normalize invocationRetry: %w", err)
	}
	cfg.InvocationRetry = invocationRetry
	metricsRetry, err := cfg.MetricsRetry.Normalized()
	if err != nil {
		return Config{}, fmt.Errorf("normalize metricsRetry: %w", err)
	}
	cfg.MetricsRetry = metricsRetry
	if cfg.MaxPendingWorkersPerRequestID <= 0 {
		cfg.MaxPendingWorkersPerRequestID = defaultMaxPendingWorkersPerRequestID
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

// LifecycleIntervalDuration parses the lifecycle reconcile cadence.
func (c Config) LifecycleIntervalDuration() time.Duration {
	d, _ := time.ParseDuration(c.LifecycleInterval)
	return d
}

// AckWaitDuration returns the durable queue ack budget required for worker creation and dispatch publication.
func (c Config) AckWaitDuration() time.Duration {
	return c.WorkerReadyWaitDuration() + 15*time.Second
}

// InvocationConsumerOptions returns the durable consumer policy for invocation ingress.
func (c Config) InvocationConsumerOptions() serverless.ConsumerOptions {
	return serverless.ConsumerOptions{
		AckWait:          c.AckWaitDuration(),
		Retry:            c.InvocationRetry,
		DeadLetterSource: serverless.DeadLetterSourceInvocation,
	}
}

// MetricsConsumerOptions returns the durable consumer policy for worker metrics.
func (c Config) MetricsConsumerOptions() serverless.ConsumerOptions {
	return serverless.ConsumerOptions{
		AckWait:          c.AckWaitDuration(),
		Retry:            c.MetricsRetry,
		DeadLetterSource: serverless.DeadLetterSourceMetric,
	}
}

func normalizeConsumerName(field, value, fallback string) (string, error) {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		normalized = fallback
	}
	if strings.ContainsAny(normalized, " .*>/\\") {
		return "", fmt.Errorf("%s %q is invalid", field, normalized)
	}
	return normalized, nil
}
