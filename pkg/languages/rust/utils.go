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
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
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

// ErrUnexpectedIndexStatus is returned when the crates.io index responds with
// an unexpected (non-200, non-404) HTTP status.
var ErrUnexpectedIndexStatus = errors.New("unexpected status from crates.io index")

// ErrNoCompatibleVersion is returned when a crate is pinned below the requested
// floor by a dependent's caret constraint and so cannot be upgraded to a
// compatible version. This is a genuine failure: omnibump must not silently pass.
var ErrNoCompatibleVersion = errors.New("no compatible version can be upgraded to")

// ErrUpgradeBrokeBuild is returned when a SemVer-breaking manifest edit was
// applied but `cargo check` then failed: the upgrade leaves the project
// unbuildable, so no upgrade is possible.
var ErrUpgradeBrokeBuild = errors.New("upgrade broke the build; no compatible upgrade is possible")

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

// ErrCrateNotActivated indicates that `cargo tree -i` found no reachable inverted
// tree for the crate: it is locked but not activated in the resolved graph this
// query sees (host target, normal+build edges). This happens for an optional
// dependency behind a disabled feature or a platform-gated dependency on a
// non-matching host. cargo reports it either as an empty tree ("warning: nothing to
// print.", exit 0) or as "did not match any packages" (exit non-zero). It is not a
// fatal error: the caller falls back to a direct lockfile pin.
var ErrCrateNotActivated = errors.New("crate is locked but not activated in the inverted dependency tree")

// cargoTreeInverted returns the raw `cargo tree -i` output for crate, in the
// depth-prefixed format the revdep parser expects. The inverted tree is rooted at
// crate with each child being a crate that depends on it. Edge kinds are limited
// to normal+build (dev-deps do not propagate to downstream resolution); the
// toolchain override is inherited via cargoCommand.
//
// It returns ErrCrateNotActivated when the crate is locked but absent from the tree
// this query sees, so the caller can fall back rather than abort (see the error's
// doc).
func cargoTreeInverted(ctx context.Context, cargoRoot, crate string) (string, error) {
	log := clog.FromContext(ctx)
	var stdout, stderr bytes.Buffer
	cmd := cargoCommand(ctx, cargoRoot, "tree", "-i", crate, "-e", "normal,build", "--charset", "ascii", "--prefix", "depth")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	if notActivatedInTree(err, stdout.String(), stderr.String()) {
		return "", ErrCrateNotActivated
	}
	if err != nil {
		if s := strings.TrimSpace(stderr.String()); s != "" {
			return "", fmt.Errorf("cargo tree -i %s: %w: %s", crate, err, s)
		}
		return "", fmt.Errorf("cargo tree -i %s: %w", crate, err)
	}
	// Surface any warnings cargo emitted on the success path (edition/resolver
	// notices) rather than silently discarding the buffered stderr.
	if s := strings.TrimSpace(stderr.String()); s != "" {
		log.Debugf("cargo tree -i %s: %s", crate, s)
	}
	return stdout.String(), nil
}

// notActivatedInTree reports whether a `cargo tree -i` run signals that the crate is
// locked but absent from this query's resolved view (host target, normal/build
// edges) — as opposed to a genuine failure. cargo exposes no machine-readable signal
// for this, so it is inferred from behavior/wording:
//   - non-zero exit whose stderr says the pkgid "did not match any packages" (or
//     "package ID specification ... did not match"): an optional dependency behind a
//     disabled feature;
//   - exit 0 with empty stdout ("warning: nothing to print."): reachable only through
//     edges this query filters out, e.g. a platform-gated dependency on a
//     non-matching host.
//
// This is coupled to cargo's human-readable diagnostics: if a future cargo release
// rewords them, the pinLockedDependency fallback stops firing and such bumps regress
// to a hard abort. There is no version-stable signal to key on instead.
func notActivatedInTree(runErr error, stdout, stderr string) bool {
	if runErr != nil {
		return strings.Contains(stderr, "did not match any packages") ||
			(strings.Contains(stderr, "package ID specification") && strings.Contains(stderr, "did not match"))
	}
	return strings.TrimSpace(stdout) == ""
}

