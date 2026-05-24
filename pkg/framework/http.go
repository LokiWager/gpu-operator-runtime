package framework

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
	"github.com/loki/gpu-operator-runtime/pkg/serverless"
)

// HandlerFunc is the user-provided local framework hook invoked by the worker sidecar.
type HandlerFunc func(context.Context, serverless.FrameworkInvocationRequest) (serverless.FrameworkInvocationResponse, error)

// HTTPConfig captures the local request paths used by the framework helper.
type HTTPConfig struct {
	InvokePath string
	HealthPath string
}

// DefaultHTTPConfig returns the default local framework paths shared with the sidecar contract.
func DefaultHTTPConfig() HTTPConfig {
	return HTTPConfig{
		InvokePath: runtimev1alpha1.DefaultServerlessFrameworkInvokePath,
		HealthPath: runtimev1alpha1.DefaultServerlessFrameworkHealthPath,
	}
}

// Normalized defaults and cleans the helper paths.
func (c HTTPConfig) Normalized() HTTPConfig {
	cfg := c
	cfg.InvokePath = normalizeFrameworkPath(cfg.InvokePath, runtimev1alpha1.DefaultServerlessFrameworkInvokePath)
	cfg.HealthPath = normalizeFrameworkPath(cfg.HealthPath, runtimev1alpha1.DefaultServerlessFrameworkHealthPath)
	return cfg
}

// NewHTTPHandler returns an HTTP handler that exposes the worker-side framework contract expected by the sidecar over a unix domain socket.
func NewHTTPHandler(handler HandlerFunc, logger *slog.Logger, cfg HTTPConfig) (http.Handler, error) {
	if handler == nil {
		return nil, errors.New("framework handler is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	cfg = cfg.Normalized()

	mux := http.NewServeMux()
	mux.HandleFunc(cfg.HealthPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc(cfg.InvokePath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req serverless.FrameworkInvocationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid invocation payload", http.StatusBadRequest)
			return
		}

		resp, err := handler(r.Context(), req)
		if err != nil {
			logger.Error("framework invocation failed", "invocationID", req.InvocationID, "error", err)
			resp = serverless.FrameworkInvocationResponse{
				StatusCode:  http.StatusInternalServerError,
				ContentType: "application/json",
				Error:       err.Error(),
				Body:        json.RawMessage(`{"error":"framework handler failed"}`),
			}
		}
		if resp.StatusCode == 0 {
			resp.StatusCode = http.StatusOK
		}
		if strings.TrimSpace(resp.ContentType) == "" {
			resp.ContentType = "application/json"
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			logger.Error("encode framework response failed", "invocationID", req.InvocationID, "error", err)
		}
	})
	return mux, nil
}

func normalizeFrameworkPath(value, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	normalized := "/" + strings.TrimPrefix(trimmed, "/")
	if normalized == "/" {
		return fallback
	}
	return normalized
}
