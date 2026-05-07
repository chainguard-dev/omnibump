/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package golang

import (
	"context"
	"errors"
	"fmt"
	"go/types"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/chainguard-dev/clog"
	"golang.org/x/exp/apidiff"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
	"golang.org/x/tools/go/packages"
)

// Sentinel errors for loadPackageTypes.
var (
	errPackageNotFound = errors.New("package not found")
	errNoTypeInfo      = errors.New("no type information available")
)

// IndirectResolution contains information about resolving an indirect dependency CVE.
type IndirectResolution struct {
	IsIndirect      bool
	DirectParents   []DirectParent
	PossibleBumps   []ParentBump // All parents that can provide the fix
	FallbackAllowed bool
}

// DirectParent represents a direct dependency that brings in an indirect one.
type DirectParent struct {
	Package         string
	CurrentVersion  string
	BringsIn        string
	BringsInVersion string
}

// ParentBump represents a recommended parent package bump that will fix the indirect CVE.
type ParentBump struct {
	Package            string
	FromVersion        string
	ToVersion          string
	WillBringIn        string
	WillBringInVersion string
	Reasoning          string
}

// ParentFixInfo contains information about a parent package version that provides the fix.
type ParentFixInfo struct {
	DirectDep         string
	CurrentVersion    string
	FixVersion        string
	IndirectPkg       string
	IndirectVersionIn string
}

// DependencyType indicates whether a dependency is direct or indirect.
type DependencyType int

const (
	// Direct indicates a direct dependency in go.mod.
	Direct DependencyType = iota
	// Indirect indicates an indirect dependency (marked with // indirect).
	Indirect
	// NotFound indicates the dependency is not in go.mod.
	NotFound
)

// ResolveIndirectDependency analyzes an indirect dependency and determines the best way to fix it.
//
// Priority:
// 1. Try to find a direct parent update that brings in the fix (PREFERRED)
// 2. Fall back to bumping indirect directly (LAST RESORT)
//
// Example:
//
//	webtransport-go@v0.9.0 is indirect (brought in by libp2p@v0.46.0)
//	To fix CVE, need webtransport-go@v0.10.0
//	Check if libp2p@v0.47.0 has webtransport-go@v0.10.0
//	If yes: Recommend bumping libp2p instead
func ResolveIndirectDependency(
	ctx context.Context,
	modRoot string,
	indirectPkg string,
	targetVersion string,
) (*IndirectResolution, error) {
	log := clog.FromContext(ctx)

	// Parse go.mod
	modFilePath := filepath.Join(modRoot, "go.mod")
	modFile, _, err := ParseGoModfile(modFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse go.mod: %w", err)
	}

	// Check if package is actually indirect
	depType := ClassifyDependency(modFile, indirectPkg)
	if depType != Indirect {
		return &IndirectResolution{IsIndirect: false}, nil
	}

	log.Info("Package is indirect - analyzing resolution options", "package", indirectPkg)

	result := &IndirectResolution{
		IsIndirect:      true,
		FallbackAllowed: false,
	}

	// Find direct parents using go mod graph
	parents, err := FindDirectParents(ctx, modRoot, indirectPkg)
	if err != nil {
		log.Warn("Could not find direct parents", "error", err)
		result.FallbackAllowed = true
		return result, nil
	}

	result.DirectParents = parents
	log.Info("Found direct parents", "count", len(parents))
	for _, p := range parents {
		log.Info("Direct parent found", "package", p.Package, "version", p.CurrentVersion)
	}

	// Check if any parent update would bring in the fix
	var possibleFixes []ParentFixInfo

	for _, parent := range parents {
		fixInfo, err := CheckIfDirectParentHasFix(ctx,
			parent.Package,
			parent.CurrentVersion,
			indirectPkg,
			targetVersion)
		if err != nil {
			log.Debug("Parent cannot provide fix", "parent", parent.Package, "error", err)
			continue
		}

		// Found a parent that can provide the fix
		log.Info("Found solution",
			"direct_dep", fixInfo.DirectDep,
			"from_version", fixInfo.CurrentVersion,
			"to_version", fixInfo.FixVersion,
			"brings_in", fixInfo.IndirectPkg,
			"brings_in_version", fixInfo.IndirectVersionIn)

		possibleFixes = append(possibleFixes, *fixInfo)
	}

	if len(possibleFixes) == 0 {
		// No parent fix found
		log.Info("No direct parent update found that provides %s@%s", indirectPkg, targetVersion)
		result.FallbackAllowed = true
		return result, nil
	}

	// Return ALL parents that can provide the fix
	log.Info("Found parents that can provide fix", "count", len(possibleFixes))
	for _, fix := range possibleFixes {
		log.Info("Parent option",
			"package", fix.DirectDep,
			"from_version", fix.CurrentVersion,
			"to_version", fix.FixVersion)

		result.PossibleBumps = append(result.PossibleBumps, ParentBump{
			Package:            fix.DirectDep,
			FromVersion:        fix.CurrentVersion,
			ToVersion:          fix.FixVersion,
			WillBringIn:        fix.IndirectPkg,
			WillBringInVersion: fix.IndirectVersionIn,
			Reasoning:          "Update direct dependency to transitively fix CVE in indirect dependency",
		})
	}

	log.Info("Caller can choose which parent(s) to bump based on their strategy")

	return result, nil
}

