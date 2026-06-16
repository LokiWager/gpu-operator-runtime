/*
Copyright 2026.
*/

package main

import (
	"crypto/tls"
	"flag"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/loki/gpu-operator-runtime/internal/controller"
	appconfig "github.com/loki/gpu-operator-runtime/pkg/config"
	"github.com/loki/gpu-operator-runtime/pkg/runtimeapp"
)

var setupLog = ctrl.Log.WithName("setup")

// main starts the reconciler-only controller manager process.
func main() {
	var configPath string
	var kubeconfig string
	var tlsOpts []func(*tls.Config)

	flag.StringVar(&configPath, "config", "", "Path to the controller-manager YAML configuration file.")
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig file. Defaults to in-cluster or ambient config.")
	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	cfg, err := appconfig.LoadControllerManagerConfig(configPath)
	if err != nil {
		setupLog.Error(err, "Failed to load controller manager config", "config", configPath)
		os.Exit(1)
	}
	blockedEgressCIDRs, err := cfg.NormalizedBlockedEgressCIDRs()
	if err != nil {
		setupLog.Error(err, "Invalid blocked egress CIDRs in controller manager config", "config", configPath)
		os.Exit(1)
	}
	serverlessCfg, err := cfg.Serverless.Normalized()
	if err != nil {
		setupLog.Error(err, "Invalid serverless queue config in controller manager config", "config", configPath)
		os.Exit(1)
	}
	serverlessWorkerCfg, err := cfg.ServerlessWorker.Normalized()
	if err != nil {
		setupLog.Error(err, "Invalid serverless worker config in controller manager config", "config", configPath)
		os.Exit(1)
	}

	if !cfg.EnableHTTP2 {
		tlsOpts = append(tlsOpts, func(c *tls.Config) {
			setupLog.Info("Disabling HTTP/2")
			c.NextProtos = []string{"http/1.1"}
		})
	}

	restConfig, err := runtimeapp.ResolveRESTConfig(kubeconfig)
	if err != nil {
		setupLog.Error(err, "Failed to resolve Kubernetes config")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme: runtimeapp.NewScheme(),
		Metrics: metricsserver.Options{
			BindAddress:   cfg.MetricsBindAddress,
			SecureServing: cfg.MetricsSecure,
			TLSOpts:       tlsOpts,
		},
		HealthProbeBindAddress: cfg.HealthProbeBindAddress,
		LeaderElection:         cfg.LeaderElect,
		LeaderElectionID:       "9d4c4758.lokiwager.io",
	})
	if err != nil {
		setupLog.Error(err, "Failed to start controller manager")
		os.Exit(1)
	}

	if err := (&controller.GPUUnitReconciler{
		Client:                mgr.GetClient(),
		Scheme:                mgr.GetScheme(),
		BlockedEgressCIDRs:    blockedEgressCIDRs,
		ServerlessQueueConfig: serverlessCfg,
		ServerlessWorker:      serverlessWorkerCfg,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "GPUUnit")
		os.Exit(1)
	}
	if err := (&controller.GPUStorageReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "GPUStorage")
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

	setupLog.Info("Starting controller manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "Failed to run controller manager")
		os.Exit(1)
	}
}
