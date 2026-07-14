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
	"github.com/google/go-cmp/cmp"
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

// Update performs dependency updates on a Rust project.
func (r *Rust) Update(ctx context.Context, cfg *languages.UpdateConfig) error {
	log := clog.FromContext(ctx)

	log.Infof("Updating Rust project at: %s", cfg.RootDir)
	log.Infof("Dependencies to update: %d", len(cfg.Dependencies))

	// Get current packages
	cargoPackages, err := GetCurrentPackages(ctx, cfg.RootDir)
	if err != nil {
		return err
	}

	// Build update configuration
	updateCfg := &UpdateConfig{
		CargoRoot: cfg.RootDir,
		Update:    cfg.Update,
		ShowDiff:  cfg.ShowDiff,
	}

	// Convert dependencies to Rust-specific format
	packages := convertDependenciesToPackages(cfg.Dependencies)

	if cfg.DryRun {
		log.Infof("Dry run mode: not making actual changes")
		return nil
	}

	// Get the path to Cargo.lock so we can diff the contents
	cargoLockPath, err := GetCargoLockPath(cfg.RootDir)
	if err != nil {
		return err
	}

	var originalContent []byte
	if cfg.ShowDiff {
		originalContent, _ = os.ReadFile(cargoLockPath) //nolint:gosec // cargoLockPath built from cfg.RootDir + constant filename
	}

	// Perform the update
	err = DoUpdate(ctx, packages, cargoPackages, updateCfg)
	if err != nil {
		return fmt.Errorf("failed to update Cargo packages: %w", err)
	}

	if cfg.ShowDiff && originalContent != nil {
		newContent, _ := os.ReadFile(cargoLockPath) //nolint:gosec // cargoLockPath built from cfg.RootDir + constant filename
		if diff := cmp.Diff(string(originalContent), string(newContent)); diff != "" {
			log.Infof("Diff for %s:\n%s", cargoLockPath, diff)
		}
	}

	log.Infof("Successfully updated Cargo packages")
	return nil
}

// Validate checks that each requested update landed in the updated Cargo.lock.
//
// A request is judged against the updater's contract — a floor (">= dep.Version")
// within the crate's Cargo caret line — not by exact equality. This is required
// because a single Cargo.lock legitimately locks one crate at several
// SemVer-incompatible versions at once (e.g. rand 0.9.0 and 0.10.1, each required
// by a different dependent). The earlier exact-equality check compared the request
// against *every* locked instance, so it emitted spurious warnings ("expected
// 0.9.0, got 0.10.1") against the unrelated line and reported a crate that had
// legitimately moved to a higher line as "not found".
//
// The floor-and-line rules below mirror the updater (upgradeReverseDependencies /
// pinPrecise) so the two agree on what "satisfied" means:
//   - the requested line is present: that instance must be >= the floor;
//   - the requested line is absent but a higher line exists (a dependent pulled
//     the crate up, e.g. anstream 0.6 -> 1.0): the floor is still met, so the
//     update correctly left it alone;
//   - only lower instances remain: genuinely stale, so warn.
//
// The precise-pin form (name@from=to) reaches here with the "@from" marker still
// on dep.Name and the exact target in dep.Version. It is judged on the same
// floor-and-line basis: a crate locked *above* its exact target — because a
// dependent requires the newer version, so pinPrecise refused to downgrade it —
// still satisfies the floor. That exact-match / refused-downgrade case is owned by
// pinPrecise and verifyTransitiveUpgrade in the updater (which warn at update
// time), so Validate defers to them rather than emitting a redundant warning.
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
	packageMap := make(map[string][]CargoPackage)
	for _, pkg := range cargoPackages {
		packageMap[pkg.Name] = append(packageMap[pkg.Name], pkg)
	}

	for _, dep := range cfg.Dependencies {
		// A precise pin (name@from=to) keeps its "@from" marker on dep.Name; strip
		// it to the base crate name. The marker only records the pin's from-line for
		// the updater — Validate judges every request on dep.Version's line below.
		baseName, _, _ := strings.Cut(dep.Name, "@")
		instances := packageMap[baseName]
		if len(instances) == 0 {
			log.Warnf("Dependency not found in Cargo.lock: %s", dep.Name)
			continue
		}

		// The request names a SemVer line (dep.Version's Cargo caret line) and a
		// floor within it; the updater's contract is ">= dep.Version", so Validate
		// checks the floor, not exact equality (e.g. tokio 1.50.0 satisfies a
		// request for 1.43.1).
		var versions, inLine []string
		for _, pkg := range instances {
			versions = append(versions, pkg.Version)
			if cargoCompatible(dep.Version, pkg.Version) {
				inLine = append(inLine, pkg.Version)
			}
		}

		// When the requested line is present, its instance must have reached the
		// floor. A crate locked at several incompatible lines (rand 0.9.0 and
		// 0.10.1, required by different crates) is judged on its own line, so a
		// higher unrelated line cannot mask a stale target line.
		if len(inLine) > 0 {
			if !satisfiesFloor(inLine, dep.Version) {
				log.Warnf("Dependency %s: expected >= %s, got %s", baseName, dep.Version, joinVersions(inLine))
			}
			continue
		}

		// The requested line is absent: the crate may have been pulled onto a higher
		// line by a dependent (e.g. anstream 0.6 -> 1.0), which still satisfies the
		// floor — the update correctly left it alone. Warn only when every present
		// instance is below the requested version.
		if !satisfiesFloor(versions, dep.Version) {
			log.Warnf("Dependency %s: expected >= %s, got %s", baseName, dep.Version, joinVersions(versions))
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
