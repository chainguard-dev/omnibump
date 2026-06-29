/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package ruby

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/languages"
	"github.com/google/go-cmp/cmp"
)

// Verify Ruby implements the languages.Language interface at compile time.
var _ languages.Language = (*Ruby)(nil)

var (
	// ErrGemfileLockNotFound is returned when Gemfile.lock is not found.
	ErrGemfileLockNotFound = errors.New("gemfile.lock not found")

	// ErrRemoteAnalysisNotImplemented is returned when remote analysis is not implemented.
	ErrRemoteAnalysisNotImplemented = errors.New("remote analysis not yet implemented")

	// ErrValidationFailed is returned when post-update validation detects a version mismatch.
	ErrValidationFailed = errors.New("validation failed")

	// ErrPackageNotFound is returned when a dependency is not found.
	ErrPackageNotFound = errors.New("package not found")

	// ErrGemDirNotFound is returned when the specified gem directory does not exist.
	ErrGemDirNotFound = errors.New("gem directory not found")

	// ErrGemDirDowngrade is returned when a gem overlay would downgrade a version.
	ErrGemDirDowngrade = errors.New("downgrade rejected")

	// ErrGemInstallFailed is returned when `gem install` exits with an error.
	ErrGemInstallFailed = errors.New("gem install failed")

	// ErrGemDirInvalidGemName is returned when a gem name contains invalid characters.
	ErrGemDirInvalidGemName = errors.New("invalid gem name")

	// ErrGemDirEmptyVersion is returned when a gem version is empty.
	ErrGemDirEmptyVersion = errors.New("empty version for gem")

	// ErrGemDirInvalidVersionFormat is returned when a gem version has an invalid format.
	ErrGemDirInvalidVersionFormat = errors.New("invalid version format")
)

// Ruby implements the Language interface for Ruby projects.
type Ruby struct{}

// init registers Ruby with the language registry.
func init() {
	languages.Register(&Ruby{})
}

// Name returns the language identifier.
func (r *Ruby) Name() string {
	return LanguageRuby
}

// Detect checks if Ruby manifest files exist in the directory.
func (r *Ruby) Detect(_ context.Context, dir string) (bool, error) {
	_, err := os.Stat(filepath.Join(dir, ManifestGemfileLock))
	if err == nil {
		return true, nil
	}
	return false, nil
}

// GetManifestFiles returns Ruby manifest files.
func (r *Ruby) GetManifestFiles() []string {
	return []string{ManifestGemfile, ManifestGemfileLock}
}

// SupportsAnalysis returns true since Ruby has analysis capabilities.
func (r *Ruby) SupportsAnalysis() bool {
	return true
}

// Update performs dependency updates on a Ruby project.
// If Options["gem-dir"] is set, uses gem-dir overlay mode (gem install into a
// staged gem directory). Otherwise uses manifest mode (Gemfile.lock editing).
func (r *Ruby) Update(ctx context.Context, cfg *languages.UpdateConfig) error {
	log := clog.FromContext(ctx)

	// Check for gem-dir overlay mode
	gemDirPath, _ := cfg.Options["gem-dir"].(string)
	if gemDirPath != "" {
		return updateGemDir(ctx, cfg, gemDirPath)
	}

	log.Infof("Updating Ruby project at: %s", cfg.RootDir)
	log.Infof("Dependencies to update: %d", len(cfg.Dependencies))

	// Find Gemfile.lock
	gemfileLockPath := filepath.Join(cfg.RootDir, ManifestGemfileLock)
	if _, err := os.Stat(gemfileLockPath); os.IsNotExist(err) {
		return fmt.Errorf("%w in: %s", ErrGemfileLockNotFound, cfg.RootDir)
	}

	// Parse Gemfile.lock to verify it's valid
	file, err := os.Open(filepath.Clean(gemfileLockPath))
	if err != nil {
		return fmt.Errorf("failed to open Gemfile.lock: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			log.Warnf("failed to close Gemfile.lock: %v", closeErr)
		}
	}()

	_, err = ParseGemfileLock(file)
	if err != nil {
		return fmt.Errorf("failed to parse Gemfile.lock: %w", err)
	}

	// Convert dependencies to Ruby-specific format
	packages := convertDependenciesToPackages(cfg.Dependencies)

	if cfg.DryRun {
		log.Infof("Dry run mode: not making actual changes")
		return nil
	}

	// Snapshot for --show-diff
	var originalContent []byte
	if cfg.ShowDiff {
		originalContent, _ = os.ReadFile(gemfileLockPath) //nolint:gosec // path from trusted caller config
	}

	// Perform the update
	err = DoUpdate(ctx, packages, gemfileLockPath)
	if err != nil {
		return fmt.Errorf("failed to update gem packages: %w", err)
	}

	if cfg.ShowDiff && originalContent != nil {
		newContent, _ := os.ReadFile(gemfileLockPath) //nolint:gosec // path from trusted caller config
		if diff := cmp.Diff(string(originalContent), string(newContent)); diff != "" {
			log.Infof("Diff for %s:\n%s", gemfileLockPath, diff)
		}
	}

	log.Infof("Successfully updated gem packages")
	return nil
}

// Validate checks if the updates were applied successfully.
// If Options["gem-dir"] is set, validates versions in the gem directory.
// Otherwise validates versions in Gemfile.lock.
func (r *Ruby) Validate(ctx context.Context, cfg *languages.UpdateConfig) error {
	log := clog.FromContext(ctx)

	// Check for gem-dir overlay mode
	gemDirPath, _ := cfg.Options["gem-dir"].(string)
	if gemDirPath != "" {
		return validateGemDir(ctx, cfg, gemDirPath)
	}

	gemfileLockPath := filepath.Join(cfg.RootDir, ManifestGemfileLock)

	// Parse the updated Gemfile.lock
	file, err := os.Open(filepath.Clean(gemfileLockPath))
	if err != nil {
		return fmt.Errorf("failed to open updated Gemfile.lock: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			log.Warnf("failed to close Gemfile.lock: %v", closeErr)
		}
	}()

	gemPackages, err := ParseGemfileLock(file)
	if err != nil {
		return fmt.Errorf("failed to parse updated Gemfile.lock: %w", err)
	}

	// Validate dependencies
	packageMap := make(map[string]GemPackage)
	for _, pkg := range gemPackages {
		packageMap[pkg.Name] = pkg
	}

	for _, dep := range cfg.Dependencies {
		pkg, exists := packageMap[dep.Name]
		if !exists {
			return fmt.Errorf("%w: %s", ErrPackageNotFound, dep.Name)
		}
		if pkg.Version != dep.Version {
			return fmt.Errorf("%w: %s expected %s, got %s", ErrValidationFailed, dep.Name, dep.Version, pkg.Version)
		}
		log.Debugf("validation ok: %s == %s", dep.Name, dep.Version)
	}

	return nil
}

// convertDependenciesToPackages converts unified dependencies to Ruby-specific packages.
func convertDependenciesToPackages(deps []languages.Dependency) map[string]*Package {
	packages := make(map[string]*Package)

	for i, dep := range deps {
		pkg := &Package{
			Name:    dep.Name,
			Version: dep.Version,
			Index:   i,
		}

		packages[dep.Name] = pkg
	}

	return packages
}