// presentVersions returns the versions of crate `name` currently resolved in the
// workspace, as reported by `cargo metadata`.
func presentVersions(ctx context.Context, cargoRoot, name string) ([]string, error) {
	output := bytes.Buffer{}
	cmd := cargoCommand(ctx, cargoRoot, "metadata", "--format-version", "1")
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

// wsMember is a workspace member crate: its package name (for `cargo add
// --package`) and the path to its Cargo.toml.
type wsMember struct {
	name         string
	manifestPath string
}

// workspaceLayout reports the workspace's member crates and root directory, as
// seen by cargo. `--no-deps` restricts the package list to workspace members
// (not the full dependency graph), which is exactly the set whose manifests may
// declare a direct dependency.
func workspaceLayout(ctx context.Context, cargoRoot string) (members []wsMember, workspaceRoot string, err error) {
	output := bytes.Buffer{}
	cmd := cargoCommand(ctx, cargoRoot, "metadata", "--format-version", "1", "--no-deps")
	cmd.Stdout = &output
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, "", fmt.Errorf("cargo metadata --no-deps: %w", err)
	}

	var meta struct {
		Packages []struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			ManifestPath string `json:"manifest_path"`
		} `json:"packages"`
		WorkspaceMembers []string `json:"workspace_members"`
		WorkspaceRoot    string   `json:"workspace_root"`
	}
	if err := json.Unmarshal(output.Bytes(), &meta); err != nil {
		return nil, "", fmt.Errorf("parsing cargo metadata: %w", err)
	}

	byID := make(map[string]int, len(meta.Packages))
	for i, p := range meta.Packages {
		byID[p.ID] = i
	}
	for _, id := range meta.WorkspaceMembers {
		if i, ok := byID[id]; ok {
			members = append(members, wsMember{name: meta.Packages[i].Name, manifestPath: meta.Packages[i].ManifestPath})
		}
	}
	return members, meta.WorkspaceRoot, nil
}

// manifestSections finds every place `name` is declared as a direct dependency
// across the workspace's member manifests, and returns the workspace root path
// (where the [workspace.dependencies] table lives for inherited deps). Each
// returned section carries what `cargo add` needs to re-target the same
// declaration. An empty result means the crate is not declared in any member
// manifest (i.e. it is only pulled in transitively).
func manifestSections(ctx context.Context, cargoRoot, name string) (sections []manifestSection, workspaceRoot string, err error) {
	members, workspaceRoot, err := workspaceLayout(ctx, cargoRoot)
	if err != nil {
		return nil, "", err
	}
	for _, m := range members {
		data, readErr := os.ReadFile(filepath.Clean(m.manifestPath))
		if readErr != nil {
			return nil, "", fmt.Errorf("reading %s: %w", m.manifestPath, readErr)
		}
		found, decErr := decodeManifestSections(data, m.name, name)
		if decErr != nil {
			return nil, "", fmt.Errorf("parsing %s: %w", m.manifestPath, decErr)
		}
		sections = append(sections, found...)
	}
	return sections, workspaceRoot, nil
}

// manifestDepTables mirrors the dependency tables of a Cargo.toml. Each map value
// is either a bare version string or an inline table decoded as
// map[string]interface{}.
type manifestDepTables struct {
	Dependencies      map[string]interface{}       `toml:"dependencies"`
	DevDependencies   map[string]interface{}       `toml:"dev-dependencies"`
	BuildDependencies map[string]interface{}       `toml:"build-dependencies"`
	Target            map[string]manifestDepTables `toml:"target"`
}

// decodeManifestSections decodes a single member Cargo.toml (raw bytes) and
// returns every section in which `name` is declared, tagged with the owning
// member name. It is split from manifestSections so the TOML logic is unit
// testable without invoking cargo.
func decodeManifestSections(data []byte, member, name string) ([]manifestSection, error) {
	var tables manifestDepTables
	if err := toml.Unmarshal(data, &tables); err != nil {
		return nil, err
	}

	var sections []manifestSection
	collect := func(deps map[string]interface{}, kind, target string) {
		val, ok := deps[name]
		if !ok {
			return
		}
		inherited, registry := classifyDepValue(val)
		sections = append(sections, manifestSection{
			member:    member,
			kind:      kind,
			target:    target,
			inherited: inherited,
			registry:  registry,
		})
	}

	collect(tables.Dependencies, "", "")
	collect(tables.DevDependencies, "dev", "")
	collect(tables.BuildDependencies, "build", "")
	for cfg, t := range tables.Target {
		collect(t.Dependencies, "", cfg)
		collect(t.DevDependencies, "dev", cfg)
		collect(t.BuildDependencies, "build", cfg)
	}
	return sections, nil
}

