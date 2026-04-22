/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package golang

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chainguard-dev/clog"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

const (
	// MaxGoModSize limits go.mod file size to prevent resource exhaustion.
	MaxGoModSize = 10 * 1024 * 1024 // 10 MB
)

var (
	// ErrPackageDowngrade is returned when trying to downgrade a package version.
	ErrPackageDowngrade = errors.New("package downgrade not allowed")

	// ErrGoModTooLarge is returned when a go.mod file exceeds size limits.
	ErrGoModTooLarge = errors.New("go.mod file too large")

	// ErrPackageNotFound is returned when a package is not found in go.mod.
	ErrPackageNotFound = errors.New("package not found in go.mod")

	// ErrMainModuleBump is returned when trying to bump the main module.
	ErrMainModuleBump = errors.New("bumping the main module is not allowed")

	// ErrUnexpectedGoVersion is returned when go version output has unexpected format.
	ErrUnexpectedGoVersion = errors.New("unexpected format of go version output")

	// ErrNoParentVersionFound is returned when no version of a parent package brings in the target fix.
	ErrNoParentVersionFound = errors.New("no parent version found with fix")

	// ErrProxyRequestFailed is returned when the Go proxy request fails.
	ErrProxyRequestFailed = errors.New("proxy request failed")

	// ErrNilHTTPResponse is returned when HTTP client returns nil response.
	ErrNilHTTPResponse = errors.New("http request returned nil response")
)

// pkgVersion holds version information for validation.
type pkgVersion struct {
	ReqVersion, AvailableVersion string
}

// ParseGoModfile parses a go.mod file from the specified path.
// Ported from gobump/pkg/update/update.go.
func ParseGoModfile(path string) (*modfile.File, []byte, error) {
	path = filepath.Clean(path)

	// Check file size before reading to prevent resource exhaustion.
	fileInfo, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	if fileInfo.Size() > MaxGoModSize {
		return nil, nil, fmt.Errorf("%w: %d bytes (max: %d)", ErrGoModTooLarge, fileInfo.Size(), MaxGoModSize)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return nil, content, err
	}
	mod, err := modfile.Parse("go.mod", content, nil)
	if err != nil {
		return nil, content, err
	}

	return mod, content, nil
}

// ParseGoModfileFromContent parses a go.mod file from byte content.
// This is useful for analyzing go.mod files fetched remotely (e.g., via GitHub API)
// without requiring a local filesystem.
func ParseGoModfileFromContent(filename string, content []byte) (*modfile.File, error) {
	mod, err := modfile.Parse(filename, content, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to parse go.mod from content: %w", err)
	}
	return mod, nil
}