// FindDirectParents finds which direct dependencies bring in an indirect package.
// Uses go mod graph to trace dependency chains.
func FindDirectParents(ctx context.Context, modRoot, indirectPkg string) ([]DirectParent, error) {
	log := clog.FromContext(ctx)

	// Run go mod graph with workspace mode off to avoid scanning all workspace modules.
	cmd := exec.CommandContext(ctx, "go", "mod", "graph")
	cmd.Dir = modRoot
	cmd.Env = append(os.Environ(), "GOWORK=off")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go mod graph failed: %w", err)
	}

	// Parse go.mod to get direct dependencies
	modFilePath := filepath.Join(modRoot, "go.mod")
	modFile, _, err := ParseGoModfile(modFilePath)
	if err != nil {
		return nil, err
	}

	// Build map of direct dependencies (excluding those with replace directives)
	directDeps := make(map[string]bool)
	replacedDeps := make(map[string]bool)

	// First, track all replaced dependencies
	for _, repl := range modFile.Replace {
		if repl != nil {
			replacedDeps[repl.Old.Path] = true
		}
	}

	// Then, identify direct dependencies that are NOT replaced
	for _, req := range modFile.Require {
		if !req.Indirect && !replacedDeps[req.Mod.Path] {
			directDeps[req.Mod.Path] = true
		}
	}

	log.Debug("Found direct dependencies", "count", len(directDeps), "excluding_replaced", true)

	// Parse go mod graph output
	// Format: source@version target@version
	var parents []DirectParent
	seen := make(map[string]bool)

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}

		source := parts[0]
		target := parts[1]

		sourcePkg := extractModulePath(source)
		targetPkg := extractModulePath(target)

		// If target matches our indirect package and source is a direct dep
		if targetPkg == indirectPkg && directDeps[sourcePkg] && !seen[sourcePkg] {
			parents = append(parents, DirectParent{
				Package:         sourcePkg,
				CurrentVersion:  extractModuleVersion(source),
				BringsIn:        targetPkg,
				BringsInVersion: extractModuleVersion(target),
			})
			seen[sourcePkg] = true
		}
	}

	log.Debug("Found direct parents", "count", len(parents), "indirect_package", indirectPkg)
	return parents, nil
}

// CheckIfDirectParentHasFix checks if updating a direct parent would bring in the target version.
// It searches through newer versions of the parent to find one that has the required indirect version.
func CheckIfDirectParentHasFix(
	ctx context.Context,
	directDep string,
	currentVersion string,
	indirectPkg string,
	targetVersion string,
) (*ParentFixInfo, error) {
	log := clog.FromContext(ctx)

	// Fetch available versions for the direct dependency
	versions, err := fetchAvailableVersions(ctx, directDep)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch versions for %s: %w", directDep, err)
	}

	log.Debug("Checking versions for fix", "count", len(versions), "direct_dep", directDep)

	return findVersionWithIndirectDep(ctx, versions, currentVersion, directDep, indirectPkg, targetVersion)
}

