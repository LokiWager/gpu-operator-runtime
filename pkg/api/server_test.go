package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
	"github.com/loki/gpu-operator-runtime/pkg/service"
)

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(nil, nil, logger)
	return NewServer(svc, logger)
}

func TestServer_Health(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestServer_CreateStockPoolJobWithoutOperator(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/operator/stockpools", strings.NewReader(`{"specName":"g1.1","replicas":1}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestServer_CreateStockPoolJob(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := runtimev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme error: %v", err)
	}
	operatorClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(nil, operatorClient, logger)
	h := NewServer(svc, logger)

	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go svc.StartOperatorJobWorker(ctx)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/operator/stockpools", strings.NewReader(`{"name":"pool-a","namespace":"default","specName":"g1.1","replicas":1}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestServer_CreateStockPoolJobWithRuntimeFields(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := runtimev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme error: %v", err)
	}
	operatorClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(nil, operatorClient, logger)
	h := NewServer(svc, logger)

	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go svc.StartOperatorJobWorker(ctx)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/operator/stockpools", strings.NewReader(`{"name":"pool-b","namespace":"default","specName":"g2.1","image":"nginx:1.27","memory":"32Gi","gpu":2,"replicas":1}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
}
