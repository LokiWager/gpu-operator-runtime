/*
Copyright 2026.
*/

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
	"github.com/loki/gpu-operator-runtime/internal/controller"
	"github.com/loki/gpu-operator-runtime/pkg/api"
	"github.com/loki/gpu-operator-runtime/pkg/jobs"
	"github.com/loki/gpu-operator-runtime/pkg/service"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(runtimev1alpha1.AddToScheme(scheme))
}

// nolint:gocyclo
func main() {
	var metricsAddr string
	var probeAddr string
	var httpAddr string
	var reportInterval time.Duration
	var enableLeaderElection bool
	var secureMetrics bool
	var enableHTTP2 bool
	var tlsOpts []func(*tls.Config)

	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.StringVar(&httpAddr, "http-addr", ":8080", "The address the API server binds to.")
	flag.DurationVar(&reportInterval, "report-interval", 30*time.Second, "Runtime status report interval.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election for controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true, "Serve metrics via HTTPS.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false, "Enable HTTP/2 for metrics endpoint.")
	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, func(c *tls.Config) {
			setupLog.Info("Disabling HTTP/2")
			c.NextProtos = []string{"http/1.1"}
		})
	}

	kubeconfig := ""
	if f := flag.Lookup("kubeconfig"); f != nil {
		kubeconfig = f.Value.String()
	}

	restConfig, err := resolveRESTConfig(kubeconfig)
	if err != nil {
		setupLog.Error(err, "Failed to resolve Kubernetes config")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress:   metricsAddr,
			SecureServing: secureMetrics,
			TLSOpts:       tlsOpts,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "9d4c4758.lokiwager.io",
	})
	if err != nil {
		setupLog.Error(err, "Failed to start manager")
		os.Exit(1)
	}

	if err := (&controller.StockPoolReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "StockPool")
		os.Exit(1)
	}

	kubeClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		setupLog.Error(err, "Failed to create kubernetes clientset")
		os.Exit(1)
	}

	appLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	svc := service.New(kubeClient, mgr.GetClient(), appLogger)

	httpHandler := api.NewServer(svc, appLogger)
	httpServer := &http.Server{
		Addr:              httpAddr,
		Handler:           httpHandler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	reporter := jobs.NewStatusReporter(svc, reportInterval, appLogger)

	if err := mgr.Add(nonLeaderRunnable{run: func(ctx context.Context) error {
		return startHTTPServer(ctx, httpServer)
	}}); err != nil {
		setupLog.Error(err, "Failed to add HTTP server runnable")
		os.Exit(1)
	}

	if err := mgr.Add(nonLeaderRunnable{run: func(ctx context.Context) error {
		reporter.Start(ctx)
		return nil
	}}); err != nil {
		setupLog.Error(err, "Failed to add reporter runnable")
		os.Exit(1)
	}

	if err := mgr.Add(nonLeaderRunnable{run: func(ctx context.Context) error {
		svc.StartOperatorJobWorker(ctx)
		return nil
	}}); err != nil {
		setupLog.Error(err, "Failed to add operator job worker runnable")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("Starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "Failed to run manager")
		os.Exit(1)
	}
}

func resolveRESTConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig == "" {
		return ctrl.GetConfig()
	}
	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}

type nonLeaderRunnable struct {
	run func(context.Context) error
}

func (r nonLeaderRunnable) Start(ctx context.Context) error {
	return r.run(ctx)
}

func (r nonLeaderRunnable) NeedLeaderElection() bool {
	return false
}

func startHTTPServer(ctx context.Context, srv *http.Server) error {
	errCh := make(chan error, 1)
	go func() {
		err := srv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		<-errCh
		return nil
	case err := <-errCh:
		return err
	}
}

var _ manager.LeaderElectionRunnable = nonLeaderRunnable{}
