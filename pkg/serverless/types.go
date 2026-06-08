package serverless

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

const InvocationVersion = "v1"

// InvocationMode distinguishes request/response-style calls from fire-and-forget submissions.
type InvocationMode string

const (
	InvocationModeAsync InvocationMode = "async"
	InvocationModeSync  InvocationMode = "sync"
)

// WorkerMetricEventType describes one worker-side lifecycle or execution event emitted to the metrics subject.
type WorkerMetricEventType string

const (
	WorkerMetricEventRegistered         WorkerMetricEventType = "registered"
	WorkerMetricEventHeartbeat          WorkerMetricEventType = "heartbeat"
	WorkerMetricEventInvocationStarted  WorkerMetricEventType = "invocation_started"
	WorkerMetricEventInvocationFinished WorkerMetricEventType = "invocation_finished"
	WorkerMetricEventInvocationFailed   WorkerMetricEventType = "invocation_failed"
)

// InvocationMessage is the durable queue payload shared by ingress, activators, and workers.
type InvocationMessage struct {
	Version             string            `json:"version"`
	InvocationID        string            `json:"invocationID"`
	ServerlessRequestID string            `json:"serverlessRequestID"`
	Mode                InvocationMode    `json:"mode"`
	ContentType         string            `json:"contentType,omitempty"`
	Headers             map[string]string `json:"headers,omitempty"`
	Attributes          map[string]string `json:"attributes,omitempty"`
	Payload             json.RawMessage   `json:"payload,omitempty"`
	TimeoutSeconds      int32             `json:"timeoutSeconds,omitempty"`
	ResultSubject       string            `json:"resultSubject,omitempty"`
	MetricsSubject      string            `json:"metricsSubject,omitempty"`
	ReplySubject        string            `json:"replySubject,omitempty"`
	EnqueuedAt          time.Time         `json:"enqueuedAt"`
}

// WorkerDispatchMessage is the durable per-worker dispatch payload consumed by one worker sidecar.
type WorkerDispatchMessage struct {
	Version             string            `json:"version"`
	InvocationID        string            `json:"invocationID"`
	ServerlessRequestID string            `json:"serverlessRequestID"`
	WorkerName          string            `json:"workerName"`
	WorkerNamespace     string            `json:"workerNamespace"`
	Mode                InvocationMode    `json:"mode"`
	ContentType         string            `json:"contentType,omitempty"`
	Headers             map[string]string `json:"headers,omitempty"`
	Attributes          map[string]string `json:"attributes,omitempty"`
	Payload             json.RawMessage   `json:"payload,omitempty"`
	TimeoutSeconds      int32             `json:"timeoutSeconds,omitempty"`
	ResultSubject       string            `json:"resultSubject,omitempty"`
	MetricsSubject      string            `json:"metricsSubject,omitempty"`
	ReplySubject        string            `json:"replySubject,omitempty"`
	DispatchedAt        time.Time         `json:"dispatchedAt"`
}

// FrameworkInvocationRequest is the pod-local UDS-backed HTTP payload delivered from the sidecar to the user framework.
type FrameworkInvocationRequest struct {
	Version             string            `json:"version"`
	InvocationID        string            `json:"invocationID"`
	ServerlessRequestID string            `json:"serverlessRequestID"`
	WorkerName          string            `json:"workerName"`
	WorkerNamespace     string            `json:"workerNamespace"`
	Mode                InvocationMode    `json:"mode"`
	ContentType         string            `json:"contentType,omitempty"`
	Headers             map[string]string `json:"headers,omitempty"`
	Attributes          map[string]string `json:"attributes,omitempty"`
	Payload             json.RawMessage   `json:"payload,omitempty"`
	TimeoutSeconds      int32             `json:"timeoutSeconds,omitempty"`
	DispatchedAt        time.Time         `json:"dispatchedAt"`
}

