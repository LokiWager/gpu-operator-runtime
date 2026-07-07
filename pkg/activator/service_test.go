package activator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
	"github.com/loki/gpu-operator-runtime/pkg/domain"
	"github.com/loki/gpu-operator-runtime/pkg/serverless"
	runtimeService "github.com/loki/gpu-operator-runtime/pkg/service"
)

type fakeRuntimeControl struct {
	listUnits   []domain.GPUUnitRuntime
	createUnit  domain.GPUUnitRuntime
	getUnits    map[string]domain.GPUUnitRuntime
	createCalls []runtimeService.CreateGPUUnitRequest
	deleteCalls []string
}

func (f *fakeRuntimeControl) ListGPUUnits(_ context.Context, _ string) ([]domain.GPUUnitRuntime, error) {
	out := make([]domain.GPUUnitRuntime, len(f.listUnits))
	copy(out, f.listUnits)
	return out, nil
}

func (f *fakeRuntimeControl) CreateGPUUnit(_ context.Context, req runtimeService.CreateGPUUnitRequest) (domain.GPUUnitRuntime, bool, error) {
	f.createCalls = append(f.createCalls, req)
	unit := f.createUnit
	if unit.Name == "" {
		unit.Name = req.Name
	}
	if unit.Namespace == "" {
		unit.Namespace = runtimev1alpha1.DefaultInstanceNamespace
	}
	if unit.Serverless.RequestID == "" {
		unit.Serverless = req.Serverless
	}
	return unit, true, nil
}

func (f *fakeRuntimeControl) GetGPUUnit(_ context.Context, _, name string) (domain.GPUUnitRuntime, error) {
	return f.getUnits[name], nil
}

func (f *fakeRuntimeControl) DeleteGPUUnit(_ context.Context, namespace, name string) error {
	f.deleteCalls = append(f.deleteCalls, namespace+"/"+name)
	return nil
}

type fakeDispatchPublisher struct {
	last serverless.WorkerDispatchMessage
	err  error
}

func (f *fakeDispatchPublisher) PublishWorkerDispatch(_ context.Context, msg serverless.WorkerDispatchMessage) error {
	if f.err != nil {
		return f.err
	}
	f.last = msg
	return nil
}

type fakeResultPublisher struct {
	last  serverless.InvocationResultMessage
	calls int
}

func (f *fakeResultPublisher) PublishInvocationResult(_ context.Context, result serverless.InvocationResultMessage) error {
	f.calls++
	f.last = result
	return nil
}

func TestServiceProcessInvocationDispatchesReadyWorker(t *testing.T) {
	runtime := &fakeRuntimeControl{
		listUnits: []domain.GPUUnitRuntime{{
			Name:      "unit-sd-webui-a",
			Namespace: runtimev1alpha1.DefaultInstanceNamespace,
			Phase:     runtimev1alpha1.PhaseReady,
			AccessURL: "http://unit-sd-webui-a.runtime-instance.svc.cluster.local:8080",
			Serverless: runtimev1alpha1.GPUUnitServerlessSpec{
				Enabled:   true,
				RequestID: "sd-webui",
			},
		}},
	}
	dispatches := &fakeDispatchPublisher{}
	results := &fakeResultPublisher{}
	cfg := mustActivatorConfig(t)
	svc := New(runtime, dispatches, results, slog.New(slog.NewTextHandler(io.Discard, nil)), cfg)

	err := svc.ProcessInvocation(context.Background(), serverless.InvocationMessage{
		InvocationID:        "inv-1",
		ServerlessRequestID: "sd-webui",
		Mode:                serverless.InvocationModeSync,
		ContentType:         "application/json",
		Attributes: map[string]string{
			"path":   "/generate",
			"method": "POST",
		},
		Payload:        []byte(`{"prompt":"hello"}`),
		ResultSubject:  "runtime.serverless.result.sd-webui",
		MetricsSubject: "runtime.serverless.metrics.sd-webui",
		ReplySubject:   "_INBOX.sync.inv-1",
	})
	if err != nil {
		t.Fatalf("process invocation: %v", err)
	}
	if dispatches.last.WorkerName != "unit-sd-webui-a" {
		t.Fatalf("expected dispatch to ready worker, got %+v", dispatches.last)
	}
	if dispatches.last.ReplySubject != "_INBOX.sync.inv-1" {
		t.Fatalf("expected reply subject to be forwarded, got %+v", dispatches.last)
	}
	if results.calls != 0 {
		t.Fatalf("expected no failure result publication, got %+v", results.last)
	}
}

