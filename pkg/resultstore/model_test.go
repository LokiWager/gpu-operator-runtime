package resultstore

import (
	"bytes"
	"testing"
	"time"

	"github.com/loki/gpu-operator-runtime/pkg/serverless"
)

func TestScyllaConfigNormalizedDefaults(t *testing.T) {
	cfg, err := (ScyllaConfig{
		Hosts: []string{" 127.0.0.1:9042 ", "127.0.0.1:9042"},
	}).Normalized()
	if err != nil {
		t.Fatalf("normalize config: %v", err)
	}
	if len(cfg.Hosts) != 1 || cfg.Hosts[0] != "127.0.0.1:9042" {
		t.Fatalf("expected normalized hosts, got %+v", cfg.Hosts)
	}
	if cfg.Keyspace != DefaultKeyspace {
		t.Fatalf("expected default keyspace %s, got %s", DefaultKeyspace, cfg.Keyspace)
	}
	if cfg.ResultsTable != DefaultResultsTable {
		t.Fatalf("expected default table %s, got %s", DefaultResultsTable, cfg.ResultsTable)
	}
	if cfg.MaxInlineBodyBytes != DefaultMaxInlineBodyBytes {
		t.Fatalf("expected default inline body limit, got %d", cfg.MaxInlineBodyBytes)
	}
}

func TestScyllaConfigRejectsInvalidIdentifier(t *testing.T) {
	_, err := (ScyllaConfig{
		Hosts:    []string{"127.0.0.1:9042"},
		Keyspace: "runtime-serverless",
	}).Normalized()
	if err == nil {
		t.Fatalf("expected invalid keyspace error")
	}
}

func TestRecordFromResultStoresSmallBodyInline(t *testing.T) {
	completedAt := time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)
	record, err := RecordFromResult(serverless.InvocationResultMessage{
		InvocationID:        "inv-1",
		ServerlessRequestID: "sd-webui",
		Mode:                serverless.InvocationModeAsync,
		StatusCode:          200,
		Headers:             map[string]string{"x-model": "demo"},
		Body:                []byte(`{"ok":true}`),
		CompletedAt:         completedAt,
	}, 64)
	if err != nil {
		t.Fatalf("convert result: %v", err)
	}
	if record.BodyTruncated {
		t.Fatalf("expected inline body, got truncated")
	}
	if !bytes.Equal(record.BodyInline, []byte(`{"ok":true}`)) {
		t.Fatalf("unexpected body: %s", record.BodyInline)
	}
	if record.StartedAt != completedAt {
		t.Fatalf("expected startedAt fallback to completedAt, got %s", record.StartedAt)
	}
}

func TestRecordFromResultTruncatesLargeBody(t *testing.T) {
	record, err := RecordFromResult(serverless.InvocationResultMessage{
		InvocationID:        "inv-2",
		ServerlessRequestID: "sd-webui",
		Body:                []byte(`{"large":true}`),
	}, 4)
	if err != nil {
		t.Fatalf("convert result: %v", err)
	}
	if !record.BodyTruncated {
		t.Fatalf("expected body to be marked truncated")
	}
	if len(record.BodyInline) != 0 {
		t.Fatalf("expected no inline body when truncated, got %q", record.BodyInline)
	}
	if record.BodyBytes != int64(len(`{"large":true}`)) {
		t.Fatalf("expected original body byte count, got %d", record.BodyBytes)
	}
}
