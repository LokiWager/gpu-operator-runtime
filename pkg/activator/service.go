package activator

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
	"github.com/loki/gpu-operator-runtime/pkg/domain"
	"github.com/loki/gpu-operator-runtime/pkg/serverless"
	runtimeService "github.com/loki/gpu-operator-runtime/pkg/service"
)

// RuntimeControl captures the runtime operations the activator needs.
type RuntimeControl interface {
	ListGPUUnits(ctx context.Context, namespace string) ([]domain.GPUUnitRuntime, error)
	CreateGPUUnit(ctx context.Context, req runtimeService.CreateGPUUnitRequest) (domain.GPUUnitRuntime, bool, error)
	GetGPUUnit(ctx context.Context, namespace, name string) (domain.GPUUnitRuntime, error)
}

// Service coordinates durable invocation consumption, worker registration, worker creation, and worker-dispatch publication.
type Service struct {
	runtime    RuntimeControl
	dispatches serverless.WorkerDispatchPublisher
	results    serverless.InvocationResultPublisher
	logger     *slog.Logger
	cfg        Config
	registry   *WorkerRegistry
}

// New builds a dedicated activator service.
func New(runtime RuntimeControl, dispatches serverless.WorkerDispatchPublisher, results serverless.InvocationResultPublisher, logger *slog.Logger, cfg Config) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		runtime:    runtime,
		dispatches: dispatches,
		results:    results,
		logger:     logger,
		cfg:        cfg,
		registry:   NewWorkerRegistry(),
	}
}

// Run drains queued invocations from JetStream until the context is cancelled.
func (s *Service) Run(ctx context.Context, consumer serverless.InvocationConsumer) error {
	if consumer == nil {
		return fmt.Errorf("invocation consumer is required")
	}
	return consumer.ConsumeInvocations(ctx, s.cfg.ConsumerName, s.cfg.AckWaitDuration(), s.ProcessInvocation)
}

// ProcessInvocation resolves one worker, publishes one worker-targeted dispatch message, and emits durable failure results when dispatch cannot proceed.
func (s *Service) ProcessInvocation(ctx context.Context, invocation serverless.InvocationMessage) error {
	result := serverless.InvocationResultMessage{
		Version:             serverless.InvocationVersion,
		InvocationID:        invocation.InvocationID,
		ServerlessRequestID: invocation.ServerlessRequestID,
		Mode:                invocation.Mode,
		ReplySubject:        invocation.ReplySubject,
		CompletedAt:         time.Now().UTC(),
	}

	worker, err := s.acquireWorker(ctx, invocation.ServerlessRequestID, invocation.InvocationID)
	if err != nil {
		result.StatusCode = http.StatusServiceUnavailable
		result.Error = err.Error()
		return s.publishResult(ctx, result)
	}

	if err := s.dispatchToWorker(ctx, worker, invocation); err != nil {
		result.WorkerName = worker.Name
		result.WorkerNamespace = worker.Namespace
		result.StatusCode = http.StatusBadGateway
		result.Error = err.Error()
		return s.publishResult(ctx, result)
	}

	s.logger.Info("serverless invocation dispatched",
		"invocationID", invocation.InvocationID,
		"serverlessRequestID", invocation.ServerlessRequestID,
		"workerName", worker.Name,
		"workerNamespace", worker.Namespace,
	)
	return nil
}

func (s *Service) publishResult(ctx context.Context, result serverless.InvocationResultMessage) error {
	if result.CompletedAt.IsZero() {
		result.CompletedAt = time.Now().UTC()
	}
	if s.results == nil {
		return fmt.Errorf("invocation result publisher is required")
	}
	if err := s.results.PublishInvocationResult(ctx, result); err != nil {
		return err
	}
	s.logger.Info("serverless invocation completed",
		"invocationID", result.InvocationID,
		"serverlessRequestID", result.ServerlessRequestID,
		"workerName", result.WorkerName,
		"statusCode", result.StatusCode,
		"error", result.Error,
	)
	return nil
}

func (s *Service) acquireWorker(ctx context.Context, requestID, invocationID string) (Worker, error) {
	units, err := s.listServerlessUnits(ctx, requestID)
	if err != nil {
		return Worker{}, err
	}
	s.registry.Sync(requestID, units)
	if worker, ok := s.registry.Pick(requestID); ok {
		return worker, nil
	}
	if len(units) == 0 {
		return Worker{}, fmt.Errorf("no registered GPUUnit template for serverlessRequestID %q", requestID)
	}

	if candidate, ok := firstPendingWorker(units, requestID); ok {
		worker, err := s.waitForWorkerReady(ctx, candidate.Name)
		if err == nil {
			return worker, nil
		}
		s.logger.Info("pending serverless worker did not become ready in time; creating a new worker",
			"requestID", requestID,
			"candidate", candidate.Name,
			"error", err,
		)
	}

	template := templateUnit(units)
	created, _, err := s.runtime.CreateGPUUnit(ctx, buildCreateRequest(template, invocationID))
	if err != nil {
		return Worker{}, fmt.Errorf("create serverless worker from template %s: %w", template.Name, err)
	}
	worker, err := s.waitForWorkerReady(ctx, created.Name)
	if err != nil {
		return Worker{}, err
	}
	return worker, nil
}