// classifyDepValue inspects a Cargo.toml dependency value and reports whether it
// inherits from the workspace (`workspace = true`) and whether it is a registry
// dependency (as opposed to a git or path dependency, which has no registry
// version to bump). A bare string value ("1.0") is a registry dependency.
func classifyDepValue(v interface{}) (inherited, registry bool) {
	switch val := v.(type) {
	case string:
		return false, true
	case map[string]interface{}:
		if w, ok := val["workspace"].(bool); ok && w {
			return true, false
		}
		if _, ok := val["git"]; ok {
			return false, false
		}
		if _, ok := val["path"]; ok {
			return false, false
		}
		if _, ok := val["version"]; ok {
			return false, true
		}
		return false, false
	default:
		return false, false
	}
}

// workspaceDepVersionRe matches the version value inside an inline-table
// dependency declaration, e.g. `version = "0.2"`, capturing the `version = `
// prefix so only the quoted value is rewritten.
var workspaceDepVersionRe = regexp.MustCompile(`(version\s*=\s*)"[^"]*"`)

// quotedStringRe matches the first double-quoted string on a line, used to
// rewrite the plain version form (`name = "0.2"`).
var quotedStringRe = regexp.MustCompile(`"[^"]*"`)

// bumpWorkspaceDependency rewrites the version constraint of `name` in the root
// Cargo.toml's [workspace.dependencies] table to caret, editing only that one
// value in place. It handles both the plain-string form (`name = "0.2"`) and the
// inline-table form (`name = { version = "0.2", features = [...] }`), preserving
// all other content, comments, and ordering. `cargo add` cannot manage the
// [workspace.dependencies] table, so this scoped, section-anchored edit is used
// for workspace-inherited deps.
func bumpWorkspaceDependency(cargoTomlPath, name, caret string) error {
	data, err := os.ReadFile(filepath.Clean(cargoTomlPath)) //nolint:gosec // cargoTomlPath is the workspace-root Cargo.toml resolved from cargo metadata
	if err != nil {
		return fmt.Errorf("reading %s: %w", cargoTomlPath, err)
	}

	lines := strings.Split(string(data), "\n")
	inSection := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inSection = trimmed == "[workspace.dependencies]"
			continue
		}
		if !inSection {
			continue
		}
		key, ok := manifestKey(trimmed)
		if !ok || key != name {
			continue
		}
		newLine, changed := replaceManifestVersion(line, caret)
		if !changed {
			return fmt.Errorf("%w: could not locate a version to rewrite for %s in [workspace.dependencies] of %s",
				ErrNoCompatibleVersion, name, cargoTomlPath)
		}
		lines[i] = newLine
		if err := os.WriteFile(cargoTomlPath, []byte(strings.Join(lines, "\n")), 0o644); err != nil { //nolint:gosec // manifest is world-readable by convention
			return fmt.Errorf("writing %s: %w", cargoTomlPath, err)
		}
		return nil
	}
	return fmt.Errorf("%w: %s not found in [workspace.dependencies] of %s", ErrNoCompatibleVersion, name, cargoTomlPath)
}

// manifestKey returns the dependency key declared on a Cargo.toml line (the part
// before the first '=', unquoted), or ok=false for blank/comment/non-assignment
// lines. The first '=' is always the top-level assignment; any '=' inside an
// inline-table value comes later.
func manifestKey(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	eq := strings.Index(trimmed, "=")
	if eq < 0 {
		return "", false
	}
	return strings.Trim(strings.TrimSpace(trimmed[:eq]), `"'`), true
}

