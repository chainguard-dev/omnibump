/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package rust implements omnibump support for Rust projects.
// Ported from cargobump with enhancements for the unified omnibump architecture.
package rust

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/chainguard-dev/clog"
	"golang.org/x/mod/semver"
)

// ErrAmbiguousTarget is returned when a bare crate name resolves to multiple
// present versions, so the caller must pin one explicitly as name@<version>.
var ErrAmbiguousTarget = errors.New("crate resolves to multiple versions")

// ErrCrateNotFound is returned when the crates.io index has no entry for a crate.
var ErrCrateNotFound = errors.New("crate not found in crates.io index")

// ErrNoMatchingVersion is returned when no published version of a crate
// satisfies the requested SemVer constraint.
var ErrNoMatchingVersion = errors.New("no published version satisfies the constraint")

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
func queryReverseDependencies(ctx context.Context, cargoRoot string, spec string) ([]string, error) {
	// cargo tree -p $pkg --target all -i --prefix none
	output := bytes.Buffer{}
	cmd := exec.CommandContext(ctx, "cargo", "tree", "-q", "-p", spec, "--target", "all", "-i", "--prefix", "none") //nolint:gosec // spec is a cargo package spec derived from the lockfile
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
func getReverseDependencies(ctx context.Context, cargoRoot string, pkgName string) ([]string, error) {
	deps, err := queryReverseDependencies(ctx, cargoRoot, pkgName)
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
func presentVersions(ctx context.Context, cargoRoot, name string) ([]string, error) {
	output := bytes.Buffer{}
	cmd := exec.CommandContext(ctx, "cargo", "metadata", "--format-version", "1")
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
		return "", false, "", fmt.Errorf("%w: %s (%s); pin one as %s@<version>",
			ErrAmbiguousTarget, name, strings.Join(present, ", "), name)
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

// maxVersion returns the highest valid semver in versions, or "" if none parse.
func maxVersion(versions []string) string {
	var best string
	for _, v := range versions {
		if !semver.IsValid("v" + v) {
			continue
		}
		if best == "" || semver.Compare("v"+v, "v"+best) > 0 {
			best = v
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

// cratesIndexBaseURL is the crates.io sparse-index endpoint (the same source
// cargo reads). Overridable via a package var so tests can point at a stub.
var cratesIndexBaseURL = "https://index.crates.io"

// indexHTTPClient is the client used for crates.io sparse-index queries.
var indexHTTPClient = &http.Client{Timeout: 15 * time.Second}

// crateIndexEntry is one newline-delimited JSON record in a crates.io
// sparse-index file. Only the fields we need are decoded.
type crateIndexEntry struct {
	Version string `json:"vers"`
	Yanked  bool   `json:"yanked"`
}

// indexPath returns the crates.io sparse-index path for a crate name, following
// cargo's directory sharding: 1- and 2-char names live under "1/" and "2/",
// 3-char names under "3/<first-char>/", and longer names under
// "<first-two>/<next-two>/". The name is lowercased to match the index's
// canonical (case-insensitive) form.
func indexPath(name string) string {
	name = strings.ToLower(name)
	switch len(name) {
	case 0:
		return ""
	case 1:
		return "1/" + name
	case 2:
		return "2/" + name
	case 3:
		return "3/" + name[:1] + "/" + name
	default:
		return name[:2] + "/" + name[2:4] + "/" + name
	}
}

// fetchCrateVersions queries the crates.io sparse index for `name` and returns
// its published, non-yanked versions in index order. The index serves one JSON
// release record per line, so it is streamed line by line.
func fetchCrateVersions(ctx context.Context, name string) ([]string, error) {
	path := indexPath(name)
	if path == "" {
		return nil, fmt.Errorf("%w: empty crate name", ErrInvalidCrateName)
	}
	urlStr := cratesIndexBaseURL + "/" + path

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, err
	}

	resp, err := indexHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("querying crates.io index for %s: %w", name, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: %s", ErrCrateNotFound, name)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("crates.io index returned status %d for %s", resp.StatusCode, name)
	}

	var versions []string
	reader := bufio.NewReader(resp.Body)
	for {
		line, readErr := reader.ReadBytes('\n')
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			var entry crateIndexEntry
			if jerr := json.Unmarshal(trimmed, &entry); jerr != nil {
				return nil, fmt.Errorf("parsing crates.io index entry for %s: %w", name, jerr)
			}
			if !entry.Yanked {
				versions = append(versions, entry.Version)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("reading crates.io index for %s: %w", name, readErr)
		}
	}
	return versions, nil
}

// latestCompatible returns the highest stable version in `versions` that
// satisfies the Cargo caret requirement implied by `constraint`: in the same
// compatible line as the constraint and not below it. When `constraint` is
// empty, the highest stable version overall is returned. Pre-release and
// unparseable versions are ignored. Returns "" when nothing qualifies.
func latestCompatible(versions []string, constraint string) string {
	var best string
	for _, v := range versions {
		sv := "v" + v
		if !semver.IsValid(sv) || semver.Prerelease(sv) != "" {
			continue
		}
		if constraint != "" {
			if !cargoCompatible(constraint, v) {
				continue
			}
			if semver.Compare(sv, "v"+constraint) < 0 {
				continue // compatible line, but below the requested floor
			}
		}
		if best == "" || semver.Compare(sv, "v"+best) > 0 {
			best = v
		}
	}
	return best
}

// LatestCrateVersion queries the crates.io index for the crate referenced by
// `pkg` and returns its latest published version within the SemVer constraint.
// A bare crate name ("rand") returns the highest stable version overall (e.g.
// 0.10.1); a pinned name ("rand@0.9.0") returns the highest stable version in
// that caret-compatible line (e.g. 0.9.4). Yanked and pre-release versions are
// ignored. Returns ErrNoMatchingVersion when no version qualifies.
//
// The constraint comes from the `@version` only; any `=precise` component of a
// "name@from=to" arg is ignored.
func LatestCrateVersion(ctx context.Context, pkg string) (string, error) {
	t := parseTarget(pkg)

	versions, err := fetchCrateVersions(ctx, t.name)
	if err != nil {
		return "", err
	}

	latest := latestCompatible(versions, t.version)
	if latest == "" {
		if t.hasVersion {
			return "", fmt.Errorf("%w: %s within %s", ErrNoMatchingVersion, t.name, t.version)
		}
		return "", fmt.Errorf("%w: %s has no published stable versions", ErrNoMatchingVersion, t.name)
	}
	return latest, nil
}
