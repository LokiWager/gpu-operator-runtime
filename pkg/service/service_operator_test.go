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
		WithStatusSubresource(&runtimev1alpha1.GPUUnit{}).
		WithStatusSubresource(&runtimev1alpha1.GPUStorage{}).
		Build()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(nil, cl, logger)

	ctx, cancel := context.WithCancel(context.Background())
	go svc.StartOperatorJobWorker(ctx)

	return svc, ctx, cancel
}

func TestService_CreateStockUnitsAsync(t *testing.T) {
	svc, ctx, cancel := newOperatorService(t)
	defer cancel()

	job, accepted, err := svc.CreateStockUnitsAsync(ctx, CreateStockUnitsRequest{
		OperationID: "op-create-1",
		SpecName:    "g1.1",
		Memory:      "16Gi",
		GPU:         1,
		Replicas:    2,
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

	for _, name := range []string{
		generatedStockUnitName("g1.1", "op-create-1", 0),
		generatedStockUnitName("g1.1", "op-create-1", 1),
	} {
		var unit runtimev1alpha1.GPUUnit
		if err := svc.operator.Get(ctx, types.NamespacedName{Name: name, Namespace: runtimev1alpha1.DefaultStockNamespace}, &unit); err != nil {
			t.Fatalf("get stock unit error: %v", err)
		}
		if unit.Spec.Image != runtimev1alpha1.StockReservationImage {
			t.Fatalf("expected stock image %s, got %s", runtimev1alpha1.StockReservationImage, unit.Spec.Image)
		}
		if len(unit.Spec.Template.Command) != 0 || len(unit.Spec.Template.Ports) != 0 {
			t.Fatalf("expected stock template to stay empty, got %+v", unit.Spec.Template)
		}
		if got := unit.GetAnnotations()[runtimev1alpha1.AnnotationOperationID]; got != "op-create-1" {
			t.Fatalf("expected operation annotation, got %q", got)
		}
	}
}

func TestService_CreateStockUnitsAsync_IsIdempotent(t *testing.T) {
	svc, ctx, cancel := newOperatorService(t)
	defer cancel()

	req := CreateStockUnitsRequest{
		OperationID: "op-same-1",
		SpecName:    "g1.1",
		Replicas:    1,
	}

	job1, accepted, err := svc.CreateStockUnitsAsync(ctx, req)
	if err != nil {
		t.Fatalf("first create error: %v", err)
	}
	if !accepted {
		t.Fatalf("expected first create to be accepted")
	}

	job2, accepted, err := svc.CreateStockUnitsAsync(ctx, req)
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

func TestService_CreateStockUnitsAsync_RejectsOperationConflict(t *testing.T) {
	svc, ctx, cancel := newOperatorService(t)
	defer cancel()

	req := CreateStockUnitsRequest{
		OperationID: "op-conflict-1",
		SpecName:    "g1.1",
		Replicas:    1,
	}

	if _, _, err := svc.CreateStockUnitsAsync(ctx, req); err != nil {
		t.Fatalf("first create error: %v", err)
	}

	_, _, err := svc.CreateStockUnitsAsync(ctx, CreateStockUnitsRequest{
		OperationID: "op-conflict-1",
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

func TestService_CreateStockUnitsAsync_ValidatesMemory(t *testing.T) {
	svc, ctx, cancel := newOperatorService(t)
	defer cancel()

	_, _, err := svc.CreateStockUnitsAsync(ctx, CreateStockUnitsRequest{
		OperationID: "op-invalid-memory",
		SpecName:    "g1.1",
		Replicas:    1,
		Memory:      "not-a-quantity",
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
