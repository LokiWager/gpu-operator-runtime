package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
	"github.com/loki/gpu-operator-runtime/pkg/contract"
	"github.com/loki/gpu-operator-runtime/pkg/serverless"
	"github.com/loki/gpu-operator-runtime/pkg/service"
)

func testRuntimePackageCatalog() contract.RuntimePackageCatalog {
	return contract.RuntimePackageCatalog{{
		ID:       "gpu-rtx3080-2x-cpu10-mem40g",
		SpecName: "gpu.rtx3080.2x.10c.40g",
		CPU:      "10",
		Memory:   "40Gi",
		GPU:      2,
		Allocation: runtimev1alpha1.GPUUnitAllocationSpec{
			DeviceClassName:  "nvidia-rtx-3080",
			ClaimRequestName: runtimev1alpha1.UnitDRAClaimRequestName,
			Count:            2,
		},
	}}
}

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(nil, nil, logger)
	if err := svc.ConfigureRuntimePackages(testRuntimePackageCatalog()); err != nil {
		t.Fatalf("configure runtime packages: %v", err)
	}
	return NewServer(svc, logger)
}

func newOperatorBackedHandler(t *testing.T) (http.Handler, *service.Service, ctrlclient.Client, context.CancelFunc) {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := runtimev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme error: %v", err)
	}
	if err := resourcev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add resource scheme error: %v", err)
	}
	operatorClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&runtimev1alpha1.GPUUnit{}).
		WithStatusSubresource(&runtimev1alpha1.GPUStorage{}).
		WithStatusSubresource(&resourcev1.ResourceClaim{}).
		Build()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(nil, operatorClient, logger)
	if err := svc.ConfigureRuntimePackages(testRuntimePackageCatalog()); err != nil {
		t.Fatalf("configure runtime packages: %v", err)
	}
	h := NewServer(svc, logger)

	ctx, cancel := context.WithCancel(context.Background())
	_ = ctx

	return h, svc, operatorClient, cancel
}

func newServerlessHandler(t *testing.T, publisher serverless.InvocationPublisher) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(nil, nil, logger)
	svc.ConfigureServerlessPublisher(publisher)
	return NewServer(svc, logger)
}

func TestServer_RuntimeInventory(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := runtimev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme error: %v", err)
	}
	if err := resourcev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add resource scheme error: %v", err)
	}
	operatorClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&runtimev1alpha1.GPUUnit{}).
		WithStatusSubresource(&resourcev1.ResourceClaim{}).
		Build()
	kubeClient := k8sfake.NewSimpleClientset(&corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{
				corev1.ResourceName(runtimev1alpha1.NVIDIAGPUResourceName): *resource.NewQuantity(4, resource.DecimalSI),
			},
			Allocatable: corev1.ResourceList{
				corev1.ResourceName(runtimev1alpha1.NVIDIAGPUResourceName): *resource.NewQuantity(4, resource.DecimalSI),
			},
			Conditions: []corev1.NodeCondition{{
				Type:   corev1.NodeReady,
				Status: corev1.ConditionTrue,
			}},
		},
	})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(kubeClient, operatorClient, logger)
	handler := NewServer(svc, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/operator/inventory", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"totalGpuAllocatable":4`) {
		t.Fatalf("expected inventory response, got %s", rec.Body.String())
	}
}

type fakeInvocationPublisher struct {
	enabled bool
	ack     serverless.PublishAck
	result  serverless.InvocationResultMessage
	err     error
}

func (f fakeInvocationPublisher) Enabled() bool {
	return f.enabled
}

func (f fakeInvocationPublisher) PublishInvocation(_ context.Context, msg serverless.InvocationMessage) (serverless.PublishAck, error) {
	if f.err != nil {
		return serverless.PublishAck{}, f.err
	}
	ack := f.ack
	if ack.InvocationID == "" {
		ack.InvocationID = msg.InvocationID
	}
	if ack.ServerlessRequestID == "" {
		ack.ServerlessRequestID = msg.ServerlessRequestID
	}
	if ack.Mode == "" {
		ack.Mode = msg.Mode
	}
	if ack.Subject == "" {
		ack.Subject = "runtime.serverless.invoke." + msg.ServerlessRequestID
	}
	if ack.ResultSubject == "" {
		ack.ResultSubject = "runtime.serverless.result." + msg.ServerlessRequestID
	}
	if ack.MetricsSubject == "" {
		ack.MetricsSubject = "runtime.serverless.metrics." + msg.ServerlessRequestID
	}
	if ack.Stream == "" {
		ack.Stream = "RUNTIME_SERVERLESS"
	}
	if ack.AcceptedAt.IsZero() {
		ack.AcceptedAt = time.Unix(1700000000, 0).UTC()
	}
	return ack, nil
}

