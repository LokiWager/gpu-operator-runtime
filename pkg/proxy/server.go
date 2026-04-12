package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Server handles shared reverse-proxy access into controller-owned runtime services.
type Server struct {
	client    client.Client
	logger    *slog.Logger
	transport http.RoundTripper
}

// NewServer builds a ready-to-use HTTP handler for shared runtime access.
func NewServer(client client.Client, logger *slog.Logger) http.Handler {
	s := &Server{
		client: client,
		logger: logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc(runtimev1alpha1.DefaultStorageProxyPathPrefix+"/", s.handleStorage)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleStorage(w http.ResponseWriter, req *http.Request) {
	namespace, storageName, ok := parseStorageRequestPath(req.URL.Path)
	if !ok {
		http.NotFound(w, req)
		return
	}

	upstreamHost := storageAccessorUpstreamHost(namespace, storageName)
	proxy := &httputil.ReverseProxy{
		Rewrite: func(proxyReq *httputil.ProxyRequest) {
			proxyReq.SetURL(&url.URL{
				Scheme: "http",
				Host:   upstreamHost,
			})
			proxyReq.Out.Header.Set("X-Forwarded-Host", proxyReq.In.Host)
			proxyReq.Out.Header.Set("X-Forwarded-Proto", forwardedProto(proxyReq.In))
		},
		ErrorHandler: func(rw http.ResponseWriter, r *http.Request, err error) {
			if s.logger != nil {
				s.logger.Error("storage proxy request failed", "namespace", namespace, "storage", storageName, "error", err)
			}
			s.writeStorageProxyError(rw, req.Context(), namespace, storageName, upstreamHost, err)
		},
	}
	if s.transport != nil {
		proxy.Transport = s.transport
	}
	proxy.ServeHTTP(w, req)
}

func (s *Server) writeStorageProxyError(
	w http.ResponseWriter,
	ctx context.Context,
	namespace string,
	storageName string,
	upstreamHost string,
	upstreamErr error,
) {
	if s.client == nil {
		http.Error(w, "storage proxy upstream error: "+upstreamErr.Error(), http.StatusBadGateway)
		return
	}

	var storage runtimev1alpha1.GPUStorage
	err := s.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: storageName}, &storage)
	switch {
	case apierrors.IsNotFound(err):
		http.Error(w, fmt.Sprintf("gpu storage %s/%s not found", namespace, storageName), http.StatusNotFound)
		return
	case err != nil:
		http.Error(w, fmt.Sprintf("failed to load gpu storage %s/%s: %v", namespace, storageName, err), http.StatusBadGateway)
		return
	}

	switch {
	case !storage.Spec.Accessor.Enabled:
		http.Error(w, fmt.Sprintf("gpu storage %s/%s has accessor disabled", namespace, storageName), http.StatusServiceUnavailable)
		return
	case storage.Status.Accessor.Phase != runtimev1alpha1.StorageAccessorPhaseReady:
		http.Error(
			w,
			fmt.Sprintf("gpu storage %s/%s accessor is not ready: phase=%s", namespace, storageName, storage.Status.Accessor.Phase),
			http.StatusServiceUnavailable,
		)
		return
	}

	expectedServiceName := runtimev1alpha1.StorageAccessorServiceResourceName(storageName)
	if serviceName := strings.TrimSpace(storage.Status.Accessor.ServiceName); serviceName != "" && serviceName != expectedServiceName {
		http.Error(
			w,
			fmt.Sprintf(
				"gpu storage %s/%s accessor service mismatch: expected=%s observed=%s",
				namespace,
				storageName,
				expectedServiceName,
				serviceName,
			),
			http.StatusBadGateway,
		)
		return
	}

	http.Error(
		w,
		fmt.Sprintf("storage accessor %s is marked ready but proxying to %s failed: %v", storageName, upstreamHost, upstreamErr),
		http.StatusBadGateway,
	)
}

func parseStorageRequestPath(raw string) (string, string, bool) {
	trimmed := strings.TrimPrefix(raw, runtimev1alpha1.DefaultStorageProxyPathPrefix+"/")
	parts := strings.SplitN(trimmed, "/", 3)
	if len(parts) < 2 {
		return "", "", false
	}

	namespace := strings.TrimSpace(parts[0])
	storageName := strings.TrimSpace(parts[1])
	if namespace == "" || storageName == "" {
		return "", "", false
	}
	return namespace, storageName, true
}

func forwardedProto(req *http.Request) string {
	if req.TLS != nil {
		return "https"
	}
	if proto := strings.TrimSpace(req.Header.Get("X-Forwarded-Proto")); proto != "" {
		return proto
	}
	return "http"
}

func storageAccessorUpstreamHost(namespace, storageName string) string {
	return fmt.Sprintf(
		"%s.%s.svc.cluster.local:%d",
		runtimev1alpha1.StorageAccessorServiceResourceName(storageName),
		namespace,
		runtimev1alpha1.DefaultStorageAccessorPort,
	)
}
