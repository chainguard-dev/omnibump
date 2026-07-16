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

	brokeSemVer := false
	requested := map[string]bool{}
	for _, pkgName := range orderedPackages {
		pkg := packages[pkgName]
		if pkg == nil {
			log.Warnf("Package %s has nil entry in packages map, skipping", pkgName)
			continue
		}

		// Record the requested crate (base name, stripping any @version) so curated
		// crate families can be coordinated once all targets have landed.
		base, _, _ := strings.Cut(pkgName, "@")
		requested[base] = true

		// Find matching package(s) in Cargo.lock
		matchingPackages := findMatchingPackages(pkgName, cargoPackages)
		if len(matchingPackages) == 0 {
			log.Warnf("Package %s not found in Cargo.lock", pkgName)
			continue
		}

		broke, err := upgradeReverseDependencies(ctx, cfg.CargoRoot, targetSpec(pkgName, pkg.Version))
		if err != nil {
			return err
		}
		brokeSemVer = brokeSemVer || broke
	}

	// Coordinate curated crate families (e.g. rand -> rand_core, rand_chacha): once
	// every requested target has landed, advance any present sibling in place. This
	// is in-line only, so it never breaks SemVer; it runs before the check so the
	// verification (if any) covers the refreshed lock.
	if err := refreshFamilies(ctx, cfg.CargoRoot, requested); err != nil {
		return err
	}

	// A SemVer-breaking upgrade (a rewritten Cargo.toml caret line) can change APIs
	// and leave the project uncompilable; in-line bumps are SemVer-compatible and
	// cannot. Verify once, after all packages are landed, and only when at least one
	// upgrade crossed a line — so the result is independent of package ordering and
	// the compile is not repeated per pin. Edits are left on disk for the caller to
	// inspect or discard.
	if brokeSemVer {
		log.Infof("Verifying the project builds after SemVer-breaking upgrades")
		if out, err := checkBuild(ctx, cfg.CargoRoot); err != nil {
			log.Warnf("cargo check failed after the applied upgrades:\n%s", out)
			return fmt.Errorf("%w: the applied upgrades do not compile", ErrUpgradeBrokeBuild)
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

// invertedTreeSpec returns the cargo pkgid to root `cargo tree -i` at. cargo emits
// one inverted tree per locked version of a crate, and the parser requires a single
// root, so when several versions are locked this scopes the query to the specific
// instance in the requested line (t.version — the caret line for @version, the
// "from" line for =precise). A request that doesn't match any locked line is
// ambiguous. A single locked version needs no scoping and returns the bare name.
func invertedTreeSpec(name string, t target, present []string) (string, error) {
	if len(present) <= 1 {
		return name, nil
	}
	instance := inLineVersion(t.version, present)
	if instance == "" {
		return "", fmt.Errorf("%w: %s is locked at multiple versions (%s), none matching %s; pin one as %s@<version>",
			ErrAmbiguousTarget, name, joinVersions(present), t.version, name)
	}
	return name + "@" + instance, nil
}

// floorSatisfiedInLine reports whether the crate already meets the floor, so the
// upgrade can be skipped. When the requested line (t.version's caret line) is
// present, it is judged on its own: a crate locked at several incompatible lines
// (e.g. rand 0.9.0 and 0.10.1, required by different crates) must not have a stale
// target line masked by a higher unrelated line. When the requested line is
// absent, the crate only exists on other lines, so the global check applies — if
// a higher line already meets the floor there is nothing to bump, and forcing the
// crate down onto the requested line would be a cross-line downgrade that breaks
// its dependents. A bare name (no @version) always uses the global check; its
// single present version is guaranteed (multiple are rejected as ambiguous first).
func floorSatisfiedInLine(t target, present []string, floor string) bool {
	if t.hasVersion {
		if inLine := inLineVersion(t.version, present); inLine != "" {
			return satisfiesFloor([]string{inLine}, floor)
		}
	}
	return satisfiesFloor(present, floor)
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

// boundaryPinSpec returns the `cargo update --package` spec for a boundary pin and
// whether the pin should be skipped. It is decided against present — the crate's
// versions currently locked, re-read after the manifest edits (which re-resolve the
// lock, so the plan's original "from" version may no longer exist).
//
// A boundary crate can be locked at several versions (e.g. axoasset 0.4.0, 0.5.1,
// 0.6.2), so a bare name is ambiguous. If the target is already locked, the pin is a
// no-op and is skipped. Otherwise the specific instance in the target's SemVer line
// is pinned (Crate@<in-line version>); a bare name is the fallback when only one
// version is locked or the target's line is absent.
func boundaryPinSpec(b revdep.Boundary, present []string) (spec string, skip bool) {
	for _, v := range present {
		if v == b.To {
			return "", true
		}
	}
	if cur := inLineVersion(b.To, present); cur != "" {
		return b.Crate + "@" + cur, false
	}
	return b.Crate, false
}

// upgradeReverseDependencies satisfies a requested crate upgrade by computing the
// minimal reverse-dependency plan (revdep.Calculate), editing the gating direct
// dependency's Cargo.toml, and pinning the boundary crates to their minimum
// versions — after which cargo resolves the transitive graph. It works uniformly
// whether the target is a direct or an indirect dependency: the calculator walks
// up the inverted tree to the workspace member that must widen a constraint.
//
// The target version is a floor (">= floor") for the bare/@version forms and an
// exact pin for the name@from=precise form. It returns whether this upgrade was
// SemVer-breaking (moved the target onto a new caret line, rewriting a Cargo.toml
// constraint); the caller runs `cargo check` once if any upgrade broke SemVer.
func upgradeReverseDependencies(ctx context.Context, cargoRoot string, arg string) (bool, error) {
	log := clog.FromContext(ctx)
	t := parseTarget(arg)

	present, err := presentVersions(ctx, cargoRoot, t.name)
	if err != nil {
		return false, err
	}
	if len(present) == 0 {
		log.Warnf("%s is not present in the dependency graph; nothing to update", t.name)
		return false, nil
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
			return false, fmt.Errorf("%w: %s (%s); pin one as %s@<version>",
				ErrAmbiguousTarget, t.name, joinVersions(present), t.name)
		}
		latest, lerr := LatestCrateVersion(ctx, t.name)
		if lerr != nil {
			return false, lerr
		}
		floor = latest
	}

	// A SemVer-breaking upgrade moves the target onto a new caret line: no currently
	// locked version shares the floor's line, so a Cargo.toml constraint must be
	// rewritten and APIs can change. An in-line bump is SemVer-compatible and cannot
	// break the build, so it needs no cargo check.
	broke := inLineVersion(floor, present) == ""

	// Cheap skip: the crate already satisfies the floor within the target's SemVer
	// line. A precise pin must still run so the exact version is landed.
	if !t.isPrecise && floorSatisfiedInLine(t, present, floor) {
		log.Infof("%s already satisfies >= %s; skipping", t.name, floor)
		return false, nil
	}

	// Compute the minimal reverse-dependency upgrade plan.
	members, _, err := workspaceLayout(ctx, cargoRoot)
	if err != nil {
		return false, err
	}
	memberSet := make(map[string]bool, len(members))
	for _, m := range members {
		memberSet[m.name] = true
	}

	// Scope the inverted-tree query so cargo emits a single tree even when the
	// crate is locked at multiple versions (see invertedTreeSpec).
	treeSpec, err := invertedTreeSpec(t.name, t, present)
	if err != nil {
		return false, err
	}

	// Fetch the inverted tree up front. An empty tree (ErrCrateNotActivated) means
	// the crate is locked but not activated in this query's view — there are no
	// dependents to widen, so the reverse-dependency engine cannot run. Fall back to
	// a direct lockfile pin instead of aborting the whole bump. A direct pin edits no
	// manifest constraint, so it is not SemVer-breaking.
	treeText, err := cargoTreeInverted(ctx, cargoRoot, treeSpec)
	if errors.Is(err, ErrCrateNotActivated) {
		return false, pinLockedDependency(ctx, cargoRoot, t, floor, present)
	}
	if err != nil {
		return false, err
	}

	plan, err := revdep.Calculate(ctx, t.name, floor, revdep.Options{
		Tree: func(context.Context, string) (string, error) {
			return treeText, nil
		},
		IndexURL:         cratesIndexBaseURL + "/",
		HTTP:             indexHTTPClient,
		WorkspaceMembers: memberSet,
	})
	if err != nil {
		return false, err
	}

	changed := len(plan.Edits) > 0 || len(plan.Boundaries) > 0
	if !changed && !t.hasVersion {
		// Bare-name empty plan: no manifest constraint needs widening and the caller
		// gave no version to advance to, so just refresh the lock to the resolved
		// floor. No SemVer constraint is being broken, so no cargo check is needed.
		// (The versioned forms fall through to the land path below, even on an empty
		// plan, so an @version floor is advanced to the latest in its line.)
		log.Infof("%s: constraints already permit >= %s; refreshing the lock", t.name, floor)
		if err := runCargoUpdate(ctx, cargoRoot, []string{treeSpec}, "--precise", floor); err != nil {
			return false, err
		}
		return false, nil
	}

	// Widen each gating direct dependency's Cargo.toml constraint.
	rootBumped := map[string]bool{}
	for _, e := range plan.Edits {
		log.Infof("Bumping %s (in %s) to allow >= %s", e.Dependency, e.Member, e.MinVersion)
		if err := applyDirectEdit(ctx, cargoRoot, e, rootBumped); err != nil {
			return false, err
		}
	}

	// Land the minimal versions: pin each boundary crate precisely; cargo resolves
	// the transitive graph to satisfy the widened constraints.
	for _, b := range plan.Boundaries {
		// Re-read the lock: the manifest edits above may have moved the boundary crate
		// (or already landed the target), so decide the pin against the current state.
		boundaryPresent, err := presentVersions(ctx, cargoRoot, b.Crate)
		if err != nil {
			return false, err
		}
		spec, skip := boundaryPinSpec(b, boundaryPresent)
		if skip {
			log.Infof("%s is already at %s; nothing to pin", b.Crate, b.To)
			continue
		}
		log.Infof("Pinning %s to %s", spec, b.To)
		if err := runCargoUpdate(ctx, cargoRoot, []string{spec}, "--precise", b.To); err != nil {
			return false, fmt.Errorf("pinning %s to %s: %w", spec, b.To, err)
		}
	}

	// Land the target deliberately. This runs only for the versioned forms; a bare
	// name is landed by the boundary pins above and keeps its existing behavior. The
	// =precise form is pinned to its exact version; the @version form is advanced to
	// the latest version in its SemVer line (floor semantics).
	if t.hasVersion {
		if t.isPrecise {
			if err := pinPrecise(ctx, cargoRoot, t); err != nil {
				return false, err
			}
		} else if err := landFloor(ctx, cargoRoot, t); err != nil {
			return false, err
		}
	}

	// Report the version actually landed, which for a floor (@version) request may
	// be newer than the requested floor (e.g. 0.9.5 for a request of >= 0.9.3). The
	// project build is verified once by the caller after all packages are landed.
	log.Infof("%s upgraded to %s", t.name, landedVersion(ctx, cargoRoot, t, floor))
	return broke, nil
}

// landedVersion resolves the version of the target now locked in the requested
// SemVer line — the exact version for a =precise pin or the latest for an @version
// floor. It falls back to the requested floor when the line cannot be resolved
// (e.g. a bare name, or a cross-line move that vacated the requested line), so log
// messages always carry a concrete version.
func landedVersion(ctx context.Context, cargoRoot string, t target, floor string) string {
	if !t.hasVersion {
		return floor
	}
	present, err := presentVersions(ctx, cargoRoot, t.name)
	if err != nil {
		return floor
	}
	marker := t.version
	if t.isPrecise {
		marker = t.precise
	}
	if v := inLineVersion(marker, present); v != "" {
		return v
	}
	return floor
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
		// The "from" line is gone because upgrading the dependents moved the crate
		// onto a newer line — the intended outcome for a transitive dependency, so
		// long as it reached the requested version. Verify rather than reporting a
		// spurious "cannot pin".
		verifyTransitiveUpgrade(ctx, t.name, present, t.precise)
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

// verifyTransitiveUpgrade reports the outcome when a precise pin's original ("from")
// line is no longer present: the crate was moved onto a newer line by upgrading its
// dependents. That is the expected result for a transitive dependency as long as it
// reached the requested version, so it logs success in that case and only warns when
// the crate is still below the requested version — signalling that the direct
// dependency upgrade did not pull the transitive dependency forward as expected.
func verifyTransitiveUpgrade(ctx context.Context, name string, present []string, requested string) {
	log := clog.FromContext(ctx)
	if satisfiesFloor(present, requested) {
		log.Infof("%s is at %s, which satisfies the requested %s; the dependency upgrade pulled it in as expected",
			name, maxVersion(present), requested)
		return
	}
	log.Warnf("%s is at %s but %s was requested; the direct dependency upgrade did not pull in %s as expected",
		name, joinVersions(present), requested, requested)
}

// pinLockedDependency lands a crate that is locked but not activated in the inverted
// dependency tree (see ErrCrateNotActivated) — an optional dep behind a disabled
// feature, or a platform-gated dep on a non-matching host. There are no dependents
// to widen, so the reverse-dependency engine cannot help; pin the crate directly in
// the lockfile to the requested version instead. A failed pin (e.g. a dependent's
// constraint forbids it) is warned, not fatal: the crate is inactive here, so a bump
// of it must never abort the run.
func pinLockedDependency(ctx context.Context, cargoRoot string, t target, floor string, present []string) error {
	log := clog.FromContext(ctx)

	// Target the currently-locked in-line instance so cargo updates the right one
	// when several versions are locked; fall back to the sole locked version.
	spec := t.name
	if cur := inLineVersion(floor, present); cur != "" {
		spec = t.name + "@" + cur
	} else if len(present) == 1 {
		spec = t.name + "@" + present[0]
	}

	log.Infof("%s is locked but not activated in the dependency tree; pinning it directly to %s", t.name, floor)
	if err := runCargoUpdate(ctx, cargoRoot, []string{spec}, "--precise", floor); err != nil {
		log.Warnf("could not pin inactive dependency %s to %s (%v); leaving it unchanged", t.name, floor, err)
	}
	return nil
}

// landFloor advances the @version target to the latest version compatible with its
// current SemVer line. The requested version is treated as a floor, not an exact
// pin, so the crate is moved to the newest release in the same caret line (e.g.
// rand 0.9.0 -> 0.9.5 for a request of rand@0.9.3). Only the target is touched; any
// curated sibling crates are advanced separately by refreshFamilies (see
// families.go).
func landFloor(ctx context.Context, cargoRoot string, t target) error {
	log := clog.FromContext(ctx)

	present, err := presentVersions(ctx, cargoRoot, t.name)
	if err != nil {
		return err
	}
	cur := inLineVersion(t.version, present)
	if cur == "" {
		// A cross-line manifest edit already moved the crate onto a new line (and
		// pulled its family with it); nothing to advance in the requested line.
		log.Infof("%s: line %s no longer present; skipping floor advance", t.name, t.version)
		return nil
	}

	log.Infof("Advancing %s to the latest version compatible with %s", t.name, cur)
	if err := runCargoUpdate(ctx, cargoRoot, []string{t.name + "@" + cur}); err != nil {
		return err
	}

	after, err := presentVersions(ctx, cargoRoot, t.name)
	if err != nil {
		return err
	}
	if inLine := inLineVersion(t.version, after); inLine == "" || !satisfiesFloor([]string{inLine}, t.version) {
		log.Warnf("%s is at %s after the floor advance but >= %s was requested", t.name, joinVersions(after), t.version)
	}
	return nil
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
