package resultstore

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	DefaultKeyspace           = "runtime_serverless"
	DefaultResultsTable       = "invocation_results"
	DefaultConnectTimeout     = "5s"
	DefaultRequestTimeout     = "5s"
	DefaultReplicationFactor  = 1
	DefaultMaxInlineBodyBytes = int64(1 << 20)
)

var cqlIdentifierPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// ScyllaConfig captures the storage settings for invocation result persistence.
type ScyllaConfig struct {
	Hosts              []string `yaml:"hosts"`
	Keyspace           string   `yaml:"keyspace"`
	ResultsTable       string   `yaml:"resultsTable"`
	Datacenter         string   `yaml:"datacenter"`
	Username           string   `yaml:"username"`
	Password           string   `yaml:"password"`
	ConnectTimeout     string   `yaml:"connectTimeout"`
	RequestTimeout     string   `yaml:"requestTimeout"`
	ReplicationFactor  int      `yaml:"replicationFactor"`
	AutoMigrate        bool     `yaml:"autoMigrate"`
	MaxInlineBodyBytes int64    `yaml:"maxInlineBodyBytes"`
}

// DefaultScyllaConfig returns local-development defaults. Hosts are intentionally empty until configured.
func DefaultScyllaConfig() ScyllaConfig {
	return ScyllaConfig{
		Keyspace:           DefaultKeyspace,
		ResultsTable:       DefaultResultsTable,
		ConnectTimeout:     DefaultConnectTimeout,
		RequestTimeout:     DefaultRequestTimeout,
		ReplicationFactor:  DefaultReplicationFactor,
		AutoMigrate:        true,
		MaxInlineBodyBytes: DefaultMaxInlineBodyBytes,
	}
}

// Normalized validates and defaults ScyllaDB storage configuration.
func (c ScyllaConfig) Normalized() (ScyllaConfig, error) {
	cfg := c
	cfg.Hosts = normalizeHosts(cfg.Hosts)
	if len(cfg.Hosts) == 0 {
		return ScyllaConfig{}, fmt.Errorf("scylla.hosts is required")
	}

	cfg.Keyspace = strings.TrimSpace(cfg.Keyspace)
	if cfg.Keyspace == "" {
		cfg.Keyspace = DefaultKeyspace
	}
	if err := validateIdentifier("scylla.keyspace", cfg.Keyspace); err != nil {
		return ScyllaConfig{}, err
	}

	cfg.ResultsTable = strings.TrimSpace(cfg.ResultsTable)
	if cfg.ResultsTable == "" {
		cfg.ResultsTable = DefaultResultsTable
	}
	if err := validateIdentifier("scylla.resultsTable", cfg.ResultsTable); err != nil {
		return ScyllaConfig{}, err
	}

	cfg.Datacenter = strings.TrimSpace(cfg.Datacenter)
	cfg.Username = strings.TrimSpace(cfg.Username)
	cfg.Password = strings.TrimSpace(cfg.Password)

	if cfg.ConnectTimeout == "" {
		cfg.ConnectTimeout = DefaultConnectTimeout
	}
	if _, err := time.ParseDuration(cfg.ConnectTimeout); err != nil {
		return ScyllaConfig{}, fmt.Errorf("parse scylla.connectTimeout %q: %w", cfg.ConnectTimeout, err)
	}

	if cfg.RequestTimeout == "" {
		cfg.RequestTimeout = DefaultRequestTimeout
	}
	if _, err := time.ParseDuration(cfg.RequestTimeout); err != nil {
		return ScyllaConfig{}, fmt.Errorf("parse scylla.requestTimeout %q: %w", cfg.RequestTimeout, err)
	}

	if cfg.ReplicationFactor <= 0 {
		cfg.ReplicationFactor = DefaultReplicationFactor
	}
	if cfg.MaxInlineBodyBytes == 0 {
		cfg.MaxInlineBodyBytes = DefaultMaxInlineBodyBytes
	}
	if cfg.MaxInlineBodyBytes < 0 {
		return ScyllaConfig{}, fmt.Errorf("scylla.maxInlineBodyBytes must be >= 0")
	}

	return cfg, nil
}

// ConnectTimeoutDuration parses the ScyllaDB connection timeout.
func (c ScyllaConfig) ConnectTimeoutDuration() time.Duration {
	d, _ := time.ParseDuration(c.ConnectTimeout)
	return d
}

// RequestTimeoutDuration parses the ScyllaDB query timeout.
func (c ScyllaConfig) RequestTimeoutDuration() time.Duration {
	d, _ := time.ParseDuration(c.RequestTimeout)
	return d
}

func normalizeHosts(hosts []string) []string {
	out := make([]string, 0, len(hosts))
	seen := map[string]struct{}{}
	for _, host := range hosts {
		trimmed := strings.TrimSpace(host)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func validateIdentifier(field, value string) error {
	if !cqlIdentifierPattern.MatchString(value) {
		return fmt.Errorf("%s %q is invalid; use letters, numbers, and underscores, starting with a letter", field, value)
	}
	return nil
}
