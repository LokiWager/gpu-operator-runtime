package resultconsumer

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/loki/gpu-operator-runtime/pkg/serverless"
)

type fakeResultStore struct {
	last  serverless.InvocationResultMessage
	calls int
}

func (f *fakeResultStore) SaveInvocationResult(_ context.Context, result serverless.InvocationResultMessage) error {
	f.calls++
	f.last = result
	return nil
}

func TestConfigNormalizedDefaults(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Serverless.URL = "nats://127.0.0.1:4222"
	cfg.Scylla.Hosts = []string{"127.0.0.1:9042"}

	normalized, err := cfg.Normalized()
	if err != nil {
		t.Fatalf("normalize config: %v", err)
	}
	if normalized.ConsumerName != defaultConsumerName {
		t.Fatalf("expected default consumer name %s, got %s", defaultConsumerName, normalized.ConsumerName)
	}
	if normalized.AckWaitDuration() != 30*time.Second {
		t.Fatalf("expected 30s ack wait, got %s", normalized.AckWaitDuration())
	}
}

func TestConfigRequiresQueueURL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Scylla.Hosts = []string{"127.0.0.1:9042"}

	if _, err := cfg.Normalized(); err == nil {
		t.Fatalf("expected missing serverless url error")
	}
}

func TestServiceHandleResultPersistsResult(t *testing.T) {
	store := &fakeResultStore{}
	cfg := mustResultConsumerConfig(t)
	svc := New(store, slog.New(slog.NewTextHandler(io.Discard, nil)), cfg)

	err := svc.HandleResult(context.Background(), serverless.InvocationResultMessage{
		InvocationID:        "inv-1",
		ServerlessRequestID: "sd-webui",
		StatusCode:          200,
		Body:                []byte(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("handle result: %v", err)
	}
	if store.calls != 1 {
		t.Fatalf("expected one store call, got %d", store.calls)
	}
	if store.last.InvocationID != "inv-1" {
		t.Fatalf("unexpected stored result: %+v", store.last)
	}
}

func TestServiceHandleResultRejectsMissingInvocationID(t *testing.T) {
	store := &fakeResultStore{}
	svc := New(store, slog.New(slog.NewTextHandler(io.Discard, nil)), mustResultConsumerConfig(t))

	err := svc.HandleResult(context.Background(), serverless.InvocationResultMessage{
		ServerlessRequestID: "sd-webui",
	})
	if err == nil {
		t.Fatalf("expected missing invocation id error")
	}
	if store.calls != 0 {
		t.Fatalf("expected no store calls, got %d", store.calls)
	}
}

func mustResultConsumerConfig(t *testing.T) Config {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Serverless.URL = "nats://127.0.0.1:4222"
	cfg.Scylla.Hosts = []string{"127.0.0.1:9042"}
	normalized, err := cfg.Normalized()
	if err != nil {
		t.Fatalf("normalize config: %v", err)
	}
	return normalized
}