// DoUpdate performs the actual update of Go module dependencies.
// Ported from gobump/pkg/update/update.go:DoUpdate.
func DoUpdate(ctx context.Context, pkgVersions map[string]*Package, cfg *UpdateConfig) (*modfile.File, error) {
	log := clog.FromContext(ctx)

	normalizeIncompatibleVersions(pkgVersions)

	var err error
	goVersion := cfg.GoVersion
	if goVersion == "" {
		if goVersion, err = getGoVersionFromEnvironment(); err != nil {
			return nil, fmt.Errorf("failed to get the Go version from the local system: %w", err)
		}
	}

	// Normalize go.mod to environment version FIRST
	modpath := filepath.Join(cfg.Modroot, "go.mod")
	if err := normalizeGoModVersion(ctx, modpath, goVersion); err != nil {
		return nil, fmt.Errorf("failed to normalize go.mod version: %w", err)
	}

	// Update go.work version before ANY go commands to avoid version mismatch errors
	if err := UpdateGoWorkVersion(ctx, cfg.Modroot, cfg.ForceWork, goVersion); err != nil {
		return nil, fmt.Errorf("failed to update go.work version: %w", err)
	}

	// Run go mod tidy before
	if cfg.Tidy && !cfg.SkipInitialTidy {
		output, err := GoModTidy(ctx, cfg.Modroot, goVersion, cfg.TidyCompat)
		if err != nil {
			return nil, fmt.Errorf("failed to run 'go mod tidy': %w with output: %v", err, output)
		}
	}

	// Read the entire go.mod one more time into memory and check that all the version constraints are met
	modFile, content, err := ParseGoModfile(modpath)
	if err != nil {
		return nil, fmt.Errorf("unable to parse the go mod file with error: %w", err)
	}

	// Detect require/replace modules and validate the version values
	err = CheckPackageValues(ctx, pkgVersions, modFile)
	if err != nil {
		return nil, err
	}

	depsBumpOrdered := orderPkgVersionsMap(pkgVersions)

	// Replace the packages first
	hasReplaceUpdates := false
	for _, k := range depsBumpOrdered {
		pkg := pkgVersions[k]
		if pkg == nil {
			continue
		}
		if pkg.Replace {
			log.Infof("Updating %s to %s (replace)", k, pkg.Version)
			if output, err := GoModEditReplaceModule(ctx, pkg.OldName, pkg.Name, pkg.Version, cfg.Modroot); err != nil {
				return nil, fmt.Errorf("failed to run 'go mod edit -replace': %w for package %s/%s@%s with output: %v", err, pkg.OldName, pkg.Name, pkg.Version, output)
			}
			hasReplaceUpdates = true
		}
	}

	// Re-parse go.mod after replace directives have been written to disk.
	// GoModEditReplaceModule writes directly to disk via go mod edit, so the
	// in-memory modFile must be refreshed before AddRequire edits are applied.
	// Without this, writing the in-memory modFile back to disk would overwrite
	// the replace directives.
	if hasReplaceUpdates {
		modFile, _, err = ParseGoModfile(modpath)
		if err != nil {
			return nil, fmt.Errorf("unable to parse go.mod after replace updates: %w", err)
		}
	}

	// First pass: run go get for new dependencies and non-semver versions.
	// go get writes directly to disk, so it must complete before any in-memory
	// AddRequire edits are applied to avoid overwriting its changes.
	hasGoGetUpdates, err := performGoGetPass(ctx, log, cfg.Modroot, depsBumpOrdered, pkgVersions)
	if err != nil {
		return nil, err
	}

	// Re-parse go.mod after go get writes so AddRequire edits are based on the
	// updated file, not the pre-go-get snapshot.
	if hasGoGetUpdates {
		modFile, _, err = ParseGoModfile(modpath)
		if err != nil {
			return nil, fmt.Errorf("unable to parse go.mod after go get updates: %w", err)
		}
	}

	// Second pass: apply in-memory AddRequire edits for existing semver deps.
	hasDirectEdits, err := performAddRequirePass(log, depsBumpOrdered, pkgVersions, modFile)
	if err != nil {
		return nil, err
	}

	// Write the updated go.mod file back to disk (only if we used AddRequire)
	if hasDirectEdits {
		newContent, err := modFile.Format()
		if err != nil {
			return nil, fmt.Errorf("failed to format go.mod: %w", err)
		}
		if err := os.WriteFile(modpath, newContent, 0o600); err != nil {
			return nil, fmt.Errorf("failed to write go.mod: %w", err)
		}
		log.Debugf("Updated go.mod file with new versions")
	}

	// Run go mod tidy
	if cfg.Tidy {
		output, err := GoModTidy(ctx, cfg.Modroot, goVersion, cfg.TidyCompat)
		if err != nil {
			return nil, fmt.Errorf("failed to run 'go mod tidy': %w with output: %v", err, output)
		}
	}

	// Verify updates and handle post-update tasks
	newModFile, err := verifyAndFinalize(ctx, modpath, pkgVersions, content, cfg)
	if err != nil {
		return nil, err
	}

	return newModFile, nil
}

