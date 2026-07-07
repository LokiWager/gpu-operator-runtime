package workersidecar

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/loki/gpu-operator-runtime/pkg/serverless"
)

type fakeFrameworkClient struct {
	healthErr error
	resp      serverless.FrameworkInvocationResponse
	err       error
}

func (f *fakeFrameworkClient) Health(context.Context) error {
	return f.healthErr
}

func (f *fakeFrameworkClient) Invoke(context.Context, serverless.FrameworkInvocationRequest) (serverless.FrameworkInvocationResponse, error) {
	return f.resp, f.err
}

type fakeResultPublisher struct {
	last  serverless.InvocationResultMessage
	calls int
	err   error
}

func (f *fakeResultPublisher) PublishInvocationResult(_ context.Context, result serverless.InvocationResultMessage) error {
	if f.err != nil {
		return f.err
	}
	f.last = result
	f.calls++
	return nil
}

type fakeMetricsPublisher struct {
	last  serverless.WorkerMetricMessage
	calls int
	err   error
}

func (f *fakeMetricsPublisher) PublishWorkerMetric(_ context.Context, metric serverless.WorkerMetricMessage) error {
	if f.err != nil {
		return f.err
	}
	f.last = metric
	f.calls++
	return nil
}

type fakeDispatchConsumer struct {
	called bool
	last   struct {
		durable    string
		requestID  string
		workerName string
	}
}

func (f *fakeDispatchConsumer) ConsumeWorkerDispatches(
	ctx context.Context,
	durable string,
	requestID string,
	workerName string,
	opts serverless.ConsumerOptions,
	handler func(context.Context, serverless.WorkerDispatchMessage) error,
) error {
	f.called = true
	f.last.durable = durable
	f.last.requestID = requestID
	f.last.workerName = workerName
	if opts.AckWait <= 0 {
		return errors.New("ackWait should be > 0")
	}
	return nil
}

func TestServiceHandleDispatchPublishesSuccessResult(t *testing.T) {
	cfg := mustSidecarConfig(t)
	results := &fakeResultPublisher{}
	metrics := &fakeMetricsPublisher{}
	svc := New(cfg, &fakeFrameworkClient{
		resp: serverless.FrameworkInvocationResponse{
			StatusCode:  http.StatusCreated,
			ContentType: "application/json",
			Body:        []byte(`{"ok":true}`),
		},
	}, results, metrics, slog.New(slog.NewTextHandler(io.Discard, nil)))

	err := svc.HandleDispatch(context.Background(), serverless.WorkerDispatchMessage{
		InvocationID:        "inv-1",
		ServerlessRequestID: cfg.ServerlessRequestID,
		WorkerName:          cfg.WorkerName,
		WorkerNamespace:     cfg.WorkerNamespace,
		Mode:                serverless.InvocationModeSync,
		ReplySubject:        "_INBOX.sync.inv-1",
	})
	if err != nil {
		t.Fatalf("handle dispatch: %v", err)
	}
	if results.calls != 1 {
		t.Fatalf("expected one result publish, got %d", results.calls)
	}
	if results.last.StatusCode != http.StatusCreated {
		t.Fatalf("expected result status 201, got %+v", results.last)
	}
	if results.last.State != serverless.InvocationStateSucceeded {
		t.Fatalf("expected succeeded state, got %+v", results.last)
	}
	if metrics.calls == 0 {
		t.Fatalf("expected worker metrics to be published")
	}
}

func TestServiceHandleDispatchPublishesFailureResult(t *testing.T) {
	cfg := mustSidecarConfig(t)
	results := &fakeResultPublisher{}
	metrics := &fakeMetricsPublisher{}
	svc := New(cfg, &fakeFrameworkClient{err: errors.New("framework unavailable")}, results, metrics, slog.New(slog.NewTextHandler(io.Discard, nil)))

	err := svc.HandleDispatch(context.Background(), serverless.WorkerDispatchMessage{
		InvocationID:        "inv-2",
		ServerlessRequestID: cfg.ServerlessRequestID,
		WorkerName:          cfg.WorkerName,
		WorkerNamespace:     cfg.WorkerNamespace,
		Mode:                serverless.InvocationModeAsync,
	})
	if err != nil {
		t.Fatalf("handle dispatch: %v", err)
	}
	if results.last.StatusCode != http.StatusBadGateway || results.last.Error == "" {
		t.Fatalf("expected bad gateway result, got %+v", results.last)
	}
	if results.last.State != serverless.InvocationStateFailed || results.last.FailureClass != serverless.InvocationFailureFrameworkError {
		t.Fatalf("expected framework failure classification, got %+v", results.last)
	}
}

func TestServiceHandleDispatchClassifiesFrameworkTimeout(t *testing.T) {
	cfg := mustSidecarConfig(t)
	results := &fakeResultPublisher{}
	metrics := &fakeMetricsPublisher{}
	svc := New(cfg, &fakeFrameworkClient{err: context.DeadlineExceeded}, results, metrics, slog.New(slog.NewTextHandler(io.Discard, nil)))

	err := svc.HandleDispatch(context.Background(), serverless.WorkerDispatchMessage{
		InvocationID:        "inv-timeout",
		ServerlessRequestID: cfg.ServerlessRequestID,
		WorkerName:          cfg.WorkerName,
		WorkerNamespace:     cfg.WorkerNamespace,
		Mode:                serverless.InvocationModeAsync,
	})
	if err != nil {
		t.Fatalf("handle dispatch: %v", err)
	}
	if results.last.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("expected gateway timeout result, got %+v", results.last)
	}
	if results.last.State != serverless.InvocationStateExpired || results.last.FailureClass != serverless.InvocationFailureFrameworkTimeout {
		t.Fatalf("expected timeout classification, got %+v", results.last)
	}
}

func TestServiceRunPublishesRegistrationAndStartsConsumer(t *testing.T) {
	cfg := mustSidecarConfig(t)
	results := &fakeResultPublisher{}
	metrics := &fakeMetricsPublisher{}
	consumer := &fakeDispatchConsumer{}
	svc := New(cfg, &fakeFrameworkClient{}, results, metrics, slog.New(slog.NewTextHandler(io.Discard, nil)))

	err := svc.Run(context.Background(), consumer)
	if err != nil {
		t.Fatalf("run sidecar service: %v", err)
	}
	if !consumer.called {
		t.Fatalf("expected consumer to be called")
	}
	if metrics.calls == 0 || metrics.last.EventType != serverless.WorkerMetricEventRegistered {
		t.Fatalf("expected registration metric, got %+v", metrics.last)
	}
}

func mustSidecarConfig(t *testing.T) Config {
	t.Helper()
	cfg, err := Config{
		Serverless: serverless.NATSConfig{
			URL: "nats://127.0.0.1:4222",
		},
		WorkerName:          "unit-sd-webui-a",
		WorkerNamespace:     "runtime-instance",
		ServerlessRequestID: "sd-webui",
	}.Normalized()
	if err != nil {
		t.Fatalf("normalize sidecar config: %v", err)
	}
	return cfg
}
