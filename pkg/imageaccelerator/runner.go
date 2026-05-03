package imageaccelerator

import (
	"context"
	"fmt"
	"os"

	officialbuilder "github.com/containerd/accelerated-container-image/cmd/convertor/builder"
)

// Run executes one userspace image conversion request through the official overlaybd convertor builder.
func Run(ctx context.Context, cfg Config) error {
	normalized, err := cfg.Normalized()
	if err != nil {
		return err
	}
	if err := validateToolchainLayout(normalized); err != nil {
		return err
	}
	if err := os.MkdirAll(normalized.WorkDir, 0o755); err != nil {
		return fmt.Errorf("create workDir %s: %w", normalized.WorkDir, err)
	}

	opts, err := buildOptions(normalized)
	if err != nil {
		return err
	}
	if err := officialbuilder.Build(ctx, opts); err != nil {
		return fmt.Errorf("convert image %s -> %s: %w", normalized.Source, normalized.Target, err)
	}
	return nil
}

func buildOptions(cfg Config) (officialbuilder.BuilderOptions, error) {
	engine, err := builderEngine(cfg.Engine)
	if err != nil {
		return officialbuilder.BuilderOptions{}, err
	}
	return officialbuilder.BuilderOptions{
		Ref:       cfg.Source,
		TargetRef: cfg.Target,
		Auth:      cfg.Registry.auth(),
		PlainHTTP: cfg.Registry.PlainHTTP,
		WorkDir:   cfg.WorkDir,
		OCI:       cfg.OCI,
		FsType:    cfg.OverlayBD.FSType,
		Mkfs:      cfg.OverlayBD.Mkfs,
		Vsize:     cfg.OverlayBD.VSizeGB,
		Engine:    engine,
		CertOption: officialbuilder.CertOption{
			CertDirs:    append([]string(nil), cfg.TLS.CertDirs...),
			RootCAs:     append([]string(nil), cfg.TLS.RootCAs...),
			ClientCerts: append([]string(nil), cfg.TLS.ClientCerts...),
			Insecure:    cfg.TLS.Insecure,
		},
		Reserve:          cfg.Debug.Reserve,
		NoUpload:         cfg.Debug.NoUpload,
		DumpManifest:     cfg.Debug.DumpManifest,
		ConcurrencyLimit: cfg.ConcurrencyLimit,
		DisableSparse:    cfg.OverlayBD.DisableSparse,
		Referrer:         cfg.Referrer,
	}, nil
}

func builderEngine(name string) (officialbuilder.BuilderEngineType, error) {
	switch normalizeEngine(name) {
	case EngineOverlayBD:
		return officialbuilder.Overlaybd, nil
	case EngineTurboOCI:
		return officialbuilder.TurboOCI, nil
	default:
		return officialbuilder.Overlaybd, fmt.Errorf("unsupported engine %q", name)
	}
}
