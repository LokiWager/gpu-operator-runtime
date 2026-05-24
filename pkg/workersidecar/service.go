package workersidecar

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/loki/gpu-operator-runtime/pkg/serverless"
)

// Service consumes worker-targeted dispatch subjects, forwards invocations to the local framework, and publishes results or metrics back into NATS.
type Service struct {
	cfg       Config
	framework FrameworkClient
	results   serverless.InvocationResultPublisher
	metrics   serverless.WorkerMetricsPublisher
	logger    *slog.Logger
	inflight  atomic.Int32
}

// New builds a worker-sidecar service.
func New(cfg Config, framework FrameworkClient, results serverless.InvocationResultPublisher, metrics serverless.WorkerMetricsPublisher, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		cfg:       cfg,
		framework: framework,
		results:   results,
		metrics:   metrics,
		logger:    logger,
	}
}

// Run waits for the local framework to become healthy, emits registration or heartbeat events, and drains the worker dispatch subject.
func (s *Service) Run(ctx context.Context, consumer serverless.WorkerDispatchConsumer) error {
	if consumer == nil {
		return fmt.Errorf("worker dispatch consumer is required")
	}
	if s.framework == nil {
		return fmt.Errorf("framework client is required")
	}
	if s.results == nil {
		return fmt.Errorf("invocation result publisher is required")
	}
	if s.metrics == nil {
		return fmt.Errorf("worker metrics publisher is required")
	}

	if err := s.framework.Health(ctx); err != nil {
		return err
	}
	if err := s.publishMetric(ctx, serverless.WorkerMetricMessage{
		Version:             serverless.InvocationVersion,
		ServerlessRequestID: s.cfg.ServerlessRequestID,
		WorkerName:          s.cfg.WorkerName,
		WorkerNamespace:     s.cfg.WorkerNamespace,
		EventType:           serverless.WorkerMetricEventRegistered,
		Inflight:            0,
		ReportedAt:          time.Now().UTC(),
	}); err != nil {
		return err
	}

	go s.heartbeatLoop(ctx)

	ackWait, err := s.cfg.DispatchAckWaitDuration()
	if err != nil {
		return err
	}
	return consumer.ConsumeWorkerDispatches(
		ctx,
		s.cfg.ConsumerName,
		s.cfg.ServerlessRequestID,
		s.cfg.WorkerName,
		ackWait,
		s.HandleDispatch,
	)
}

