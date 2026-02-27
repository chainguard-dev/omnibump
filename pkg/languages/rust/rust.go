/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package rust implements omnibump support for Rust projects.
// Ported from cargobump with enhancements for the unified omnibump architecture.
package rust

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/languages"
)

var (
	// ErrCargoLockNotFound is returned when Cargo.lock is not found.
	ErrCargoLockNotFound = errors.New("cargo.lock not found")

	// ErrRemoteAnalysisNotImplemented is returned when remote analysis is not implemented.
	ErrRemoteAnalysisNotImplemented = errors.New("remote analysis not yet implemented")
)

// Rust implements the Language interface for Rust projects.
type Rust struct{}

// init registers Rust with the language registry.
func init() {
	languages.Register(&Rust{})
}

// Name returns the language identifier.
func (r *Rust) Name() string {
	return "rust"
}

// Detect checks if Rust manifest files exist in the directory.
func (r *Rust) Detect(ctx context.Context, dir string) (bool, error) {
	log := clog.FromContext(ctx)
	cargoLockPath := filepath.Join(dir, "Cargo.lock")
	_, err := os.Stat(cargoLockPath)
	if err == nil {
		log.Debugf("Detected Rust project at %s", dir)
		return true, nil
	}
	log.Debugf("No Rust project detected at %s", dir)
	return false, nil
}

// GetManifestFiles returns Rust manifest files.
func (r *Rust) GetManifestFiles() []string {
	return []string{"Cargo.toml", "Cargo.lock"}
}

// SupportsAnalysis returns true since Rust now has analysis capabilities.
func (r *Rust) SupportsAnalysis() bool {
	return true
}

// ContainsPackage checks if the package exists in Cargo.toml or Cargo.lock.
func (r *Rust) ContainsPackage(ctx context.Context, dir string, packageName string) (bool, error) {
	log := clog.FromContext(ctx)

	// Check Cargo.toml
	cargoTomlPath := filepath.Join(dir, "Cargo.toml")
	if content, err := os.ReadFile(cargoTomlPath); err == nil { //nolint:gosec // G304: cargoTomlPath is constructed from validated dir
		if strings.Contains(string(content), packageName) {
			log.Debugf("Found package %s in Cargo.toml", packageName)
			return true, nil
		}
	}

	// Check Cargo.lock
	cargoLockPath := filepath.Join(dir, "Cargo.lock")
	if content, err := os.ReadFile(cargoLockPath); err == nil { //nolint:gosec // G304: cargoLockPath is constructed from validated dir
		if strings.Contains(string(content), fmt.Sprintf("name = %q", packageName)) {
			log.Debugf("Found package %s in Cargo.lock", packageName)
			return true, nil
		}
	}

	log.Debugf("Package %s not found in Rust manifests", packageName)
	return false, nil
}

// Update performs dependency updates on a Rust project.
func (r *Rust) Update(ctx context.Context, cfg *languages.UpdateConfig) error {
	log := clog.FromContext(ctx)

	log.Infof("Updating Rust project at: %s", cfg.RootDir)
	log.Infof("Dependencies to update: %d", len(cfg.Dependencies))

	// Find Cargo.lock
	cargoLockPath := filepath.Join(cfg.RootDir, "Cargo.lock")
	if _, err := os.Stat(cargoLockPath); os.IsNotExist(err) {
		return fmt.Errorf("%w in: %s", ErrCargoLockNotFound, cfg.RootDir)
	}

	// Parse Cargo.lock to get current packages
	file, err := os.Open(filepath.Clean(cargoLockPath))
	if err != nil {
		return fmt.Errorf("failed to open Cargo.lock: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			log.Warnf("failed to close Cargo.lock: %v", closeErr)
		}
	}()

	cargoPackages, err := ParseCargoLock(file)
	if err != nil {
		return fmt.Errorf("failed to parse Cargo.lock: %w", err)
	}

	// Build update configuration
	updateCfg := &UpdateConfig{
		CargoRoot: cfg.RootDir,
		Update:    getOptionBool(cfg.Options, "update", false),
		ShowDiff:  cfg.ShowDiff,
	}

	// Convert dependencies to Rust-specific format
	packages := convertDependenciesToPackages(cfg.Dependencies)

	if cfg.DryRun {
		log.Infof("Dry run mode: not making actual changes")
		return nil
	}

	// Perform the update
	err = DoUpdate(ctx, packages, cargoPackages, updateCfg)
	if err != nil {
		return fmt.Errorf("failed to update Cargo packages: %w", err)
	}

	log.Infof("Successfully updated Cargo packages")
	return nil
}

// Validate checks if the updates were applied successfully.
func (r *Rust) Validate(ctx context.Context, cfg *languages.UpdateConfig) error {
	log := clog.FromContext(ctx)

	cargoLockPath := filepath.Join(cfg.RootDir, "Cargo.lock")

	// Parse the updated Cargo.lock
	file, err := os.Open(filepath.Clean(cargoLockPath))
	if err != nil {
		return fmt.Errorf("failed to open updated Cargo.lock: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			log.Warnf("failed to close Cargo.lock: %v", closeErr)
		}
	}()

	cargoPackages, err := ParseCargoLock(file)
	if err != nil {
		return fmt.Errorf("failed to parse updated Cargo.lock: %w", err)
	}

	// Validate dependencies
	packageMap := make(map[string]CargoPackage)
	for _, pkg := range cargoPackages {
		packageMap[pkg.Name] = pkg
	}

	for _, dep := range cfg.Dependencies {
		if pkg, exists := packageMap[dep.Name]; exists {
			if pkg.Version != dep.Version {
				log.Warnf("Dependency %s: expected %s, got %s",
					dep.Name, dep.Version, pkg.Version)
			}
		} else {
			log.Warnf("Dependency not found in Cargo.lock: %s", dep.Name)
		}
	}

	log.Infof("Validation completed successfully")
	return nil
}

// convertDependenciesToPackages converts unified dependencies to Rust-specific packages.
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

// getOptionBool retrieves a boolean option from the options map.
func getOptionBool(options map[string]any, key string, defaultValue bool) bool {
	if val, ok := options[key]; ok {
		if boolVal, ok := val.(bool); ok {
			return boolVal
		}
	}
	return defaultValue
}