func TestServiceProcessInvocationCreatesWorkerWhenNoReadyWorkerExists(t *testing.T) {
	cfg := mustActivatorConfig(t)
	cfg.WorkerReadyWait = "50ms"
	cfg.WorkerPollInterval = "10ms"
	cfg = mustNormalizedActivatorConfig(t, cfg)

	templateUnit := domain.GPUUnitRuntime{
		Name:      "unit-sd-webui-template",
		Namespace: runtimev1alpha1.DefaultInstanceNamespace,
		Phase:     runtimev1alpha1.PhaseProgressing,
		PackageID: "gpu-rtx3080-2x-cpu10-mem40g",
		SpecName:  "gpu.rtx3080.2x.10c.40g",
		Image:     "python:3.12",
		CPU:       "10",
		Memory:    "40Gi",
		GPU:       2,
		Allocation: runtimev1alpha1.GPUUnitAllocationSpec{
			DeviceClassName:  "nvidia-rtx-3080",
			ClaimName:        "unit-sd-webui-template-gpu",
			ClaimRequestName: runtimev1alpha1.UnitDRAClaimRequestName,
			Count:            2,
		},
		Template: runtimev1alpha1.GPUUnitTemplate{
			Ports: []runtimev1alpha1.GPUUnitPortSpec{{Name: "http", Port: 8080}},
		},
		Access: runtimev1alpha1.GPUUnitAccess{PrimaryPort: "http", Scheme: "http"},
		Serverless: runtimev1alpha1.GPUUnitServerlessSpec{
			Enabled:   true,
			RequestID: "sd-webui",
		},
	}
	runtime := &fakeRuntimeControl{
		listUnits:  []domain.GPUUnitRuntime{templateUnit},
		createUnit: domain.GPUUnitRuntime{Name: "unit-sd-webui-new", Namespace: runtimev1alpha1.DefaultInstanceNamespace},
		getUnits: map[string]domain.GPUUnitRuntime{
			"unit-sd-webui-new": {
				Name:      "unit-sd-webui-new",
				Namespace: runtimev1alpha1.DefaultInstanceNamespace,
				Phase:     runtimev1alpha1.PhaseReady,
				AccessURL: "http://unit-sd-webui-new.runtime-instance.svc.cluster.local:8080",
				Serverless: runtimev1alpha1.GPUUnitServerlessSpec{
					Enabled:   true,
					RequestID: "sd-webui",
				},
			},
		},
	}
	dispatches := &fakeDispatchPublisher{}
	results := &fakeResultPublisher{}
	svc := New(runtime, dispatches, results, slog.New(slog.NewTextHandler(io.Discard, nil)), cfg)

	err := svc.ProcessInvocation(context.Background(), serverless.InvocationMessage{
		InvocationID:        "inv-create-1",
		ServerlessRequestID: "sd-webui",
		Mode:                serverless.InvocationModeAsync,
		Payload:             []byte(`{"prompt":"clone"}`),
	})
	if err != nil {
		t.Fatalf("process invocation: %v", err)
	}
	if len(runtime.createCalls) != 1 {
		t.Fatalf("expected one worker creation, got %d", len(runtime.createCalls))
	}
	if runtime.createCalls[0].OperationID != "activate-inv-create-1" {
		t.Fatalf("unexpected create request: %+v", runtime.createCalls[0])
	}
	if runtime.createCalls[0].PackageID != "gpu-rtx3080-2x-cpu10-mem40g" {
		t.Fatalf("expected package to be cloned, got %+v", runtime.createCalls[0])
	}
	if runtime.createCalls[0].Allocation.DeviceClassName != "nvidia-rtx-3080" || runtime.createCalls[0].Allocation.ClaimName != "" {
		t.Fatalf("expected DRA allocation with regenerated claim name, got %+v", runtime.createCalls[0].Allocation)
	}
	if dispatches.last.WorkerName != "unit-sd-webui-new" {
		t.Fatalf("expected dispatch to cloned worker, got %+v", dispatches.last)
	}
}

func TestServiceProcessInvocationRetriesWhenDispatchFails(t *testing.T) {
	runtime := &fakeRuntimeControl{
		listUnits: []domain.GPUUnitRuntime{{
			Name:      "unit-sd-webui-a",
			Namespace: runtimev1alpha1.DefaultInstanceNamespace,
			Phase:     runtimev1alpha1.PhaseReady,
			AccessURL: "http://unit-sd-webui-a.runtime-instance.svc.cluster.local:8080",
			Serverless: runtimev1alpha1.GPUUnitServerlessSpec{
				Enabled:   true,
				RequestID: "sd-webui",
			},
		}},
	}
	dispatches := &fakeDispatchPublisher{err: errors.New("dispatch queue unavailable")}
	results := &fakeResultPublisher{}
	svc := New(runtime, dispatches, results, slog.New(slog.NewTextHandler(io.Discard, nil)), mustActivatorConfig(t))

	err := svc.ProcessInvocation(context.Background(), serverless.InvocationMessage{
		InvocationID:        "inv-err-1",
		ServerlessRequestID: "sd-webui",
		Mode:                serverless.InvocationModeSync,
	})
	if err == nil {
		t.Fatalf("expected dispatch error")
	}
	if results.calls != 0 {
		t.Fatalf("expected dispatch failure to be retried instead of published as a terminal result, got %+v", results.last)
	}
}

