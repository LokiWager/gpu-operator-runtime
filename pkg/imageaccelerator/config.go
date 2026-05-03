package imageaccelerator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	EngineOverlayBD = "overlaybd"
	EngineTurboOCI  = "turbo-oci"

	defaultWorkDir          = "tmp_conv"
	defaultFSType           = "ext4"
	defaultVSizeGB          = 64
	defaultConcurrencyLimit = 4

	overlayBDBinDir       = "/opt/overlaybd/bin"
	overlayBDBaseLayer    = "/opt/overlaybd/baselayers/ext4_64"
	overlayBDCreateBinary = overlayBDBinDir + "/overlaybd-create"
	overlayBDCommitBinary = overlayBDBinDir + "/overlaybd-commit"
	overlayBDApplyBinary  = overlayBDBinDir + "/overlaybd-apply"
	turboOCIApplyBinary   = overlayBDBinDir + "/turboOCI-apply"
)

// Config captures one standalone userspace image conversion request.
type Config struct {
	Source           string          `yaml:"source"`
	Target           string          `yaml:"target"`
	Engine           string          `yaml:"engine"`
	WorkDir          string          `yaml:"workDir"`
	OCI              bool            `yaml:"oci"`
	Referrer         bool            `yaml:"referrer"`
	Verbose          bool            `yaml:"verbose"`
	ConcurrencyLimit int             `yaml:"concurrencyLimit"`
	Registry         RegistryConfig  `yaml:"registry"`
	TLS              TLSConfig       `yaml:"tls"`
	OverlayBD        OverlayBDConfig `yaml:"overlaybd"`
	Debug            DebugConfig     `yaml:"debug"`
}

// RegistryConfig captures the registry authentication and transport options.
type RegistryConfig struct {
	Username  string `yaml:"username"`
	Password  string `yaml:"password"`
	PlainHTTP bool   `yaml:"plainHTTP"`
}

// TLSConfig captures optional registry TLS customization.
type TLSConfig struct {
	CertDirs    []string `yaml:"certDirs"`
	RootCAs     []string `yaml:"rootCAs"`
	ClientCerts []string `yaml:"clientCerts"`
	Insecure    bool     `yaml:"insecure"`
}

// OverlayBDConfig captures the overlaybd-specific conversion knobs.
type OverlayBDConfig struct {
	FSType        string `yaml:"fsType"`
	Mkfs          bool   `yaml:"mkfs"`
	VSizeGB       int    `yaml:"vsizeGB"`
	DisableSparse bool   `yaml:"disableSparse"`
}

// DebugConfig captures optional local debugging controls passed through to the official builder.
type DebugConfig struct {
	Reserve      bool `yaml:"reserve"`
	NoUpload     bool `yaml:"noUpload"`
	DumpManifest bool `yaml:"dumpManifest"`
}

// DefaultConfig returns the baseline official-builder settings with a simpler source/target contract.
func DefaultConfig() Config {
	return Config{
		Engine:           EngineOverlayBD,
		WorkDir:          defaultWorkDir,
		ConcurrencyLimit: defaultConcurrencyLimit,
		OverlayBD: OverlayBDConfig{
			FSType:  defaultFSType,
			Mkfs:    true,
			VSizeGB: defaultVSizeGB,
		},
	}
}

// LoadConfig loads a YAML file on top of the built-in defaults.
func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	if strings.TrimSpace(path) == "" {
		return cfg, nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read image accelerator config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		return Config{}, fmt.Errorf("unmarshal image accelerator config %s: %w", path, err)
	}
	return cfg, nil
}

