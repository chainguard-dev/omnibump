/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package omnibump implements the omnibump CLI for unified dependency version bumping.
package omnibump

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/config"
	"github.com/chainguard-dev/omnibump/pkg/languages"
	_ "github.com/chainguard-dev/omnibump/pkg/languages/golang" // Register Go
	_ "github.com/chainguard-dev/omnibump/pkg/languages/java"   // Register Java (Maven, Gradle, etc.)
	"github.com/chainguard-dev/omnibump/pkg/languages/java/maven"
	"github.com/chainguard-dev/omnibump/pkg/languages/js"
	_ "github.com/chainguard-dev/omnibump/pkg/languages/php"  // Register PHP (Composer, etc.)
	_ "github.com/chainguard-dev/omnibump/pkg/languages/rust" // Register Rust
	charmlog "github.com/charmbracelet/log"
	"github.com/spf13/cobra"
	"sigs.k8s.io/release-utils/version"
)

type rootFlags struct {
	language       string
	managers       []string
	depsFile       string
	propertiesFile string
	packages       string
	replaces       string
	properties     string
	rootDir        string
	manifestFile   string
	tidy           bool
	tidyCompat     string
	showDiff       bool
	dryRun         bool
	logLevel       string
	logPolicy      []string
}

var flags rootFlags

// logFileHandle stores the log file handle so it can be closed on exit.
var logFileHandle *os.File

// New creates the root omnibump command.
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "omnibump",
		Short:        "dependency version bumping tool",
		Long:         `omnibump is a tool for bumping dependency versions across multiple language ecosystems with automatic language detection.`,
		SilenceUsage: true,
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			return setupLogging()
		},
		PersistentPostRunE: func(_ *cobra.Command, _ []string) error { // _ unused but required by cobra interface
			if logFileHandle != nil {
				if err := logFileHandle.Close(); err != nil {
					return fmt.Errorf("failed to close log file: %w", err)
				}
			}
			return nil
		},
		RunE: runUpdate,
	}

	// Add persistent flags
	pf := cmd.PersistentFlags()
	pf.StringVar(&flags.logLevel, "log-level", "info", "log level (debug, info, warn, error)")
	pf.StringSliceVar(&flags.logPolicy, "log-policy", []string{"builtin:stderr"}, "log policy")

	// Add root command flags
	f := cmd.Flags()
	f.StringVarP(&flags.language, "language", "l", "auto", "language to use (auto, java, go, rust, js, or deprecated: maven)")
	f.StringSliceVar(&flags.managers, "manager", nil, "build tool(s) within a language (currently only used for js: pnpm, yarn, npm, bun). May be repeated or comma-separated to write the same overrides under more than one manager's field.")
	f.StringVar(&flags.depsFile, "deps", "", "dependencies file (deps.yaml, or legacy names)")
	f.StringVar(&flags.propertiesFile, "properties", "", "properties file (properties.yaml)")
	f.StringVar(&flags.packages, "packages", "", "inline package list (space-separated)")
	f.StringVar(&flags.replaces, "replaces", "", "inline replace list (space-separated, format: oldpkg=newpkg@version)")
	f.StringVar(&flags.properties, "props", "", "inline properties list (space-separated)")
	f.StringVar(&flags.rootDir, "dir", ".", "project root directory")
	f.StringVar(&flags.tidyCompat, "tidy-compat", "", "set the go version for which the tidied go.mod and go.sum files should be compatible")
	f.BoolVar(&flags.tidy, "tidy", false, "run tidy command after update")
	f.BoolVar(&flags.showDiff, "show-diff", false, "show diff of changes")
	f.BoolVar(&flags.dryRun, "dry-run", false, "simulate update without making changes")
	f.StringVar(&flags.manifestFile, "manifest", "", "path to manifest file to update (e.g. a specific pom.xml); defaults to <dir>/pom.xml")

	// Add version command
	cmd.AddCommand(version.WithFont("starwars"))

	// Add analyze command
	cmd.AddCommand(analyzeCmd())

	// Add analyze-remote command
	cmd.AddCommand(analyzeRemoteCmd())

	// Add supported command
	cmd.AddCommand(supportedCmd())

	cmd.DisableAutoGenTag = true

	return cmd
}

