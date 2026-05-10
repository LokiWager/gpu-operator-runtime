package service

import (
	"context"
	"encoding/json"

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
