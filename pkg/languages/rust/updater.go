/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package rust

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/languages/rust/revdep"
)

// checkBuild verifies the project still compiles after a SemVer-breaking edit.
// It is a package var so tests can substitute a stub instead of invoking cargo.
var checkBuild = CargoCheck

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

		if err := upgradeReverseDependencies(ctx, cfg.CargoRoot, targetSpec(pkgName, pkg.Version)); err != nil {
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

// upgradeReverseDependencies satisfies a requested crate upgrade by computing the
// minimal reverse-dependency plan (revdep.Calculate), editing the gating direct
// dependency's Cargo.toml, and pinning the boundary crates to their minimum
// versions — after which cargo resolves the transitive graph. It works uniformly
// whether the target is a direct or an indirect dependency: the calculator walks
// up the inverted tree to the workspace member that must widen a constraint.
//
// The target version is a floor (">= floor") for the bare/@version forms and an
// exact pin for the name@from=precise form. A SemVer-breaking edit is verified
// with `cargo check`; a build failure means no upgrade is possible.
func upgradeReverseDependencies(ctx context.Context, cargoRoot string, arg string) error {
	log := clog.FromContext(ctx)
	t := parseTarget(arg)

	present, err := presentVersions(ctx, cargoRoot, t.name)
	if err != nil {
		return err
	}
	if len(present) == 0 {
		log.Warnf("%s is not present in the dependency graph; nothing to update", t.name)
		return nil
	}

	// Determine the concrete floor version to reach.
	floor := t.version
	switch {
	case t.isPrecise:
		floor = t.precise
	case !t.hasVersion:
		// Bare crate name: refuse an ambiguous bump and resolve the latest
		// published version as the floor.
		if len(present) > 1 {
			return fmt.Errorf("%w: %s (%s); pin one as %s@<version>",
				ErrAmbiguousTarget, t.name, joinVersions(present), t.name)
		}
		latest, lerr := LatestCrateVersion(ctx, t.name)
		if lerr != nil {
			return lerr
		}
		floor = latest
	}

	// Cheap skip: a present version already satisfies the floor. A precise pin must
	// still run so the exact version is landed.
	if !t.isPrecise && satisfiesFloor(present, floor) {
		log.Infof("%s already satisfies >= %s; skipping", t.name, floor)
		return nil
	}

	// Compute the minimal reverse-dependency upgrade plan.
	members, _, err := workspaceLayout(ctx, cargoRoot)
	if err != nil {
		return err
	}
	memberSet := make(map[string]bool, len(members))
	for _, m := range members {
		memberSet[m.name] = true
	}

	plan, err := revdep.Calculate(ctx, t.name, floor, revdep.Options{
		Tree: func(ctx context.Context, crate string) (string, error) {
			return cargoTreeInverted(ctx, cargoRoot, crate)
		},
		IndexURL:         cratesIndexBaseURL + "/",
		HTTP:             indexHTTPClient,
		WorkspaceMembers: memberSet,
	})
	if err != nil {
		return err
	}

	changed := len(plan.Edits) > 0 || len(plan.Boundaries) > 0
	if !changed && !t.isPrecise {
		log.Infof("%s already satisfies >= %s; nothing to upgrade", t.name, floor)
		return nil
	}

	// Widen each gating direct dependency's Cargo.toml constraint.
	rootBumped := map[string]bool{}
	for _, e := range plan.Edits {
		log.Infof("Bumping %s (in %s) to allow >= %s", e.Dependency, e.Member, e.MinVersion)
		if err := applyDirectEdit(ctx, cargoRoot, e, rootBumped); err != nil {
			return err
		}
	}

	// Land the minimal versions: pin each boundary crate precisely; cargo resolves
	// the transitive graph to satisfy the widened constraints.
	for _, b := range plan.Boundaries {
		log.Infof("Pinning %s to %s", b.Crate, b.To)
		if err := runCargoUpdate(ctx, cargoRoot, []string{b.Crate}, "--precise", b.To); err != nil {
			return fmt.Errorf("pinning %s to %s: %w", b.Crate, b.To, err)
		}
	}

	// For the exact =precise form, guarantee the final pin.
	if t.isPrecise {
		if err := pinPrecise(ctx, cargoRoot, t); err != nil {
			return err
		}
	}

	// A bump can break the build (a SemVer-breaking edit changes APIs). Verify with
	// `cargo check`; failure means the upgrade is not viable. Edits are left on disk
	// for the caller to inspect or discard.
	log.Infof("Verifying the project builds after upgrading %s to %s", t.name, floor)
	if out, err := checkBuild(ctx, cargoRoot); err != nil {
		log.Warnf("cargo check failed after upgrading %s to %s:\n%s", t.name, floor, out)
		return fmt.Errorf("%w: upgrading %s to %s does not compile", ErrUpgradeBrokeBuild, t.name, floor)
	}
	return nil
}

// applyDirectEdit widens the Cargo.toml constraint on e.Dependency in workspace
// member e.Member to allow at least e.MinVersion. Registry deps are edited with
// cargo add (lossless); workspace-inherited deps are edited in the root
// [workspace.dependencies] table once (tracked in rootBumped). A git/path dep or
// a crate not actually declared by the member cannot be bumped and is an error.
func applyDirectEdit(ctx context.Context, cargoRoot string, e revdep.DirectEdit, rootBumped map[string]bool) error {
	sections, workspaceRoot, err := manifestSections(ctx, cargoRoot, e.Dependency)
	if err != nil {
		return err
	}
	caret := caretConstraint(e.MinVersion)

	applied := false
	for _, sec := range sections {
		if sec.member != e.Member {
			continue
		}
		switch {
		case sec.inherited:
			// The constraint lives in the root [workspace.dependencies] table; edit
			// it once no matter how many members inherit it.
			if rootBumped[e.Dependency] {
				applied = true
				continue
			}
			if err := bumpWorkspaceDependency(filepath.Join(workspaceRoot, "Cargo.toml"), e.Dependency, caret); err != nil {
				return err
			}
			rootBumped[e.Dependency] = true
			applied = true
		case !sec.registry:
			return fmt.Errorf("%w: %s in %s is a git/path dependency and cannot be bumped to require %s",
				ErrNoCompatibleVersion, e.Dependency, e.Member, e.MinVersion)
		default:
			if err := cargoAdd(ctx, cargoRoot, sec, e.Dependency, caret); err != nil {
				return err
			}
			applied = true
		}
	}
	if !applied {
		return fmt.Errorf("%w: %s is not declared by workspace member %s; cannot satisfy the upgrade",
			ErrNoCompatibleVersion, e.Dependency, e.Member)
	}
	return nil
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
