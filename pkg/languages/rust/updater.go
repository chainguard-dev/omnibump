/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package rust

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/chainguard-dev/clog"
	"golang.org/x/mod/semver"
)

// DoUpdate performs the actual update of Rust package dependencies.
// Ported from cargobump/pkg/update.go.
func DoUpdate(ctx context.Context, packages map[string]*Package, cargoPackages []CargoPackage, cfg *UpdateConfig) error {
	log := clog.FromContext(ctx)

	// Run 'cargo update' prior to upgrading any dependency
	if cfg.Update {
		log.Infof("Running 'cargo update'...")
		if output, err := CargoUpdate(ctx, cfg.CargoRoot); err != nil {
			return fmt.Errorf("failed to run 'cargo update': %w with output: %v", err, output)
		}

		// Re-read Cargo.lock post-update
		var err error
		cargoPackages, err = GetCurrentPackages(ctx, cfg.CargoRoot)
		if err != nil {
			return err
		}
	}

	// Order packages by index for consistent updates
	orderedPackages := orderPackages(packages)

	for _, pkgName := range orderedPackages {
		pkg := packages[pkgName]
		if pkg == nil {
			log.Warnf("Package %s has nil entry in packages map, skipping", pkgName)
			continue
		}

		// Find matching package(s) in Cargo.lock
		matchingPackages := findMatchingPackages(pkgName, cargoPackages)
		if len(matchingPackages) == 0 {
			log.Warnf("Package %s not found in Cargo.lock", pkgName)
			continue
		}

		if err := upgradeReverseDependencies(cfg.CargoRoot, targetSpec(pkgName, pkg.Version)); err != nil {
			return err
		}

		// // Update each matching package version
		// for _, cargoPkg := range matchingPackages {
		// 	if err := updatePackage(ctx, pkg, cargoPkg, cfg); err != nil {
		// 		return err
		// 	}
		// }
	}

	return nil
}

// updatePackage updates a single package to the target version.
func updatePackage(ctx context.Context, pkg *Package, cargoPkg CargoPackage, cfg *UpdateConfig) error {
	log := clog.FromContext(ctx)

	// Check if already at target version
	if semver.Compare("v"+cargoPkg.Version, "v"+pkg.Version) == 0 {
		log.Infof("Package %s is already at version %s, skipping", cargoPkg.Name, pkg.Version)
		return nil
	}

	// Check for downgrade
	if semver.Compare("v"+cargoPkg.Version, "v"+pkg.Version) > 0 {
		log.Warnf("Package %s: current version %s is newer than requested %s, skipping",
			cargoPkg.Name, cargoPkg.Version, pkg.Version)
		return nil
	}

	log.Infof("Updating package %s from version %s to %s", cargoPkg.Name, cargoPkg.Version, pkg.Version)

	if output, err := CargoUpdatePackage(ctx, cargoPkg.Name, cargoPkg.Version, pkg.Version, cfg.CargoRoot); err != nil {
		return fmt.Errorf("failed to run cargo update for package '%s' from version '%s' to '%s': %w with output: %v",
			cargoPkg.Name, cargoPkg.Version, pkg.Version, err, output)
	}

	log.Infof("Package updated successfully: %s to version %s", cargoPkg.Name, pkg.Version)
	return nil
}

// targetSpec reconstructs the "name[@version[=precise]]" spec that parseTarget
// expects from a (pkgName, version) pair produced by the inline-package parser.
//
// The parser splits the precise form "name@from=precise" on '=', leaving the
// precise-pin version in version and the "name@from" half in pkgName. Rejoin
// those with '=' so the precise form survives the round-trip; a bare pkgName
// (no '@') is a plain "name@version" update.
func targetSpec(pkgName, version string) string {
	if strings.Contains(pkgName, "@") {
		return pkgName + "=" + version
	}
	return pkgName + "@" + version
}

// findMatchingPackages finds all packages in Cargo.lock that match the given name.
// This handles cases where a package name can have @version appended for specific version updates.
func findMatchingPackages(name string, cargoPackages []CargoPackage) []CargoPackage {
	var matches []CargoPackage

	// We need to get the base name (without @version suffix) to match against
	// e.g. "rand@0.8.5" should match "rand"
	baseName, version, found := strings.Cut(name, "@")

	for _, p := range cargoPackages {
		if p.Name == baseName && (!found || version == p.Version) {
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

// runCargoUpdate runs a single `cargo update` over the given name@version specs.
// Any extraArgs (e.g. "--precise", version) are inserted before the package list.
func runCargoUpdate(cargoRoot string, specs []string, extraArgs ...string) error {
	args := append([]string{"update", "-q"}, extraArgs...)
	for _, s := range specs {
		args = append(args, "--package", s)
	}
	cmd := exec.Command("cargo", args...)
	cmd.Dir = cargoRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cargo update: %w", err)
	}
	return nil
}

// upgradeReverseDependencies updates every crate that depends on the target, plus
// the target itself. For name/@version the target is resolved against the lock
// (skipping gracefully if an equal-or-newer compatible version is already there)
// and bumped to the latest compatible version. For name@version=precise the
// target is pinned to the exact version, even if that is a downgrade.
func upgradeReverseDependencies(cargoRoot string, arg string) error {
	t := parseTarget(arg)

	present, err := presentVersions(cargoRoot, t.name)
	if err != nil {
		return err
	}

	// Resolve the target to a concrete, present "name@version" spec to invert from.
	var discoverySpec string
	if t.isPrecise {
		// Precise always acts (no "already satisfied" skip) — it identifies an
		// instance to pin. Only skip if that compatibility line isn't present.
		inv := inLineVersion(t.version, present)
		if inv == "" {
			clog.Warnf("no version of %s compatible with %s is present (have: %s); skipping",
				t.name, t.version, joinVersions(present))
			return nil
		}
		discoverySpec = t.name + "@" + inv
	} else {
		spec, skip, msg, err := resolveVersion(t.name, t.version, t.hasVersion, present)
		if err != nil {
			return err
		}
		if skip {
			clog.Warnf("%s", msg)
			return nil
		}
		if msg != "" {
			clog.Infof("%s", msg)
		}
		discoverySpec = spec
	}

	clog.Infof("Calculating reverse dependencies for %s", discoverySpec)
	deps, err := getReverseDependencies(cargoRoot, discoverySpec)
	if err != nil {
		return err
	}

	// Reverse deps — and, for the non-precise form, the target itself — are bumped
	// to their latest compatible versions in a single cargo update invocation.
	// cargo resolves all specs in one pass (no drift between updates) and loads the
	// lockfile once.
	batch := deps
	if !t.isPrecise {
		batch = append([]string{discoverySpec}, deps...)
	}
	if len(batch) > 0 {
		clog.Infof("Updating %d dependencies", len(batch))
		if err := runCargoUpdate(cargoRoot, batch); err != nil {
			return err
		}
	}

	// Pin the precise target last, so the exact version is the final state. The
	// batch above re-resolves dependents' subtrees and may have moved the target,
	// so re-resolve its current in-line version before pinning.
	if t.isPrecise {
		present, err := presentVersions(cargoRoot, t.name)
		if err != nil {
			return err
		}
		cur := inLineVersion(t.version, present)
		if cur == "" {
			clog.Warnf("%s %s line is no longer present after updating dependents; cannot pin to %s",
				t.name, t.version, t.precise)
			return nil
		}
		spec := t.name + "@" + cur
		clog.Infof("Pinning %s precisely to %s", spec, t.precise)
		if err := runCargoUpdate(cargoRoot, []string{spec}, "--precise", t.precise); err != nil {
			return err
		}
	}
	return nil
}
