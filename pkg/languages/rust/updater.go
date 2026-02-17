/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package rust

import (
	"context"
	"fmt"
	"sort"

	"github.com/chainguard-dev/clog"
	"golang.org/x/mod/semver"
)

// DoUpdate performs the actual update of Rust package dependencies.
// Ported from cargobump/pkg/update.go
func DoUpdate(ctx context.Context, packages map[string]*Package, cargoPackages []CargoPackage, cfg *UpdateConfig) error {
	log := clog.FromContext(ctx)

	// Run 'cargo update' prior to upgrading any dependency
	if cfg.Update {
		log.Infof("Running 'cargo update'...")
		if output, err := CargoUpdate(cfg.CargoRoot); err != nil {
			return fmt.Errorf("failed to run 'cargo update': %w with output: %v", err, output)
		}
	}

	// Order packages by index for consistent updates
	orderedPackages := orderPackages(packages)

	for _, pkgName := range orderedPackages {
		pkg := packages[pkgName]

		// Find matching package(s) in Cargo.lock
		matchingPackages := findMatchingPackages(pkgName, cargoPackages)
		if len(matchingPackages) == 0 {
			log.Warnf("Package %s not found in Cargo.lock", pkgName)
			continue
		}

		// Update each matching package version
		for _, cargoPkg := range matchingPackages {
			if err := updatePackage(ctx, pkg, cargoPkg, cfg); err != nil {
				return err
			}
		}
	}

	return nil
}

// updatePackage updates a single package to the target version.
func updatePackage(ctx context.Context, pkg *Package, cargoPkg CargoPackage, cfg *UpdateConfig) error {
	log := clog.FromContext(ctx)

	// Check if already at target version
	if semver.Compare("v"+cargoPkg.Version, "v"+pkg.Version) == 0 {
		log.Infof("Package %s is already at version %s, skipping", pkg.Name, pkg.Version)
		return nil
	}

	// Check for downgrade
	if semver.Compare("v"+cargoPkg.Version, "v"+pkg.Version) > 0 {
		log.Warnf("Package %s: current version %s is newer than requested %s, skipping",
			pkg.Name, cargoPkg.Version, pkg.Version)
		return nil
	}

	log.Infof("Updating package %s from version %s to %s", pkg.Name, cargoPkg.Version, pkg.Version)

	if output, err := CargoUpdatePackage(pkg.Name, cargoPkg.Version, pkg.Version, cfg.CargoRoot); err != nil {
		return fmt.Errorf("failed to run cargo update for package '%s' from version '%s' to '%s': %w with output: %v",
			pkg.Name, cargoPkg.Version, pkg.Version, err, output)
	}

	log.Infof("Package updated successfully: %s to version %s", pkg.Name, pkg.Version)
	return nil
}

// findMatchingPackages finds all packages in Cargo.lock that match the given name.
// This handles cases where a package name can have @version appended for specific version updates.
func findMatchingPackages(name string, cargoPackages []CargoPackage) []CargoPackage {
	var matches []CargoPackage

	for _, p := range cargoPackages {
		if p.Name == name {
			matches = append(matches, p)
		}
	}

	return matches
}

// orderPackages returns package names sorted by their index.
func orderPackages(packages map[string]*Package) []string {
	names := make([]string, 0, len(packages))
	for name := range packages {
		names = append(names, name)
	}

	sort.SliceStable(names, func(i, j int) bool {
		return packages[names[i]].Index < packages[names[j]].Index
	})

	return names
}