func (f fakeInvocationPublisher) RequestSyncInvocation(_ context.Context, msg serverless.InvocationMessage) (serverless.PublishAck, serverless.InvocationResultMessage, error) {
	ack, err := f.PublishInvocation(context.Background(), msg)
	if err != nil {
		return serverless.PublishAck{}, serverless.InvocationResultMessage{}, err
	}
	result := f.result
	if result.InvocationID == "" {
		result.InvocationID = msg.InvocationID
	}
	if result.ServerlessRequestID == "" {
		result.ServerlessRequestID = msg.ServerlessRequestID
	}
	if result.Mode == "" {
		result.Mode = msg.Mode
	}
	if result.CompletedAt.IsZero() {
		result.CompletedAt = time.Unix(1700000010, 0).UTC()
	}
	return ack, result, nil
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

func TestServer_CreateGPUUnit(t *testing.T) {
	h, _, operatorClient, cancel := newOperatorBackedHandler(t)
	defer cancel()

	seedAPIGPUStorage(t, operatorClient, "model-cache", runtimev1alpha1.StoragePhaseReady)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/gpu-units", strings.NewReader(`{"operationID":"gpu-op-1","name":"demo-instance","packageID":"gpu-rtx3080-2x-cpu10-mem40g","image":"pytorch:2.6","template":{"ports":[{"name":"http","port":8080}]},"access":{"primaryPort":"http","scheme":"http"},"storageMounts":[{"name":"model-cache","mountPath":"/data"}]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestServer_CreateServerlessInvocationWithoutQueue(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/serverless/invocations", strings.NewReader(`{"serverlessRequestID":"sd-webui","mode":"async","payload":{"prompt":"hello"}}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestServer_CreateServerlessInvocationAccepted(t *testing.T) {
	h := newServerlessHandler(t, fakeInvocationPublisher{
		enabled: true,
		result: serverless.InvocationResultMessage{
			StatusCode:  200,
			ContentType: "application/json",
			Body:        []byte(`{"ok":true}`),
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/serverless/invocations", strings.NewReader(`{"serverlessRequestID":"sd-webui","mode":"sync","payload":{"prompt":"hello"}}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestServer_CreateServerlessInvocationDuplicate(t *testing.T) {
	h := newServerlessHandler(t, fakeInvocationPublisher{
		enabled: true,
		ack: serverless.PublishAck{
			Sequence:  43,
			Duplicate: true,
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/serverless/invocations", strings.NewReader(`{"serverlessRequestID":"sd-webui","mode":"async","payload":{"prompt":"hello"}}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestServer_CreateGPUUnit_ValidatesRequestBeforeService(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/gpu-units", strings.NewReader(`{"operationID":"gpu-op-invalid","name":"demo-instance","packageID":"gpu-rtx3080-2x-cpu10-mem40g"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "image is required") {
		t.Fatalf("expected image validation error, got %s", w.Body.String())
	}
}

func TestServer_GPUUnitCrud(t *testing.T) {
	h, _, operatorClient, cancel := newOperatorBackedHandler(t)
	defer cancel()

	seedAPIGPUStorage(t, operatorClient, "model-cache", runtimev1alpha1.StoragePhaseReady)

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/gpu-units", strings.NewReader(`{"operationID":"gpu-op-2","name":"demo-instance","packageID":"gpu-rtx3080-2x-cpu10-mem40g","image":"python:3.12","template":{"ports":[{"name":"http","port":8080}]},"access":{"primaryPort":"http","scheme":"http"},"storageMounts":[{"name":"model-cache","mountPath":"/data"}]}`))
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
	h, _, _, cancel := newOperatorBackedHandler(t)
	defer cancel()

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/gpu-storages", strings.NewReader(`{"name":"model-cache","size":"20Gi","prepare":{"fromImage":"busybox:1.36","command":["sh","-c"],"args":["echo seeded > /workspace/README.txt"]},"accessor":{"enabled":true}}`))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	h.ServeHTTP(createW, createReq)
	if createW.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", createW.Code, createW.Body.String())
	}

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

	recoverReq := httptest.NewRequest(http.MethodPost, "/api/v1/gpu-storages/model-cache/recover?namespace=runtime-instance", nil)
	recoverW := httptest.NewRecorder()
	h.ServeHTTP(recoverW, recoverReq)
	if recoverW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recoverW.Code, recoverW.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/gpu-storages/model-cache?namespace=runtime-instance", nil)
	deleteW := httptest.NewRecorder()
	h.ServeHTTP(deleteW, deleteReq)
	if deleteW.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", deleteW.Code, deleteW.Body.String())
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
