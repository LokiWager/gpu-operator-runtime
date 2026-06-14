package resultconsumer

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/loki/gpu-operator-runtime/pkg/serverless"
)

// Queue captures the result stream consumer required by the result-store process.
type Queue interface {
	serverless.InvocationResultConsumer
}

// Store captures durable invocation result persistence.
type Store interface {
	SaveInvocationResult(ctx context.Context, result serverless.InvocationResultMessage) error
}

// Service drains invocation results from JetStream and persists them for control-plane lookup.
type Service struct {
	store  Store
	logger *slog.Logger
	cfg    Config
}

// New builds a result consumer service.
func New(store Store, logger *slog.Logger, cfg Config) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		store:  store,
		logger: logger,
		cfg:    cfg,
	}
}

// Run consumes completed invocation results until the context is cancelled.
func (s *Service) Run(ctx context.Context, queue Queue) error {
	if queue == nil {
		return fmt.Errorf("result queue is required")
	}
	if s.store == nil {
		return fmt.Errorf("result store is required")
	}
	return queue.ConsumeInvocationResults(ctx, s.cfg.ConsumerName, s.cfg.AckWaitDuration(), s.HandleResult)
}

// HandleResult stores one completed invocation result.
func (s *Service) HandleResult(ctx context.Context, result serverless.InvocationResultMessage) error {
	if result.InvocationID == "" {
		return fmt.Errorf("invocationID is required")
	}
	if result.ServerlessRequestID == "" {
		return fmt.Errorf("serverlessRequestID is required")
	}
	if err := s.store.SaveInvocationResult(ctx, result); err != nil {
		return err
	}
	s.logger.Info("persisted serverless invocation result",
		"invocationID", result.InvocationID,
		"serverlessRequestID", result.ServerlessRequestID,
		"statusCode", result.StatusCode,
	)
	return nil
}