var (
	// ErrInvalidLogPath is returned when a log-policy path fails validation.
	ErrInvalidLogPath = errors.New("invalid log-policy path")

	// ErrMissingInput is returned when no input source is specified.
	ErrMissingInput = errors.New("missing input")

	// ErrConflictingInput is returned when conflicting input sources are specified.
	ErrConflictingInput = errors.New("conflicting input")

	// disallowedLogPaths lists sensitive paths that should never be written to.
	disallowedLogPaths = []string{
		"/etc/",
		"/root/",
		"/bin/",
		"/sbin/",
		"/usr/bin/",
		"/usr/sbin/",
		"/boot/",
		"/sys/",
		"/proc/",
		"/.ssh/",
		"/var/spool/cron/",
		"/etc/cron",
	}
)

// validateLogPath checks if a log file path is safe to write to.
// Returns an error if the path is disallowed or suspicious.
func validateLogPath(path string) error {
	// Get absolute path to normalize it
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("%w: failed to resolve absolute path: %w", ErrInvalidLogPath, err)
	}

	// Clean the path to remove any .. or . components
	cleanPath := filepath.Clean(absPath)

	// Check against disallowed paths
	for _, disallowed := range disallowedLogPaths {
		if strings.HasPrefix(cleanPath, disallowed) {
			return fmt.Errorf("%w: path %q is in disallowed directory %q", ErrInvalidLogPath, path, disallowed)
		}
	}

	// Check for suspicious path components
	for component := range strings.SplitSeq(cleanPath, string(filepath.Separator)) {
		// Disallow paths with suspicious components that could enable attacks
		if component == ".ssh" || component == "authorized_keys" ||
			strings.HasPrefix(component, "cron") {
			return fmt.Errorf("%w: path %q contains disallowed component %q", ErrInvalidLogPath, path, component)
		}
	}

	return nil
}

