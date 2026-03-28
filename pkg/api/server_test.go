package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
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

func newOperatorBackedHandler(t *testing.T) (http.Handler, *service.Service, ctrlclient.Client, context.CancelFunc) {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := runtimev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme error: %v", err)
	}
	operatorClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&runtimev1alpha1.GPUUnit{}).
		WithStatusSubresource(&runtimev1alpha1.GPUStorage{}).
		Build()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(nil, operatorClient, logger)
	h := NewServer(svc, logger)

	ctx, cancel := context.WithCancel(context.Background())
	go svc.StartOperatorJobWorker(ctx)

	return h, svc, operatorClient, cancel
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

func TestServer_SwaggerSpec(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/swagger/doc.json", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "GPU Operator Runtime API") {
		t.Fatalf("expected swagger spec title in response, got %s", w.Body.String())
	}
}

func TestServer_CreateStockUnitsJobWithoutOperator(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/operator/stock-units", strings.NewReader(`{"operationID":"op-a","specName":"g1.1","replicas":1}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestServer_CreateStockUnitsJob(t *testing.T) {
	h, _, _, cancel := newOperatorBackedHandler(t)
	defer cancel()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/operator/stock-units", strings.NewReader(`{"operationID":"op-create-1","specName":"g1.1","replicas":1}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestServer_CreateStockUnitsJob_IsIdempotent(t *testing.T) {
	h, _, _, cancel := newOperatorBackedHandler(t)
	defer cancel()

	body := `{"operationID":"op-create-2","specName":"g2.1","replicas":1}`

	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/operator/stock-units", strings.NewReader(body))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, req1)
	if w1.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w1.Code, w1.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/operator/stock-units", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w2.Code, w2.Body.String())
	}
}

func TestServer_CreateStockUnitsJob_RejectsConflict(t *testing.T) {
	h, _, _, cancel := newOperatorBackedHandler(t)
	defer cancel()

	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/operator/stock-units", strings.NewReader(`{"operationID":"op-create-3","specName":"g1.1","replicas":1}`))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, req1)
	if w1.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w1.Code, w1.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/operator/stock-units", strings.NewReader(`{"operationID":"op-create-3","specName":"g2.1","replicas":1}`))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	if w2.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", w2.Code, w2.Body.String())
	}
}

func TestServer_CreateGPUUnit(t *testing.T) {
	h, _, operatorClient, cancel := newOperatorBackedHandler(t)
	defer cancel()

	seedAPIStockUnit(t, operatorClient, "stock-g1-001", runtimev1alpha1.PhaseReady)
	seedAPIGPUStorage(t, operatorClient, "model-cache", runtimev1alpha1.StoragePhaseReady)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/gpu-units", strings.NewReader(`{"operationID":"gpu-op-1","name":"demo-instance","specName":"g1.1","image":"pytorch:2.6","template":{"ports":[{"name":"http","port":8080}]},"access":{"primaryPort":"http","scheme":"http"},"storageMounts":[{"name":"model-cache","mountPath":"/data"}]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestServer_GPUUnitCrud(t *testing.T) {
	h, _, operatorClient, cancel := newOperatorBackedHandler(t)
	defer cancel()

	seedAPIStockUnit(t, operatorClient, "stock-g1-001", runtimev1alpha1.PhaseReady)
	seedAPIStockUnit(t, operatorClient, "stock-g1-002", runtimev1alpha1.PhaseReady)
	seedAPIGPUStorage(t, operatorClient, "model-cache", runtimev1alpha1.StoragePhaseReady)

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/gpu-units", strings.NewReader(`{"operationID":"gpu-op-2","name":"demo-instance","specName":"g1.1","image":"python:3.12","template":{"ports":[{"name":"http","port":8080}]},"access":{"primaryPort":"http","scheme":"http"},"storageMounts":[{"name":"model-cache","mountPath":"/data"}]}`))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	h.ServeHTTP(createW, createReq)
	if createW.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", createW.Code, createW.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/gpu-units/demo-instance?namespace=runtime-instance", nil)
	getW := httptest.NewRecorder()
	h.ServeHTTP(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", getW.Code, getW.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/gpu-units?namespace=runtime-instance", nil)
	listW := httptest.NewRecorder()
	h.ServeHTTP(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", listW.Code, listW.Body.String())
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/gpu-units/demo-instance?namespace=runtime-instance", strings.NewReader(`{"image":"pytorch:2.6","template":{"ports":[{"name":"web","port":7860}]},"access":{"primaryPort":"web","scheme":"http"},"storageMounts":[{"name":"model-cache","mountPath":"/workspace/cache"}]}`))
	updateReq.Header.Set("Content-Type", "application/json")
	updateW := httptest.NewRecorder()
	h.ServeHTTP(updateW, updateReq)
	if updateW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", updateW.Code, updateW.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/gpu-units/demo-instance?namespace=runtime-instance", nil)
	deleteW := httptest.NewRecorder()
	h.ServeHTTP(deleteW, deleteReq)
	if deleteW.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", deleteW.Code, deleteW.Body.String())
	}
}

