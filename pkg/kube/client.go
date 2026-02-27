package kube

import (
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"

	"github.com/loki/gpu-operator-runtime/pkg/config"
)

func BuildClient(mode config.KubeMode, kubeconfig string) (kubernetes.Interface, error) {
	if mode == config.KubeModeOff {
		return nil, nil
	}

	restConfig, err := buildRestConfig(kubeconfig)
	if err != nil {
		if mode == config.KubeModeAuto {
			return nil, nil
		}
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		if mode == config.KubeModeAuto {
			return nil, nil
		}
		return nil, err
	}
	return clientset, nil
}

func buildRestConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}

	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}

	home := homedir.HomeDir()
	if home == "" {
		return nil, fmt.Errorf("cannot resolve in-cluster config and home directory")
	}
	path := filepath.Join(home, ".kube", "config")
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("cannot find kubeconfig at %s", path)
	}

	return clientcmd.BuildConfigFromFlags("", path)
}