// replaceManifestVersion rewrites the version value in a single manifest line to
// caret, returning the new line and whether a replacement was made. For an
// inline table it rewrites the `version = "..."` field; otherwise it rewrites the
// first quoted string (the plain version form). The key prefix before the first
// '=' is left untouched.
func replaceManifestVersion(line, caret string) (string, bool) {
	eq := strings.Index(line, "=")
	if eq < 0 {
		return line, false
	}
	prefix, value := line[:eq+1], line[eq+1:]

	if strings.Contains(value, "{") {
		loc := workspaceDepVersionRe.FindStringSubmatchIndex(value)
		if loc == nil {
			return line, false
		}
		return prefix + value[:loc[0]] + value[loc[2]:loc[3]] + `"` + caret + `"` + value[loc[1]:], true
	}

	loc := quotedStringRe.FindStringIndex(value)
	if loc == nil {
		return line, false
	}
	return prefix + value[:loc[0]] + `"` + caret + `"` + value[loc[1]:], true
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

// caretConstraint reduces a concrete version to the Cargo caret token to write
// into a Cargo.toml constraint: "major.minor" for 0.x releases (0.3.4 -> "0.3")
// and "major" for >=1 releases (2.5.1 -> "2"). This mirrors cargoCompatible's
// line rules and matches idiomatic hand-written manifests, so the manifest holds
// the compatible line while Cargo.lock holds the exact pin. Build metadata is
// stripped; a version too short to reduce is returned unchanged.
//
// A pre-release version is NOT truncated: cargo only matches a pre-release when the
// requirement names the same major.minor.patch with a pre-release tag, so a
// truncated "0.10" would never match "0.10.0-rc.18". The full version is returned
// instead (a bare "0.10.0-rc.18" is read by cargo as "^0.10.0-rc.18", which matches
// and opts into the pre-release line).
func caretConstraint(version string) string {
	v := version
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i] // build metadata is not significant to matching
	}
	if strings.IndexByte(v, '-') >= 0 {
		return v // pre-release: keep the full version so the constraint opts in
	}
	parts := strings.Split(v, ".")
	if len(parts) == 0 || parts[0] == "" {
		return version
	}
	if parts[0] == "0" {
		if len(parts) >= 2 {
			return parts[0] + "." + parts[1]
		}
		return version
	}
	return parts[0]
}

// satisfiesFloor reports whether any present version is >= floor, i.e. the crate
// is already at or above the requested version and no upgrade is needed. floor may
// be a partial version (e.g. "0.9"), which compares as its zero-filled form.
func satisfiesFloor(present []string, floor string) bool {
	mx := maxVersion(present)
	if mx == "" {
		return false
	}
	return semver.Compare("v"+mx, "v"+floor) >= 0
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

// inLineSpec builds an unambiguous `name@version` cargo `--package` spec targeting
// the instance of name locked in `line`'s SemVer line. cur is
// inLineVersion(line, present); it is "" when no locked version shares that line, in
// which case the bare name is returned and the caller decides the fallback (skip,
// transitive handling, or trusting cargo to disambiguate a single instance). Callers
// use this so the spec construction — and this fallback contract — lives in one
// place.
func inLineSpec(name, line string, present []string) (spec, cur string) {
	cur = inLineVersion(line, present)
	if cur == "" {
		return name, ""
	}
	return name + "@" + cur, cur
}

// lockedVersionsOf returns the versions of crate `name` recorded in a parsed
// Cargo.lock. Reading the lock (via GetCurrentPackages) is a file parse with no cargo
// subprocess, unlike presentVersions' `cargo metadata`; it is the right source for
// post-landing "which instances are pinned" queries.
func lockedVersionsOf(pkgs []CargoPackage, name string) []string {
	var out []string
	for _, p := range pkgs {
		if p.Name == name {
			out = append(out, p.Version)
		}
	}
	return out
}

// isDowngrade reports whether target is a lower SemVer than current. Non-SemVer
// versions are never considered a downgrade (there's no ordering to compare).
func isDowngrade(current, target string) bool {
	cv, tv := "v"+current, "v"+target
	if !semver.IsValid(cv) || !semver.IsValid(tv) {
		return false
	}
	return semver.Compare(tv, cv) < 0
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

	resp, err := indexHTTPClient.Do(req) // #nosec G704 - scheme and host are the constant crates.io index base; only the sharded crate-name path is dynamic
	if err != nil {
		return nil, fmt.Errorf("querying crates.io index for %s: %w", name, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: %s", ErrCrateNotFound, name)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d for %s", ErrUnexpectedIndexStatus, resp.StatusCode, name)
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
