/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package rust implements omnibump support for Rust projects.
// Ported from cargobump with enhancements for the unified omnibump architecture.
package rust

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/chainguard-dev/clog"
	"golang.org/x/mod/semver"
)

// GetCargoLockPath returns the path to Cargo.lock in the given cargo root directory.
func GetCargoLockPath(cargoRoot string) (string, error) {
	cargoLockPath := filepath.Join(cargoRoot, "Cargo.lock")
	if _, err := os.Stat(cargoLockPath); os.IsNotExist(err) {
		return "", fmt.Errorf("%w in: %s", ErrCargoLockNotFound, cargoRoot)
	}

	return cargoLockPath, nil
}

// GetCurrentPackages parses Cargo.lock to get the current packages.
func GetCurrentPackages(ctx context.Context, cargoRoot string) ([]CargoPackage, error) {
	log := clog.FromContext(ctx)

	// Find Cargo.lock
	cargoLockPath, err := GetCargoLockPath(cargoRoot)
	if err != nil {
		return nil, err
	}

	// Parse Cargo.lock to get current packages
	file, err := os.Open(filepath.Clean(cargoLockPath))
	if err != nil {
		return nil, fmt.Errorf("failed to open Cargo.lock: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			log.Warnf("failed to close Cargo.lock: %v", closeErr)
		}
	}()

	cargoPackages, err := ParseCargoLock(file)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Cargo.lock: %w", err)
	}

	return cargoPackages, nil
}

// queryReverseDependencies runs a single `cargo tree` invocation and returns the
// reverse dependencies (crates that depend on spec) as "name@version" strings.
// Workspace members are intentionally included — they sit at the top of the
// reverse chain and should be picked up too. The queried package itself is
// included in cargo's inverted-tree output; callers are responsible for
// filtering it out.
func queryReverseDependencies(cargoRoot string, spec string) ([]string, error) {
	// cargo tree -p $pkg --target all -i --prefix none
	output := bytes.Buffer{}
	cmd := exec.Command("cargo", "tree", "-q", "-p", spec, "--target", "all", "-i", "--prefix", "none")
	cmd.Dir = cargoRoot
	cmd.Stdout = &output
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("cargo tree -p %s: %w", spec, err)
	}

	return parseCargoTree(output.String()), nil
}

// parseCargoTree turns flattened `cargo tree --prefix none` output into a list
// of "name@version" specs. Each line looks like "name vX.Y.Z [(/path)] [(*)]";
// blank and malformed lines are skipped. Duplicates are preserved — a crate is
// printed once per branch it appears in — and are de-duplicated by the caller.
func parseCargoTree(output string) []string {
	var deps []string
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Each line is "name vX.Y.Z [(/path)]"; strip the leading 'v' from the version.
		parts := strings.Fields(line)
		if len(parts) < 2 || len(parts[1]) < 2 {
			continue
		}
		deps = append(deps, fmt.Sprintf("%s@%s", parts[0], parts[1][1:]))
	}
	return deps
}

// getReverseDependencies returns every crate that (transitively) depends on
// pkgName. `cargo tree -i` already emits the full transitive reverse-dependency
// closure in a single pass — flattened by --prefix none — so one query suffices;
// there is no need to re-walk each discovered crate. Workspace members are kept
// (they sit at the top of the chain); only the target itself is removed.
func getReverseDependencies(cargoRoot string, pkgName string) ([]string, error) {
	deps, err := queryReverseDependencies(cargoRoot, pkgName)
	if err != nil {
		return nil, err
	}
	return reverseDepsFromTree(deps, pkgName), nil
}

// reverseDepsFromTree filters parsed `cargo tree -i` output into the set of
// crates to upgrade: it drops the target itself (matched by base name, since the
// target appears in its own inverted tree, whether pkgName is bare like "rand"
// or pinned like "rand@0.8.6") and returns a sorted, de-duplicated list.
func reverseDepsFromTree(deps []string, pkgName string) []string {
	targetName, _, _ := strings.Cut(pkgName, "@")

	var result []string
	for _, dep := range deps {
		if name, _, _ := strings.Cut(dep, "@"); name == targetName {
			continue
		}
		result = append(result, dep)
	}

	// Deduplicate: cargo lists a crate once per branch it appears in.
	slices.Sort(result)
	return slices.Compact(result)
}

// presentVersions returns the versions of crate `name` currently resolved in the
// workspace, as reported by `cargo metadata`.
func presentVersions(cargoRoot, name string) ([]string, error) {
	output := bytes.Buffer{}
	cmd := exec.Command("cargo", "metadata", "--format-version", "1")
	cmd.Dir = cargoRoot
	cmd.Stdout = &output
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("cargo metadata: %w", err)
	}

	var meta struct {
		Packages []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(output.Bytes(), &meta); err != nil {
		return nil, fmt.Errorf("parsing cargo metadata: %w", err)
	}

	var versions []string
	for _, p := range meta.Packages {
		if p.Name == name {
			versions = append(versions, p.Version)
		}
	}
	return versions, nil
}