// FrameworkInvocationResponse is the pod-local UDS-backed HTTP response returned by the user framework to the sidecar.
type FrameworkInvocationResponse struct {
	StatusCode  int               `json:"statusCode,omitempty"`
	ContentType string            `json:"contentType,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Body        json.RawMessage   `json:"body,omitempty"`
	Error       string            `json:"error,omitempty"`
}

// InvocationResultMessage is the durable execution result published by the worker sidecar or activator failure path.
type InvocationResultMessage struct {
	Version             string            `json:"version"`
	InvocationID        string            `json:"invocationID"`
	ServerlessRequestID string            `json:"serverlessRequestID"`
	Mode                InvocationMode    `json:"mode"`
	ReplySubject        string            `json:"replySubject,omitempty"`
	WorkerName          string            `json:"workerName,omitempty"`
	WorkerNamespace     string            `json:"workerNamespace,omitempty"`
	StatusCode          int               `json:"statusCode,omitempty"`
	ContentType         string            `json:"contentType,omitempty"`
	Headers             map[string]string `json:"headers,omitempty"`
	Body                json.RawMessage   `json:"body,omitempty"`
	Error               string            `json:"error,omitempty"`
	StartedAt           time.Time         `json:"startedAt,omitempty"`
	CompletedAt         time.Time         `json:"completedAt"`
}

// WorkerMetricMessage is the durable worker-side event emitted to the metrics subject for lifecycle and execution tracking.
type WorkerMetricMessage struct {
	Version             string                `json:"version"`
	ServerlessRequestID string                `json:"serverlessRequestID"`
	WorkerName          string                `json:"workerName"`
	WorkerNamespace     string                `json:"workerNamespace"`
	InvocationID        string                `json:"invocationID,omitempty"`
	EventType           WorkerMetricEventType `json:"eventType"`
	Inflight            int32                 `json:"inflight,omitempty"`
	StatusCode          int                   `json:"statusCode,omitempty"`
	Error               string                `json:"error,omitempty"`
	ReportedAt          time.Time             `json:"reportedAt"`
}

// PublishAck reports the persisted queue location for one invocation message.
type PublishAck struct {
	InvocationID        string
	ServerlessRequestID string
	Mode                InvocationMode
	Subject             string
	ResultSubject       string
	MetricsSubject      string
	Stream              string
	Sequence            uint64
	Duplicate           bool
	AcceptedAt          time.Time
}

// InvocationPublisher persists invocation messages to a durable queue.
type InvocationPublisher interface {
	Enabled() bool
	PublishInvocation(ctx context.Context, msg InvocationMessage) (PublishAck, error)
}

// WorkerDispatchPublisher persists one worker-targeted dispatch message to the durable queue.
type WorkerDispatchPublisher interface {
	PublishWorkerDispatch(ctx context.Context, msg WorkerDispatchMessage) error
}

// WorkerMetricsPublisher persists one worker lifecycle or execution event into the metrics subject.
type WorkerMetricsPublisher interface {
	PublishWorkerMetric(ctx context.Context, metric WorkerMetricMessage) error
}

// SyncInvocationRequester publishes one invocation and waits for the invocation-specific reply path to return a result.
type SyncInvocationRequester interface {
	RequestSyncInvocation(ctx context.Context, msg InvocationMessage) (PublishAck, InvocationResultMessage, error)
}

// InvocationResultPublisher persists one invocation result into the durable result subject.
type InvocationResultPublisher interface {
	PublishInvocationResult(ctx context.Context, result InvocationResultMessage) error
}

// InvocationConsumer continuously drains invocation messages from the durable queue.
type InvocationConsumer interface {
	ConsumeInvocations(ctx context.Context, durable string, ackWait time.Duration, handler func(context.Context, InvocationMessage) error) error
}

// WorkerMetricConsumer continuously drains worker lifecycle and execution metrics from the durable queue.
type WorkerMetricConsumer interface {
	ConsumeWorkerMetrics(ctx context.Context, durable string, ackWait time.Duration, handler func(context.Context, WorkerMetricMessage) error) error
}

// WorkerDispatchConsumer continuously drains worker-targeted dispatch messages for one concrete worker sidecar.
type WorkerDispatchConsumer interface {
	ConsumeWorkerDispatches(
		ctx context.Context,
		durable string,
		requestID string,
		workerName string,
		ackWait time.Duration,
		handler func(context.Context, WorkerDispatchMessage) error,
	) error
}

// NormalizeInvocationMode defaults omitted modes to async and rejects unsupported values.
func NormalizeInvocationMode(mode InvocationMode) (InvocationMode, error) {
	switch mode {
	case "":
		return InvocationModeAsync, nil
	case InvocationModeAsync, InvocationModeSync:
		return mode, nil
	default:
		return "", fmt.Errorf("invocation mode %q is invalid", mode)
	}
}

// NewInvocationID returns a stable hex identifier suitable for NATS de-duplication headers.
func NewInvocationID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate invocation id: %w", err)
	}
	return "inv-" + hex.EncodeToString(buf), nil
}
