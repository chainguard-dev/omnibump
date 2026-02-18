/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package golang

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/chainguard-dev/clog"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

// ParseGoModfile parses a go.mod file from the specified path.
// Ported from gobump/pkg/update/update.go.
func ParseGoModfile(path string) (*modfile.File, []byte, error) {
	path = filepath.Clean(path)
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

	// Update go.work version FIRST before ANY go commands to avoid version mismatch errors
	if err := UpdateGoWorkVersion(ctx, cfg.Modroot, cfg.ForceWork, goVersion); err != nil {
		log.Warnf("Failed to update go.work version: %v", err)
	}

	// Run go mod tidy before
	if cfg.Tidy && !cfg.SkipInitialTidy {
		output, err := GoModTidy(ctx, cfg.Modroot, goVersion, cfg.TidyCompat)
		if err != nil {
			return nil, fmt.Errorf("failed to run 'go mod tidy': %w with output: %v", err, output)
		}
	}

	// Read the entire go.mod one more time into memory and check that all the version constraints are met
	modpath := filepath.Join(cfg.Modroot, "go.mod")
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
		if !pkg.Replace {
			log.Infof("Update package: %s", k)
			useDirectEdit := pkg.Require && semver.IsValid(pkg.Version)

			if useDirectEdit {
				log.Infof("Updating existing require with AddRequire ...")
				if err := modFile.AddRequire(pkg.Name, pkg.Version); err != nil {
					return nil, fmt.Errorf("failed to update require for %s@%s: %w", pkg.Name, pkg.Version, err)
				}
			} else {
				// For new dependencies or commit hashes, use go get
				if !pkg.Require {
					log.Infof("Running go get for new dependency ...")
				} else {
					log.Infof("Running go get for commit hash or non-semver version ...")
				}
				if output, err := GoGetModule(ctx, pkg.Name, pkg.Version, cfg.Modroot); err != nil {
					return nil, fmt.Errorf("failed to run 'go get': %w with output: %v", err, output)
				}
			}
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

	// Read the entire go.mod one more time into memory and check that all the version constraints are met
	newModFile, newContent, err := ParseGoModfile(modpath)
	if err != nil {
		return nil, fmt.Errorf("unable to parse the go mod file with error: %w", err)
	}

	for _, pkg := range pkgVersions {
		verStr := getVersion(newModFile, pkg.Name)
		if verStr != "" && semver.Compare(verStr, pkg.Version) < 0 {
			return nil, fmt.Errorf("package %s with %s is less than the desired version %s", pkg.Name, verStr, pkg.Version)
		}
		if verStr == "" {
			return nil, fmt.Errorf("package %s was not found on the go.mod file. Please remove the package or add it to the list of 'replaces'", pkg.Name)
		}
	}

	if cfg.ShowDiff {
		if diff := cmp.Diff(string(content), string(newContent)); diff != "" {
			fmt.Println(diff)
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

// CheckPackageValues validates that package versions to be updated are valid
// Checks for main module bumps and downgrades in both replace and require directives.
func CheckPackageValues(ctx context.Context, pkgVersions map[string]*Package, modFile *modfile.File) error {
	log := clog.FromContext(ctx)

	if _, ok := pkgVersions[modFile.Module.Mod.Path]; ok {
		return fmt.Errorf("bumping the main module is not allowed '%s'", modFile.Module.Mod.Path)
	}

	type pkgVersion struct {
		ReqVersion, AvailableVersion string
	}
	errorPkgVer := make(map[string]pkgVersion)
	// Track which packages have replace directives (replace takes precedence over require in Go)
	replacedPackages := make(map[string]bool)

	// Detect if the list of packages contain any replace statement for the package
	for _, replace := range modFile.Replace {
		if replace != nil {
			if _, ok := pkgVersions[replace.New.Path]; ok {
				pkgVersions[replace.New.Path].Replace = true
				if pkgVersions[replace.New.Path].OldName == "" {
					pkgVersions[replace.New.Path].OldName = replace.Old.Path
				}
				// Mark that this package (Old.Path) has a replace directive
				replacedPackages[replace.Old.Path] = true
				if semver.IsValid(pkgVersions[replace.New.Path].Version) {
					if semver.Compare(replace.New.Version, pkgVersions[replace.New.Path].Version) > 0 {
						errorPkgVer[replace.New.Path] = pkgVersion{
							ReqVersion:       pkgVersions[replace.New.Path].Version,
							AvailableVersion: replace.New.Version,
						}
						continue
					}
				} else {
					log.Warnf("Requesting pin to %s. This is not a valid SemVer, so skipping version check.", pkgVersions[replace.New.Path].Version)
				}
			}
		}
	}

	// Detect if the list of packages contain any require statement for the package
	// Skip packages that have replace directives (replace takes precedence in Go)
	for _, require := range modFile.Require {
		if require != nil {
			if _, ok := pkgVersions[require.Mod.Path]; ok {
				// Skip if this package has a replace directive (replace takes precedence)
				if replacedPackages[require.Mod.Path] {
					continue
				}
				pkgVersions[require.Mod.Path].Require = true
				if semver.IsValid(pkgVersions[require.Mod.Path].Version) {
					if semver.Compare(require.Mod.Version, pkgVersions[require.Mod.Path].Version) > 0 {
						if existingPkg, exists := errorPkgVer[require.Mod.Path]; exists {
							if semver.Compare(require.Mod.Version, existingPkg.AvailableVersion) > 0 {
								errorPkgVer[require.Mod.Path] = pkgVersion{
									ReqVersion:       pkgVersions[require.Mod.Path].Version,
									AvailableVersion: require.Mod.Version,
								}
							}
						} else {
							errorPkgVer[require.Mod.Path] = pkgVersion{
								ReqVersion:       pkgVersions[require.Mod.Path].Version,
								AvailableVersion: require.Mod.Version,
							}
						}
						continue
					}
				} else {
					log.Warnf("Requesting pin to %s. This is not a valid SemVer, so skipping version check.", pkgVersions[require.Mod.Path].Version)
				}
			}
		}
	}

	if len(errorPkgVer) > 0 {
		var errorMsg strings.Builder
		errorMsg.WriteString("The following errors were found:\n")
		for pkg, ver := range errorPkgVer {
			fmt.Fprintf(&errorMsg, "  - package %s: requested version '%s', is already at version '%s'\n", pkg, ver.ReqVersion, ver.AvailableVersion)
		}
		return fmt.Errorf("%s", errorMsg.String())
	}

	return nil
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

// getGoVersionFromEnvironment returns the Go version from the local environment.
func getGoVersionFromEnvironment() (string, error) {
	versionOutput := fmt.Sprintf("go version %s", runtime.Version())
	return parseGoVersionString(versionOutput)
}

// parseGoVersionString parses the output of `go version` command and extracts the Go version.
func parseGoVersionString(versionOutput string) (string, error) {
	parts := strings.Fields(versionOutput)
	if len(parts) < 3 || !strings.HasPrefix(parts[2], "go") {
		return "", fmt.Errorf("unexpected format of 'go version' output")
	}

	goVersion := strings.TrimPrefix(parts[2], "go")
	return goVersion, nil
}
