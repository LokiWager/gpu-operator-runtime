package imageaccelerator

import "testing"

func TestNormalizedDefaults(t *testing.T) {
	cfg, err := (Config{
		Source: "docker.io/library/redis:7",
		Target: "docker.io/example/redis:7-obd",
	}).Normalized()
	if err != nil {
		t.Fatalf("normalize config: %v", err)
	}

	if cfg.Engine != EngineOverlayBD {
		t.Fatalf("expected default engine %q, got %q", EngineOverlayBD, cfg.Engine)
	}
	if cfg.WorkDir == "" {
		t.Fatalf("expected default workDir")
	}
	if cfg.OverlayBD.FSType != defaultFSType {
		t.Fatalf("expected default fsType %q, got %q", defaultFSType, cfg.OverlayBD.FSType)
	}
	if cfg.OverlayBD.VSizeGB != defaultVSizeGB {
		t.Fatalf("expected default vsize %d, got %d", defaultVSizeGB, cfg.OverlayBD.VSizeGB)
	}
	if cfg.ConcurrencyLimit != defaultConcurrencyLimit {
		t.Fatalf("expected default concurrency %d, got %d", defaultConcurrencyLimit, cfg.ConcurrencyLimit)
	}
}

func TestNormalizedValidatesInputs(t *testing.T) {
	_, err := (Config{
		Source: "docker.io/library/redis:7",
		Target: "docker.io/library/redis:7",
	}).Normalized()
	if err == nil {
		t.Fatalf("expected target/source validation error")
	}

	_, err = (Config{
		Source: "docker.io/library/redis:7",
		Target: "docker.io/example/redis:7-obd",
		Engine: "unknown",
	}).Normalized()
	if err == nil {
		t.Fatalf("expected engine validation error")
	}

	_, err = (Config{
		Source: "docker.io/library/redis:7",
		Target: "docker.io/example/redis:7-obd",
		Registry: RegistryConfig{
			Password: "secret",
		},
	}).Normalized()
	if err == nil {
		t.Fatalf("expected registry auth validation error")
	}
}

func TestBuildOptionsMapsOfficialBuilderFields(t *testing.T) {
	cfg, err := (Config{
		Source:           "docker.io/library/redis:7",
		Target:           "docker.io/example/redis:7-obd",
		Engine:           EngineTurboOCI,
		OCI:              true,
		Referrer:         true,
		ConcurrencyLimit: 8,
		Registry: RegistryConfig{
			Username:  "demo",
			Password:  "secret",
			PlainHTTP: true,
		},
		TLS: TLSConfig{
			CertDirs:    []string{"/tmp/certs"},
			RootCAs:     []string{"/tmp/root.crt"},
			ClientCerts: []string{"/tmp/client.cert:/tmp/client.key"},
			Insecure:    true,
		},
		OverlayBD: OverlayBDConfig{
			FSType:        "ext4",
			Mkfs:          true,
			VSizeGB:       128,
			DisableSparse: true,
		},
		Debug: DebugConfig{
			Reserve:      true,
			NoUpload:     true,
			DumpManifest: true,
		},
	}).Normalized()
	if err != nil {
		t.Fatalf("normalize config: %v", err)
	}

	opts, err := buildOptions(cfg)
	if err != nil {
		t.Fatalf("build options: %v", err)
	}

	if opts.Ref != cfg.Source || opts.TargetRef != cfg.Target {
		t.Fatalf("unexpected refs %+v", opts)
	}
	if opts.Auth != "demo:secret" {
		t.Fatalf("expected auth demo:secret, got %q", opts.Auth)
	}
	if !opts.PlainHTTP || !opts.OCI || !opts.Referrer {
		t.Fatalf("expected mapped transport and oci flags %+v", opts)
	}
	if opts.Vsize != 128 || !opts.DisableSparse || !opts.NoUpload || !opts.DumpManifest || !opts.Reserve {
		t.Fatalf("expected mapped debug/overlaybd fields %+v", opts)
	}
	if opts.ConcurrencyLimit != 8 {
		t.Fatalf("expected concurrency 8, got %d", opts.ConcurrencyLimit)
	}
}

func TestExpectedToolchainPaths(t *testing.T) {
	cfg := DefaultConfig()
	paths := expectedToolchainPaths(cfg)
	if len(paths) != 3 {
		t.Fatalf("expected 3 overlaybd binaries, got %d", len(paths))
	}

	cfg.Engine = EngineTurboOCI
	paths = expectedToolchainPaths(cfg)
	if len(paths) != 4 {
		t.Fatalf("expected turboOCI binary to be required, got %d entries", len(paths))
	}

	cfg = DefaultConfig()
	cfg.OverlayBD.Mkfs = false
	paths = expectedToolchainPaths(cfg)
	if len(paths) != 4 {
		t.Fatalf("expected baselayer to be required when mkfs=false, got %d entries", len(paths))
	}
}
