package resultconsumer

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/loki/gpu-operator-runtime/pkg/resultstore"
	"github.com/loki/gpu-operator-runtime/pkg/serverless"
	"gopkg.in/yaml.v3"
)

const (
	defaultConsumerName = "runtime-result-store"
	defaultAckWait      = "30s"
)

// Config captures the process settings for the serverless invocation result consumer.
type Config struct {
	Serverless   serverless.NATSConfig    `yaml:"serverless"`
	ConsumerName string                   `yaml:"consumerName"`
	AckWait      string                   `yaml:"ackWait"`
	Retry        serverless.RetryPolicy   `yaml:"retry"`
	Scylla       resultstore.ScyllaConfig `yaml:"scylla"`
}

// DefaultConfig returns local result consumer defaults.
func DefaultConfig() Config {
	return Config{
		Serverless:   serverless.DefaultNATSConfig(),
		ConsumerName: defaultConsumerName,
		AckWait:      defaultAckWait,
		Retry:        serverless.DefaultRetryPolicy(),
		Scylla:       resultstore.DefaultScyllaConfig(),
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
		return Config{}, fmt.Errorf("read result consumer config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		return Config{}, fmt.Errorf("unmarshal result consumer config %s: %w", path, err)
	}
	return cfg.Normalized()
}

// Normalized validates and defaults the result consumer configuration.
func (c Config) Normalized() (Config, error) {
	cfg := c

	normalizedServerless, err := cfg.Serverless.Normalized()
	if err != nil {
		return Config{}, err
	}
	if !normalizedServerless.Enabled() {
		return Config{}, fmt.Errorf("serverless.url is required for the result consumer")
	}
	cfg.Serverless = normalizedServerless

	cfg.ConsumerName = strings.TrimSpace(cfg.ConsumerName)
	if cfg.ConsumerName == "" {
		cfg.ConsumerName = defaultConsumerName
	}
	if strings.ContainsAny(cfg.ConsumerName, " .*>/\\") {
		return Config{}, fmt.Errorf("consumerName %q is invalid", cfg.ConsumerName)
	}

	if cfg.AckWait == "" {
		cfg.AckWait = defaultAckWait
	}
	if _, err := time.ParseDuration(cfg.AckWait); err != nil {
		return Config{}, fmt.Errorf("parse ackWait %q: %w", cfg.AckWait, err)
	}
	retry, err := cfg.Retry.Normalized()
	if err != nil {
		return Config{}, fmt.Errorf("normalize retry: %w", err)
	}
	cfg.Retry = retry

	normalizedScylla, err := cfg.Scylla.Normalized()
	if err != nil {
		return Config{}, err
	}
	cfg.Scylla = normalizedScylla
	return cfg, nil
}

// AckWaitDuration parses the result consumer ack wait budget.
func (c Config) AckWaitDuration() time.Duration {
	d, _ := time.ParseDuration(c.AckWait)
	return d
}

// ConsumerOptions returns the durable consumer policy for completed invocation results.
func (c Config) ConsumerOptions() serverless.ConsumerOptions {
	return serverless.ConsumerOptions{
		AckWait:          c.AckWaitDuration(),
		Retry:            c.Retry,
		DeadLetterSource: serverless.DeadLetterSourceResult,
	}
}
