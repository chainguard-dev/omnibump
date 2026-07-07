/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package rust

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chainguard-dev/clog"
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

	// Partition the lockfile into direct dependencies (declared in a member's
	// Cargo.toml) and everything reachable only transitively. A SemVer-breaking
	// bump of a direct dep can be satisfied by editing its Cargo.toml constraint;
	// an indirect dep can only move by upgrading its dependents.
	direct, _ := classifyDependencies(cargoPackages)

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

		if err := upgradeReverseDependencies(ctx, cfg.CargoRoot, targetSpec(pkgName, pkg.Version), direct); err != nil {
			return err
		}
	}

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
func runCargoUpdate(ctx context.Context, cargoRoot string, specs []string, extraArgs ...string) error {
	args := append([]string{"update", "-q"}, extraArgs...)
	for _, s := range specs {
		args = append(args, "--package", s)
	}
	cmd := cargoCommand(ctx, cargoRoot, args...)
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
// target is pinned to the exact version, unless that would be a downgrade, in
// which case the pin is skipped with a warning.
func upgradeReverseDependencies(ctx context.Context, cargoRoot string, arg string, direct map[string]bool) error {
	log := clog.FromContext(ctx)
	t := parseTarget(arg)

	present, err := presentVersions(ctx, cargoRoot, t.name)
	if err != nil {
		return err
	}

	// A precise pin whose target version sits outside every present caret line is a
	// SemVer-boundary crossing: `cargo update --precise` would be rejected by the
	// existing Cargo.toml constraint, so the manifest must be widened first. The
	// non-precise @version form reaches handleCrossSemver via ErrNoCompatibleVersion
	// (below); the precise form is resolved against the "from" line and never
	// surfaces that error, so detect and route it here.
	if t.isPrecise && crossesSemverBoundary(t.precise, present) {
		if fixed, ferr := handleCrossSemver(ctx, cargoRoot, t, direct); fixed {
			return ferr
		}
	}

	// Resolve the target to a concrete, present "name@version" spec to invert from.
	log.Debug("Running resolveDiscoverySpec...")
	discoverySpec, skip, err := resolveDiscoverySpec(ctx, t, present)
	if err != nil {
		log.Debug("resolveDiscoverySpec failed, checking for compatible version")
		// The crate is pinned below the requested floor and no compatible version
		// is present. For a direct dependency we can satisfy this by rewriting its
		// Cargo.toml constraint across the SemVer boundary; anything else keeps the
		// original (fatal) error.
		if errors.Is(err, ErrNoCompatibleVersion) {
			if fixed, ferr := handleCrossSemver(ctx, cargoRoot, t, direct); fixed {
				return ferr
			}
		}
		return err
	}
	if skip {
		return nil
	}

	log.Infof("Calculating reverse dependencies for %s", discoverySpec)
	deps, err := getReverseDependencies(ctx, cargoRoot, discoverySpec)
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
		log.Infof("Updating %d dependencies", len(batch))
		if err := runCargoUpdate(ctx, cargoRoot, batch); err != nil {
			return err
		}
	}

	// Pin the precise target last, so the exact version is the final state.
	if t.isPrecise {
		return pinPrecise(ctx, cargoRoot, t)
	}
	return nil
}

// resolveDiscoverySpec resolves the target to a concrete, present "name@version"
// spec to invert reverse dependencies from. It returns skip=true (with the reason
// already logged) when there is nothing to act on.
func resolveDiscoverySpec(ctx context.Context, t target, present []string) (spec string, skip bool, err error) {
	log := clog.FromContext(ctx)

	if t.isPrecise {
		// Precise always acts (no "already satisfied" skip) — it identifies an
		// instance to pin. Only skip if that compatibility line isn't present.
		inv := inLineVersion(t.version, present)
		if inv == "" {
			log.Warnf("no version of %s compatible with %s is present (have: %s); skipping",
				t.name, t.version, joinVersions(present))
			return "", true, nil
		}
		return t.name + "@" + inv, false, nil
	}

	spec, skip, msg, err := resolveVersion(t.name, t.version, t.hasVersion, present)
	if err != nil {
		return "", false, err
	}
	if skip {
		log.Warnf("%s", msg)
		return "", true, nil
	}
	if msg != "" {
		log.Infof("%s", msg)
	}
	return spec, false, nil
}

