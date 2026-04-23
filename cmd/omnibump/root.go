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
	_ "github.com/chainguard-dev/omnibump/pkg/languages/php"    // Register PHP (Composer, etc.)
	_ "github.com/chainguard-dev/omnibump/pkg/languages/python" // Register Python
	_ "github.com/chainguard-dev/omnibump/pkg/languages/rust"   // Register Rust
	charmlog "github.com/charmbracelet/log"
	"github.com/spf13/cobra"
	"sigs.k8s.io/release-utils/version"
)

type rootFlags struct {
	language       string
	depsFile       string
	propertiesFile string
	packages       string
	replaces       string
	properties     string
	rootDir        string
	tidy           bool
	showDiff       bool
	dryRun         bool
	logLevel       string
	logPolicy      []string
	tool           string
	venv           string
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
	f.StringVarP(&flags.language, "language", "l", "auto", "language to use (auto, java, go, python, rust, or deprecated: maven)")
	f.StringVar(&flags.depsFile, "deps", "", "dependencies file (deps.yaml, or legacy names)")
	f.StringVar(&flags.propertiesFile, "properties", "", "properties file (properties.yaml)")
	f.StringVar(&flags.packages, "packages", "", "inline package list (space-separated)")
	f.StringVar(&flags.replaces, "replaces", "", "inline replace list (space-separated, format: oldpkg=newpkg@version)")
	f.StringVar(&flags.properties, "props", "", "inline properties list (space-separated)")
	f.StringVar(&flags.rootDir, "dir", ".", "project root directory")
	f.StringVar(&flags.tool, "tool", "", "build tool override (Python: uv, pip, poetry, hatch, pdm, setuptools)")
	f.StringVar(&flags.venv, "venv", "", "path to staged Python venv for in-place bumping (Python only)")
	f.BoolVar(&flags.tidy, "tidy", false, "run tidy command after update")
	f.BoolVar(&flags.showDiff, "show-diff", false, "show diff of changes")
	f.BoolVar(&flags.dryRun, "dry-run", false, "simulate update without making changes")

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

	// Validate input - require at least one input source
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

	// Load configuration
	var cfg *config.Config
	var err error

	if hasFileInput {
		cfg, err = loadFileInputConfig(ctx)
		if err != nil {
			return err
		}
	} else {
		cfg, err = loadInlineInputConfig()
		if err != nil {
			return err
		}
	}

	// Detect language if not specified
	detectedLang := flags.language

	// Handle backward compatibility: "maven" -> "java"
	if detectedLang == languageMaven {
		log.Warnf("Language 'maven' is deprecated, use 'java' instead")
		detectedLang = languageJava
	}

	if detectedLang == languageAuto || detectedLang == "" {
		detectedLang, err = languages.DetectLanguage(ctx, flags.rootDir)
		if err != nil {
			return fmt.Errorf("failed to detect language: %w (try specifying --language explicitly)", err)
		}
		log.Infof("Detected language: %s", detectedLang)
	}

	// Override language from config if specified
	if cfg.Language != "" && cfg.Language != "auto" {
		detectedLang = cfg.Language
		// Handle backward compatibility in config too
		if detectedLang == "maven" {
			log.Warnf("Language 'maven' in config is deprecated, use 'java' instead")
			detectedLang = "java"
		}
	}

	// Get language implementation
	lang, err := languages.Get(detectedLang)
	if err != nil {
		return fmt.Errorf("failed to get language implementation: %w", err)
	}

	log.Infof("Using language: %s", lang.Name())

	// Convert config to UpdateConfig
	updateCfg := convertToUpdateConfig(cfg)
	updateCfg.RootDir = flags.rootDir
	updateCfg.Tidy = flags.tidy
	updateCfg.ShowDiff = flags.showDiff
	updateCfg.DryRun = flags.dryRun
	if flags.tool != "" {
		updateCfg.Options["tool"] = flags.tool
	}
	if flags.venv != "" {
		updateCfg.Options["venv"] = flags.venv
	}

	// Perform update
	if err := lang.Update(ctx, updateCfg); err != nil {
		return fmt.Errorf("update failed: %w", err)
	}

	// Validate
	if !flags.dryRun {
		if err := lang.Validate(ctx, updateCfg); err != nil {
			log.Warnf("Validation completed with warnings: %v", err)
		}
	}

	log.Infof("Update completed successfully")
	return nil
}

func convertToUpdateConfig(cfg *config.Config) *languages.UpdateConfig {
	updateCfg := &languages.UpdateConfig{
		Dependencies: make([]languages.Dependency, 0, len(cfg.Packages)),
		Properties:   make(map[string]string),
		Options:      make(map[string]any),
	}

	// Convert packages
	for _, pkg := range cfg.Packages {
		dep := languages.Dependency{
			Name:     pkg.Name,
			Version:  pkg.Version,
			Scope:    pkg.Scope,
			Type:     pkg.Type,
			Metadata: make(map[string]any),
		}

		// Store Maven-specific fields in metadata
		if pkg.GroupID != "" {
			dep.Metadata["groupId"] = pkg.GroupID
		}
		if pkg.ArtifactID != "" {
			dep.Metadata["artifactId"] = pkg.ArtifactID
		}

		updateCfg.Dependencies = append(updateCfg.Dependencies, dep)
	}

	// Convert properties
	for _, prop := range cfg.Properties {
		updateCfg.Properties[prop.Property] = prop.Value
	}

	// Convert replaces (Go-specific)
	if len(cfg.Replaces) > 0 {
		for _, repl := range cfg.Replaces {
			dep := languages.Dependency{
				OldName: repl.OldName,
				Name:    repl.Name,
				Version: repl.Version,
				Replace: true,
			}
			updateCfg.Dependencies = append(updateCfg.Dependencies, dep)
		}
	}

	return updateCfg
}
