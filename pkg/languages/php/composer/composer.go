/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package composer implements Composer build tool support for PHP projects.
package composer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/analyzer"
	"github.com/chainguard-dev/omnibump/pkg/languages"
)

// ErrLockNotFound indicates composer.lock was not found.
var ErrLockNotFound = errors.New("composer.lock not found")

// Composer implements the BuildTool interface for PHP Composer projects.
type Composer struct{}

// Name returns the build tool identifier.
func (c *Composer) Name() string {
	return "composer"
}

// Detect checks if Composer manifest files exist in the directory.
func (c *Composer) Detect(_ context.Context, dir string) (bool, error) {
	composerLockPath := filepath.Join(dir, "composer.lock")
	_, err := os.Stat(composerLockPath)
	return err == nil, nil
}

// GetManifestFiles returns Composer manifest files.
func (c *Composer) GetManifestFiles() []string {
	return []string{"composer.json", "composer.lock"}
}

// GetAnalyzer returns the Composer analyzer.
func (c *Composer) GetAnalyzer() analyzer.Analyzer {
	return &Analyzer{}
}

// Update performs dependency updates on a Composer project.
func (c *Composer) Update(ctx context.Context, cfg *languages.UpdateConfig) error {
	log := clog.FromContext(ctx)

	log.Infof("Updating Composer project at: %s", cfg.RootDir)
	log.Infof("Dependencies to update: %d", len(cfg.Dependencies))

	// Find composer.lock
	composerLockPath := filepath.Join(cfg.RootDir, "composer.lock")
	if _, err := os.Stat(composerLockPath); os.IsNotExist(err) {
		return fmt.Errorf("%w in: %s", ErrLockNotFound, cfg.RootDir)
	}

	// Parse composer.lock to get current packages
	file, err := os.Open(filepath.Clean(composerLockPath))
	if err != nil {
		return fmt.Errorf("failed to open composer.lock: %w", err)
	}

	composerPackages, err := ParseLock(file)
	if closeErr := file.Close(); closeErr != nil {
		return fmt.Errorf("failed to close composer.lock: %w", closeErr)
	}
	if err != nil {
		return fmt.Errorf("failed to parse composer.lock: %w", err)
	}

	// Build update configuration
	updateCfg := &UpdateConfig{
		ComposerRoot: cfg.RootDir,
		Update:       getOptionBool(cfg.Options, "update", false),
		ShowDiff:     cfg.ShowDiff,
		NoInstall:    getOptionBool(cfg.Options, "noInstall", false),
	}

	// Convert dependencies to Composer-specific format
	packages := convertDependenciesToPackages(cfg.Dependencies)

	if cfg.DryRun {
		log.Infof("Dry run mode: not making actual changes")
		return nil
	}

	// Perform the update
	err = DoUpdate(ctx, packages, composerPackages, updateCfg)
	if err != nil {
		return fmt.Errorf("failed to update Composer packages: %w", err)
	}

	log.Infof("Successfully updated Composer packages")
	return nil
}

// Validate checks if the updates were applied successfully.
func (c *Composer) Validate(ctx context.Context, cfg *languages.UpdateConfig) error {
	log := clog.FromContext(ctx)

	composerLockPath := filepath.Join(cfg.RootDir, "composer.lock")

	// Parse the updated composer.lock
	file, err := os.Open(filepath.Clean(composerLockPath))
	if err != nil {
		return fmt.Errorf("failed to open updated composer.lock: %w", err)
	}
	defer func() { _ = file.Close() }()

	composerPackages, err := ParseLock(file)
	if err != nil {
		return fmt.Errorf("failed to parse updated composer.lock: %w", err)
	}

	// Validate dependencies
	packageMap := make(map[string]LockPackage)
	for _, pkg := range composerPackages {
		packageMap[pkg.Name] = pkg
	}

	for _, dep := range cfg.Dependencies {
		if pkg, exists := packageMap[dep.Name]; exists {
			if pkg.Version != dep.Version {
				log.Warnf("Dependency %s: expected %s, got %s",
					dep.Name, dep.Version, pkg.Version)
			}
		} else {
			log.Warnf("Dependency not found in composer.lock: %s", dep.Name)
		}
	}

	log.Infof("Validation completed successfully")
	return nil
}

// convertDependenciesToPackages converts unified dependencies to Composer-specific packages.
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