// pinPrecise pins the target crate to its exact version (t.precise). The preceding
// batch update re-resolves dependents' subtrees and may have moved the target, so
// re-resolve its current in-line version before pinning. Refuses to pin if doing
// so would downgrade the crate below its current version.
func pinPrecise(ctx context.Context, cargoRoot string, t target) error {
	log := clog.FromContext(ctx)

	present, err := presentVersions(ctx, cargoRoot, t.name)
	if err != nil {
		return err
	}
	cur := inLineVersion(t.version, present)
	if cur == "" {
		log.Warnf("%s %s line is no longer present after updating dependents; cannot pin to %s",
			t.name, t.version, t.precise)
		return nil
	}
	if isDowngrade(cur, t.precise) {
		log.Warnf("Refusing to downgrade package %s from %s to %s", t.name, cur, t.precise)
		return nil
	}

	spec := t.name + "@" + cur
	log.Infof("Pinning %s precisely to %s", spec, t.precise)
	return runCargoUpdate(ctx, cargoRoot, []string{spec}, "--precise", t.precise)
}

// handleCrossSemver satisfies a SemVer-breaking upgrade of a DIRECT dependency by
// rewriting its Cargo.toml constraint to the requested caret line, then
// reconciling Cargo.lock. This is the robust replacement for the fragile
// `sed -i 's/name = "0.2"/name = "0.3"/' Cargo.toml` hack operators fall back to.
//
// It returns fixed=false to signal "not handled here — propagate the caller's
// original error" for cases that genuinely cannot be resolved by a manifest edit:
// an indirect dependency (its version can only move by upgrading its dependents,
// which is the reverse-dependency path's job), a git/path dependency (no registry
// version to bump), or a crate not declared in any manifest. When fixed=true, the
// returned error is the result of attempting the edit (nil on success).
func handleCrossSemver(ctx context.Context, cargoRoot string, t target, direct map[string]bool) (fixed bool, err error) {
	log := clog.FromContext(ctx)

	if !isDirect(t.name, direct) {
		return false, nil
	}

	sections, workspaceRoot, err := manifestSections(ctx, cargoRoot, t.name)
	if err != nil {
		return true, err
	}
	if len(sections) == 0 {
		// Classified direct by the lockfile but not found in any manifest; leave it
		// to the original error rather than guessing.
		return false, nil
	}

	// The version the manifest must allow: the exact pin target for a precise bump,
	// otherwise the requested floor.
	target := t.version
	if t.isPrecise {
		target = t.precise
	}
	caret := caretConstraint(target)
	log.Infof("%s requires a SemVer-breaking upgrade to %s; rewriting its Cargo.toml constraint to \"%s\"", t.name, target, caret)

	rootBumped := false
	for _, sec := range sections {
		switch {
		case sec.inherited:
			// The constraint lives in the root [workspace.dependencies] table; edit
			// it once no matter how many members inherit it.
			if rootBumped {
				continue
			}
			rootPath := filepath.Join(workspaceRoot, "Cargo.toml")
			if err := bumpWorkspaceDependency(rootPath, t.name, caret); err != nil {
				return true, err
			}
			rootBumped = true
		case !sec.registry:
			// git/path dependency: no registry version to write. Not fixable by a
			// version bump — propagate the original error.
			return false, nil
		default:
			if err := cargoAdd(ctx, cargoRoot, sec, t.name, caret); err != nil {
				return true, err
			}
		}
	}

	// Reconcile Cargo.lock with the widened constraint: a precise bump pins the
	// exact target, otherwise take the latest version in the newly-allowed line.
	if t.isPrecise {
		if err := runCargoUpdate(ctx, cargoRoot, []string{t.name}, "--precise", target); err != nil {
			return true, err
		}
		return true, nil
	}
	if err := runCargoUpdate(ctx, cargoRoot, []string{t.name}); err != nil {
		return true, err
	}
	return true, nil
}

// isDirect reports whether crate `name` is a direct dependency. The `direct` map
// (from classifyDependencies) is keyed "name@version", so matching is by base
// name across every locked version.
func isDirect(name string, direct map[string]bool) bool {
	for key := range direct {
		if base, _, _ := strings.Cut(key, "@"); base == name {
			return true
		}
	}
	return false
}

// cargoAdd rewrites the version requirement of an existing direct dependency in a
// member's Cargo.toml via `cargo add name@caret`, targeting the same section the
// crate is already declared in. cargo edits the manifest through toml_edit, so
// existing features and formatting are preserved and only the version changes.
func cargoAdd(ctx context.Context, cargoRoot string, sec manifestSection, name, caret string) error {
	args := []string{"add", "-q", name + "@" + caret}
	if sec.member != "" {
		args = append(args, "--package", sec.member)
	}
	switch sec.kind {
	case "dev":
		args = append(args, "--dev")
	case "build":
		args = append(args, "--build")
	}
	if sec.target != "" {
		args = append(args, "--target", sec.target)
	}

	cmd := cargoCommand(ctx, cargoRoot, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cargo add %s@%s: %w", name, caret, err)
	}
	return nil
}
