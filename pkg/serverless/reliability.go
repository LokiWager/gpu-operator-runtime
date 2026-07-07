package serverless

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultRetryMaxDeliver = 5
)

var defaultRetryBackoff = []string{"2s", "10s", "30s"}

// InvocationState describes the durable execution state observed by runtime components.
type InvocationState string

const (
	InvocationStateQueued       InvocationState = "queued"
	InvocationStateDispatching  InvocationState = "dispatching"
	InvocationStateRunning      InvocationState = "running"
	InvocationStateSucceeded    InvocationState = "succeeded"
	InvocationStateFailed       InvocationState = "failed"
	InvocationStateExpired      InvocationState = "expired"
	InvocationStateDeadLettered InvocationState = "dead_lettered"
)

// InvocationFailureClass describes why an invocation or queue message failed.
type InvocationFailureClass string

const (
	InvocationFailureNone              InvocationFailureClass = ""
	InvocationFailureMalformedMessage  InvocationFailureClass = "malformed_message"
	InvocationFailureNoWorkerTemplate  InvocationFailureClass = "no_worker_template"
	InvocationFailureActivationTimeout InvocationFailureClass = "activation_timeout"
	InvocationFailureBackpressure      InvocationFailureClass = "backpressure"
	InvocationFailureDispatchError     InvocationFailureClass = "dispatch_error"
	InvocationFailureFrameworkError    InvocationFailureClass = "framework_error"
	InvocationFailureFrameworkTimeout  InvocationFailureClass = "framework_timeout"
	InvocationFailureResultStoreError  InvocationFailureClass = "result_store_error"
	InvocationFailureRetryExhausted    InvocationFailureClass = "retry_exhausted"
)

// DeadLetterSource identifies which queue family moved a message to DLQ.
type DeadLetterSource string

const (
	DeadLetterSourceInvocation DeadLetterSource = "invocation"
	DeadLetterSourceDispatch   DeadLetterSource = "dispatch"
	DeadLetterSourceResult     DeadLetterSource = "result"
	DeadLetterSourceMetric     DeadLetterSource = "metric"
)

// RetryPolicy controls JetStream redelivery attempts for one consumer.
type RetryPolicy struct {
	MaxDeliver int      `json:"maxDeliver,omitempty" yaml:"maxDeliver,omitempty"`
	Backoff    []string `json:"backoff,omitempty" yaml:"backoff,omitempty"`
}

// ConsumerOptions is the normalized runtime form used when creating a JetStream consumer.
type ConsumerOptions struct {
	AckWait          time.Duration
	Retry            RetryPolicy
	DeadLetterSource DeadLetterSource
}

// DeadLetterMessage records a terminal queue failure after retry exhaustion or malformed payloads.
type DeadLetterMessage struct {
	Version             string                 `json:"version"`
	Source              DeadLetterSource       `json:"source"`
	State               InvocationState        `json:"state"`
	FailureClass        InvocationFailureClass `json:"failureClass"`
	InvocationID        string                 `json:"invocationID,omitempty"`
	ServerlessRequestID string                 `json:"serverlessRequestID,omitempty"`
	WorkerName          string                 `json:"workerName,omitempty"`
	WorkerNamespace     string                 `json:"workerNamespace,omitempty"`
	OriginalSubject     string                 `json:"originalSubject"`
	Stream              string                 `json:"stream,omitempty"`
	Consumer            string                 `json:"consumer,omitempty"`
	StreamSequence      uint64                 `json:"streamSequence,omitempty"`
	ConsumerSequence    uint64                 `json:"consumerSequence,omitempty"`
	DeliveryCount       uint64                 `json:"deliveryCount,omitempty"`
	Error               string                 `json:"error"`
	Payload             json.RawMessage        `json:"payload,omitempty"`
	FailedAt            time.Time              `json:"failedAt"`
}

// DefaultRetryPolicy returns the baseline retry settings used by tutorial consumers.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxDeliver: DefaultRetryMaxDeliver,
		Backoff:    append([]string(nil), defaultRetryBackoff...),
	}
}

// RetryPolicyFromEnv builds a retry policy from sidecar environment variables.
func RetryPolicyFromEnv(maxDeliverValue, backoffValue string) RetryPolicy {
	policy := RetryPolicy{}
	if parsed, err := strconv.Atoi(strings.TrimSpace(maxDeliverValue)); err == nil {
		policy.MaxDeliver = parsed
	}
	if values := SplitRetryBackoff(backoffValue); len(values) > 0 {
		policy.Backoff = values
	}
	return policy
}

// SplitRetryBackoff splits a comma-separated duration list.
func SplitRetryBackoff(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// Normalized validates and defaults one retry policy.
func (p RetryPolicy) Normalized() (RetryPolicy, error) {
	policy := p
	if policy.MaxDeliver <= 0 {
		policy.MaxDeliver = DefaultRetryMaxDeliver
	}
	if len(policy.Backoff) == 0 {
		policy.Backoff = append([]string(nil), defaultRetryBackoff...)
	}
	if len(policy.Backoff) > policy.MaxDeliver {
		return RetryPolicy{}, fmt.Errorf("retry backoff entries (%d) must be <= maxDeliver (%d)", len(policy.Backoff), policy.MaxDeliver)
	}
	for _, value := range policy.Backoff {
		duration, err := time.ParseDuration(strings.TrimSpace(value))
		if err != nil {
			return RetryPolicy{}, fmt.Errorf("parse retry backoff %q: %w", value, err)
		}
		if duration <= 0 {
			return RetryPolicy{}, fmt.Errorf("retry backoff %q must be > 0", value)
		}
	}
	return policy, nil
}

// BackoffDurations parses the normalized backoff list.
func (p RetryPolicy) BackoffDurations() []time.Duration {
	out := make([]time.Duration, 0, len(p.Backoff))
	for _, value := range p.Backoff {
		duration, _ := time.ParseDuration(strings.TrimSpace(value))
		if duration > 0 {
			out = append(out, duration)
		}
	}
	return out
}

// DelayForDelivery returns the retry delay after the current delivery attempt fails.
func (p RetryPolicy) DelayForDelivery(delivery uint64) time.Duration {
	delays := p.BackoffDurations()
	if len(delays) == 0 {
		return 0
	}
	if delivery == 0 {
		return delays[0]
	}
	index := int(delivery - 1)
	if index >= len(delays) {
		index = len(delays) - 1
	}
	return delays[index]
}

// Normalized validates consumer options and fills retry defaults.
func (o ConsumerOptions) Normalized(defaultAckWait time.Duration) (ConsumerOptions, error) {
	opts := o
	if opts.AckWait <= 0 {
		opts.AckWait = defaultAckWait
	}
	if opts.AckWait <= 0 {
		opts.AckWait = 30 * time.Second
	}
	retry, err := opts.Retry.Normalized()
	if err != nil {
		return ConsumerOptions{}, err
	}
	opts.Retry = retry
	return opts, nil
}
