package serverless

import (
	"fmt"
	"strings"
	"time"
)

const (
	DefaultWorkerSidecarImage                   = "ghcr.io/lokiwager/gpu-runtime-serverless-sidecar:latest"
	DefaultWorkerSidecarHealthPort        int32 = 8091
	DefaultWorkerSidecarHeartbeatInterval       = "15s"
)

const (
	EnvNATSURL             = "SERVERLESS_NATS_URL"
	EnvSubjectPrefix       = "SERVERLESS_SUBJECT_PREFIX"
	EnvStreamName          = "SERVERLESS_STREAM_NAME"
	EnvWorkerName          = "SERVERLESS_WORKER_NAME"
	EnvWorkerNamespace     = "SERVERLESS_WORKER_NAMESPACE"
	EnvServerlessRequestID = "SERVERLESS_REQUEST_ID"
	EnvWorkerConsumerName  = "SERVERLESS_WORKER_CONSUMER_NAME"
	EnvHeartbeatInterval   = "SERVERLESS_HEARTBEAT_INTERVAL"
	EnvDispatchAckWait     = "SERVERLESS_DISPATCH_ACK_WAIT"
	EnvDispatchMaxDeliver  = "SERVERLESS_DISPATCH_MAX_DELIVER"
	EnvDispatchBackoff     = "SERVERLESS_DISPATCH_BACKOFF"
	EnvFrameworkSocketPath = "SERVERLESS_FRAMEWORK_SOCKET_PATH"
	EnvFrameworkInvokePath = "SERVERLESS_FRAMEWORK_INVOKE_PATH"
	EnvFrameworkHealthPath = "SERVERLESS_FRAMEWORK_HEALTH_PATH"
	EnvSidecarHealthPort   = "SERVERLESS_SIDECAR_HEALTH_PORT"
)

// WorkerSidecarConfig captures the platform-managed sidecar image and pod-local defaults injected into each serverless worker.
type WorkerSidecarConfig struct {
	Image             string      `yaml:"image"`
	HealthPort        int32       `yaml:"healthPort"`
	HeartbeatInterval string      `yaml:"heartbeatInterval"`
	DispatchRetry     RetryPolicy `yaml:"dispatchRetry"`
}

// DefaultWorkerSidecarConfig returns the baseline worker-sidecar injection settings.
func DefaultWorkerSidecarConfig() WorkerSidecarConfig {
	return WorkerSidecarConfig{
		Image:             DefaultWorkerSidecarImage,
		HealthPort:        DefaultWorkerSidecarHealthPort,
		HeartbeatInterval: DefaultWorkerSidecarHeartbeatInterval,
		DispatchRetry:     DefaultRetryPolicy(),
	}
}

// Normalized validates and defaults one worker-sidecar injection config.
func (c WorkerSidecarConfig) Normalized() (WorkerSidecarConfig, error) {
	cfg := c
	cfg.Image = strings.TrimSpace(cfg.Image)
	if cfg.Image == "" {
		cfg.Image = DefaultWorkerSidecarImage
	}
	if cfg.HealthPort <= 0 || cfg.HealthPort > 65535 {
		cfg.HealthPort = DefaultWorkerSidecarHealthPort
	}
	if strings.TrimSpace(cfg.HeartbeatInterval) == "" {
		cfg.HeartbeatInterval = DefaultWorkerSidecarHeartbeatInterval
	}
	if _, err := time.ParseDuration(cfg.HeartbeatInterval); err != nil {
		return WorkerSidecarConfig{}, fmt.Errorf("parse heartbeatInterval %q: %w", cfg.HeartbeatInterval, err)
	}
	dispatchRetry, err := cfg.DispatchRetry.Normalized()
	if err != nil {
		return WorkerSidecarConfig{}, fmt.Errorf("normalize dispatchRetry: %w", err)
	}
	cfg.DispatchRetry = dispatchRetry
	return cfg, nil
}

// HeartbeatIntervalDuration parses the configured sidecar heartbeat cadence.
func (c WorkerSidecarConfig) HeartbeatIntervalDuration() (time.Duration, error) {
	return time.ParseDuration(c.HeartbeatInterval)
}
