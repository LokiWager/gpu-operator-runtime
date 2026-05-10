package serverless

import (
	"fmt"
	"strings"
	"time"
)

const (
	DefaultSubjectPrefix    = "runtime.serverless"
	DefaultStreamName       = "RUNTIME_SERVERLESS"
	DefaultStreamMaxAge     = "72h"
	DefaultConnectTimeout   = "5s"
	DefaultDuplicatesWindow = "24h"
)

// NATSConfig configures the queue-first serverless ingress backed by NATS JetStream.
type NATSConfig struct {
	URL              string `yaml:"url"`
	SubjectPrefix    string `yaml:"subjectPrefix"`
	StreamName       string `yaml:"streamName"`
	StreamReplicas   int    `yaml:"streamReplicas"`
	StreamMaxAge     string `yaml:"streamMaxAge"`
	ConnectTimeout   string `yaml:"connectTimeout"`
	DuplicatesWindow string `yaml:"duplicatesWindow"`
}

// DefaultNATSConfig returns the baseline queue configuration with ingress disabled until url is set.
func DefaultNATSConfig() NATSConfig {
	return NATSConfig{
		SubjectPrefix:    DefaultSubjectPrefix,
		StreamName:       DefaultStreamName,
		StreamReplicas:   1,
		StreamMaxAge:     DefaultStreamMaxAge,
		ConnectTimeout:   DefaultConnectTimeout,
		DuplicatesWindow: DefaultDuplicatesWindow,
	}
}

// Enabled reports whether queue-first serverless ingress should connect to NATS.
func (c NATSConfig) Enabled() bool {
	return strings.TrimSpace(c.URL) != ""
}

// Normalized validates and defaults the queue configuration.
func (c NATSConfig) Normalized() (NATSConfig, error) {
	cfg := c
	cfg.URL = strings.TrimSpace(cfg.URL)
	cfg.SubjectPrefix = NormalizeSubjectPrefix(cfg.SubjectPrefix)
	if cfg.StreamName == "" {
		cfg.StreamName = DefaultStreamName
	}
	cfg.StreamName = strings.TrimSpace(cfg.StreamName)
	if strings.ContainsAny(cfg.StreamName, " .*>/\\") {
		return NATSConfig{}, fmt.Errorf("streamName %q is invalid", cfg.StreamName)
	}
	if cfg.StreamReplicas <= 0 {
		cfg.StreamReplicas = 1
	}
	if cfg.StreamMaxAge == "" {
		cfg.StreamMaxAge = DefaultStreamMaxAge
	}
	if cfg.ConnectTimeout == "" {
		cfg.ConnectTimeout = DefaultConnectTimeout
	}
	if cfg.DuplicatesWindow == "" {
		cfg.DuplicatesWindow = DefaultDuplicatesWindow
	}
	if _, err := time.ParseDuration(cfg.StreamMaxAge); err != nil {
		return NATSConfig{}, fmt.Errorf("parse streamMaxAge %q: %w", cfg.StreamMaxAge, err)
	}
	if _, err := time.ParseDuration(cfg.ConnectTimeout); err != nil {
		return NATSConfig{}, fmt.Errorf("parse connectTimeout %q: %w", cfg.ConnectTimeout, err)
	}
	if _, err := time.ParseDuration(cfg.DuplicatesWindow); err != nil {
		return NATSConfig{}, fmt.Errorf("parse duplicatesWindow %q: %w", cfg.DuplicatesWindow, err)
	}
	return cfg, nil
}

// StreamMaxAgeDuration parses the configured stream message retention.
func (c NATSConfig) StreamMaxAgeDuration() (time.Duration, error) {
	return time.ParseDuration(c.StreamMaxAge)
}

// ConnectTimeoutDuration parses the configured NATS connect timeout.
func (c NATSConfig) ConnectTimeoutDuration() (time.Duration, error) {
	return time.ParseDuration(c.ConnectTimeout)
}

// DuplicatesWindowDuration parses the configured message de-duplication window.
func (c NATSConfig) DuplicatesWindowDuration() (time.Duration, error) {
	return time.ParseDuration(c.DuplicatesWindow)
}