// maxVersionsToCheck limits how many newer versions of a parent package are scanned
// when searching for one that brings in the required indirect dependency. Packages like
// github.com/elastic/beats/v7 can have thousands of pseudo-versions, and checking each
// requires an HTTP round-trip, which would cause the analysis to hang indefinitely.
const maxVersionsToCheck = 50

// findVersionWithIndirectDep searches through versions to find one that has the required indirect dependency.
// It checks at most maxVersionsToCheck versions newer than the current version.
func findVersionWithIndirectDep(
	ctx context.Context,
	versions []string,
	currentVersion string,
	directDep string,
	indirectPkg string,
	targetVersion string,
) (*ParentFixInfo, error) {
	log := clog.FromContext(ctx)

	checked := 0
	// Check each version newer than current, capped at maxVersionsToCheck.
	// Versions are sorted newest-first so we find the minimal required bump quickly.
	for _, ver := range versions {
		// Skip older or equal versions
		if semver.Compare(ver, currentVersion) <= 0 {
			continue
		}

		if checked >= maxVersionsToCheck {
			log.Debug("Reached version check limit, stopping search",
				"package", directDep,
				"limit", maxVersionsToCheck)
			break
		}
		checked++

		// Fetch this version's go.mod
		modFile, err := fetchGoModForPackage(ctx, directDep, ver)
		if err != nil {
			log.Debug("Could not fetch version", "package", directDep, "version", ver, "error", err)
			continue
		}

		// Check if this version has the target indirect dependency version
		fixInfo := checkModFileForIndirectDep(modFile, directDep, currentVersion, ver, indirectPkg, targetVersion)
		if fixInfo != nil {
			log.Info("Found fix in version",
				"direct_dep", directDep,
				"version", ver,
				"has_indirect", indirectPkg,
				"indirect_version", fixInfo.IndirectVersionIn)
			return fixInfo, nil
		}
	}

	return nil, fmt.Errorf("no version found: %w (package: %s, looking for %s@%s)",
		ErrNoParentVersionFound, directDep, indirectPkg, targetVersion)
}

// checkModFileForIndirectDep checks if a modfile has the required indirect dependency at target version.
func checkModFileForIndirectDep(
	modFile *modfile.File,
	directDep string,
	currentVersion string,
	checkVersion string,
	indirectPkg string,
	targetVersion string,
) *ParentFixInfo {
	for _, req := range modFile.Require {
		if req.Mod.Path == indirectPkg {
			// Check if version is >= target
			if semver.Compare(req.Mod.Version, targetVersion) >= 0 {
				return &ParentFixInfo{
					DirectDep:         directDep,
					CurrentVersion:    currentVersion,
					FixVersion:        checkVersion,
					IndirectPkg:       indirectPkg,
					IndirectVersionIn: req.Mod.Version,
				}
			}
		}
	}
	return nil
}

// goProxyBase is the base URL for the Go module proxy.
const goProxyBase = "https://proxy.golang.org"

// proxyHost is the hostname used for all module proxy requests.
// It is a variable rather than a constant so tests can redirect requests
// to a local httptest server.
var proxyHost = "proxy.golang.org"

// proxyClient is used for all Go module proxy requests with a reasonable timeout.
var proxyClient = &http.Client{Timeout: 30 * time.Second}

// goModCache maps (package@version) -> modfile.File to avoid redundant HTTP requests when analyzing multiple packages.
type goModCache map[string]*modfile.File

func newGoModCache() goModCache {
	return make(goModCache)
}

func (c goModCache) key(pkg, ver string) string {
	return pkg + "@" + ver
}

func (c goModCache) get(pkg, ver string) (*modfile.File, bool) {
	mf, ok := c[c.key(pkg, ver)]
	return mf, ok
}

func (c goModCache) set(pkg, ver string, mf *modfile.File) {
	c[c.key(pkg, ver)] = mf
}