func TestServiceProcessInvocationPublishesTerminalFailureWhenNoTemplateExists(t *testing.T) {
	runtime := &fakeRuntimeControl{}
	dispatches := &fakeDispatchPublisher{}
	results := &fakeResultPublisher{}
	svc := New(runtime, dispatches, results, slog.New(slog.NewTextHandler(io.Discard, nil)), mustActivatorConfig(t))

	err := svc.ProcessInvocation(context.Background(), serverless.InvocationMessage{
		InvocationID:        "inv-no-template",
		ServerlessRequestID: "sd-webui",
		Mode:                serverless.InvocationModeAsync,
	})
	if err != nil {
		t.Fatalf("process invocation: %v", err)
	}
	if results.calls != 1 {
		t.Fatalf("expected terminal failure result, got %d calls", results.calls)
	}
	if results.last.State != serverless.InvocationStateFailed || results.last.FailureClass != serverless.InvocationFailureNoWorkerTemplate {
		t.Fatalf("expected no-template failure result, got %+v", results.last)
	}
}

func TestServiceProcessInvocationBackpressuresWhenPendingWorkerLimitReached(t *testing.T) {
	cfg := mustActivatorConfig(t)
	cfg.MaxPendingWorkersPerRequestID = 1
	cfg.WorkerReadyWait = "50ms"
	cfg.WorkerPollInterval = "10ms"
	cfg = mustNormalizedActivatorConfig(t, cfg)
	requestID := "sd-webui"
	template := domain.GPUUnitRuntime{
		Name:      "unit-sd-webui-template",
		Namespace: runtimev1alpha1.DefaultInstanceNamespace,
		Phase:     runtimev1alpha1.PhaseProgressing,
		Serverless: runtimev1alpha1.GPUUnitServerlessSpec{
			Enabled:   true,
			RequestID: requestID,
		},
	}
	pending := domain.GPUUnitRuntime{
		Name:      generatedWorkerName(requestID, "inv-pending"),
		Namespace: runtimev1alpha1.DefaultInstanceNamespace,
		Phase:     runtimev1alpha1.PhaseProgressing,
		Serverless: runtimev1alpha1.GPUUnitServerlessSpec{
			Enabled:   true,
			RequestID: requestID,
		},
	}
	runtime := &fakeRuntimeControl{listUnits: []domain.GPUUnitRuntime{template, pending}}
	dispatches := &fakeDispatchPublisher{}
	results := &fakeResultPublisher{}
	svc := New(runtime, dispatches, results, slog.New(slog.NewTextHandler(io.Discard, nil)), cfg)

	err := svc.ProcessInvocation(context.Background(), serverless.InvocationMessage{
		InvocationID:        "inv-backpressure",
		ServerlessRequestID: requestID,
		Mode:                serverless.InvocationModeAsync,
	})
	if err == nil {
		t.Fatalf("expected backpressure error")
	}
	var pressure *backpressureError
	if !errors.As(err, &pressure) {
		t.Fatalf("expected backpressure error, got %v", err)
	}
	if len(runtime.createCalls) != 0 || results.calls != 0 {
		t.Fatalf("expected no worker creation or terminal result, creates=%d results=%d", len(runtime.createCalls), results.calls)
	}
}

func TestGeneratedWorkerNameUsesInvocationID(t *testing.T) {
	name := generatedWorkerName("sd_webui", "inv-1234567890")
	if name != "unit-sd-webui-worker-12345678" {
		t.Fatalf("unexpected worker name: %s", name)
	}
}

func TestGeneratedWorkerNameSanitizesCustomInvocationID(t *testing.T) {
	name := generatedWorkerName("sd.webui", "CUSTOM/request:001")
	if name != "unit-sd-webui-worker-custom-r" {
		t.Fatalf("unexpected worker name: %s", name)
	}
}

