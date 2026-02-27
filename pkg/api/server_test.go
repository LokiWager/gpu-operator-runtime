package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/loki/gpu-operator-runtime/pkg/service"
	"github.com/loki/gpu-operator-runtime/pkg/store"
)

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(store.NewMemoryStore(), nil, logger)
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

func TestServer_CreateStockAndVM(t *testing.T) {
	h := newTestHandler(t)

	createStockReq := httptest.NewRequest(http.MethodPost, "/api/v1/stocks", strings.NewReader(`{"number":1,"specName":"g1.1"}`))
	createStockReq.Header.Set("Content-Type", "application/json")
	createStockW := httptest.NewRecorder()
	h.ServeHTTP(createStockW, createStockReq)
	if createStockW.Code != http.StatusCreated {
		t.Fatalf("expected stock create 201, got %d body=%s", createStockW.Code, createStockW.Body.String())
	}

	createVMReq := httptest.NewRequest(http.MethodPost, "/api/v1/vms", strings.NewReader(`{"specName":"g1.1","tenantID":"t1"}`))
	createVMReq.Header.Set("Content-Type", "application/json")
	createVMW := httptest.NewRecorder()
	h.ServeHTTP(createVMW, createVMReq)
	if createVMW.Code != http.StatusCreated {
		t.Fatalf("expected vm create 201, got %d body=%s", createVMW.Code, createVMW.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(createVMW.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json response: %v", err)
	}
	if _, ok := resp["data"]; !ok {
		t.Fatalf("response should contain data field")
	}
}
