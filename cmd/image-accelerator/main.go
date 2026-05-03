package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/loki/gpu-operator-runtime/pkg/imageaccelerator"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

func main() {
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: time.RFC3339Nano,
	})

	cfgPath := ""
	cfg := imageaccelerator.DefaultConfig()

	rootCmd := &cobra.Command{
		Use:   "image-accelerator",
		Short: "Build accelerated OverlayBD or TurboOCI images in userspace with the official overlaybd convertor",
		Long: `image-accelerator is a thin wrapper around containerd/accelerated-container-image's
standalone userspace convertor. It only adds local YAML config, flag overrides,
and clearer preflight validation for the official /opt/overlaybd toolchain layout.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			loaded, err := imageaccelerator.LoadConfig(cfgPath)
			if err != nil {
				return err
			}
			cfg = loaded
			applyOverrides(cmd, &cfg)

			if cfg.Verbose {
				logrus.SetLevel(logrus.DebugLevel)
			}

			normalized, err := cfg.Normalized()
			if err != nil {
				return err
			}
			if normalized.Verbose {
				data, marshalErr := normalized.RedactedYAML()
				if marshalErr == nil {
					logrus.Debugf("effective config:\n%s", data)
				}
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return imageaccelerator.Run(ctx, normalized)
		},
	}

	flags := rootCmd.Flags()
	flags.StringVar(&cfgPath, "config", "", "path to the YAML config file")
	flags.String("source", "", "source image reference, for example docker.io/library/redis:7")
	flags.String("target", "", "target image reference, for example docker.io/example/redis:7-obd")
	flags.String("engine", "", "accelerated image engine: overlaybd or turbo-oci")
	flags.String("workdir", "", "work directory used for temporary conversion state")
	flags.Bool("oci", false, "export the converted image with OCI media types")
	flags.Bool("referrer", false, "publish the converted image as an OCI referrer; this also enables --oci")
	flags.Bool("verbose", false, "enable debug logging")
	flags.Int("concurrency-limit", 0, "maximum number of manifests processed concurrently for multi-arch images")
	flags.String("registry-username", "", "registry username")
	flags.String("registry-password", "", "registry password")
	flags.Bool("plain-http", false, "use plain HTTP instead of HTTPS for registry access")
	flags.StringArray("cert-dir", nil, "directory containing *.crt and *.cert/*.key registry TLS files")
	flags.StringArray("root-ca", nil, "additional registry root CA PEM files")
	flags.StringArray("client-cert", nil, "registry client certificates in cert:key format")
	flags.Bool("insecure", false, "skip registry TLS verification")
	flags.String("fs-type", "", "filesystem type for the converted image, for example ext4")
	flags.Bool("mkfs", false, "create a fresh filesystem in the bottom layer")
	flags.Int("vsize-gb", 0, "virtual block device size in GB")
	flags.Bool("disable-sparse", false, "disable sparse file creation for overlaybd layers")
	flags.Bool("reserve", false, "keep temporary conversion data on disk after the build")
	flags.Bool("no-upload", false, "build locally without uploading the converted layers and manifests")
	flags.Bool("dump-manifest", false, "dump the converted manifest and config into the work directory")

	if err := rootCmd.Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func applyOverrides(cmd *cobra.Command, cfg *imageaccelerator.Config) {
	flags := cmd.Flags()

	if flags.Changed("source") {
		cfg.Source, _ = flags.GetString("source")
	}
	if flags.Changed("target") {
		cfg.Target, _ = flags.GetString("target")
	}
	if flags.Changed("engine") {
		cfg.Engine, _ = flags.GetString("engine")
	}
	if flags.Changed("workdir") {
		cfg.WorkDir, _ = flags.GetString("workdir")
	}
	if flags.Changed("oci") {
		cfg.OCI, _ = flags.GetBool("oci")
	}
	if flags.Changed("referrer") {
		cfg.Referrer, _ = flags.GetBool("referrer")
	}
	if flags.Changed("verbose") {
		cfg.Verbose, _ = flags.GetBool("verbose")
	}
	if flags.Changed("concurrency-limit") {
		cfg.ConcurrencyLimit, _ = flags.GetInt("concurrency-limit")
	}
	if flags.Changed("registry-username") {
		cfg.Registry.Username, _ = flags.GetString("registry-username")
	}
	if flags.Changed("registry-password") {
		cfg.Registry.Password, _ = flags.GetString("registry-password")
	}
	if flags.Changed("plain-http") {
		cfg.Registry.PlainHTTP, _ = flags.GetBool("plain-http")
	}
	if flags.Changed("cert-dir") {
		cfg.TLS.CertDirs, _ = flags.GetStringArray("cert-dir")
	}
	if flags.Changed("root-ca") {
		cfg.TLS.RootCAs, _ = flags.GetStringArray("root-ca")
	}
	if flags.Changed("client-cert") {
		cfg.TLS.ClientCerts, _ = flags.GetStringArray("client-cert")
	}
	if flags.Changed("insecure") {
		cfg.TLS.Insecure, _ = flags.GetBool("insecure")
	}
	if flags.Changed("fs-type") {
		cfg.OverlayBD.FSType, _ = flags.GetString("fs-type")
	}
	if flags.Changed("mkfs") {
		cfg.OverlayBD.Mkfs, _ = flags.GetBool("mkfs")
	}
	if flags.Changed("vsize-gb") {
		cfg.OverlayBD.VSizeGB, _ = flags.GetInt("vsize-gb")
	}
	if flags.Changed("disable-sparse") {
		cfg.OverlayBD.DisableSparse, _ = flags.GetBool("disable-sparse")
	}
	if flags.Changed("reserve") {
		cfg.Debug.Reserve, _ = flags.GetBool("reserve")
	}
	if flags.Changed("no-upload") {
		cfg.Debug.NoUpload, _ = flags.GetBool("no-upload")
	}
	if flags.Changed("dump-manifest") {
		cfg.Debug.DumpManifest, _ = flags.GetBool("dump-manifest")
	}
}
