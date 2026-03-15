package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	types "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
	"github.com/loki/gpu-operator-runtime/pkg/domain"
)

func newOperatorService(t *testing.T) (*Service, context.Context, context.CancelFunc) {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := runtimev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme error: %v", err)
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&runtimev1alpha1.StockPool{}).
		Build()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(nil, cl, logger)

	ctx, cancel := context.WithCancel(context.Background())
	go svc.StartOperatorJobWorker(ctx)

	return svc, ctx, cancel
}

func TestService_CreateStockPoolAsync(t *testing.T) {
	svc, ctx, cancel := newOperatorService(t)
	defer cancel()

	job, accepted, err := svc.CreateStockPoolAsync(ctx, CreateStockPoolRequest{
		OperationID: "op-create-1",
		Name:        "pool-g1",
		Namespace:   "default",
		SpecName:    "g1.1",
		Image:       "nginx:1.27",
		Memory:      "16Gi",
		GPU:         1,
		Replicas:    2,
		Template: runtimev1alpha1.StockPoolTemplate{
			Command: []string{"python"},
			Args:    []string{"-m", "http.server", "8080"},
			Ports: []runtimev1alpha1.StockPoolPortSpec{{
				Name: "http",
				Port: 8080,
			}},
		},
	})
	if err != nil {
		t.Fatalf("create async job error: %v", err)
	}
	if !accepted {
		t.Fatalf("expected new operation to be accepted")
	}
	if job.OperationID != "op-create-1" {
		t.Fatalf("expected operationID=op-create-1, got %s", job.OperationID)
	}

	waitForJobStatus(t, ctx, svc, job.ID, domain.OperatorJobSucceeded)

	var pool runtimev1alpha1.StockPool
	if err := svc.operator.Get(ctx, types.NamespacedName{Name: "pool-g1", Namespace: "default"}, &pool); err != nil {
		t.Fatalf("get stockpool error: %v", err)
	}
	if pool.Spec.Replicas != 2 {
		t.Fatalf("expected replicas=2, got %d", pool.Spec.Replicas)
	}
	if pool.Spec.Template.Command[0] != "python" {
		t.Fatalf("expected runtime command to be stored")
	}
	if got := pool.GetAnnotations()[runtimev1alpha1.AnnotationOperationID]; got != "op-create-1" {
		t.Fatalf("expected operation annotation, got %q", got)
	}
}

func TestService_CreateStockPoolAsync_IsIdempotent(t *testing.T) {
	svc, ctx, cancel := newOperatorService(t)
	defer cancel()

	req := CreateStockPoolRequest{
		OperationID: "op-same-1",
		Name:        "pool-g1",
		Namespace:   "default",
		SpecName:    "g1.1",
		Replicas:    1,
	}

	job1, accepted, err := svc.CreateStockPoolAsync(ctx, req)
	if err != nil {
		t.Fatalf("first create error: %v", err)
	}
	if !accepted {
		t.Fatalf("expected first create to be accepted")
	}

	job2, accepted, err := svc.CreateStockPoolAsync(ctx, req)
	if err != nil {
		t.Fatalf("second create error: %v", err)
	}
	if accepted {
		t.Fatalf("expected second create to reuse existing operation")
	}
	if job1.ID != job2.ID {
		t.Fatalf("expected same job id, got %s and %s", job1.ID, job2.ID)
	}
}

func TestService_CreateStockPoolAsync_RejectsOperationConflict(t *testing.T) {
	svc, ctx, cancel := newOperatorService(t)
	defer cancel()

	req := CreateStockPoolRequest{
		OperationID: "op-conflict-1",
		Name:        "pool-g1",
		Namespace:   "default",
		SpecName:    "g1.1",
		Replicas:    1,
	}

	if _, _, err := svc.CreateStockPoolAsync(ctx, req); err != nil {
		t.Fatalf("first create error: %v", err)
	}

	_, _, err := svc.CreateStockPoolAsync(ctx, CreateStockPoolRequest{
		OperationID: "op-conflict-1",
		Name:        "pool-g1",
		Namespace:   "default",
		SpecName:    "g2.1",
		Replicas:    1,
	})
	if err == nil {
		t.Fatalf("expected conflict error")
	}

	var conflictErr *ConflictError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("expected conflict error, got %T", err)
	}
}

func TestService_CreateStockPoolAsync_ValidatesTemplate(t *testing.T) {
	svc, ctx, cancel := newOperatorService(t)
	defer cancel()

	_, _, err := svc.CreateStockPoolAsync(ctx, CreateStockPoolRequest{
		OperationID: "op-invalid-template",
		Name:        "pool-g1",
		Namespace:   "default",
		SpecName:    "g1.1",
		Replicas:    1,
		Template: runtimev1alpha1.StockPoolTemplate{
			Ports: []runtimev1alpha1.StockPoolPortSpec{{Name: "http", Port: 70000}},
		},
	})
	if err == nil {
		t.Fatalf("expected validation error")
	}

	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected validation error, got %T", err)
	}
}

func waitForJobStatus(t *testing.T, ctx context.Context, svc *Service, operationID string, expected domain.OperatorJobStatus) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, err := svc.GetOperatorJob(ctx, operationID)
		if err != nil {
			t.Fatalf("get job error: %v", err)
		}
		if got.Status == expected {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job did not reach %s in time", expected)
}