// preFetchDependencies fetches all dependency go.mod files to populate the cache.
// This REDUCES the total number of HTTP calls by identifying all needed packages upfront
// and fetching them once, rather than fetching them separately for each package being updated.
// For 50 dependencies and 10 package updates: 50 calls instead of up to 500.
func preFetchDependencies(ctx context.Context, modFile *modfile.File, cache goModCache) {
	log := clog.FromContext(ctx)

	// Collect all unique (package, version) pairs from current module requirements
	toFetch := make([]struct{ pkg, ver string }, 0)
	seen := make(map[string]struct{})

	for _, req := range modFile.Require {
		if req != nil && !req.Indirect {
			key := req.Mod.Path + "@" + req.Mod.Version
			if _, alreadySeen := seen[key]; !alreadySeen && !cache.has(req.Mod.Path, req.Mod.Version) {
				seen[key] = struct{}{}
				toFetch = append(toFetch, struct{ pkg, ver string }{req.Mod.Path, req.Mod.Version})
			}
		}
	}

	if len(toFetch) == 0 {
		return
	}

	log.Debugf("Pre-fetching %d dependency go.mod files in batch", len(toFetch))

	for _, pv := range toFetch {
		mf, err := fetchGoModForPackage(ctx, pv.pkg, pv.ver)
		if err != nil {
			log.Debug("Could not pre-fetch go.mod", "package", pv.pkg, "version", pv.ver, "error", err)
			continue
		}
		cache.set(pv.pkg, pv.ver, mf)
	}

	log.Debug("Completed pre-fetching dependency go.mod files")
}

func (c goModCache) has(pkg, ver string) bool {
	_, ok := c.get(pkg, ver)
	return ok
}

// fetchFromProxy performs an HTTP GET request to the Go module proxy and returns the response body.
// path must begin with "/" and is appended to goProxyBase.
func fetchFromProxy(ctx context.Context, path string) ([]byte, error) {
	// Parse the path to validate it before use; only .Path is taken so the
	// host component of the final request always comes from the proxyHost variable.
	parsedPath, err := url.Parse(path)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy path: %w", err)
	}
	u := &url.URL{
		Scheme: "https",
		Host:   proxyHost,
		Path:   parsedPath.Path,
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := proxyClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, ErrNilHTTPResponse
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: status 404 for %s", ErrModuleVersionNotFound, goProxyBase+path)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d for %s", ErrProxyRequestFailed, resp.StatusCode, goProxyBase+path)
	}

	return io.ReadAll(resp.Body)
}

// fetchAvailableVersions fetches the list of available versions for a module from the Go proxy.
func fetchAvailableVersions(ctx context.Context, modulePath string) ([]string, error) {
	escapedPath, err := module.EscapePath(modulePath)
	if err != nil {
		return nil, fmt.Errorf("failed to escape module path: %w", err)
	}
	body, err := fetchFromProxy(ctx, fmt.Sprintf("/%s/@v/list", escapedPath))
	if err != nil {
		return nil, err
	}

	// Parse version list (one version per line)
	versionList := strings.TrimSpace(string(body))
	if versionList == "" {
		return []string{}, nil
	}

	versions := strings.Split(versionList, "\n")

	// Sort by semver (newest first)
	semver.Sort(versions)

	// Reverse to get newest first
	for i := 0; i < len(versions)/2; i++ {
		versions[i], versions[len(versions)-1-i] = versions[len(versions)-1-i], versions[i]
	}

	return versions, nil
}

// fetchGoModForPackage fetches a go.mod file from the Go module proxy.
func fetchGoModForPackage(ctx context.Context, pkgPath, version string) (*modfile.File, error) {
	escapedPath, err := module.EscapePath(pkgPath)
	if err != nil {
		return nil, fmt.Errorf("failed to escape module path: %w", err)
	}
	escapedVersion, err := module.EscapeVersion(version)
	if err != nil {
		return nil, fmt.Errorf("failed to escape version: %w", err)
	}
	body, err := fetchFromProxy(ctx, fmt.Sprintf("/%s/@v/%s.mod", escapedPath, escapedVersion))
	if err != nil {
		return nil, err
	}

	mod, err := modfile.Parse("go.mod", body, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to parse fetched go.mod: %w", err)
	}

	return mod, nil
}

