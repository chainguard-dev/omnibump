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

	// ErrValidationFailed is returned when package validation fails.
	ErrValidationFailed = errors.New("validation failed")

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
	for _, k := range depsBumpOrdered {
		pkg := pkgVersions[k]
		if pkg == nil {
			continue
		}
		if pkg.Replace {
			log.Infof("Update package: %s", k)
			log.Infof("Running go mod edit replace ...")
			if output, err := GoModEditReplaceModule(ctx, pkg.OldName, pkg.Name, pkg.Version, cfg.Modroot); err != nil {
				return nil, fmt.Errorf("failed to run 'go mod edit -replace': %w for package %s/%s@%s with output: %v", err, pkg.OldName, pkg.Name, pkg.Version, output)
			}
		}
	}

	// Bump the require or new get packages in the specified order
	for _, k := range depsBumpOrdered {
		pkg := pkgVersions[k]
		if pkg == nil {
			continue
		}
		// Skip the replace that have been updated above
		if pkg.Replace {
			continue
		}

		log.Infof("Update package: %s", k)
		if err := updateRequirePackage(ctx, log, pkg, modFile, cfg.Modroot); err != nil {
			return nil, err
		}
	}

	// Write the updated go.mod file back to disk (only if we used AddRequire)
	hasDirectEdits := false
	for _, pkg := range pkgVersions {
		if !pkg.Replace && pkg.Require && semver.IsValid(pkg.Version) {
			hasDirectEdits = true
			break
		}
	}
	if hasDirectEdits {
		newContent, err := modFile.Format()
		if err != nil {
			return nil, fmt.Errorf("failed to format go.mod: %w", err)
		}
		if err := os.WriteFile(modpath, newContent, 0o600); err != nil {
			return nil, fmt.Errorf("failed to write go.mod: %w", err)
		}
		log.Infof("Updated go.mod file with new versions")
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
		return fmt.Errorf("%w: '%s'", ErrMainModuleBump, modFile.Module.Mod.Path)
	}

	errorPkgVer := make(map[string]pkgVersion)
	// Track which packages have replace directives (replace takes precedence over require in Go)
	replacedPackages := make(map[string]bool)

	// Detect if the list of packages contain any replace statement for the package
	for _, replace := range modFile.Replace {
		if replace == nil {
			continue
		}
		processReplaceDirective(log, replace, pkgVersions, replacedPackages, errorPkgVer)
	}

	// Detect if the list of packages contain any require statement for the package
	// Skip packages that have replace directives (replace takes precedence in Go)
	for _, require := range modFile.Require {
		if require == nil {
			continue
		}
		processRequireDirective(log, require, pkgVersions, replacedPackages, errorPkgVer)
	}

	if len(errorPkgVer) > 0 {
		var errorMsg strings.Builder
		errorMsg.WriteString("The following errors were found:\n")
		for pkg, ver := range errorPkgVer {
			fmt.Fprintf(&errorMsg, "  - package %s: requested version '%s', is already at version '%s'\n", pkg, ver.ReqVersion, ver.AvailableVersion)
		}
		return fmt.Errorf("%w:\n%s", ErrValidationFailed, errorMsg.String())
	}

	return nil
}

// processReplaceDirective processes a single replace directive for package validation.
func processReplaceDirective(log *clog.Logger, replace *modfile.Replace, pkgVersions map[string]*Package, replacedPackages map[string]bool, errorPkgVer map[string]pkgVersion) {
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

	if semver.Compare(replace.New.Version, pkg.Version) > 0 {
		errorPkgVer[replace.New.Path] = pkgVersion{
			ReqVersion:       pkg.Version,
			AvailableVersion: replace.New.Version,
		}
	}
}

// processRequireDirective processes a single require directive for package validation.
func processRequireDirective(log *clog.Logger, require *modfile.Require, pkgVersions map[string]*Package, replacedPackages map[string]bool, errorPkgVer map[string]pkgVersion) {
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

	if semver.Compare(require.Mod.Version, pkg.Version) <= 0 {
		return
	}

	// Check if we need to update or add new error
	if existingPkg, exists := errorPkgVer[require.Mod.Path]; exists {
		if semver.Compare(require.Mod.Version, existingPkg.AvailableVersion) > 0 {
			errorPkgVer[require.Mod.Path] = pkgVersion{
				ReqVersion:       pkg.Version,
				AvailableVersion: require.Mod.Version,
			}
		}
	} else {
		errorPkgVer[require.Mod.Path] = pkgVersion{
			ReqVersion:       pkg.Version,
			AvailableVersion: require.Mod.Version,
		}
	}
}

// updateRequirePackage updates a single require package using either AddRequire or go get.
func updateRequirePackage(ctx context.Context, log *clog.Logger, pkg *Package, modFile *modfile.File, modroot string) error {
	useDirectEdit := pkg.Require && semver.IsValid(pkg.Version)

	if useDirectEdit {
		log.Infof("Updating existing require with AddRequire ...")
		if err := modFile.AddRequire(pkg.Name, pkg.Version); err != nil {
			return fmt.Errorf("failed to update require for %s@%s: %w", pkg.Name, pkg.Version, err)
		}
		return nil
	}

	// For new dependencies or commit hashes, use go get
	if !pkg.Require {
		log.Infof("Running go get for new dependency ...")
	} else {
		log.Infof("Running go get for commit hash or non-semver version ...")
	}
	if output, err := GoGetModule(ctx, pkg.Name, pkg.Version, modroot); err != nil {
		return fmt.Errorf("failed to run 'go get': %w with output: %v", err, output)
	}
	return nil
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
			return nil, fmt.Errorf("%w: package %s. Please remove the package or add it to the list of 'replaces'", ErrPackageNotFound, pkg.Name)
		}
	}

	if cfg.ShowDiff {
		if diff := cmp.Diff(string(originalContent), string(newContent)); diff != "" {
			log.Info(diff)
		}
	}

	if _, err := os.Stat(filepath.Join(cfg.Modroot, "vendor")); err == nil {
		output, err := GoVendor(ctx, cfg.Modroot, cfg.ForceWork)
		if err != nil {
			return nil, fmt.Errorf("failed to run 'go vendor': %w with output: %v", err, output)
		}
	}

	return newModFile, nil
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
