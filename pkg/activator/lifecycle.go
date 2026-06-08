package activator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"sync"
	"time"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
	"github.com/loki/gpu-operator-runtime/pkg/domain"
	"github.com/loki/gpu-operator-runtime/pkg/serverless"
)

// WorkerState is the activator's metrics-derived view of one concrete worker.
type WorkerState struct {
	ServerlessRequestID string                           `json:"serverlessRequestID"`
	WorkerName          string                           `json:"workerName"`
	WorkerNamespace     string                           `json:"workerNamespace"`
	LastEvent           serverless.WorkerMetricEventType `json:"lastEvent"`
	LastSeen            time.Time                        `json:"lastSeen"`
	LastActivity        time.Time                        `json:"lastActivity"`
	Inflight            int32                            `json:"inflight"`
}

// LifecycleManager reconciles serverless worker pools from GPUUnit specs and sidecar metrics.
type LifecycleManager struct {
	runtime  RuntimeControl
	registry *WorkerRegistry
	logger   *slog.Logger
	cfg      Config

	mu      sync.RWMutex
	workers map[string]WorkerState
}

// NewLifecycleManager builds a lifecycle reconciler for activator-owned worker pools.
func NewLifecycleManager(runtime RuntimeControl, registry *WorkerRegistry, logger *slog.Logger, cfg Config) *LifecycleManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &LifecycleManager{
		runtime:  runtime,
		registry: registry,
		logger:   logger,
		cfg:      cfg,
		workers:  map[string]WorkerState{},
	}
}

// ObserveMetric folds one worker-side metric event into the lifecycle state table.
func (m *LifecycleManager) ObserveMetric(metric serverless.WorkerMetricMessage) {
	if m == nil || metric.ServerlessRequestID == "" || metric.WorkerName == "" {
		return
	}
	reportedAt := metric.ReportedAt
	if reportedAt.IsZero() {
		reportedAt = time.Now().UTC()
	}

	key := workerStateKey(metric.WorkerNamespace, metric.WorkerName)
	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.workers[key]
	state.ServerlessRequestID = metric.ServerlessRequestID
	state.WorkerName = metric.WorkerName
	state.WorkerNamespace = metric.WorkerNamespace
	state.LastEvent = metric.EventType
	state.LastSeen = reportedAt
	state.Inflight = metric.Inflight
	switch metric.EventType {
	case serverless.WorkerMetricEventRegistered:
		if state.LastActivity.IsZero() {
			state.LastActivity = reportedAt
		}
	case serverless.WorkerMetricEventInvocationStarted,
		serverless.WorkerMetricEventInvocationFinished,
		serverless.WorkerMetricEventInvocationFailed:
		state.LastActivity = reportedAt
	}
	m.workers[key] = state
}

// Snapshot returns a copy of the current lifecycle state table.
func (m *LifecycleManager) Snapshot() map[string]WorkerState {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make(map[string]WorkerState, len(m.workers))
	for key, value := range m.workers {
		out[key] = value
	}
	return out
}