func TestLifecycleManagerCreatesPrewarmWorkers(t *testing.T) {
	cfg := mustActivatorConfig(t)
	template := domain.GPUUnitRuntime{
		Name:      "sd-webui-template",
		Namespace: runtimev1alpha1.DefaultInstanceNamespace,
		Phase:     runtimev1alpha1.PhaseProgressing,
		SpecName:  "g1.1",
		Image:     "python:3.12",
		Template: runtimev1alpha1.GPUUnitTemplate{
			Ports: []runtimev1alpha1.GPUUnitPortSpec{{Name: "http", Port: 8080}},
		},
		Access: runtimev1alpha1.GPUUnitAccess{PrimaryPort: "http", Scheme: "http"},
		Serverless: runtimev1alpha1.GPUUnitServerlessSpec{
			Enabled:           true,
			RequestID:         "sd-webui",
			MinAvailableCount: 2,
		},
	}
	runtime := &fakeRuntimeControl{listUnits: []domain.GPUUnitRuntime{template}}
	lifecycle := NewLifecycleManager(runtime, NewWorkerRegistry(), slog.New(slog.NewTextHandler(io.Discard, nil)), cfg)

	if err := lifecycle.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile lifecycle: %v", err)
	}
	if len(runtime.createCalls) != 1 {
		t.Fatalf("expected one prewarm worker creation, got %d", len(runtime.createCalls))
	}
	if runtime.createCalls[0].Serverless.RequestID != "sd-webui" {
		t.Fatalf("expected serverless policy to be copied, got %+v", runtime.createCalls[0])
	}
	if !strings.Contains(runtime.createCalls[0].Name, "-worker-") {
		t.Fatalf("expected managed worker name, got %s", runtime.createCalls[0].Name)
	}
}

func TestLifecycleManagerDeletesIdleManagedWorkers(t *testing.T) {
	cfg := mustActivatorConfig(t)
	requestID := "sd-webui"
	template := domain.GPUUnitRuntime{
		Name:      "sd-webui-template",
		Namespace: runtimev1alpha1.DefaultInstanceNamespace,
		Phase:     runtimev1alpha1.PhaseReady,
		AccessURL: "http://sd-webui-template.runtime-instance.svc.cluster.local:8080",
		Serverless: runtimev1alpha1.GPUUnitServerlessSpec{
			Enabled:            true,
			RequestID:          requestID,
			MinAvailableCount:  1,
			IdleTimeoutSeconds: 1,
		},
	}
	managedWorker := domain.GPUUnitRuntime{
		Name:      generatedWorkerName(requestID, "inv-aaaaaaaa"),
		Namespace: runtimev1alpha1.DefaultInstanceNamespace,
		Phase:     runtimev1alpha1.PhaseReady,
		AccessURL: "http://managed.runtime-instance.svc.cluster.local:8080",
		Serverless: runtimev1alpha1.GPUUnitServerlessSpec{
			Enabled:            true,
			RequestID:          requestID,
			MinAvailableCount:  1,
			IdleTimeoutSeconds: 1,
		},
	}
	runtime := &fakeRuntimeControl{listUnits: []domain.GPUUnitRuntime{template, managedWorker}}
	lifecycle := NewLifecycleManager(runtime, NewWorkerRegistry(), slog.New(slog.NewTextHandler(io.Discard, nil)), cfg)
	lifecycle.ObserveMetric(serverless.WorkerMetricMessage{
		ServerlessRequestID: requestID,
		WorkerName:          managedWorker.Name,
		WorkerNamespace:     managedWorker.Namespace,
		EventType:           serverless.WorkerMetricEventRegistered,
		Inflight:            0,
		ReportedAt:          time.Now().Add(-2 * time.Second),
	})

	if err := lifecycle.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile lifecycle: %v", err)
	}
	if len(runtime.deleteCalls) != 1 {
		t.Fatalf("expected one idle worker deletion, got %d", len(runtime.deleteCalls))
	}
	if runtime.deleteCalls[0] != runtimev1alpha1.DefaultInstanceNamespace+"/"+managedWorker.Name {
		t.Fatalf("unexpected delete calls: %+v", runtime.deleteCalls)
	}
}

func TestConfigAckWaitDurationExceedsReadyWait(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Serverless.URL = "nats://127.0.0.1:4222"
	cfg.WorkerReadyWait = "30s"
	normalized, err := cfg.Normalized()
	if err != nil {
		t.Fatalf("normalize config: %v", err)
	}
	if normalized.AckWaitDuration() <= 30*time.Second {
		t.Fatalf("expected ack wait headroom, got %s", normalized.AckWaitDuration())
	}
}

func mustActivatorConfig(t *testing.T) Config {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Serverless.URL = "nats://127.0.0.1:4222"
	return mustNormalizedActivatorConfig(t, cfg)
}

func mustNormalizedActivatorConfig(t *testing.T, cfg Config) Config {
	t.Helper()
	normalized, err := cfg.Normalized()
	if err != nil {
		t.Fatalf("normalize config: %v", err)
	}
	return normalized
}
