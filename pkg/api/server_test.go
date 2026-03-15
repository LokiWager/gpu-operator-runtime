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

func newOperatorBackedHandler(t *testing.T) (http.Handler, *service.Service, context.CancelFunc) {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := runtimev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme error: %v", err)
	}
	operatorClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&runtimev1alpha1.StockPool{}).
		Build()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(nil, operatorClient, logger)
	h := NewServer(svc, logger)

	ctx, cancel := context.WithCancel(context.Background())
	go svc.StartOperatorJobWorker(ctx)

	return h, svc, cancel
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

func TestServer_Swagger(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/swagger/index.html", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestServer_CreateStockPoolJobWithoutOperator(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/operator/stockpools", strings.NewReader(`{"operationID":"op-a","specName":"g1.1","replicas":1}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestServer_CreateStockPoolJob(t *testing.T) {
	h, _, cancel := newOperatorBackedHandler(t)
	defer cancel()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/operator/stockpools", strings.NewReader(`{"operationID":"op-create-1","name":"pool-a","namespace":"default","specName":"g1.1","replicas":1}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestServer_CreateStockPoolJob_IsIdempotent(t *testing.T) {
	h, _, cancel := newOperatorBackedHandler(t)
	defer cancel()

	body := `{"operationID":"op-create-2","name":"pool-b","namespace":"default","specName":"g2.1","replicas":1}`

	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/operator/stockpools", strings.NewReader(body))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, req1)
	if w1.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w1.Code, w1.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/operator/stockpools", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w2.Code, w2.Body.String())
	}
}

func TestServer_CreateStockPoolJob_RejectsConflict(t *testing.T) {
	h, _, cancel := newOperatorBackedHandler(t)
	defer cancel()

	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/operator/stockpools", strings.NewReader(`{"operationID":"op-create-3","name":"pool-c","namespace":"default","specName":"g1.1","replicas":1}`))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, req1)
	if w1.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w1.Code, w1.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/operator/stockpools", strings.NewReader(`{"operationID":"op-create-3","name":"pool-c","namespace":"default","specName":"g2.1","replicas":1}`))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	if w2.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", w2.Code, w2.Body.String())
	}
}
