/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package golang

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/chainguard-dev/clog"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
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

// findVersionWithIndirectDep searches through versions to find one that has the required indirect dependency.
func findVersionWithIndirectDep(
	ctx context.Context,
	versions []string,
	currentVersion string,
	directDep string,
	indirectPkg string,
	targetVersion string,
) (*ParentFixInfo, error) {
	log := clog.FromContext(ctx)

	// Check each version newer than current
	for _, ver := range versions {
		// Skip older or equal versions
		if semver.Compare(ver, currentVersion) <= 0 {
			continue
		}

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

// fetchFromProxy performs an HTTP GET request to the Go module proxy and returns the response body.
//
//nolint:gosec // G107: URL is constructed from validated module paths via module.EscapePath
// proxyClient is used for all Go module proxy requests with a reasonable timeout.
var proxyClient = &http.Client{Timeout: 30 * time.Second}

func fetchFromProxy(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
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

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d for %s", ErrProxyRequestFailed, resp.StatusCode, url)
	}

	return io.ReadAll(resp.Body)
}

// fetchAvailableVersions fetches the list of available versions for a module from the Go proxy.
func fetchAvailableVersions(ctx context.Context, modulePath string) ([]string, error) {
	escapedPath, err := module.EscapePath(modulePath)
	if err != nil {
		return nil, fmt.Errorf("failed to escape module path: %w", err)
	}
	url := fmt.Sprintf("https://proxy.golang.org/%s/@v/list", escapedPath)

	body, err := fetchFromProxy(ctx, url)
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
	url := fmt.Sprintf("https://proxy.golang.org/%s/@v/%s.mod", escapedPath, escapedVersion)

	body, err := fetchFromProxy(ctx, url)
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

	// Build map of current versions in the project
	currentVersions := make(map[string]string)
	for _, req := range currentModFile.Require {
		if req != nil {
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