// ClassifyDependency determines if a package is direct or indirect in go.mod.
func ClassifyDependency(modFile *modfile.File, packageName string) DependencyType {
	for _, req := range modFile.Require {
		if req.Mod.Path == packageName {
			if req.Indirect {
				return Indirect
			}
			return Direct
		}
	}
	return NotFound
}

// extractModulePath extracts the module path from a module@version string.
func extractModulePath(moduleWithVersion string) string {
	idx := strings.LastIndex(moduleWithVersion, "@")
	if idx == -1 {
		return moduleWithVersion
	}
	return moduleWithVersion[:idx]
}

// extractModuleVersion extracts the version from a module@version string.
func extractModuleVersion(moduleWithVersion string) string {
	idx := strings.LastIndex(moduleWithVersion, "@")
	if idx == -1 {
		return ""
	}
	return moduleWithVersion[idx+1:]
}

// MissingDependency represents a dependency that needs to be updated.
type MissingDependency struct {
	Package         string
	RequiredVersion string
	CurrentVersion  string
	Reason          string
}

// CheckTransitiveRequirements checks if updating a package to a target version
// would require updating other dependencies in the project.
// Returns a list of dependencies that would need co-updating.
func CheckTransitiveRequirements(
	ctx context.Context,
	packageName string,
	targetVersion string,
	currentModFile *modfile.File,
) ([]MissingDependency, error) {
	log := clog.FromContext(ctx)

	log.Debug("Checking transitive requirements", "package", packageName, "version", targetVersion)

	// Fetch the target version's go.mod from the proxy
	targetModFile, err := fetchGoModForPackage(ctx, packageName, targetVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch go.mod for %s@%s: %w", packageName, targetVersion, err)
	}

	// Build map of current versions for packages the project directly depends on.
	// Indirect deps are excluded: if a package is indirect in the project's go.mod,
	// the project doesn't import it directly, so API changes in that package cannot
	// break the project's own code. Go's MVS resolves the appropriate version
	// automatically from the transitive dependency graph.
	currentVersions := make(map[string]string)
	for _, req := range currentModFile.Require {
		if req != nil && !req.Indirect {
			currentVersions[req.Mod.Path] = req.Mod.Version
		}
	}

	// Check each requirement of the target version.
	// Only consider direct requirements (non-indirect) from the target's go.mod —
	// indirect ones are resolved automatically by MVS when go get or go mod tidy runs.
	var missing []MissingDependency
	for _, req := range targetModFile.Require {
		if req == nil || req.Indirect {
			continue
		}

		reqPkg := req.Mod.Path
		reqVer := req.Mod.Version

		currentVer, exists := currentVersions[reqPkg]

		// If package doesn't exist in current project, skip (go get will add it)
		if !exists {
			continue
		}

		// Compare versions
		if semver.IsValid(currentVer) && semver.IsValid(reqVer) {
			if semver.Compare(currentVer, reqVer) < 0 {
				// Current version is older than required
				missing = append(missing, MissingDependency{
					Package:         reqPkg,
					RequiredVersion: reqVer,
					CurrentVersion:  currentVer,
					Reason:          fmt.Sprintf("%s@%s requires %s@%s but project has %s", packageName, targetVersion, reqPkg, reqVer, currentVer),
				})
				log.Warn("Dependency requires newer version",
					"updating", packageName,
					"requires", reqPkg,
					"required_version", reqVer,
					"current_version", currentVer)
			}
		}
	}

	if len(missing) > 0 {
		log.Info("Found missing co-updates", "count", len(missing))
	}

	return missing, nil
}

