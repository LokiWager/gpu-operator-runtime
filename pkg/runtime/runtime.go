package runtime

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/loki/gpu-operator-runtime/pkg/api"
	"github.com/loki/gpu-operator-runtime/pkg/config"
	"github.com/loki/gpu-operator-runtime/pkg/jobs"
	"github.com/loki/gpu-operator-runtime/pkg/kube"
	"github.com/loki/gpu-operator-runtime/pkg/service"
	"github.com/loki/gpu-operator-runtime/pkg/store"
)

type Runtime struct {
	cfg        config.Config
	logger     *slog.Logger
	httpServer *http.Server
	reporter   *jobs.StatusReporter
}

func New(cfg config.Config) (*Runtime, error) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	kubeClient, err := kube.BuildClient(cfg.KubeMode, cfg.Kubeconfig)
	if err != nil {
		return nil, err
	}

	memStore := store.NewMemoryStore()
	svc := service.New(memStore, kubeClient, logger)

	handler := api.NewServer(svc, logger)
	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	return &Runtime{
		cfg:        cfg,
		logger:     logger,
		httpServer: httpServer,
		reporter:   jobs.NewStatusReporter(svc, cfg.ReportInterval, logger),
	}, nil
}

func (r *Runtime) Run(ctx context.Context) error {
	go r.reporter.Start(ctx)

	errCh := make(chan error, 1)
	go func() {
		r.logger.Info("runtime started", "addr", r.cfg.HTTPAddr, "kubeMode", r.cfg.KubeMode)
		if err := r.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		r.logger.Info("shutdown signal received")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = r.httpServer.Shutdown(shutdownCtx)
		<-errCh
		return nil
	case err := <-errCh:
		return err
	}
}
