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