func setupLogging() error {
	// Simple log writer setup
	out := os.Stderr
	for _, policy := range flags.logPolicy {
		if policy != "builtin:stderr" {
			// Validate the log path to prevent arbitrary file writes
			if err := validateLogPath(policy); err != nil {
				return fmt.Errorf("log-policy validation failed: %w", err)
			}

			f, err := os.OpenFile(filepath.Clean(policy), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			if err != nil {
				return fmt.Errorf("failed to create log writer: %w", err)
			}
			out = f
			logFileHandle = f // Store handle for cleanup in PersistentPostRunE
			break
		}
	}

	// Parse log level
	var level charmlog.Level
	switch flags.logLevel {
	case "debug":
		level = charmlog.DebugLevel
	case "info":
		level = charmlog.InfoLevel
	case "warn":
		level = charmlog.WarnLevel
	case "error":
		level = charmlog.ErrorLevel
	default:
		level = charmlog.InfoLevel
	}

	slog.SetDefault(slog.New(charmlog.NewWithOptions(out, charmlog.Options{
		ReportTimestamp: true,
		Level:           level,
	})))

	return nil
}

// loadFileInputConfig loads configuration from file sources (--deps and/or --properties).
func loadFileInputConfig(ctx context.Context) (*config.Config, error) {
	var files []string
	if flags.depsFile != "" {
		files = append(files, flags.depsFile)
	}
	if flags.propertiesFile != "" {
		files = append(files, flags.propertiesFile)
	}

	cfg, err := config.LoadMultipleConfigs(ctx, files)
	if err != nil {
		return nil, fmt.Errorf("failed to load configuration: %w", err)
	}
	return cfg, nil
}

// loadInlineInputConfig loads configuration from inline sources (--packages, --replaces, and/or --props).
func loadInlineInputConfig() (*config.Config, error) {
	cfg := &config.Config{}

	if flags.packages != "" {
		packages, err := config.ParseInlinePackages(flags.packages)
		if err != nil {
			return nil, fmt.Errorf("failed to parse inline packages: %w", err)
		}
		cfg.Packages = packages
	}

	if flags.replaces != "" {
		replaces, err := config.ParseInlineReplaces(flags.replaces)
		if err != nil {
			return nil, fmt.Errorf("failed to parse inline replaces: %w", err)
		}
		cfg.Replaces = replaces
	}

	if flags.properties != "" {
		properties, err := config.ParseInlineProperties(flags.properties)
		if err != nil {
			return nil, fmt.Errorf("failed to parse inline properties: %w", err)
		}
		cfg.Properties = properties
	}

	return cfg, nil
}

func runUpdate(cmd *cobra.Command, _ []string) error { // args unused but required by cobra interface
	ctx := cmd.Context()
	log := clog.FromContext(ctx)

	if err := validateUpdateFlags(); err != nil {
		return err
	}

	cfg, err := loadUpdateConfig(ctx)
	if err != nil {
		return err
	}

	detectedLang, err := resolveLanguage(ctx, cfg)
	if err != nil {
		return err
	}

	lang, err := languages.Get(detectedLang)
	if err != nil {
		return fmt.Errorf("failed to get language implementation: %w", err)
	}

	log.Infof("Using language: %s", lang.Name())

	updateCfg := buildUpdateConfig(cfg)

	if err := lang.Update(ctx, updateCfg); err != nil {
		return fmt.Errorf("update failed: %w", err)
	}

	if !flags.dryRun {
		if err := lang.Validate(ctx, updateCfg); err != nil {
			log.Warnf("Validation completed with warnings: %v", err)
		}
	}

	log.Infof("Update completed")
	return nil
}

// validateUpdateFlags checks the CLI flags for mutually exclusive or missing inputs.
func validateUpdateFlags() error {
	hasFileInput := flags.depsFile != "" || flags.propertiesFile != ""
	hasInlineInput := flags.packages != "" || flags.replaces != "" || flags.properties != ""

	if !hasFileInput && !hasInlineInput {
		return fmt.Errorf("%w: at least one of --deps, --properties, --packages, --replaces, or --props must be specified", ErrMissingInput)
	}

	if flags.depsFile != "" && flags.packages != "" {
		return fmt.Errorf("%w: cannot use both --deps and --packages", ErrConflictingInput)
	}

	if flags.propertiesFile != "" && flags.properties != "" {
		return fmt.Errorf("%w: cannot use both --properties (file) and --props (inline)", ErrConflictingInput)
	}

	return nil
}

// loadUpdateConfig loads configuration from file or inline sources based on the flags.
func loadUpdateConfig(ctx context.Context) (*config.Config, error) {
	if flags.depsFile != "" || flags.propertiesFile != "" {
		return loadFileInputConfig(ctx)
	}
	return loadInlineInputConfig()
}

// resolveLanguage determines which language implementation to use, honouring
// --language, --manifest, config overrides and auto-detection.
func resolveLanguage(ctx context.Context, cfg *config.Config) (string, error) {
	log := clog.FromContext(ctx)

	detectedLang := normaliseLanguage(flags.language, "Language 'maven' is deprecated, use 'java' instead", log)

	if flags.manifestFile != "" && (detectedLang == languageAuto || detectedLang == "") {
		ok, err := maven.IsMavenPom(flags.manifestFile)
		if err != nil {
			return "", fmt.Errorf("failed to read manifest file: %w", err)
		}
		if !ok {
			return "", fmt.Errorf("--manifest %q: %w", flags.manifestFile, maven.ErrNotMavenPOM)
		}
		detectedLang = languageJava
		log.Infof("Detected language: %s", detectedLang)
	}

	if detectedLang == languageAuto || detectedLang == "" {
		auto, err := languages.DetectLanguage(ctx, flags.rootDir)
		if err != nil && auto == "" {
			return "", fmt.Errorf("failed to detect language: %w (try specifying --language explicitly)", err)
		}
		if err != nil {
			// Multiple languages detected — warn but proceed with the chosen one.
			log.Warnf("%v", err)
		}
		detectedLang = auto
		log.Infof("Detected language: %s", detectedLang)
	}

	if cfg.Language != "" && cfg.Language != languageAuto {
		detectedLang = normaliseLanguage(cfg.Language, "Language 'maven' in config is deprecated, use 'java' instead", log)
	}

	return detectedLang, nil
}

// normaliseLanguage applies backward-compatibility aliasing (e.g. "maven" -> "java").
func normaliseLanguage(name, deprecationMsg string, log *clog.Logger) string {
	if name == languageMaven {
		log.Warnf("%s", deprecationMsg)
		return languageJava
	}
	return name
}

// buildUpdateConfig converts the loaded config plus CLI flags into an UpdateConfig.
func buildUpdateConfig(cfg *config.Config) *languages.UpdateConfig {
	updateCfg := cfg.ToUpdateConfig()
	updateCfg.RootDir = flags.rootDir
	updateCfg.Tidy = flags.tidy
	updateCfg.ShowDiff = flags.showDiff
	updateCfg.DryRun = flags.dryRun
	updateCfg.ManifestFile = flags.manifestFile
	if flags.tidyCompat != "" {
		updateCfg.Options["tidy-compat"] = flags.tidyCompat
	}

	// The CLI's --manager flag always wins over a value in the deps file
	// (which ToUpdateConfig already stamped into Options).
	if len(flags.managers) > 0 {
		managers := make([]js.Manager, len(flags.managers))
		for i, s := range flags.managers {
			managers[i] = js.Manager(s)
		}
		updateCfg.Options["manager"] = managers
	}

	return updateCfg
}
