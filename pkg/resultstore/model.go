package resultstore

import (
	"errors"
	"fmt"
	"time"

	"github.com/loki/gpu-operator-runtime/pkg/serverless"
)

var ErrNotFound = errors.New("invocation result not found")

// InvocationRecord is the storage model used by the ScyllaDB-backed result store.
type InvocationRecord struct {
	InvocationID        string                    `db:"invocation_id"`
	ServerlessRequestID string                    `db:"serverless_request_id"`
	Mode                serverless.InvocationMode `db:"mode"`
	WorkerName          string                    `db:"worker_name"`
	WorkerNamespace     string                    `db:"worker_namespace"`
	StatusCode          int                       `db:"status_code"`
	ContentType         string                    `db:"content_type"`
	Headers             map[string]string         `db:"headers"`
	BodyInline          []byte                    `db:"body_inline"`
	BodyBytes           int64                     `db:"body_bytes"`
	BodyTruncated       bool                      `db:"body_truncated"`
	Error               string                    `db:"error"`
	StartedAt           time.Time                 `db:"started_at"`
	CompletedAt         time.Time                 `db:"completed_at"`
	StoredAt            time.Time                 `db:"stored_at"`
}

// RecordFromResult converts the queue result contract into the ScyllaDB storage model.
func RecordFromResult(result serverless.InvocationResultMessage, maxInlineBodyBytes int64) (InvocationRecord, error) {
	if result.InvocationID == "" {
		return InvocationRecord{}, fmt.Errorf("invocationID is required")
	}
	if result.ServerlessRequestID == "" {
		return InvocationRecord{}, fmt.Errorf("serverlessRequestID is required")
	}
	if maxInlineBodyBytes < 0 {
		return InvocationRecord{}, fmt.Errorf("maxInlineBodyBytes must be >= 0")
	}

	completedAt := result.CompletedAt
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	startedAt := result.StartedAt
	if startedAt.IsZero() {
		startedAt = completedAt
	}

	record := InvocationRecord{
		InvocationID:        result.InvocationID,
		ServerlessRequestID: result.ServerlessRequestID,
		Mode:                result.Mode,
		WorkerName:          result.WorkerName,
		WorkerNamespace:     result.WorkerNamespace,
		StatusCode:          result.StatusCode,
		ContentType:         result.ContentType,
		Headers:             cloneStringMap(result.Headers),
		BodyBytes:           int64(len(result.Body)),
		Error:               result.Error,
		StartedAt:           startedAt,
		CompletedAt:         completedAt,
		StoredAt:            time.Now().UTC(),
	}

	if int64(len(result.Body)) <= maxInlineBodyBytes {
		record.BodyInline = append([]byte(nil), result.Body...)
		return record, nil
	}

	record.BodyTruncated = true
	return record, nil
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}
