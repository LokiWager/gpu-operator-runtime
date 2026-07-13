package resultstore

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/loki/gpu-operator-runtime/pkg/secureconfig"
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
	Hosts              []string               `yaml:"hosts"`
	Keyspace           string                 `yaml:"keyspace"`
	ResultsTable       string                 `yaml:"resultsTable"`
	Datacenter         string                 `yaml:"datacenter"`
	Username           string                 `yaml:"username"`
	UsernameFile       string                 `yaml:"usernameFile"`
	Password           string                 `yaml:"password"`
	PasswordFile       string                 `yaml:"passwordFile"`
	TLS                secureconfig.TLSConfig `yaml:"tls"`
	ConnectTimeout     string                 `yaml:"connectTimeout"`
	RequestTimeout     string                 `yaml:"requestTimeout"`
	ReplicationFactor  int                    `yaml:"replicationFactor"`
	AutoMigrate        bool                   `yaml:"autoMigrate"`
	MaxInlineBodyBytes int64                  `yaml:"maxInlineBodyBytes"`
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
	cfg.UsernameFile = strings.TrimSpace(cfg.UsernameFile)
	cfg.Password = strings.TrimSpace(cfg.Password)
	cfg.PasswordFile = strings.TrimSpace(cfg.PasswordFile)
	cfg.TLS = cfg.TLS.Normalized()
	if cfg.Username != "" && cfg.UsernameFile != "" {
		return ScyllaConfig{}, fmt.Errorf("scylla.username and scylla.usernameFile are mutually exclusive")
	}
	if cfg.Password != "" && cfg.PasswordFile != "" {
		return ScyllaConfig{}, fmt.Errorf("scylla.password and scylla.passwordFile are mutually exclusive")
	}
	if (cfg.Username != "" || cfg.UsernameFile != "") != (cfg.Password != "" || cfg.PasswordFile != "") {
		return ScyllaConfig{}, fmt.Errorf("scylla username and password must be configured together")
	}

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

// ResolvedCredentials returns ScyllaDB username/password from inline config or mounted Secret files.
func (c ScyllaConfig) ResolvedCredentials() (string, string, error) {
	username := c.Username
	if username == "" {
		var err error
		username, err = secureconfig.ReadSecretFile(c.UsernameFile)
		if err != nil {
			return "", "", err
		}
	}
	password := c.Password
	if password == "" {
		var err error
		password, err = secureconfig.ReadSecretFile(c.PasswordFile)
		if err != nil {
			return "", "", err
		}
	}
	return username, password, nil
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
