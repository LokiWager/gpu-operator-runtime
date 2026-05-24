package workersidecar

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/loki/gpu-operator-runtime/pkg/framework"
	"github.com/loki/gpu-operator-runtime/pkg/serverless"
)

func TestUDSFrameworkClientHealthAndInvoke(t *testing.T) {
	socketPath := fmt.Sprintf("/tmp/framework-client-%d.sock", time.Now().UnixNano())
	_ = os.Remove(socketPath)
	defer os.Remove(socketPath)
	handler, err := framework.NewHTTPHandler(func(_ context.Context, req serverless.FrameworkInvocationRequest) (serverless.FrameworkInvocationResponse, error) {
		return serverless.FrameworkInvocationResponse{
			StatusCode:  200,
			ContentType: "application/json",
			Body:        json.RawMessage(`{"ok":true}`),
		}, nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), framework.DefaultHTTPConfig())
	if err != nil {
		t.Fatalf("new framework handler: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = framework.ServeUnix(ctx, socketPath, handler)
	}()

	client := NewUDSFrameworkClient(Config{
		FrameworkSocketPath: socketPath,
		FrameworkInvokePath: "/invoke",
		FrameworkHealthPath: "/healthz",
	})

	deadline := time.Now().Add(2 * time.Second)
	for {
		err = client.Health(context.Background())
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("health over uds: %v", err)
	}

	resp, err := client.Invoke(context.Background(), serverless.FrameworkInvocationRequest{InvocationID: "inv-1"})
	if err != nil {
		t.Fatalf("invoke over uds: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
}
