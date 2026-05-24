package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
	"github.com/loki/gpu-operator-runtime/pkg/framework"
	"github.com/loki/gpu-operator-runtime/pkg/serverless"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg := framework.DefaultHTTPConfig()
	socketPath := os.Getenv(serverless.EnvFrameworkSocketPath)
	if raw := os.Getenv(serverless.EnvFrameworkInvokePath); raw != "" {
		cfg.InvokePath = raw
	}
	if raw := os.Getenv(serverless.EnvFrameworkHealthPath); raw != "" {
		cfg.HealthPath = raw
	}

	handler, err := framework.NewHTTPHandler(func(_ context.Context, req serverless.FrameworkInvocationRequest) (serverless.FrameworkInvocationResponse, error) {
		body := map[string]any{
			"invocationID":        req.InvocationID,
			"serverlessRequestID": req.ServerlessRequestID,
			"workerName":          req.WorkerName,
			"workerNamespace":     req.WorkerNamespace,
			"contentType":         req.ContentType,
			"attributes":          req.Attributes,
			"payload":             json.RawMessage(req.Payload),
		}
		payload, err := json.Marshal(body)
		if err != nil {
			return serverless.FrameworkInvocationResponse{}, err
		}
		return serverless.FrameworkInvocationResponse{
			StatusCode:  200,
			ContentType: "application/json",
			Body:        payload,
		}, nil
	}, logger, cfg)
	if err != nil {
		logger.Error("failed to create framework HTTP handler", "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if socketPath == "" {
		socketPath = runtimev1alpha1.DefaultServerlessFrameworkSocketPath
	}
	logger.Info("starting example framework", "socketPath", socketPath, "invokePath", cfg.InvokePath, "healthPath", cfg.HealthPath)
	if err := framework.ServeUnix(ctx, socketPath, handler); err != nil {
		logger.Error("framework example stopped with error", "error", err)
		os.Exit(1)
	}
}
