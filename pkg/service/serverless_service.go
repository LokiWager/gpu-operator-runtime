package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/loki/gpu-operator-runtime/pkg/domain"
	"github.com/loki/gpu-operator-runtime/pkg/serverless"
)

// CreateServerlessInvocation persists one queue-first invocation request into the configured serverless queue.
func (s *Service) CreateServerlessInvocation(ctx context.Context, req CreateServerlessInvocationRequest) (domain.ServerlessInvocationAck, bool, error) {
	if s.serverlessPublisher == nil || !s.serverlessPublisher.Enabled() {
		return domain.ServerlessInvocationAck{}, false, &UnavailableError{Message: "serverless queue is not configured"}
	}

	payload := append(json.RawMessage(nil), req.Payload...)
	ack, err := s.serverlessPublisher.PublishInvocation(ctx, serverless.InvocationMessage{
		InvocationID:        req.InvocationID,
		ServerlessRequestID: req.ServerlessRequestID,
		Mode:                req.Mode,
		ContentType:         req.ContentType,
		Headers:             cloneStringMap(req.Headers),
		Attributes:          cloneStringMap(req.Attributes),
		Payload:             payload,
		TimeoutSeconds:      req.TimeoutSeconds,
	})
	if err != nil {
		return domain.ServerlessInvocationAck{}, false, err
	}

	return domain.ServerlessInvocationAck{
		InvocationID:        ack.InvocationID,
		ServerlessRequestID: ack.ServerlessRequestID,
		Mode:                string(ack.Mode),
		Subject:             ack.Subject,
		ResultSubject:       ack.ResultSubject,
		MetricsSubject:      ack.MetricsSubject,
		Stream:              ack.Stream,
		Sequence:            ack.Sequence,
		Duplicate:           ack.Duplicate,
		AcceptedAt:          ack.AcceptedAt,
	}, !ack.Duplicate, nil
}

// InvokeServerlessSync publishes one invocation and waits for the invocation-specific reply path to return a worker-side result.
func (s *Service) InvokeServerlessSync(ctx context.Context, req CreateServerlessInvocationRequest) (domain.ServerlessInvocationResult, error) {
	if s.serverlessPublisher == nil || !s.serverlessPublisher.Enabled() {
		return domain.ServerlessInvocationResult{}, &UnavailableError{Message: "serverless queue is not configured"}
	}

	requester, ok := s.serverlessPublisher.(serverless.SyncInvocationRequester)
	if !ok {
		return domain.ServerlessInvocationResult{}, &UnavailableError{Message: "sync serverless invocation is not configured"}
	}

	if req.TimeoutSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutSeconds)*time.Second)
		defer cancel()
	}

	payload := append(json.RawMessage(nil), req.Payload...)
	_, result, err := requester.RequestSyncInvocation(ctx, serverless.InvocationMessage{
		InvocationID:        req.InvocationID,
		ServerlessRequestID: req.ServerlessRequestID,
		Mode:                req.Mode,
		ContentType:         req.ContentType,
		Headers:             cloneStringMap(req.Headers),
		Attributes:          cloneStringMap(req.Attributes),
		Payload:             payload,
		TimeoutSeconds:      req.TimeoutSeconds,
	})
	if err != nil {
		return domain.ServerlessInvocationResult{}, err
	}

	return domain.ServerlessInvocationResult{
		InvocationID:        result.InvocationID,
		ServerlessRequestID: result.ServerlessRequestID,
		Mode:                string(result.Mode),
		WorkerName:          result.WorkerName,
		WorkerNamespace:     result.WorkerNamespace,
		StatusCode:          result.StatusCode,
		ContentType:         result.ContentType,
		Headers:             cloneStringMap(result.Headers),
		Body:                append(json.RawMessage(nil), result.Body...),
		Error:               result.Error,
		StartedAt:           result.StartedAt,
		CompletedAt:         result.CompletedAt,
	}, nil
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}
