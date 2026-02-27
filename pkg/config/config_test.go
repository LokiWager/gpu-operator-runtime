package config

import (
	"testing"
	"time"
)

func TestParseKubeMode(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    KubeMode
		wantErr bool
	}{
		{name: "auto", in: "auto", want: KubeModeAuto},
		{name: "off with spaces", in: " OFF ", want: KubeModeOff},
		{name: "required", in: "required", want: KubeModeRequired},
		{name: "invalid", in: "foo", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseKubeMode(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestConfigValidate(t *testing.T) {
	valid := Config{
		HTTPAddr:       ":8080",
		ReportInterval: 30 * time.Second,
		KubeMode:       KubeModeAuto,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	invalids := []Config{
		{ReportInterval: 30 * time.Second, KubeMode: KubeModeAuto},
		{HTTPAddr: ":8080", ReportInterval: time.Second, KubeMode: KubeModeAuto},
		{HTTPAddr: ":8080", ReportInterval: 30 * time.Second, KubeMode: "invalid"},
	}

	for i, cfg := range invalids {
		if err := cfg.Validate(); err == nil {
			t.Fatalf("invalid config[%d] expected error", i)
		}
	}
}
