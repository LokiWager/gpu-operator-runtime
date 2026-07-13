package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	_ "k8s.io/client-go/plugin/pkg/client/auth"

	networkingv1 "k8s.io/api/networking/v1"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	runtimev1alpha1 "github.com/loki/gpu-operator-runtime/api/v1alpha1"
	"github.com/loki/gpu-operator-runtime/pkg/activator"
	"github.com/loki/gpu-operator-runtime/pkg/serverless"
	"github.com/loki/gpu-operator-runtime/pkg/service"
)

var activatorScheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(activatorScheme))
	utilruntime.Must(networkingv1.AddToScheme(activatorScheme))
	utilruntime.Must(resourcev1.AddToScheme(activatorScheme))
	utilruntime.Must(runtimev1alpha1.AddToScheme(activatorScheme))
}

func main() {
	var configPath string

	flag.StringVar(&configPath, "config", "", "Path to the activator YAML configuration file.")
	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	cfg, err := activator.LoadConfig(configPath)
	if err != nil {
		ctrl.Log.WithName("setup").Error(err, "Failed to load activator config", "config", configPath)
		os.Exit(1)
	}

	kubeconfig := ""
	if f := flag.Lookup("kubeconfig"); f != nil {
		kubeconfig = f.Value.String()
	}

	restConfig, err := resolveRESTConfig(kubeconfig)
	if err != nil {
		ctrl.Log.WithName("setup").Error(err, "Failed to resolve Kubernetes config")
		os.Exit(1)
	}

	operatorClient, err := ctrlclient.New(restConfig, ctrlclient.Options{Scheme: activatorScheme})
	if err != nil {
		ctrl.Log.WithName("setup").Error(err, "Failed to create operator client")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	queue, err := serverless.NewNATSPublisher(ctx, cfg.Serverless, logger)
	if err != nil {
		ctrl.Log.WithName("setup").Error(err, "Failed to configure serverless queue")
		os.Exit(1)
	}
	defer queue.Close()

	runtimeService := service.New(nil, operatorClient, logger)
	activatorService := activator.New(runtimeService, queue, queue, logger, cfg)

	logger.Info("starting serverless activator",
		"consumerName", cfg.ConsumerName,
		"metricsConsumerName", cfg.MetricsConsumerName,
		"streamName", cfg.Serverless.StreamName,
	)
	if err := activatorService.Run(ctx, queue); err != nil {
		logger.Error("activator stopped with error", "error", err)
		os.Exit(1)
	}
}

func resolveRESTConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig == "" {
		return ctrl.GetConfig()
	}
	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}