// CheckPackageValues validates that package versions to be updated are valid
// Checks for main module bumps and downgrades in both replace and require directives.
func CheckPackageValues(ctx context.Context, pkgVersions map[string]*Package, modFile *modfile.File) error {
	log := clog.FromContext(ctx)

	if _, ok := pkgVersions[modFile.Module.Mod.Path]; ok {
		return fmt.Errorf("%w: %q", ErrMainModuleBump, modFile.Module.Mod.Path)
	}

	warnPkgVer := make(map[string]pkgVersion)
	// Track which packages have replace directives (replace takes precedence over require in Go)
	replacedPackages := make(map[string]bool)

	// Detect if the list of packages contain any replace statement for the package
	for _, replace := range modFile.Replace {
		if replace == nil {
			continue
		}
		processReplaceDirective(log, replace, pkgVersions, replacedPackages, warnPkgVer)
	}

	// Detect if the list of packages contain any require statement for the package
	// Skip packages that have replace directives (replace takes precedence in Go)
	for _, require := range modFile.Require {
		if require == nil {
			continue
		}
		processRequireDirective(log, require, pkgVersions, replacedPackages, warnPkgVer)
	}

	for pkg, ver := range warnPkgVer {
		clog.WarnContextf(ctx, "Package %s: requested version %q is older than current version %q, skipping", pkg, ver.ReqVersion, ver.AvailableVersion)
		delete(pkgVersions, pkg)
	}

	return nil
}

// processReplaceDirective processes a single replace directive for package validation.
func processReplaceDirective(log *clog.Logger, replace *modfile.Replace, pkgVersions map[string]*Package, replacedPackages map[string]bool, warnPkgVer map[string]pkgVersion) {
	pkg, ok := pkgVersions[replace.New.Path]
	if !ok {
		return
	}

	pkg.Replace = true
	if pkg.OldName == "" {
		pkg.OldName = replace.Old.Path
	}
	// Mark that this package (Old.Path) has a replace directive
	replacedPackages[replace.Old.Path] = true

	if !semver.IsValid(pkg.Version) {
		log.Warnf("Requesting pin to %s. This is not a valid SemVer, so skipping version check.", pkg.Version)
		return
	}

	if pkg.Force {
		return
	}

	if semver.Compare(replace.New.Version, pkg.Version) > 0 {
		warnPkgVer[replace.New.Path] = pkgVersion{
			ReqVersion:       pkg.Version,
			AvailableVersion: replace.New.Version,
		}
	}
}

// processRequireDirective processes a single require directive for package validation.
func processRequireDirective(log *clog.Logger, require *modfile.Require, pkgVersions map[string]*Package, replacedPackages map[string]bool, warnPkgVer map[string]pkgVersion) {
	pkg, ok := pkgVersions[require.Mod.Path]
	if !ok {
		return
	}

	// Skip if this package has a replace directive (replace takes precedence)
	if replacedPackages[require.Mod.Path] {
		return
	}

	pkg.Require = true

	if !semver.IsValid(pkg.Version) {
		log.Warnf("Requesting pin to %s. This is not a valid SemVer, so skipping version check.", pkg.Version)
		return
	}

	if pkg.Force {
		return
	}

	if semver.Compare(require.Mod.Version, pkg.Version) <= 0 {
		return
	}

	// Track the highest known current version for this package across multiple require entries
	if existingPkg, exists := warnPkgVer[require.Mod.Path]; exists {
		if semver.Compare(require.Mod.Version, existingPkg.AvailableVersion) > 0 {
			warnPkgVer[require.Mod.Path] = pkgVersion{
				ReqVersion:       pkg.Version,
				AvailableVersion: require.Mod.Version,
			}
		}
	} else {
		warnPkgVer[require.Mod.Path] = pkgVersion{
			ReqVersion:       pkg.Version,
			AvailableVersion: require.Mod.Version,
		}
	}
}

// goGetPackage runs go get for a package that is either a new dependency or uses a non-semver version.
func goGetPackage(ctx context.Context, pkg *Package, modroot string) error {
	if output, err := GoGetModule(ctx, pkg.Name, pkg.Version, modroot); err != nil {
		return fmt.Errorf("failed to run 'go get': %w with output: %v", err, output)
	}
	return nil
}

// addRequirePackage updates an existing require directive in-memory via AddRequire.
func addRequirePackage(pkg *Package, modFile *modfile.File) error {
	if err := modFile.AddRequire(pkg.Name, pkg.Version); err != nil {
		return fmt.Errorf("failed to update require for %s@%s: %w", pkg.Name, pkg.Version, err)
	}
	return nil
}

