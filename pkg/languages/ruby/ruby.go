/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package ruby implements omnibump support for Ruby projects using Bundler.
// Updates are performed via direct Gemfile.lock text editing — no Ruby/Bundler CLI required.
package ruby

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/languages"
)

var (
	// ErrGemfileLockNotFound is returned when Gemfile.lock is not found.
	ErrGemfileLockNotFound = errors.New("gemfile.lock not found")

	// ErrRemoteAnalysisNotImplemented is returned when remote analysis is not implemented.
	ErrRemoteAnalysisNotImplemented = errors.New("remote analysis not yet implemented")
)

// Ruby implements the Language interface for Ruby projects.
type Ruby struct{}

// init registers Ruby with the language registry.
func init() {
	languages.Register(&Ruby{})
}

// Name returns the language identifier.
func (r *Ruby) Name() string {
	return "ruby"
}

// Detect checks if Ruby manifest files exist in the directory.
func (r *Ruby) Detect(ctx context.Context, dir string) (bool, error) {
	log := clog.FromContext(ctx)
	gemfileLockPath := filepath.Join(dir, "Gemfile.lock")
	_, err := os.Stat(gemfileLockPath)
	if err == nil {
		log.Debugf("Detected Ruby project at %s", dir)
		return true, nil
	}
	log.Debugf("No Ruby project detected at %s", dir)
	return false, nil
}

// GetManifestFiles returns Ruby manifest files.
func (r *Ruby) GetManifestFiles() []string {
	return []string{"Gemfile", "Gemfile.lock"}
}

// SupportsAnalysis returns true since Ruby has analysis capabilities.
func (r *Ruby) SupportsAnalysis() bool {
	return true
}

// Update performs dependency updates on a Ruby project.
func (r *Ruby) Update(ctx context.Context, cfg *languages.UpdateConfig) error {
	log := clog.FromContext(ctx)

	log.Infof("Updating Ruby project at: %s", cfg.RootDir)
	log.Infof("Dependencies to update: %d", len(cfg.Dependencies))

	// Find Gemfile.lock
	gemfileLockPath := filepath.Join(cfg.RootDir, "Gemfile.lock")
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

	// Perform the update
	err = DoUpdate(ctx, packages, gemfileLockPath)
	if err != nil {
		return fmt.Errorf("failed to update gem packages: %w", err)
	}

	log.Infof("Successfully updated gem packages")
	return nil
}

// Validate checks if the updates were applied successfully.
func (r *Ruby) Validate(ctx context.Context, cfg *languages.UpdateConfig) error {
	log := clog.FromContext(ctx)

	gemfileLockPath := filepath.Join(cfg.RootDir, "Gemfile.lock")

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
		if pkg, exists := packageMap[dep.Name]; exists {
			if pkg.Version != dep.Version {
				log.Warnf("Dependency %s: expected %s, got %s",
					dep.Name, dep.Version, pkg.Version)
			}
		} else {
			log.Warnf("Dependency not found in Gemfile.lock: %s", dep.Name)
		}
	}

	log.Infof("Validation completed successfully")
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