// moduleFamilyPrefix returns the module family prefix used to identify tightly-coupled
// module ecosystems. For vanity domains (e.g., go.opentelemetry.io/otel/sdk), the family
// is domain/project (go.opentelemetry.io/otel), grouping all otel sub-modules together.
// For standard code-hosting domains (github.com, gitlab.com, etc.), the family is the
// full three-part path (github.com/org/repo), since different repos are unrelated projects.
func moduleFamilyPrefix(pkg string) string {
	parts := strings.SplitN(pkg, "/", 4)
	if len(parts) < 2 {
		return pkg
	}
	domain := parts[0]
	switch domain {
	case "github.com", "gitlab.com", "bitbucket.org", "codeberg.org",
		// golang.org and gopkg.in host many independent projects under a shared
		// path prefix (e.g. golang.org/x/net vs golang.org/x/oauth2 release
		// independently). Treat them like hosting domains so each project gets
		// its own family prefix rather than being grouped under domain/x.
		"golang.org", "gopkg.in":
		// Family is domain/org/repo (or domain/prefix/project for vanity domains).
		if len(parts) >= 3 {
			return parts[0] + "/" + parts[1] + "/" + parts[2]
		}
	}
	// Vanity domain: family is domain/project (first path component after domain).
	return parts[0] + "/" + parts[1]
}

// FindVersionGroupPackages returns all packages in the project's go.mod that belong to the
// same module family as packageName and are at or below currentVersion. This covers both
// the common case (all packages co-released at the same version) and the drift case (a
// package in the same ecosystem that was left behind at an older version in a prior update).
//
// Module family is determined by moduleFamilyPrefix: for example, all
// go.opentelemetry.io/otel/* packages share the family go.opentelemetry.io/otel and must
// move together to preserve internal API compatibility.
func FindVersionGroupPackages(packageName, currentVersion string, modFile *modfile.File) []string {
	if !semver.IsValid(currentVersion) {
		return nil
	}
	family := moduleFamilyPrefix(packageName)
	group := make([]string, 0, len(modFile.Require))
	for _, req := range modFile.Require {
		if req == nil || req.Mod.Path == packageName {
			continue
		}
		// Only include packages in the same module family.
		if req.Mod.Path != family && !strings.HasPrefix(req.Mod.Path, family+"/") {
			continue
		}
		// Include packages at or below the current version: same release or drifted behind.
		if semver.IsValid(req.Mod.Version) && semver.Compare(req.Mod.Version, currentVersion) <= 0 {
			group = append(group, req.Mod.Path)
		}
	}
	return group
}

// FindMinCompatibleVersion returns the lowest version of depPkg (above depVer) whose go.mod
// requires importedPkg at >= minVersion. This identifies the minimum version of a package
// that is compatible with a dependency upgrade — e.g., the lowest go-ldap that requires
// go-ntlmssp@v0.1.1 after ntlmssp's ProcessChallenge signature changed.
//
// Returns empty string if no compatible version is found within the version limit.
func FindMinCompatibleVersion(ctx context.Context, depPkg, depVer, importedPkg, minVersion string, cache goModCache) string {
	versions, err := fetchAvailableVersions(ctx, depPkg)
	if err != nil {
		return ""
	}

	// Collect versions strictly above the current one, then sort ascending
	// so we find the minimum compatible version rather than the latest.
	candidates := make([]string, 0, len(versions))
	for _, v := range versions {
		if semver.IsValid(v) && semver.Compare(v, depVer) > 0 {
			candidates = append(candidates, v)
		}
	}
	semver.Sort(candidates)

	// Cap to avoid excessive HTTP calls for packages with many releases.
	const maxCandidates = 30
	if len(candidates) > maxCandidates {
		candidates = candidates[:maxCandidates]
	}

	for _, v := range candidates {
		var mod *modfile.File
		if cached, ok := cache.get(depPkg, v); ok {
			mod = cached
		} else {
			mod, err = fetchGoModForPackage(ctx, depPkg, v)
			if err != nil {
				continue
			}
			cache.set(depPkg, v, mod)
		}
		for _, req := range mod.Require {
			if req == nil || req.Indirect || req.Mod.Path != importedPkg {
				continue
			}
			if semver.IsValid(req.Mod.Version) && semver.Compare(req.Mod.Version, minVersion) >= 0 {
				return v
			}
		}
	}
	return ""
}