// Reconcile enforces min-available and idle-timeout policy for all serverless request IDs.
func (m *LifecycleManager) Reconcile(ctx context.Context) error {
	if m == nil || m.runtime == nil {
		return nil
	}

	units, err := m.runtime.ListGPUUnits(ctx, "")
	if err != nil {
		return err
	}

	groups := groupServerlessUnits(units)
	var errs []error
	for requestID, requestUnits := range groups {
		if m.registry != nil {
			m.registry.Sync(requestID, requestUnits)
		}
		if err := m.ensureMinAvailable(ctx, requestID, requestUnits); err != nil {
			errs = append(errs, err)
		}
		if err := m.deleteIdleWorkers(ctx, requestID, requestUnits); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *LifecycleManager) ensureMinAvailable(ctx context.Context, requestID string, units []domain.GPUUnitRuntime) error {
	if len(units) == 0 {
		return nil
	}
	template := templateUnit(units)
	minAvailable := int(template.Serverless.MinAvailableCount)
	if minAvailable <= 0 {
		return nil
	}

	available := 0
	for _, unit := range units {
		if unit.Phase == runtimev1alpha1.PhaseReady || unit.Phase == runtimev1alpha1.PhaseProgressing {
			available++
		}
	}
	missing := minAvailable - available
	if missing <= 0 {
		return nil
	}

	var errs []error
	for i := 0; i < missing; i++ {
		sourceID := prewarmSourceID(time.Now().UTC().Add(time.Duration(i) * time.Nanosecond))
		req := buildCreateRequest(template, sourceID)
		if _, _, err := m.runtime.CreateGPUUnit(ctx, req); err != nil {
			errs = append(errs, fmt.Errorf("prewarm worker for serverlessRequestID %s: %w", requestID, err))
			continue
		}
		m.logger.Info("created prewarm serverless worker",
			"serverlessRequestID", requestID,
			"workerName", req.Name,
			"minAvailable", minAvailable,
		)
	}
	return errors.Join(errs...)
}

func (m *LifecycleManager) deleteIdleWorkers(ctx context.Context, requestID string, units []domain.GPUUnitRuntime) error {
	if len(units) == 0 {
		return nil
	}
	template := templateUnit(units)
	idleTimeoutSeconds := template.Serverless.IdleTimeoutSeconds
	if idleTimeoutSeconds <= 0 {
		return nil
	}

	minAvailable := int(template.Serverless.MinAvailableCount)
	readyUnits := make([]domain.GPUUnitRuntime, 0, len(units))
	for _, unit := range units {
		if isReadyWorkerUnit(unit, requestID) {
			readyUnits = append(readyUnits, unit)
		}
	}
	if len(readyUnits) <= minAvailable {
		return nil
	}

	now := time.Now().UTC()
	idleTimeout := time.Duration(idleTimeoutSeconds) * time.Second
	candidates := make([]idleWorkerCandidate, 0, len(readyUnits))
	for _, unit := range readyUnits {
		if !isActivatorManagedWorker(unit) {
			continue
		}
		state, ok := m.workerState(unit.Namespace, unit.Name)
		if !ok || state.Inflight != 0 {
			continue
		}
		idleSince := state.LastActivity
		if idleSince.IsZero() {
			idleSince = state.LastSeen
		}
		if idleSince.IsZero() || now.Sub(idleSince) < idleTimeout {
			continue
		}
		candidates = append(candidates, idleWorkerCandidate{unit: unit, idleSince: idleSince})
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].idleSince.Before(candidates[j].idleSince)
	})

	deletions := len(readyUnits) - minAvailable
	if deletions > len(candidates) {
		deletions = len(candidates)
	}

	var errs []error
	for i := 0; i < deletions; i++ {
		unit := candidates[i].unit
		if err := m.runtime.DeleteGPUUnit(ctx, unit.Namespace, unit.Name); err != nil {
			errs = append(errs, fmt.Errorf("delete idle worker %s/%s: %w", unit.Namespace, unit.Name, err))
			continue
		}
		m.logger.Info("deleted idle serverless worker",
			"serverlessRequestID", requestID,
			"workerName", unit.Name,
			"workerNamespace", unit.Namespace,
			"idleSince", candidates[i].idleSince,
		)
	}
	return errors.Join(errs...)
}

func (m *LifecycleManager) workerState(namespace, name string) (WorkerState, bool) {
	key := workerStateKey(namespace, name)
	m.mu.RLock()
	defer m.mu.RUnlock()
	state, ok := m.workers[key]
	return state, ok
}

type idleWorkerCandidate struct {
	unit      domain.GPUUnitRuntime
	idleSince time.Time
}

func groupServerlessUnits(units []domain.GPUUnitRuntime) map[string][]domain.GPUUnitRuntime {
	groups := map[string][]domain.GPUUnitRuntime{}
	for _, unit := range units {
		if !unit.Serverless.Enabled || unit.Serverless.RequestID == "" {
			continue
		}
		groups[unit.Serverless.RequestID] = append(groups[unit.Serverless.RequestID], unit)
	}
	for requestID := range groups {
		sort.Slice(groups[requestID], func(i, j int) bool {
			return groups[requestID][i].Name < groups[requestID][j].Name
		})
	}
	return groups
}

func workerStateKey(namespace, name string) string {
	if namespace == "" {
		namespace = runtimev1alpha1.DefaultInstanceNamespace
	}
	return namespace + "/" + name
}

func prewarmSourceID(now time.Time) string {
	return "warm-" + strconv.FormatInt(now.UnixNano(), 36)
}
