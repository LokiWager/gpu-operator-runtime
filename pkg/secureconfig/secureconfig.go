package secureconfig

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
)

// TLSConfig describes client-side TLS settings for runtime dependencies.
type TLSConfig struct {
	Enabled            bool   `yaml:"enabled"`
	CAFile             string `yaml:"caFile"`
	CertFile           string `yaml:"certFile"`
	KeyFile            string `yaml:"keyFile"`
	ServerName         string `yaml:"serverName"`
	InsecureSkipVerify bool   `yaml:"insecureSkipVerify"`
}

// EnabledByConfig reports whether any TLS field requires TLS to be configured.
func (c TLSConfig) EnabledByConfig() bool {
	return c.Enabled ||
		strings.TrimSpace(c.CAFile) != "" ||
		strings.TrimSpace(c.CertFile) != "" ||
		strings.TrimSpace(c.KeyFile) != "" ||
		strings.TrimSpace(c.ServerName) != "" ||
		c.InsecureSkipVerify
}

// Normalized trims path and name fields.
func (c TLSConfig) Normalized() TLSConfig {
	c.CAFile = strings.TrimSpace(c.CAFile)
	c.CertFile = strings.TrimSpace(c.CertFile)
	c.KeyFile = strings.TrimSpace(c.KeyFile)
	c.ServerName = strings.TrimSpace(c.ServerName)
	if c.EnabledByConfig() {
		c.Enabled = true
	}
	return c
}

// BuildClientTLSConfig builds a tls.Config from mounted Secret files.
func (c TLSConfig) BuildClientTLSConfig() (*tls.Config, error) {
	cfg := c.Normalized()
	if !cfg.Enabled {
		return nil, nil
	}
	if (cfg.CertFile == "") != (cfg.KeyFile == "") {
		return nil, fmt.Errorf("tls certFile and keyFile must be configured together")
	}

	tlsCfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         cfg.ServerName,
		InsecureSkipVerify: cfg.InsecureSkipVerify, //nolint:gosec // Explicit local-development escape hatch.
	}
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read tls caFile %s: %w", cfg.CAFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("tls caFile %s does not contain a valid PEM certificate", cfg.CAFile)
		}
		tlsCfg.RootCAs = pool
	}
	if cfg.CertFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load tls client certificate: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	return tlsCfg, nil
}

// ReadSecretFile reads a mounted Secret file and trims trailing whitespace.
func ReadSecretFile(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read secret file %s: %w", path, err)
	}
	return strings.TrimSpace(string(content)), nil
}