// Normalized trims, defaults, and validates the config.
func (c Config) Normalized() (Config, error) {
	cfg := c
	cfg.Source = strings.TrimSpace(cfg.Source)
	cfg.Target = strings.TrimSpace(cfg.Target)
	cfg.Engine = normalizeEngine(cfg.Engine)
	cfg.WorkDir = strings.TrimSpace(cfg.WorkDir)
	cfg.Registry.Username = strings.TrimSpace(cfg.Registry.Username)
	cfg.Registry.Password = strings.TrimSpace(cfg.Registry.Password)
	cfg.TLS.CertDirs = trimStringSlice(cfg.TLS.CertDirs)
	cfg.TLS.RootCAs = trimStringSlice(cfg.TLS.RootCAs)
	cfg.TLS.ClientCerts = trimStringSlice(cfg.TLS.ClientCerts)
	cfg.OverlayBD.FSType = strings.TrimSpace(cfg.OverlayBD.FSType)

	if cfg.WorkDir == "" {
		cfg.WorkDir = defaultWorkDir
	}
	if cfg.Engine == "" {
		cfg.Engine = EngineOverlayBD
	}
	if cfg.OverlayBD.FSType == "" {
		cfg.OverlayBD.FSType = defaultFSType
	}
	if cfg.OverlayBD.VSizeGB <= 0 {
		cfg.OverlayBD.VSizeGB = defaultVSizeGB
	}
	if cfg.ConcurrencyLimit < 0 {
		return Config{}, fmt.Errorf("concurrencyLimit must be >= 0")
	}
	if cfg.ConcurrencyLimit == 0 {
		cfg.ConcurrencyLimit = defaultConcurrencyLimit
	}
	if cfg.Referrer {
		cfg.OCI = true
	}

	switch cfg.Engine {
	case EngineOverlayBD, EngineTurboOCI:
	default:
		return Config{}, fmt.Errorf("engine must be one of %q or %q", EngineOverlayBD, EngineTurboOCI)
	}
	if cfg.Source == "" {
		return Config{}, fmt.Errorf("source is required")
	}
	if cfg.Target == "" {
		return Config{}, fmt.Errorf("target is required")
	}
	if cfg.Source == cfg.Target {
		return Config{}, fmt.Errorf("source and target must be different")
	}
	if cfg.Registry.Username == "" && cfg.Registry.Password != "" {
		return Config{}, fmt.Errorf("registry.username is required when registry.password is set")
	}
	if !cfg.OverlayBD.Mkfs && !strings.EqualFold(cfg.OverlayBD.FSType, "ext4") {
		return Config{}, fmt.Errorf("overlaybd.fsType must be ext4 when overlaybd.mkfs is false")
	}

	absWorkDir, err := filepath.Abs(cfg.WorkDir)
	if err != nil {
		return Config{}, fmt.Errorf("resolve workDir %q: %w", cfg.WorkDir, err)
	}
	cfg.WorkDir = absWorkDir
	return cfg, nil
}

// Redacted returns a copy of the config with secrets masked for logs.
func (c Config) Redacted() Config {
	cfg := c
	if cfg.Registry.Password != "" {
		cfg.Registry.Password = "***"
	}
	return cfg
}

// RedactedYAML returns a redacted YAML rendering suitable for debug logging.
func (c Config) RedactedYAML() ([]byte, error) {
	redacted := c.Redacted()
	return yaml.Marshal(redacted)
}

func (c RegistryConfig) auth() string {
	if c.Username == "" {
		return ""
	}
	return c.Username + ":" + c.Password
}

func expectedToolchainPaths(cfg Config) []string {
	paths := []string{
		overlayBDCreateBinary,
		overlayBDCommitBinary,
		overlayBDApplyBinary,
	}
	if cfg.Engine == EngineTurboOCI {
		paths = append(paths, turboOCIApplyBinary)
	}
	if cfg.Engine == EngineOverlayBD && !cfg.OverlayBD.Mkfs {
		paths = append(paths, overlayBDBaseLayer)
	}
	return paths
}

func validateToolchainLayout(cfg Config) error {
	for _, path := range expectedToolchainPaths(cfg) {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf(
				"official overlaybd userspace convertor expects %s to exist: %w",
				path,
				err,
			)
		}
	}
	return nil
}

func normalizeEngine(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", EngineOverlayBD:
		return EngineOverlayBD
	case "turbooci", "turbo-oci":
		return EngineTurboOCI
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func trimStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}
