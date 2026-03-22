package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/loki/gpu-operator-runtime/pkg/service"
)

// StatusReporter periodically logs a coarse runtime health snapshot.
type StatusReporter struct {
	service  *service.Service
	interval time.Duration
	logger   *slog.Logger
}

// NewStatusReporter builds a reporter that polls service health on a fixed interval.
func NewStatusReporter(service *service.Service, interval time.Duration, logger *slog.Logger) *StatusReporter {
	return &StatusReporter{
		service:  service,
		interval: interval,
		logger:   logger,
	}
}

// Start runs the reporting loop until the context is cancelled.
func (r *StatusReporter) Start(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			health, err := r.service.Health(ctx)
			if err != nil {
				r.logger.Error("status report failed", "err", err)
				continue
			}

			r.logger.Info("runtime status",
				"kubeConnected", health.KubernetesConnected,
				"nodeCount", health.NodeCount,
				"stockUnits", health.StockUnitCount,
				"activeUnits", health.ActiveUnitCount,
			)
		}
	}
}
