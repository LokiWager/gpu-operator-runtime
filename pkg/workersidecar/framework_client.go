package workersidecar

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/loki/gpu-operator-runtime/pkg/serverless"
)

// FrameworkClient calls the user framework over the pod-local UDS-backed HTTP contract.
type FrameworkClient interface {
	Health(ctx context.Context) error
	Invoke(ctx context.Context, req serverless.FrameworkInvocationRequest) (serverless.FrameworkInvocationResponse, error)
}

// UDSFrameworkClient is the default unix-socket HTTP client used by the worker sidecar.
type UDSFrameworkClient struct {
	healthURL string
	invokeURL string
	client    *http.Client
}

// NewUDSFrameworkClient builds the default unix-socket HTTP client expected by the worker sidecar.
func NewUDSFrameworkClient(cfg Config) *UDSFrameworkClient {
	socketPath := cfg.FrameworkSocketPath
	return &UDSFrameworkClient{
		healthURL: "http://serverless-framework" + cfg.FrameworkHealthPath,
		invokeURL: "http://serverless-framework" + cfg.FrameworkInvokePath,
		client: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var dialer net.Dialer
					return dialer.DialContext(ctx, "unix", socketPath)
				},
			},
		},
	}
}

// Health checks whether the local framework is ready to accept requests.
func (c *UDSFrameworkClient) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.healthURL, nil)
	if err != nil {
		return fmt.Errorf("build framework health request: %w", err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("call framework health endpoint: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("framework health endpoint returned %d", resp.StatusCode)
	}
	return nil
}

// Invoke forwards one queue dispatch to the local framework unix-socket HTTP endpoint and decodes the response envelope.
func (c *UDSFrameworkClient) Invoke(ctx context.Context, reqPayload serverless.FrameworkInvocationRequest) (serverless.FrameworkInvocationResponse, error) {
	payload, err := json.Marshal(reqPayload)
	if err != nil {
		return serverless.FrameworkInvocationResponse{}, fmt.Errorf("marshal framework invocation request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.invokeURL, bytes.NewReader(payload))
	if err != nil {
		return serverless.FrameworkInvocationResponse{}, fmt.Errorf("build framework invocation request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return serverless.FrameworkInvocationResponse{}, fmt.Errorf("call framework invoke endpoint: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return serverless.FrameworkInvocationResponse{}, fmt.Errorf("framework invoke endpoint returned %d", resp.StatusCode)
	}

	var frameworkResp serverless.FrameworkInvocationResponse
	if err := json.NewDecoder(resp.Body).Decode(&frameworkResp); err != nil {
		return serverless.FrameworkInvocationResponse{}, fmt.Errorf("decode framework invocation response: %w", err)
	}
	return frameworkResp, nil
}
