/*
Copyright 2026.
*/

package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/loki/gpu-operator-runtime/pkg/api"
	appconfig "github.com/loki/gpu-operator-runtime/pkg/config"
	"github.com/loki/gpu-operator-runtime/pkg/jobs"
	"github.com/loki/gpu-operator-runtime/pkg/runtimeapp"
	"github.com/loki/gpu-operator-runtime/pkg/serverless"
	"github.com/loki/gpu-operator-runtime/pkg/service"
)

var setupLog = ctrl.Log.WithName("setup")

// main starts the runtime HTTP API and API-owned background workers.
func main() {
	var configPath string
	var kubeconfig string

	flag.StringVar(&configPath, "config", "", "Path to the runtime-api YAML configuration file.")
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig file. Defaults to in-cluster or ambient config.")
	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	cfg, err := appconfig.LoadRuntimeAPIConfig(configPath)
	if err != nil {
		setupLog.Error(err, "Failed to load runtime API config", "config", configPath)
		os.Exit(1)
	}
	reportInterval, err := cfg.ReportIntervalDuration()
	if err != nil {
		setupLog.Error(err, "Invalid report interval in runtime API config", "config", configPath)
		os.Exit(1)
	}
	serverlessCfg, err := cfg.Serverless.Normalized()
	if err != nil {
		setupLog.Error(err, "Invalid serverless queue config in runtime API config", "config", configPath)
		os.Exit(1)
	}

	restConfig, err := runtimeapp.ResolveRESTConfig(kubeconfig)
	if err != nil {
		setupLog.Error(err, "Failed to resolve Kubernetes config")
		os.Exit(1)
	}

	scheme := runtimeapp.NewScheme()
	operatorClient, err := ctrlclient.New(restConfig, ctrlclient.Options{Scheme: scheme})
	if err != nil {
		setupLog.Error(err, "Failed to create runtime operator client")
		os.Exit(1)
	}
	kubeClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		setupLog.Error(err, "Failed to create kubernetes clientset")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	appLogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	svc := service.New(kubeClient, operatorClient, appLogger)
	if err := svc.ConfigureRuntimePackages(cfg.Packages); err != nil {
		setupLog.Error(err, "Invalid runtime package catalog", "config", configPath)
		os.Exit(1)
	}
	svc.ConfigureNvidiaMetrics(cfg.NvidiaMetricsEndpoint, nil)
	serverlessPublisher, err := serverless.NewNATSPublisher(ctx, serverlessCfg, appLogger)
	if err != nil {
		setupLog.Error(err, "Failed to configure serverless queue publisher")
		os.Exit(1)
	}
	defer serverlessPublisher.Close()
	svc.ConfigureServerlessPublisher(serverlessPublisher)

	reporter := jobs.NewStatusReporter(svc, reportInterval, appLogger)
	go reporter.Start(ctx)

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           api.NewServer(svc, appLogger),
		ReadHeaderTimeout: 5 * time.Second,
	}

	setupLog.Info("Starting runtime API", "addr", cfg.HTTPAddr)
	if err := runtimeapp.RunHTTPServer(ctx, httpServer, 10*time.Second); err != nil {
		setupLog.Error(err, "Failed to run runtime API")
		os.Exit(1)
	}
}
