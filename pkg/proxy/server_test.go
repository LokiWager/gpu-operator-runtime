package proxy

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
)

func TestHandleStorage_UsesFixedUpstreamMappingOnSuccess(t *testing.T) {
	scheme := newProxyScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	server := &Server{
		client: cl,
		transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			expectedHost := "storage-accessor-model-cache.runtime-instance.svc.cluster.local:5000"
			if req.URL.Host != expectedHost {
				t.Fatalf("expected host %s, got %s", expectedHost, req.URL.Host)
			}
			if req.Header.Get("X-Forwarded-Host") != "proxy.example.com" {
				t.Fatalf("expected X-Forwarded-Host proxy.example.com, got %q", req.Header.Get("X-Forwarded-Host"))
			}
			if req.Header.Get("X-Forwarded-Proto") != "http" {
				t.Fatalf("expected X-Forwarded-Proto http, got %q", req.Header.Get("X-Forwarded-Proto"))
			}
			return okTestResponse("proxied"), nil
		}),
	}

	req := httptest.NewRequest(http.MethodGet, "/storage/runtime-instance/model-cache/", nil)
	req.Host = "proxy.example.com"
	rec := httptest.NewRecorder()
	server.handleStorage(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "proxied" {
		t.Fatalf("expected body proxied, got %q", rec.Body.String())
	}
}

func TestHandleStorage_FallsBackToKubernetesOnUpstreamError(t *testing.T) {
	scheme := newProxyScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	server := &Server{
		client:    cl,
		transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errUpstreamDial }),
	}

	req := httptest.NewRequest(http.MethodGet, "/storage/runtime-instance/model-cache/", nil)
	rec := httptest.NewRecorder()
	server.handleStorage(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "gpu storage runtime-instance/model-cache not found") {
		t.Fatalf("unexpected body %q", rec.Body.String())
	}
}

func TestHandleStorage_ReturnsAccessorDisabledFromKubernetes(t *testing.T) {
	scheme := newProxyScheme(t)
	storage := &runtimev1alpha1.GPUStorage{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-cache",
			Namespace: "runtime-instance",
		},
		Spec: runtimev1alpha1.GPUStorageSpec{
			Size: "20Gi",
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(storage).Build()

	server := &Server{
		client:    cl,
		transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errUpstreamDial }),
	}

	req := httptest.NewRequest(http.MethodGet, "/storage/runtime-instance/model-cache/", nil)
	rec := httptest.NewRecorder()
	server.handleStorage(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "has accessor disabled") {
		t.Fatalf("unexpected body %q", rec.Body.String())
	}
}

func TestHandleStorage_ReturnsReadyStateMismatchAsBadGateway(t *testing.T) {
	scheme := newProxyScheme(t)
	storage := &runtimev1alpha1.GPUStorage{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-cache",
			Namespace: "runtime-instance",
		},
		Spec: runtimev1alpha1.GPUStorageSpec{
			Size: "20Gi",
			Accessor: runtimev1alpha1.GPUStorageAccessorSpec{
				Enabled: true,
			},
		},
		Status: runtimev1alpha1.GPUStorageStatus{
			Accessor: runtimev1alpha1.GPUStorageAccessorStatus{
				Phase:       runtimev1alpha1.StorageAccessorPhaseReady,
				ServiceName: runtimev1alpha1.StorageAccessorServiceResourceName("model-cache"),
			},
		},
	}
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&runtimev1alpha1.GPUStorage{}).
		WithObjects(storage).
		Build()

	server := &Server{
		client:    cl,
		transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errUpstreamDial }),
	}

	req := httptest.NewRequest(http.MethodGet, "/storage/runtime-instance/model-cache/", nil)
	rec := httptest.NewRecorder()
	server.handleStorage(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected status 502, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "marked ready but proxying") {
		t.Fatalf("unexpected body %q", rec.Body.String())
	}
}

func newProxyScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := runtimev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add runtime scheme error: %v", err)
	}
	return scheme
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func okTestResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

var errUpstreamDial = errors.New("upstream dial failed")