// CheckAPIBreakingChanges compares the exported API of packageName between oldVersion and
// newVersion using apidiff. Returns the list of incompatible (breaking) changes, or nil if
// the APIs are compatible or the comparison cannot be completed.
//
// Use this to distinguish genuine API incompatibilities (e.g. a changed function signature)
// from false-positive compat alerts where the dependency simply added new symbols.
func CheckAPIBreakingChanges(ctx context.Context, packageName, oldVersion, newVersion string) ([]string, error) {
	log := clog.FromContext(ctx)

	oldTypes, err := loadPackageTypes(ctx, packageName, oldVersion)
	if err != nil {
		return nil, fmt.Errorf("loading %s@%s: %w", packageName, oldVersion, err)
	}

	newTypes, err := loadPackageTypes(ctx, packageName, newVersion)
	if err != nil {
		// The new version failed to load. This means either:
		// - the package was removed from the module (e.g. loki/v3/pkg/storage/wal)
		// - the new version is internally broken (e.g. references an undefined symbol)
		// Both are breaking changes from the consumer's perspective.
		log.Warnf("Package %s unavailable in %s (removed or internally broken): %v", packageName, newVersion, err)
		return []string{fmt.Sprintf("package %s is unavailable in %s — it may have been removed or contains internal errors", packageName, newVersion)}, nil
	}

	report := apidiff.Changes(oldTypes, newTypes)

	var breaking []string
	for _, change := range report.Changes {
		if !change.Compatible {
			log.Infof("Breaking change in %s %s→%s: %s", packageName, oldVersion, newVersion, change.Message)
			breaking = append(breaking, change.Message)
		}
	}
	return breaking, nil
}

// loadPackageTypes creates a temporary module, resolves packageName@version, and returns
// the type-checked *types.Package for use with apidiff.
func loadPackageTypes(ctx context.Context, packageName, version string) (*types.Package, error) {
	tmpDir, err := os.MkdirTemp("", "omnibump-apidiff-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	goModContent := fmt.Sprintf("module apidiff_temp\n\ngo 1.21\n\nrequire %s %s\n", packageName, version)
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goModContent), 0o600); err != nil {
		return nil, err
	}
	// A valid .go file is required for packages.Load to initialise the module.
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("package main\n"), 0o600); err != nil {
		return nil, err
	}

	cfg := &packages.Config{
		Mode:    packages.NeedTypes | packages.NeedSyntax | packages.NeedImports | packages.NeedDeps,
		Dir:     tmpDir,
		Context: ctx,
		// GONOSUMCHECK=* disables checksum verification because this is a throwaway
		// temp module used only for type analysis, not a production build artifact.
		Env: append(os.Environ(), "GOFLAGS=-mod=mod", "GONOSUMCHECK=*"),
	}

	pkgs, err := packages.Load(cfg, packageName)
	if err != nil {
		return nil, err
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("%s@%s: %w", packageName, version, errPackageNotFound)
	}
	if pkgs[0].Types == nil {
		if len(pkgs[0].Errors) > 0 {
			return nil, fmt.Errorf("loading package: %w", pkgs[0].Errors[0])
		}
		return nil, fmt.Errorf("%s@%s: %w", packageName, version, errNoTypeInfo)
	}
	return pkgs[0].Types, nil
}

