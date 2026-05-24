package framework

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/loki/gpu-operator-runtime/pkg/serverless"
)

func TestNewHTTPHandlerHandlesHealthAndInvoke(t *testing.T) {
	handler, err := NewHTTPHandler(func(_ context.Context, req serverless.FrameworkInvocationRequest) (serverless.FrameworkInvocationResponse, error) {
		if req.InvocationID != "inv-1" {
			t.Fatalf("expected invocation id inv-1, got %s", req.InvocationID)
		}
		return serverless.FrameworkInvocationResponse{
			StatusCode:  http.StatusCreated,
			ContentType: "application/json",
			Body:        json.RawMessage(`{"ok":true}`),
		}, nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), DefaultHTTPConfig())
	if err != nil {
		t.Fatalf("new framework handler: %v", err)
	}

	srv := httptest.NewServer(handler)
	defer srv.Close()

	healthResp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("get healthz: %v", err)
	}
	if healthResp.StatusCode != http.StatusOK {
		t.Fatalf("expected health status 200, got %d", healthResp.StatusCode)
	}

	invokeResp, err := http.Post(srv.URL+"/invoke", "application/json", strings.NewReader(`{"invocationID":"inv-1"}`))
	if err != nil {
		t.Fatalf("post invoke: %v", err)
	}
	defer invokeResp.Body.Close()

	var payload serverless.FrameworkInvocationResponse
	if err := json.NewDecoder(invokeResp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode invoke response: %v", err)
	}
	if payload.StatusCode != http.StatusCreated {
		t.Fatalf("expected response status code 201, got %d", payload.StatusCode)
	}
}
