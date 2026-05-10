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
	EnqueuedAt          time.Time         `json:"enqueuedAt"`
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
