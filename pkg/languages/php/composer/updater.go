/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package composer

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/chainguard-dev/clog"
	"golang.org/x/mod/semver"
)

// DoUpdate performs the actual update of Composer package dependencies.
func DoUpdate(ctx context.Context, packages map[string]*Package, composerPackages []LockPackage, cfg *UpdateConfig) error {
	log := clog.FromContext(ctx)

	// Build extra args
	var extraArgs []string
	if cfg.NoInstall {
		extraArgs = append(extraArgs, "--no-install")
	}

	// Run 'composer update' prior to upgrading any dependency
	if cfg.Update {
		log.Infof("Running 'composer update'...")
		if output, err := RunUpdate(ctx, cfg.ComposerRoot, extraArgs...); err != nil {
			return fmt.Errorf("failed to run 'composer update': %w with output: %v", err, output)
		}
	}

	// Order packages by index for consistent updates
	orderedPackages := orderPackages(packages)

	for _, pkgName := range orderedPackages {
		pkg := packages[pkgName]

		// Find matching package(s) in composer.lock
		matchingPackages := findMatchingPackages(pkgName, composerPackages)
		if len(matchingPackages) == 0 {
			log.Warnf("Package %s not found in composer.lock", pkgName)
			continue
		}

		// Update each matching package version
		for _, composerPkg := range matchingPackages {
			if err := updatePackage(ctx, pkg, composerPkg, cfg); err != nil {
				return err
			}
		}
	}

	return nil
}

// updatePackage updates a single package to the target version.
func updatePackage(ctx context.Context, pkg *Package, composerPkg LockPackage, cfg *UpdateConfig) error {
	log := clog.FromContext(ctx)

	currentVersion := normalizeVersionForSemver(composerPkg.Version)
	targetVersion := normalizeVersionForSemver(pkg.Version)

	// Check if already at target version
	if semver.Compare(currentVersion, targetVersion) == 0 {
		log.Infof("Package %s is already at version %s, skipping", pkg.Name, pkg.Version)
		return nil
	}

	// Check for downgrade
	if semver.Compare(currentVersion, targetVersion) > 0 {
		log.Warnf("Package %s: current version %s is newer than requested %s, skipping",
			pkg.Name, composerPkg.Version, pkg.Version)
		return nil
	}

	log.Infof("Updating package %s from version %s to %s", pkg.Name, composerPkg.Version, pkg.Version)

	// Build extra args
	var extraArgs []string
	if cfg.NoInstall {
		extraArgs = append(extraArgs, "--no-install")
	}

	if output, err := RunRequire(ctx, pkg.Name, pkg.Version, cfg.ComposerRoot, extraArgs...); err != nil {
		return fmt.Errorf("failed to run composer require for package '%s' to version '%s': %w with output: %v",
			pkg.Name, pkg.Version, err, output)
	}

	log.Infof("Package updated successfully: %s to version %s", pkg.Name, pkg.Version)
	return nil
}

// normalizeVersionForSemver ensures version has "v" prefix for semver comparison.
// Composer versions may or may not have "v" prefix and may have 4 parts.
func normalizeVersionForSemver(version string) string {
	// Remove "v" prefix if present
	v := strings.TrimPrefix(version, "v")

	// Handle versions with 4 parts (e.g., 1.2.3.0) by taking first 3
	parts := strings.Split(v, ".")
	if len(parts) > 3 {
		v = strings.Join(parts[:3], ".")
	}

	// Add "v" prefix for semver
	return "v" + v
}

// findMatchingPackages finds all packages in composer.lock that match the given name.
func findMatchingPackages(name string, composerPackages []LockPackage) []LockPackage {
	var matches []LockPackage

	for _, p := range composerPackages {
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