// performGoGetPass runs go get for packages that need it (new deps or non-semver versions).
func performGoGetPass(ctx context.Context, log *clog.Logger, modroot string, depsBumpOrdered []string, pkgVersions map[string]*Package) (bool, error) {
	hasGoGetUpdates := false
	for _, k := range depsBumpOrdered {
		pkg := pkgVersions[k]
		if pkg == nil || pkg.Replace {
			continue
		}
		if pkg.Require && semver.IsValid(pkg.Version) {
			// Handled in the AddRequire pass below.
			continue
		}
		log.Infof("Updating %s to %s", k, pkg.Version)
		if err := goGetPackage(ctx, pkg, modroot); err != nil {
			return false, err
		}
		hasGoGetUpdates = true
	}
	return hasGoGetUpdates, nil
}

// performAddRequirePass applies in-memory AddRequire edits for existing semver deps.
func performAddRequirePass(log *clog.Logger, depsBumpOrdered []string, pkgVersions map[string]*Package, modFile *modfile.File) (bool, error) {
	hasDirectEdits := false
	for _, k := range depsBumpOrdered {
		pkg := pkgVersions[k]
		if pkg == nil || pkg.Replace {
			continue
		}
		if !pkg.Require || !semver.IsValid(pkg.Version) {
			continue
		}
		log.Infof("Updating %s to %s", k, pkg.Version)
		if err := addRequirePackage(pkg, modFile); err != nil {
			return false, err
		}
		hasDirectEdits = true
	}
	return hasDirectEdits, nil
}

// verifyAndFinalize verifies package versions and handles final tasks.
func verifyAndFinalize(ctx context.Context, modpath string, pkgVersions map[string]*Package, originalContent []byte, cfg *UpdateConfig) (*modfile.File, error) {
	log := clog.FromContext(ctx)

	// Read the entire go.mod one more time into memory and check that all the version constraints are met
	newModFile, newContent, err := ParseGoModfile(modpath)
	if err != nil {
		return nil, fmt.Errorf("unable to parse the go mod file with error: %w", err)
	}

	for _, pkg := range pkgVersions {
		verStr := getVersion(newModFile, pkg.Name)
		if verStr != "" && semver.Compare(verStr, pkg.Version) < 0 {
			return nil, fmt.Errorf("%w: package %s with %s is less than the desired version %s", ErrPackageDowngrade, pkg.Name, verStr, pkg.Version)
		}
		if verStr == "" {
			if cfg.Tidy {
				// go mod tidy may remove a package when a dependency migrates to a
				// newer major version path (e.g. containerd/v2 replaces containerd).
				// The package is already covered by the updated dependency, so skip it.
				log.Warnf("Package %s not found in go.mod after tidy; it may have been superseded by a major version upgrade from another updated dependency. Skipping.", pkg.Name)
				continue
			}
			return nil, fmt.Errorf("%w: package %s. Please remove the package or add it to the list of 'replaces'", ErrPackageNotFound, pkg.Name)
		}
	}

	if cfg.ShowDiff {
		if diff := cmp.Diff(string(originalContent), string(newContent)); diff != "" {
			log.Info(diff)
		}
	}

	if _, err := os.Stat(filepath.Join(cfg.Modroot, "vendor")); err == nil {
		// Before running go vendor, ensure go.sum is up-to-date
		// When AddRequire is used, go.sum is not updated, so we need to run tidy
		log.Infof("Vendor directory detected, running go mod tidy to update go.sum")
		var goVersion string
		if goVersion, err = getGoVersionFromEnvironment(); err != nil {
			return nil, fmt.Errorf("failed to get the Go version from the local system: %w", err)
		}
		if output, err := GoModTidy(ctx, cfg.Modroot, goVersion, cfg.TidyCompat); err != nil {
			return nil, fmt.Errorf("failed to run 'go mod tidy' before vendoring: %w with output: %v", err, output)
		}

		output, err := GoVendor(ctx, cfg.Modroot, cfg.ForceWork)
		if err != nil {
			return nil, fmt.Errorf("failed to run 'go vendor': %w with output: %v", err, output)
		}
	}

	return newModFile, nil
}

// normalizeIncompatibleVersions ensures all package versions have the +incompatible
// suffix where required, so all code paths through DoUpdate receive correctly-suffixed
// versions regardless of how DoUpdate was called.
func normalizeIncompatibleVersions(pkgVersions map[string]*Package) {
	for _, pkg := range pkgVersions {
		pkg.Version = appendIncompatibleIfNeeded(pkg.Name, pkg.Version)
	}
}

