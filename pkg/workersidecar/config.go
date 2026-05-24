package workersidecar

import (
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
	"github.com/loki/gpu-operator-runtime/pkg/serverless"
)

const defaultDispatchAckWait = "5m"

// Config captures the worker-sidecar runtime settings loaded from pod env vars.
type Config struct {
	Serverless          serverless.NATSConfig
	WorkerName          string
	WorkerNamespace     string
	ServerlessRequestID string
	ConsumerName        string
	HeartbeatInterval   string
	FrameworkSocketPath string
	FrameworkInvokePath string
	FrameworkHealthPath string
	HealthPort          int32
	DispatchAckWait     string
}

// LoadConfigFromEnv builds one worker-sidecar config from the injected environment variables.
func LoadConfigFromEnv() (Config, error) {
	cfg := Config{
		Serverless: serverless.NATSConfig{
			URL:           os.Getenv(serverless.EnvNATSURL),
			SubjectPrefix: os.Getenv(serverless.EnvSubjectPrefix),
			StreamName:    os.Getenv(serverless.EnvStreamName),
		},
		WorkerName:          os.Getenv(serverless.EnvWorkerName),
		WorkerNamespace:     os.Getenv(serverless.EnvWorkerNamespace),
		ServerlessRequestID: os.Getenv(serverless.EnvServerlessRequestID),
		ConsumerName:        os.Getenv(serverless.EnvWorkerConsumerName),
		HeartbeatInterval:   os.Getenv(serverless.EnvHeartbeatInterval),
		FrameworkSocketPath: os.Getenv(serverless.EnvFrameworkSocketPath),
		FrameworkInvokePath: os.Getenv(serverless.EnvFrameworkInvokePath),
		FrameworkHealthPath: os.Getenv(serverless.EnvFrameworkHealthPath),
		DispatchAckWait:     defaultEnv(serverless.EnvDispatchAckWait, defaultDispatchAckWait),
	}

	return cfg.Normalized()
}

// Normalized validates and defaults one worker-sidecar config.
func (c Config) Normalized() (Config, error) {
	cfg := c

	serverlessCfg, err := cfg.Serverless.Normalized()
	if err != nil {
		return Config{}, err
	}
	if !serverlessCfg.Enabled() {
		return Config{}, fmt.Errorf("serverless.url is required for the worker sidecar")
	}
	cfg.Serverless = serverlessCfg

	cfg.WorkerName = strings.TrimSpace(cfg.WorkerName)
	if cfg.WorkerName == "" {
		return Config{}, fmt.Errorf("%s is required", serverless.EnvWorkerName)
	}
	cfg.WorkerNamespace = strings.TrimSpace(cfg.WorkerNamespace)
	if cfg.WorkerNamespace == "" {
		return Config{}, fmt.Errorf("%s is required", serverless.EnvWorkerNamespace)
	}

	requestID, err := serverless.NormalizeRequestID(cfg.ServerlessRequestID)
	if err != nil {
		return Config{}, err
	}
	cfg.ServerlessRequestID = requestID

	cfg.ConsumerName = strings.TrimSpace(cfg.ConsumerName)
	if cfg.ConsumerName == "" {
		cfg.ConsumerName = "serverless-sidecar-" + cfg.WorkerName
	}
	cfg.HeartbeatInterval = defaultEnvValue(cfg.HeartbeatInterval, serverless.DefaultWorkerSidecarHeartbeatInterval)
	if _, err := time.ParseDuration(cfg.HeartbeatInterval); err != nil {
		return Config{}, fmt.Errorf("parse heartbeat interval %q: %w", cfg.HeartbeatInterval, err)
	}

	cfg.FrameworkSocketPath = strings.TrimSpace(cfg.FrameworkSocketPath)
	if cfg.FrameworkSocketPath == "" {
		cfg.FrameworkSocketPath = runtimev1alpha1.DefaultServerlessFrameworkSocketPath
	}
	if !strings.HasPrefix(cfg.FrameworkSocketPath, "/") {
		cfg.FrameworkSocketPath = "/" + cfg.FrameworkSocketPath
	}
	cfg.FrameworkSocketPath = path.Clean(cfg.FrameworkSocketPath)
	socketDir := path.Clean(runtimev1alpha1.DefaultServerlessFrameworkSocketDir)
	if cfg.FrameworkSocketPath == socketDir || !strings.HasPrefix(cfg.FrameworkSocketPath, socketDir+"/") {
		return Config{}, fmt.Errorf("framework socket path %q must stay under %s", cfg.FrameworkSocketPath, runtimev1alpha1.DefaultServerlessFrameworkSocketDir)
	}
	cfg.FrameworkInvokePath = normalizePath(cfg.FrameworkInvokePath, runtimev1alpha1.DefaultServerlessFrameworkInvokePath)
	cfg.FrameworkHealthPath = normalizePath(cfg.FrameworkHealthPath, runtimev1alpha1.DefaultServerlessFrameworkHealthPath)

	cfg.HealthPort = defaultHealthPortFromEnv(cfg.HealthPort)

	cfg.DispatchAckWait = defaultEnvValue(cfg.DispatchAckWait, defaultDispatchAckWait)
	if _, err := time.ParseDuration(cfg.DispatchAckWait); err != nil {
		return Config{}, fmt.Errorf("parse dispatch ack wait %q: %w", cfg.DispatchAckWait, err)
	}

	return cfg, nil
}

// HeartbeatIntervalDuration parses the worker-side heartbeat cadence.
func (c Config) HeartbeatIntervalDuration() (time.Duration, error) {
	return time.ParseDuration(c.HeartbeatInterval)
}

// DispatchAckWaitDuration parses the queue ack wait window used by the worker dispatch consumer.
func (c Config) DispatchAckWaitDuration() (time.Duration, error) {
	return time.ParseDuration(c.DispatchAckWait)
}

func defaultEnv(key, fallback string) string {
	return defaultEnvValue(os.Getenv(key), fallback)
}

func defaultEnvValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func defaultHealthPortFromEnv(value int32) int32 {
	if value > 0 && value <= 65535 {
		return value
	}
	raw := strings.TrimSpace(os.Getenv(serverless.EnvSidecarHealthPort))
	if raw == "" {
		return serverless.DefaultWorkerSidecarHealthPort
	}
	var parsed int64
	if _, err := fmt.Sscanf(raw, "%d", &parsed); err != nil || parsed <= 0 || parsed > 65535 {
		return serverless.DefaultWorkerSidecarHealthPort
	}
	return int32(parsed)
}

func normalizePath(value, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	normalized := "/" + strings.TrimPrefix(trimmed, "/")
	if normalized == "/" {
		return fallback
	}
	return normalized
}