func TestServer_GPUStorageCrud(t *testing.T) {
	h, _, operatorClient, cancel := newOperatorBackedHandler(t)
	defer cancel()

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/gpu-storages", strings.NewReader(`{"name":"model-cache","size":"20Gi"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	h.ServeHTTP(createW, createReq)
	if createW.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", createW.Code, createW.Body.String())
	}

	seedAPIStockUnit(t, operatorClient, "stock-g1-001", runtimev1alpha1.PhaseReady)

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/gpu-storages/model-cache?namespace=runtime-instance", nil)
	getW := httptest.NewRecorder()
	h.ServeHTTP(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", getW.Code, getW.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/gpu-storages?namespace=runtime-instance", nil)
	listW := httptest.NewRecorder()
	h.ServeHTTP(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", listW.Code, listW.Body.String())
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/gpu-storages/model-cache?namespace=runtime-instance", strings.NewReader(`{"size":"40Gi"}`))
	updateReq.Header.Set("Content-Type", "application/json")
	updateW := httptest.NewRecorder()
	h.ServeHTTP(updateW, updateReq)
	if updateW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", updateW.Code, updateW.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/gpu-storages/model-cache?namespace=runtime-instance", nil)
	deleteW := httptest.NewRecorder()
	h.ServeHTTP(deleteW, deleteReq)
	if deleteW.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", deleteW.Code, deleteW.Body.String())
	}
}

func seedAPIStockUnit(t *testing.T, operatorClient ctrlclient.Client, unitName, phase string) {
	t.Helper()

	ctx := context.Background()

	unit := &runtimev1alpha1.GPUUnit{
		ObjectMeta: metav1.ObjectMeta{
			Name:      unitName,
			Namespace: runtimev1alpha1.DefaultStockNamespace,
			Labels: map[string]string{
				runtimev1alpha1.LabelUnitKey: unitName,
			},
		},
		Spec: runtimev1alpha1.GPUUnitSpec{
			SpecName: "g1.1",
			Image:    runtimev1alpha1.StockReservationImage,
			Memory:   "16Gi",
			GPU:      1,
		},
		Status: runtimev1alpha1.GPUUnitStatus{
			Phase: phase,
			Conditions: []metav1.Condition{{
				Type:    runtimev1alpha1.ConditionReady,
				Status:  metav1.ConditionFalse,
				Reason:  runtimev1alpha1.ReasonStockNotReady,
				Message: runtimev1alpha1.StatusMessageStockWait,
			}},
		},
	}
	if phase == runtimev1alpha1.PhaseReady {
		unit.Status.ReadyReplicas = 1
		unit.Status.Conditions[0].Status = metav1.ConditionTrue
		unit.Status.Conditions[0].Reason = runtimev1alpha1.ReasonStockReady
		unit.Status.Conditions[0].Message = runtimev1alpha1.StatusMessageStockReady
	}

	if err := operatorClient.Create(ctx, unit); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create stock unit error: %v", err)
	}
	if err := operatorClient.Status().Update(ctx, unit); err != nil {
		t.Fatalf("update stock unit status error: %v", err)
	}
}

func seedAPIGPUStorage(t *testing.T, operatorClient ctrlclient.Client, name, phase string) {
	t.Helper()

	ctx := context.Background()

	storage := &runtimev1alpha1.GPUStorage{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: runtimev1alpha1.DefaultInstanceNamespace,
			Labels: map[string]string{
				runtimev1alpha1.LabelStorageKey: name,
			},
		},
		Spec: runtimev1alpha1.GPUStorageSpec{
			Size: "20Gi",
		},
		Status: runtimev1alpha1.GPUStorageStatus{
			ClaimName: name,
			Phase:     phase,
			Conditions: []metav1.Condition{{
				Type:    runtimev1alpha1.ConditionReady,
				Status:  metav1.ConditionFalse,
				Reason:  runtimev1alpha1.ReasonStoragePending,
				Message: runtimev1alpha1.StatusMessageStoragePending,
			}},
		},
	}
	if phase == runtimev1alpha1.StoragePhaseReady {
		storage.Status.Capacity = "20Gi"
		storage.Status.Conditions[0].Status = metav1.ConditionTrue
		storage.Status.Conditions[0].Reason = runtimev1alpha1.ReasonStorageReady
		storage.Status.Conditions[0].Message = runtimev1alpha1.StatusMessageStorageReady
	}

	if err := operatorClient.Create(ctx, storage); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create gpu storage error: %v", err)
	}
	if err := operatorClient.Status().Update(ctx, storage); err != nil {
		t.Fatalf("update gpu storage status error: %v", err)
	}
}