func orderPkgVersionsMap(pkgVersions map[string]*Package) []string {
	depsBumpOrdered := make([]string, 0, len(pkgVersions))
	for repo := range pkgVersions {
		depsBumpOrdered = append(depsBumpOrdered, repo)
	}
	sort.SliceStable(depsBumpOrdered, func(i, j int) bool {
		return pkgVersions[depsBumpOrdered[i]].Index < pkgVersions[depsBumpOrdered[j]].Index
	})
	return depsBumpOrdered
}

func getVersion(modFile *modfile.File, packageName string) string {
	// Replace checks have to come first!
	for _, replace := range modFile.Replace {
		if replace.New.Path == packageName {
			return replace.New.Version
		}
	}

	for _, req := range modFile.Require {
		if req.Mod.Path == packageName {
			return req.Mod.Version
		}
	}

	return ""
}

// getGoVersionFromEnvironment returns the Go version from the local environment by running `go version`.
// This gets the actual Go toolchain version available in the environment, not the version omnibump was built with.
func getGoVersionFromEnvironment() (string, error) {
	cmd := exec.CommandContext(context.Background(), "go", "version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to run 'go version': %w, output: %s", err, strings.TrimSpace(string(output)))
	}
	return parseGoVersionString(strings.TrimSpace(string(output)))
}

// parseGoVersionString parses the output of `go version` command and extracts the Go version.
func parseGoVersionString(versionOutput string) (string, error) {
	parts := strings.Fields(versionOutput)
	if len(parts) < 3 || !strings.HasPrefix(parts[2], "go") {
		return "", ErrUnexpectedGoVersion
	}

	goVersion := strings.TrimPrefix(parts[2], "go")
	return goVersion, nil
}

// shouldDowngradeGoVersion checks if currentVersion should be downgraded to envGoVersion.
func shouldDowngradeGoVersion(currentVersion, envGoVersion string) bool {
	if currentVersion == envGoVersion {
		return false
	}
	if !semver.IsValid("v"+currentVersion) || !semver.IsValid("v"+envGoVersion) {
		return false
	}
	return semver.Compare("v"+currentVersion, "v"+envGoVersion) > 0
}

// normalizeGoModVersion normalizes a go.mod file to match the environment's Go version.
// This downgrades the go directive if needed and removes any toolchain directive.
func normalizeGoModVersion(ctx context.Context, goModPath, envGoVersion string) error {
	log := clog.FromContext(ctx)

	modFile, _, err := ParseGoModfile(goModPath)
	if err != nil {
		return fmt.Errorf("failed to parse go.mod: %w", err)
	}

	modified := false

	currentVersion := ""
	if modFile.Go != nil {
		currentVersion = modFile.Go.Version
	}

	// Downgrade go directive if it's higher than environment version
	if currentVersion == "" {
		log.Infof("Setting go.mod go directive to %s (environment version)", envGoVersion)
		if err := modFile.AddGoStmt(envGoVersion); err != nil {
			return fmt.Errorf("failed to add go directive: %w", err)
		}
		modified = true
	} else if shouldDowngradeGoVersion(currentVersion, envGoVersion) {
		log.Infof("Downgrading go.mod go directive from %s to %s (environment version)", currentVersion, envGoVersion)
		if err := modFile.AddGoStmt(envGoVersion); err != nil {
			return fmt.Errorf("failed to update go directive: %w", err)
		}
		modified = true
	}

	// Remove toolchain directive if present
	if modFile.Toolchain != nil {
		log.Infof("Removing toolchain directive (%s) from go.mod", modFile.Toolchain.Name)
		modFile.DropToolchainStmt()
		modified = true
	}

	if modified {
		newContent, err := modFile.Format()
		if err != nil {
			return fmt.Errorf("failed to format go.mod: %w", err)
		}

		if err := os.WriteFile(goModPath, newContent, 0o600); err != nil {
			return fmt.Errorf("failed to write go.mod: %w", err)
		}

		log.Debugf("Normalized %s to match environment", goModPath)
	}

	return nil
}
