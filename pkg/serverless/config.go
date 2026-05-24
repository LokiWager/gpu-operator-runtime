package serverless

import (
	"fmt"
	neturl "net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	k8svalidation "k8s.io/apimachinery/pkg/util/validation"
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
	URL                 string                  `yaml:"url"`
	SubjectPrefix       string                  `yaml:"subjectPrefix"`
	StreamName          string                  `yaml:"streamName"`
	StreamReplicas      int                     `yaml:"streamReplicas"`
	StreamMaxAge        string                  `yaml:"streamMaxAge"`
	ConnectTimeout      string                  `yaml:"connectTimeout"`
	DuplicatesWindow    string                  `yaml:"duplicatesWindow"`
	NetworkPolicyTarget NATSNetworkPolicyTarget `yaml:"networkPolicyTarget"`
}

// NATSNetworkPolicyTarget identifies the in-cluster NATS Pods that serverless workers may reach.
type NATSNetworkPolicyTarget struct {
	Namespace string            `yaml:"namespace"`
	PodLabels map[string]string `yaml:"podLabels"`
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
	normalizedTarget, err := cfg.NetworkPolicyTarget.Normalized()
	if err != nil {
		return NATSConfig{}, fmt.Errorf("normalize networkPolicyTarget: %w", err)
	}
	cfg.NetworkPolicyTarget = normalizedTarget
	return cfg, nil
}

// Normalized trims and validates the configured network policy target.
func (t NATSNetworkPolicyTarget) Normalized() (NATSNetworkPolicyTarget, error) {
	target := t
	target.Namespace = strings.TrimSpace(target.Namespace)
	if target.Namespace != "" {
		if errs := k8svalidation.IsDNS1123Label(target.Namespace); len(errs) > 0 {
			return NATSNetworkPolicyTarget{}, fmt.Errorf("namespace %q is invalid: %s", target.Namespace, strings.Join(errs, ", "))
		}
	}
	if len(target.PodLabels) == 0 {
		target.PodLabels = nil
		return target, nil
	}

	normalizedLabels := make(map[string]string, len(target.PodLabels))
	keys := make([]string, 0, len(target.PodLabels))
	for key := range target.PodLabels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			return NATSNetworkPolicyTarget{}, fmt.Errorf("podLabels contains an empty key")
		}
		if errs := k8svalidation.IsQualifiedName(trimmedKey); len(errs) > 0 {
			return NATSNetworkPolicyTarget{}, fmt.Errorf("podLabels key %q is invalid: %s", trimmedKey, strings.Join(errs, ", "))
		}
		trimmedValue := strings.TrimSpace(target.PodLabels[key])
		if errs := k8svalidation.IsValidLabelValue(trimmedValue); len(errs) > 0 {
			return NATSNetworkPolicyTarget{}, fmt.Errorf("podLabels[%q] value %q is invalid: %s", trimmedKey, trimmedValue, strings.Join(errs, ", "))
		}
		normalizedLabels[trimmedKey] = trimmedValue
	}
	target.PodLabels = normalizedLabels
	return target, nil
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

// URLHostname extracts the host name from the configured NATS URL.
func (c NATSConfig) URLHostname() string {
	u, err := neturl.Parse(strings.TrimSpace(c.URL))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(u.Hostname())
}

// URLPort extracts the configured port or returns the NATS default port.
func (c NATSConfig) URLPort() int {
	u, err := neturl.Parse(strings.TrimSpace(c.URL))
	if err != nil {
		return 4222
	}
	if u.Port() == "" {
		return 4222
	}
	port := strings.TrimSpace(u.Port())
	if port == "" {
		return 4222
	}
	parsed, err := strconv.Atoi(port)
	if err != nil || parsed <= 0 || parsed > 65535 {
		return 4222
	}
	return parsed
}

// UsesClusterServiceHost reports whether the configured URL points at a Kubernetes service DNS name.
func (c NATSConfig) UsesClusterServiceHost() bool {
	parts := strings.Split(c.URLHostname(), ".")
	return len(parts) >= 3 && parts[2] == "svc"
}

// InferredClusterServiceNamespace extracts the namespace from a standard service DNS name.
func (c NATSConfig) InferredClusterServiceNamespace() string {
	parts := strings.Split(c.URLHostname(), ".")
	if len(parts) >= 3 && parts[2] == "svc" {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

// EffectiveNetworkPolicyNamespace returns the explicit target namespace or the namespace inferred from the service DNS name.
func (c NATSConfig) EffectiveNetworkPolicyNamespace() string {
	if ns := strings.TrimSpace(c.NetworkPolicyTarget.Namespace); ns != "" {
		return ns
	}
	return c.InferredClusterServiceNamespace()
}

// HasNetworkPolicyTarget reports whether a Pod selector has been configured for in-cluster NATS access.
func (c NATSConfig) HasNetworkPolicyTarget() bool {
	return len(c.NetworkPolicyTarget.PodLabels) > 0
}