// HandleDispatch processes one worker-targeted dispatch message.
func (s *Service) HandleDispatch(ctx context.Context, dispatch serverless.WorkerDispatchMessage) error {
	inflight := s.inflight.Add(1)
	defer s.inflight.Add(-1)

	startedAt := time.Now().UTC()
	s.logMetricError(ctx, serverless.WorkerMetricMessage{
		Version:             serverless.InvocationVersion,
		ServerlessRequestID: dispatch.ServerlessRequestID,
		WorkerName:          dispatch.WorkerName,
		WorkerNamespace:     dispatch.WorkerNamespace,
		InvocationID:        dispatch.InvocationID,
		EventType:           serverless.WorkerMetricEventInvocationStarted,
		Inflight:            inflight,
		ReportedAt:          startedAt,
	})

	invokeCtx := ctx
	cancel := func() {}
	if dispatch.TimeoutSeconds > 0 {
		invokeCtx, cancel = context.WithTimeout(ctx, time.Duration(dispatch.TimeoutSeconds)*time.Second)
	}
	defer cancel()

	frameworkResp, err := s.framework.Invoke(invokeCtx, serverless.FrameworkInvocationRequest{
		Version:             serverless.InvocationVersion,
		InvocationID:        dispatch.InvocationID,
		ServerlessRequestID: dispatch.ServerlessRequestID,
		WorkerName:          dispatch.WorkerName,
		WorkerNamespace:     dispatch.WorkerNamespace,
		Mode:                dispatch.Mode,
		ContentType:         dispatch.ContentType,
		Headers:             cloneStringMap(dispatch.Headers),
		Attributes:          cloneStringMap(dispatch.Attributes),
		Payload:             append([]byte(nil), dispatch.Payload...),
		TimeoutSeconds:      dispatch.TimeoutSeconds,
		DispatchedAt:        dispatch.DispatchedAt,
	})

	completedAt := time.Now().UTC()
	if err != nil {
		result := serverless.InvocationResultMessage{
			Version:             serverless.InvocationVersion,
			InvocationID:        dispatch.InvocationID,
			ServerlessRequestID: dispatch.ServerlessRequestID,
			Mode:                dispatch.Mode,
			ReplySubject:        dispatch.ReplySubject,
			WorkerName:          dispatch.WorkerName,
			WorkerNamespace:     dispatch.WorkerNamespace,
			StatusCode:          http.StatusBadGateway,
			Error:               err.Error(),
			StartedAt:           startedAt,
			CompletedAt:         completedAt,
		}
		if pubErr := s.results.PublishInvocationResult(ctx, result); pubErr != nil {
			return pubErr
		}
		s.logMetricError(ctx, serverless.WorkerMetricMessage{
			Version:             serverless.InvocationVersion,
			ServerlessRequestID: dispatch.ServerlessRequestID,
			WorkerName:          dispatch.WorkerName,
			WorkerNamespace:     dispatch.WorkerNamespace,
			InvocationID:        dispatch.InvocationID,
			EventType:           serverless.WorkerMetricEventInvocationFailed,
			Inflight:            s.inflight.Load(),
			StatusCode:          http.StatusBadGateway,
			Error:               err.Error(),
			ReportedAt:          completedAt,
		})
		return nil
	}

	statusCode := frameworkResp.StatusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	result := serverless.InvocationResultMessage{
		Version:             serverless.InvocationVersion,
		InvocationID:        dispatch.InvocationID,
		ServerlessRequestID: dispatch.ServerlessRequestID,
		Mode:                dispatch.Mode,
		ReplySubject:        dispatch.ReplySubject,
		WorkerName:          dispatch.WorkerName,
		WorkerNamespace:     dispatch.WorkerNamespace,
		StatusCode:          statusCode,
		ContentType:         frameworkResp.ContentType,
		Headers:             cloneStringMap(frameworkResp.Headers),
		Body:                append([]byte(nil), frameworkResp.Body...),
		Error:               frameworkResp.Error,
		StartedAt:           startedAt,
		CompletedAt:         completedAt,
	}
	if err := s.results.PublishInvocationResult(ctx, result); err != nil {
		return err
	}
	s.logMetricError(ctx, serverless.WorkerMetricMessage{
		Version:             serverless.InvocationVersion,
		ServerlessRequestID: dispatch.ServerlessRequestID,
		WorkerName:          dispatch.WorkerName,
		WorkerNamespace:     dispatch.WorkerNamespace,
		InvocationID:        dispatch.InvocationID,
		EventType:           serverless.WorkerMetricEventInvocationFinished,
		Inflight:            s.inflight.Load(),
		StatusCode:          statusCode,
		ReportedAt:          completedAt,
	})
	return nil
}

func (s *Service) heartbeatLoop(ctx context.Context) {
	interval, err := s.cfg.HeartbeatIntervalDuration()
	if err != nil {
		s.logger.Error("invalid heartbeat interval", "error", err)
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.logMetricError(ctx, serverless.WorkerMetricMessage{
				Version:             serverless.InvocationVersion,
				ServerlessRequestID: s.cfg.ServerlessRequestID,
				WorkerName:          s.cfg.WorkerName,
				WorkerNamespace:     s.cfg.WorkerNamespace,
				EventType:           serverless.WorkerMetricEventHeartbeat,
				Inflight:            s.inflight.Load(),
				ReportedAt:          time.Now().UTC(),
			})
		}
	}
}

func (s *Service) publishMetric(ctx context.Context, metric serverless.WorkerMetricMessage) error {
	return s.metrics.PublishWorkerMetric(ctx, metric)
}

func (s *Service) logMetricError(ctx context.Context, metric serverless.WorkerMetricMessage) {
	if err := s.publishMetric(ctx, metric); err != nil {
		s.logger.Error("publish worker metric failed",
			"workerName", metric.WorkerName,
			"eventType", metric.EventType,
			"error", err,
		)
	}
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