// CheckAPICompatibilityWithCache checks API compatibility using a shared cache to reduce HTTP requests.
// The cache improves performance when analyzing multiple packages by avoiding redundant go.mod fetches.
func CheckAPICompatibilityWithCache(
	ctx context.Context,
	packageName string,
	targetVersion string,
	currentModFile *modfile.File,
	cache goModCache,
) ([]MissingDependency, error) {
	log := clog.FromContext(ctx)

	log.Debug("Checking API compatibility", "package", packageName, "version", targetVersion)

	var potentialIssues []MissingDependency

	// Check all direct dependencies in the current project
	for _, req := range currentModFile.Require {
		if req == nil || req.Indirect {
			continue
		}

		depPkg := req.Mod.Path
		depVer := req.Mod.Version

		// Skip checking the package against itself
		if depPkg == packageName {
			continue
		}

		// Fetch this dependency's go.mod (from cache if available)
		var depModFile *modfile.File
		if cached, ok := cache.get(depPkg, depVer); ok {
			depModFile = cached
		} else {
			var err error
			depModFile, err = fetchGoModForPackage(ctx, depPkg, depVer)
			if err != nil {
				log.Debug("Could not fetch dependency go.mod",
					"package", depPkg,
					"version", depVer,
					"error", err)
				continue
			}
			// Cache the result for subsequent package checks
			cache.set(depPkg, depVer, depModFile)
		}

		// Check if this dependency imports the package being updated
		for _, depReq := range depModFile.Require {
			if depReq != nil && depReq.Mod.Path == packageName {
				// This dependency imports the package being updated.
				// Try to find the minimum version of depPkg that is compatible with
				// the new targetVersion, so we can recommend a concrete upgrade path.
				recommendedVer := FindMinCompatibleVersion(ctx, depPkg, depVer, packageName, targetVersion, cache)
				if recommendedVer == "" {
					// No compatible version found within the search limit; keep current.
					recommendedVer = depVer
				}
				potentialIssues = append(potentialIssues, MissingDependency{
					Package:         depPkg,
					RequiredVersion: recommendedVer,
					CurrentVersion:  depVer,
					Reason:          fmt.Sprintf("%s imports %s which is being updated to %s (potential API/schema incompatibility — may need manual verification and version bump)", depPkg, packageName, targetVersion),
				})
				log.Info("Potential API compatibility issue detected",
					"package", depPkg,
					"imports", packageName,
					"new_version", targetVersion)
				break
			}
		}
	}

	return potentialIssues, nil
}

// CheckAPICompatibility checks if updating a package might require co-updates to other
// packages due to schema/API breaking changes. This is a heuristic approach: for packages
// that depend on the updated package, we flag them as potentially needing updates.
//
// Example: If opentelemetry/otel/sdk is updated with schema changes, and knative.dev/pkg
// imports from it, knative.dev/pkg might need updating even if the go.mod doesn't explicitly
// require a newer version.
//
// Deprecated: Use CheckAPICompatibilityWithCache to leverage caching for better performance
// when analyzing multiple packages.
func CheckAPICompatibility(
	ctx context.Context,
	packageName string,
	targetVersion string,
	currentModFile *modfile.File,
) ([]MissingDependency, error) {
	log := clog.FromContext(ctx)

	log.Debug("Checking API compatibility", "package", packageName, "version", targetVersion)

	var potentialIssues []MissingDependency

	// Check all direct dependencies in the current project
	for _, req := range currentModFile.Require {
		if req == nil || req.Indirect {
			continue
		}

		depPkg := req.Mod.Path
		depVer := req.Mod.Version

		// Skip checking the package against itself
		if depPkg == packageName {
			continue
		}

		// Fetch this dependency's go.mod and check if it imports the package being updated
		depModFile, err := fetchGoModForPackage(ctx, depPkg, depVer)
		if err != nil {
			log.Debug("Could not fetch dependency go.mod",
				"package", depPkg,
				"version", depVer,
				"error", err)
			continue
		}

		// Check if this dependency imports the package being updated
		for _, depReq := range depModFile.Require {
			if depReq != nil && depReq.Mod.Path == packageName {
				// This dependency imports the package being updated.
				// Flag it as potentially needing an update due to API/schema changes.
				potentialIssues = append(potentialIssues, MissingDependency{
					Package:         depPkg,
					RequiredVersion: depVer, // Keep current version as suggestion; user should verify
					CurrentVersion:  depVer,
					Reason:          fmt.Sprintf("%s imports %s which is being updated to %s (potential API/schema incompatibility — may need manual verification and version bump)", depPkg, packageName, targetVersion),
				})
				log.Info("Potential API compatibility issue detected",
					"package", depPkg,
					"imports", packageName,
					"new_version", targetVersion)
				break
			}
		}
	}

	return potentialIssues, nil
}
