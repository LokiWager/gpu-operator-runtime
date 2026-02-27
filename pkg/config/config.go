package config

import (
	"fmt"
	"strings"
	"time"
)

type KubeMode string

const (
	KubeModeAuto     KubeMode = "auto"
	KubeModeOff      KubeMode = "off"
	KubeModeRequired KubeMode = "required"
)

type Config struct {
	HTTPAddr       string
	ReportInterval time.Duration
	KubeMode       KubeMode
	Kubeconfig     string
}

func ParseKubeMode(v string) (KubeMode, error) {
	mode := KubeMode(strings.ToLower(strings.TrimSpace(v)))
	switch mode {
	case KubeModeAuto, KubeModeOff, KubeModeRequired:
		return mode, nil
	default:
		return "", fmt.Errorf("unsupported kube mode %q", v)
	}
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.HTTPAddr) == "" {
		return fmt.Errorf("http addr is required")
	}
	if c.ReportInterval < 5*time.Second {
		return fmt.Errorf("report interval should be >= 5s")
	}
	switch c.KubeMode {
	case KubeModeAuto, KubeModeOff, KubeModeRequired:
		return nil
	default:
		return fmt.Errorf("invalid kube mode %q", c.KubeMode)
	}
}