// cargoCompatible reports whether `have` falls in the same Cargo caret-compatible
// line as `req` (both bare versions like "0.9.3"). Cargo's default caret: for
// major>0 the major must match; for 0.minor (minor>0) the major.minor must match
// (so 0.9.x ✓ but 0.10.x ✗); for 0.0.patch it must be the exact version.
func cargoCompatible(req, have string) bool {
	rv, hv := "v"+req, "v"+have
	if !semver.IsValid(rv) || !semver.IsValid(hv) {
		return req == have // non-semver versions: only an exact match is "compatible"
	}
	if semver.Major(rv) != semver.Major(hv) {
		return false
	}
	if semver.Major(rv) == "v0" {
		if semver.MajorMinor(rv) != semver.MajorMinor(hv) {
			return false
		}
		if semver.MajorMinor(rv) == "v0.0" {
			return semver.Compare(rv, hv) == 0
		}
	}
	return true
}

// resolveVersion maps a user-supplied target (a crate name and an optional pinned
// version) onto a concrete "name@version" spec that actually exists in `present`.
// It implements the "upgrade to at least version X" intent: if a compatible
// version >= the request is already present, the requirement is satisfied and we
// skip. It is pure (no I/O) so the version logic is unit-testable.
//
// Returns the spec to act on (empty when skipping), a skip flag, a human-readable
// reason to log, and an error for genuinely unusable input (an ambiguous bare
// name with several versions present).
func resolveVersion(name, version string, hasVersion bool, present []string) (spec string, skip bool, msg string, err error) {
	if len(present) == 0 {
		return "", true, fmt.Sprintf("%s is not present in the dependency graph; nothing to update", name), nil
	}

	if !hasVersion {
		if len(present) == 1 {
			return name + "@" + present[0], false, "", nil
		}
		return "", false, "", fmt.Errorf("%s resolves to multiple versions (%s); pin one as %s@<version>",
			name, strings.Join(present, ", "), name)
	}

	// Versioned target: collect the present versions in the request's caret line,
	// short-circuiting if the exact version is still present (then just upgrade it).
	var inLine []string
	for _, p := range present {
		if p == version {
			return name + "@" + version, false, "", nil
		}
		if cargoCompatible(version, p) {
			inLine = append(inLine, p)
		}
	}
	if len(inLine) == 0 {
		return "", true, fmt.Sprintf("no version of %s compatible with %s is present (have: %s); skipping",
			name, version, strings.Join(present, ", ")), nil
	}

	best := inLine[0]
	for _, p := range inLine[1:] {
		if semver.Compare("v"+p, "v"+best) > 0 {
			best = p
		}
	}
	if semver.Compare("v"+best, "v"+version) > 0 {
		return "", true, fmt.Sprintf("%s is already at %s, which satisfies >= %s; skipping", name, best, version), nil
	}
	// A compatible version is present but below the requested floor; act on it and
	// let `cargo update` advance it toward the latest in the line.
	return name + "@" + best, false,
		fmt.Sprintf("%s@%s not present; using compatible %s@%s", name, version, name, best), nil
}

// target is a parsed command-line argument of the form name[@version[=precise]].
type target struct {
	name       string // crate name
	version    string // pinned/"from" version; "" when bare
	precise    string // exact version to pin to (the "=to" form); "" otherwise
	hasVersion bool   // an @version was supplied
	isPrecise  bool   // an =precise was supplied
}

// parseTarget splits "name", "name@version", and "name@version=precise" into a
// target. The three forms drive different update behavior: bare/@version resolve
// against the lock and bump to the latest compatible version, while =precise pins
// the crate to an exact version (even a downgrade).
func parseTarget(arg string) target {
	name, rest, hasAt := strings.Cut(arg, "@")
	t := target{name: name}
	if !hasAt {
		return t
	}
	t.hasVersion = true
	from, to, hasEq := strings.Cut(rest, "=")
	t.version = from
	if hasEq {
		t.isPrecise = true
		t.precise = to
	}
	return t
}

// inLineVersion returns the highest present version in the same Cargo caret line
// as `version`, or "" if none is present. (cargo unifies a compatible range to a
// single locked version, so there is normally at most one.)
func inLineVersion(version string, present []string) string {
	var best string
	for _, p := range present {
		if !cargoCompatible(version, p) {
			continue
		}
		if best == "" || semver.Compare("v"+p, "v"+best) > 0 {
			best = p
		}
	}
	return best
}

// joinVersions renders a version list for log messages, or "none" when empty.
func joinVersions(versions []string) string {
	if len(versions) == 0 {
		return "none"
	}
	return strings.Join(versions, ", ")
}