func (s *Service) waitForWorkerReady(ctx context.Context, name string) (Worker, error) {
	readyCtx, cancel := context.WithTimeout(ctx, s.cfg.WorkerReadyWaitDuration())
	defer cancel()

	ticker := time.NewTicker(s.cfg.WorkerPollIntervalDuration())
	defer ticker.Stop()

	for {
		unit, err := s.runtime.GetGPUUnit(readyCtx, runtimev1alpha1.DefaultInstanceNamespace, name)
		if err == nil && isReadyWorkerUnit(unit, unit.Serverless.RequestID) {
			s.registry.Sync(unit.Serverless.RequestID, []domain.GPUUnitRuntime{unit})
			return workerFromUnit(unit), nil
		}

		select {
		case <-readyCtx.Done():
			return Worker{}, fmt.Errorf("wait for worker %s to become ready: %w", name, readyCtx.Err())
		case <-ticker.C:
		}
	}
}

func (s *Service) listServerlessUnits(ctx context.Context, requestID string) ([]domain.GPUUnitRuntime, error) {
	units, err := s.runtime.ListGPUUnits(ctx, "")
	if err != nil {
		return nil, err
	}

	filtered := make([]domain.GPUUnitRuntime, 0, len(units))
	for _, unit := range units {
		if !unit.Serverless.Enabled || unit.Serverless.RequestID != requestID {
			continue
		}
		filtered = append(filtered, unit)
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Name < filtered[j].Name
	})
	return filtered, nil
}

func (s *Service) dispatchToWorker(ctx context.Context, worker Worker, invocation serverless.InvocationMessage) error {
	if s.dispatches == nil {
		return fmt.Errorf("worker dispatch publisher is required")
	}

	resultSubject := strings.TrimSpace(invocation.ResultSubject)
	if resultSubject == "" {
		resultSubject = serverless.ResultSubject(s.cfg.Serverless.SubjectPrefix, invocation.ServerlessRequestID)
	}
	metricsSubject := strings.TrimSpace(invocation.MetricsSubject)
	if metricsSubject == "" {
		metricsSubject = serverless.MetricsSubject(s.cfg.Serverless.SubjectPrefix, invocation.ServerlessRequestID)
	}

	return s.dispatches.PublishWorkerDispatch(ctx, serverless.WorkerDispatchMessage{
		Version:             serverless.InvocationVersion,
		InvocationID:        invocation.InvocationID,
		ServerlessRequestID: invocation.ServerlessRequestID,
		WorkerName:          worker.Name,
		WorkerNamespace:     worker.Namespace,
		Mode:                invocation.Mode,
		ContentType:         invocation.ContentType,
		Headers:             cloneStringMap(invocation.Headers),
		Attributes:          cloneStringMap(invocation.Attributes),
		Payload:             append([]byte(nil), invocation.Payload...),
		TimeoutSeconds:      invocation.TimeoutSeconds,
		ResultSubject:       resultSubject,
		MetricsSubject:      metricsSubject,
		ReplySubject:        invocation.ReplySubject,
		DispatchedAt:        time.Now().UTC(),
	})
}

func buildCreateRequest(template domain.GPUUnitRuntime, invocationID string) runtimeService.CreateGPUUnitRequest {
	return runtimeService.CreateGPUUnitRequest{
		OperationID:   "activate-" + invocationID,
		Name:          generatedWorkerName(template.Serverless.RequestID, invocationID),
		SpecName:      template.SpecName,
		Image:         template.Image,
		Template:      template.Template,
		Access:        template.Access,
		SSH:           template.SSH,
		Serverless:    template.Serverless,
		StorageMounts: append([]runtimev1alpha1.GPUUnitStorageMount(nil), template.StorageMounts...),
	}
}

func generatedWorkerName(requestID, invocationID string) string {
	base := strings.ToLower(strings.TrimSpace(requestID))
	base = strings.ReplaceAll(base, "_", "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = "serverless"
	}
	suffix := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(invocationID)), "inv-")
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	prefix := "unit-" + base
	if len(prefix) > 54 {
		prefix = prefix[:54]
		prefix = strings.TrimRight(prefix, "-")
	}
	return prefix + "-" + suffix
}

func isReadyWorkerUnit(unit domain.GPUUnitRuntime, requestID string) bool {
	return unit.Serverless.Enabled &&
		unit.Serverless.RequestID == requestID &&
		unit.Phase == runtimev1alpha1.PhaseReady &&
		strings.TrimSpace(unit.AccessURL) != ""
}

func firstPendingWorker(units []domain.GPUUnitRuntime, requestID string) (domain.GPUUnitRuntime, bool) {
	for _, unit := range units {
		if !unit.Serverless.Enabled || unit.Serverless.RequestID != requestID {
			continue
		}
		if unit.Phase == runtimev1alpha1.PhaseReady {
			continue
		}
		return unit, true
	}
	return domain.GPUUnitRuntime{}, false
}

func templateUnit(units []domain.GPUUnitRuntime) domain.GPUUnitRuntime {
	return units[0]
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
