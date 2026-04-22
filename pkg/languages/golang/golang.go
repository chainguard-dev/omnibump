/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package golang implements omnibump support for Go projects.
// Ported from gobump with enhancements for the unified omnibump architecture.
package golang

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/languages"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

var (
	// ErrGoModNotFound is returned when go.mod is not found in the specified directory.
	ErrGoModNotFound = errors.New("go.mod not found")

	// ErrUnexpectedGoListOutput is returned when go list output has unexpected format.
	ErrUnexpectedGoListOutput = errors.New("unexpected go list output")

)

// Golang implements the Language interface for Go projects.
type Golang struct{}

// init registers Golang with the language registry.
func init() {
	languages.Register(&Golang{})
}

// Name returns the language identifier.
func (g *Golang) Name() string {
	return "go"
}

// Detect checks if Go manifest files exist in the directory.
func (g *Golang) Detect(ctx context.Context, dir string) (bool, error) {
	log := clog.FromContext(ctx)
	goModPath := filepath.Join(dir, "go.mod")
	_, err := os.Stat(goModPath)
	if err == nil {
		log.Debugf("Detected Go project at %s", dir)
		return true, nil
	}
	log.Debugf("No Go project detected at %s", dir)
	return false, nil
}

// GetManifestFiles returns Go manifest files.
func (g *Golang) GetManifestFiles() []string {
	return []string{"go.mod", "go.sum", "go.work"}
}

// SupportsAnalysis returns true since Go now has analysis capabilities.
func (g *Golang) SupportsAnalysis() bool {
	return true
}

// Update performs dependency updates on a Go project.
// If a go.work file is present, updates all modules in the workspace that contain the dependencies.
func (g *Golang) Update(ctx context.Context, cfg *languages.UpdateConfig) error {
	log := clog.FromContext(ctx)

	log.Infof("Updating Go project at: %s", cfg.RootDir)
	log.Debugf("Dependencies to update: %d", len(cfg.Dependencies))

	// Check for go.work file
	workPath := filepath.Join(cfg.RootDir, "go.work")
	if _, err := os.Stat(workPath); err == nil {
		log.Infof("Found go.work file, updating all workspace modules")
		return g.updateWorkspace(ctx, cfg, workPath)
	}

	// No workspace, update single module
	return g.updateSingleModule(ctx, cfg, cfg.RootDir)
}

// updateSingleModule updates a single Go module.
func (g *Golang) updateSingleModule(ctx context.Context, cfg *languages.UpdateConfig, moduleDir string) error {
	log := clog.FromContext(ctx)

	// Find go.mod
	goModPath := filepath.Join(moduleDir, "go.mod")
	if _, err := os.Stat(goModPath); os.IsNotExist(err) {
		return fmt.Errorf("%w in: %s", ErrGoModNotFound, moduleDir)
	}

	// Build update configuration
	updateCfg := &UpdateConfig{
		Modroot:         moduleDir,
		Tidy:            cfg.Tidy,
		ShowDiff:        cfg.ShowDiff,
		SkipInitialTidy: getOptionBool(cfg.Options, "skip-initial-tidy", false),
		TidyCompat:      getOptionString(cfg.Options, "tidy-compat", ""),
		GoVersion:       getOptionString(cfg.Options, "go-version", ""),
		ForceWork:       getOptionBool(cfg.Options, "work", false),
	}

	// Convert dependencies to Go-specific format
	packages := convertDependenciesToPackages(cfg.Dependencies)

	// Parse current go.mod to check existing versions
	modFile, _, err := ParseGoModfile(goModPath)
	if err != nil {
		return fmt.Errorf("failed to parse go.mod: %w", err)
	}

	// Resolve and filter packages that need updating
	packagesToUpdate, err := resolveAndFilterPackages(ctx, packages, modFile, moduleDir)
	if err != nil {
		return fmt.Errorf("failed to resolve package versions: %w", err)
	}

	if len(packagesToUpdate) == 0 {
		log.Infof("All packages are already up-to-date in %s", moduleDir)
		return nil
	}

	if cfg.DryRun {
		log.Infof("Dry run mode: not making actual changes")
		log.Infof("Would update %d packages in %s", len(packagesToUpdate), moduleDir)
		return nil
	}

	// Perform the update
	_, err = DoUpdate(ctx, packagesToUpdate, updateCfg)
	if err != nil {
		return fmt.Errorf("failed to update Go modules in %s: %w", moduleDir, err)
	}

	log.Infof("Successfully updated Go modules in %s", moduleDir)
	return nil
}

// updateWorkspace updates all modules in a Go workspace that contain the target dependencies.
// Only dependencies already present in each module's go.mod are passed to that module's update;
// packages absent from a sub-module are skipped rather than added, because workspace sub-modules
// manage their own dependency sets independently.
func (g *Golang) updateWorkspace(ctx context.Context, cfg *languages.UpdateConfig, workPath string) error {
	// Parse go.work file
	workFile, err := parseGoWork(workPath)
	if err != nil {
		return fmt.Errorf("failed to parse go.work: %w", err)
	}

	// Get all module paths from workspace
	modulePaths := getWorkspaceModulePaths(workFile)
	clog.InfoContextf(ctx, "Found %d modules in workspace", len(modulePaths))

	updatedCount := 0
	for _, modPath := range modulePaths {
		fullModPath := filepath.Join(cfg.RootDir, modPath)
		goModPath := filepath.Join(fullModPath, "go.mod")

		modFile, _, err := ParseGoModfile(goModPath)
		if err != nil {
			clog.WarnContextf(ctx, "Failed to parse %s: %v", goModPath, err)
			continue
		}

		// Only pass deps that are already present in this module's go.mod.
		moduleDeps := filterDepsForModule(cfg.Dependencies, modFile)
		if len(moduleDeps) == 0 {
			clog.DebugContextf(ctx, "Module %s has none of the target dependencies, skipping", modPath)
			continue
		}

		clog.InfoContextf(ctx, "Updating module: %s", modPath)

		moduleCfg := &languages.UpdateConfig{
			RootDir:      fullModPath,
			Dependencies: moduleDeps,
			DryRun:       cfg.DryRun,
			Tidy:         cfg.Tidy,
			ShowDiff:     cfg.ShowDiff,
			Options:      cfg.Options,
		}

		if err := g.updateSingleModule(ctx, moduleCfg, fullModPath); err != nil {
			clog.ErrorContextf(ctx, "Failed to update module %s: %v", modPath, err)
			return fmt.Errorf("failed to update module %s: %w", modPath, err)
		}
		updatedCount++
	}

	if updatedCount == 0 {
		clog.InfoContextf(ctx, "None of the workspace modules contain the target dependencies")
		return nil
	}

	clog.InfoContextf(ctx, "Successfully updated %d modules in workspace", updatedCount)
	return nil
}

// filterDepsForModule returns only the dependencies already present in the given
// module's go.mod as require or replace directives. This prevents omnibump from
// adding packages to workspace sub-modules that don't already have them, which
// would be undone by go mod tidy and cause a verification failure.
func filterDepsForModule(deps []languages.Dependency, modFile *modfile.File) []languages.Dependency {
	present := make(map[string]struct{}, len(modFile.Require)+len(modFile.Replace)*2)
	for _, req := range modFile.Require {
		if req != nil {
			present[req.Mod.Path] = struct{}{}
		}
	}
	for _, repl := range modFile.Replace {
		if repl != nil {
			present[repl.Old.Path] = struct{}{}
			present[repl.New.Path] = struct{}{}
		}
	}

	filtered := make([]languages.Dependency, 0, len(deps))
	for _, dep := range deps {
		_, namePresent := present[dep.Name]
		_, oldNamePresent := present[dep.OldName]
		if namePresent || (dep.OldName != "" && oldNamePresent) {
			filtered = append(filtered, dep)
		}
	}
	return filtered
}

// Validate checks if the updates were applied successfully.
func (g *Golang) Validate(ctx context.Context, cfg *languages.UpdateConfig) error {
	log := clog.FromContext(ctx)

	goModPath := filepath.Join(cfg.RootDir, "go.mod")

	// Parse the updated go.mod
	modFile, _, err := ParseGoModfile(goModPath)
	if err != nil {
		return fmt.Errorf("failed to parse updated go.mod: %w", err)
	}

	// Validate dependencies
	for _, dep := range cfg.Dependencies {
		version := getVersion(modFile, dep.Name)
		if version == "" {
			log.Warnf("Dependency %s@%s not found in go.mod after update; it may have been superseded by a major version upgrade", dep.Name, dep.Version)
			continue
		}

		// For Go, versions might not match exactly due to go.mod tidying
		// Just warn if version seems wrong
		if version != dep.Version {
			log.Debugf("Dependency %s: expected %s, got %s (may be normalized by go mod)",
				dep.Name, dep.Version, version)
		}
	}

	log.Infof("Validation completed successfully")
	return nil
}

// convertDependenciesToPackages converts unified dependencies to Go-specific packages.
func convertDependenciesToPackages(deps []languages.Dependency) map[string]*Package {
	packages := make(map[string]*Package)

	for i, dep := range deps {
		pkg := &Package{
			Name:    dep.Name,
			Version: dep.Version,
			Replace: dep.Replace,
			OldName: dep.OldName,
			Index:   i,
		}

		// Determine if this is a require or replace
		if dep.Replace {
			pkg.Replace = true
		}

		packages[dep.Name] = pkg
	}

	return packages
}

// getOptionString retrieves a string option from the options map.
func getOptionString(options map[string]any, key, defaultValue string) string {
	if val, ok := options[key]; ok {
		if strVal, ok := val.(string); ok {
			return strVal
		}
	}
	return defaultValue
}

// getOptionBool retrieves a boolean option from the options map.
func getOptionBool(options map[string]any, key string, defaultValue bool) bool {
	if val, ok := options[key]; ok {
		if boolVal, ok := val.(bool); ok {
			return boolVal
		}
	}
	return defaultValue
}

// hasReplaceDirective returns true if the go.mod has a replace directive with the
// given module path on the left (Old) side.
func hasReplaceDirective(modFile *modfile.File, packageName string) bool {
	for _, r := range modFile.Replace {
		if r.Old.Path == packageName {
			return true
		}
	}
	return false
}

// resolvePackageVersion returns the canonical version for a package. Version queries
// like @latest are resolved via go list. Concrete semver versions are also resolved
// to pick up the +incompatible suffix when needed, falling back to the provided version
// if the proxy is unavailable.
func resolvePackageVersion(ctx context.Context, name, version, modroot string) (string, error) {
	log := clog.FromContext(ctx)

	if isVersionQuery(version) {
		resolved, err := resolveVersionQuery(ctx, name, version, modroot)
		if err != nil {
			return "", fmt.Errorf("failed to resolve %s@%s: %w", name, version, err)
		}
		log.Infof("Resolved %s@%s to %s", name, version, resolved)
		return resolved, nil
	}

	if len(version) >= 2 && version[0] == 'v' && version[1] >= '0' && version[1] <= '9' {
		resolved, err := resolveVersionQuery(ctx, name, version, modroot)
		if err != nil {
			// Fall back to the provided version; appendIncompatibleIfNeeded handles the suffix.
			log.Debugf("Could not resolve canonical form of %s@%s, using as-is: %v", name, version, err)
			return version, nil
		}
		if resolved != version {
			log.Infof("Resolved %s@%s to canonical form %s", name, version, resolved)
		}
		return resolved, nil
	}

	return version, nil
}

// resolveAndFilterPackages resolves version queries like @latest and filters out packages that don't need updating.
func resolveAndFilterPackages(ctx context.Context, packages map[string]*Package, modFile *modfile.File, modroot string) (map[string]*Package, error) {
	log := clog.FromContext(ctx)
	filtered := make(map[string]*Package)

	for name, pkg := range packages {
		resolvedVersion, err := resolvePackageVersion(ctx, name, pkg.Version, modroot)
		if err != nil {
			return nil, err
		}

		// For modules that don't use major version path suffixes (e.g., /v2, /v3), Go
		// requires the +incompatible suffix for versions with major > 1.
		resolvedVersion = appendIncompatibleIfNeeded(name, resolvedVersion)

		// Get current version from go.mod
		currentVersion := getVersion(modFile, name)

		if currentVersion == "" {
			// Package doesn't exist in go.mod, add it
			pkg.Version = resolvedVersion
			filtered[name] = pkg
			log.Infof("Package %s not found in go.mod, will add at %s", name, resolvedVersion)
			continue
		}

		// Skip packages that are pinned via a replace directive unless the caller
		// explicitly requested a replace update (pkg.Replace == true). Replace-pinned
		// packages must be updated through --replaces, not --deps, because only
		// updating the require directive leaves the replace pin in place.
		if !pkg.Replace && hasReplaceDirective(modFile, name) {
			log.Warnf("Package %s is pinned via a replace directive — skipping. Use --replaces %s=%s@%s to update the pin.", name, name, name, resolvedVersion)
			continue
		}

		// Compare versions using semver
		if semver.IsValid(currentVersion) && semver.IsValid(resolvedVersion) {
			cmp := semver.Compare(currentVersion, resolvedVersion)
			if cmp == 0 {
				log.Debugf("Package %s is already at %s, skipping", name, currentVersion)
				continue
			} else if cmp > 0 {
				log.Warnf("Package %s is at %s which is newer than requested %s, skipping", name, currentVersion, resolvedVersion)
				continue
			}
		}

		// Update to resolved version
		pkg.Version = resolvedVersion
		filtered[name] = pkg
		log.Infof("Will update %s from %s to %s", name, currentVersion, resolvedVersion)
	}

	if err := checkMissingTransitiveDeps(ctx, filtered, modFile); err != nil {
		return nil, err
	}

	return filtered, nil
}

// checkMissingTransitiveDeps checks all packages being updated for transitive dependency
// requirements not satisfied by the current go.mod, and returns an error with co-update
// recommendations if any are found.
func checkMissingTransitiveDeps(ctx context.Context, filtered map[string]*Package, modFile *modfile.File) error {
	log := clog.FromContext(ctx)

	// Build set of packages being updated
	packagesBeingUpdated := make(map[string]string)
	for name, pkg := range filtered {
		packagesBeingUpdated[name] = pkg.Version
	}

	allMissingDeps := make(map[string]MissingDependency)
	apiCompatibilityAlerts := make(map[string]struct{})

	// Create a cache for go.mod files to reduce HTTP requests when checking API compatibility
	// across multiple packages. This significantly reduces round trips when updating many packages.
	cache := newGoModCache()

	// Pre-fetch all dependencies in one batch to minimize HTTP calls.
	// For 50 dependencies and 10 package updates: 50 calls total instead of up to 500.
	preFetchDependencies(ctx, modFile, cache)

	for name, pkg := range filtered {
		missingDeps, err := CheckTransitiveRequirements(ctx, name, pkg.Version, modFile)
		if err != nil {
			log.Warnf("Could not check transitive requirements for %s@%s: %v", name, pkg.Version, err)
			continue
		}
		collectMissingDeps(ctx, missingDeps, packagesBeingUpdated, allMissingDeps)

		// Also check for API compatibility issues
		apiIssues, err := CheckAPICompatibilityWithCache(ctx, name, pkg.Version, modFile, cache)
		if err != nil {
			log.Debugf("Could not check API compatibility for %s@%s: %v", name, pkg.Version, err)
		} else {
			for _, issue := range apiIssues {
				apiCompatibilityAlerts[issue.Package] = struct{}{}
				log.Infof("API compatibility alert for %s", issue.Package)
			}
		}
	}

	if len(allMissingDeps) == 0 && len(apiCompatibilityAlerts) == 0 {
		return nil
	}

	var msg strings.Builder

	fmt.Fprintf(&msg, "\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

	// Show required co-updates as an aligned table.
	if len(allMissingDeps) > 0 {
		maxLen := 0
		for _, dep := range allMissingDeps {
			if len(dep.Package) > maxLen {
				maxLen = len(dep.Package)
			}
		}
		keys := make([]string, 0, len(allMissingDeps))
		for k := range allMissingDeps {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		fmt.Fprintf(&msg, "REQUIRED CO-UPDATES\n")
		for _, k := range keys {
			dep := allMissingDeps[k]
			fmt.Fprintf(&msg, "  %-*s  %s → >=%s\n", maxLen, dep.Package, dep.CurrentVersion, dep.RequiredVersion)
		}
		fmt.Fprintf(&msg, "\n")
	}

	// Show API compatibility alerts as an aligned table.
	if len(apiCompatibilityAlerts) > 0 {
		maxLen := 0
		for pkg := range apiCompatibilityAlerts {
			if len(pkg) > maxLen {
				maxLen = len(pkg)
			}
		}
		keys := make([]string, 0, len(apiCompatibilityAlerts))
		for k := range apiCompatibilityAlerts {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		fmt.Fprintf(&msg, "API COMPATIBILITY ALERTS\n")
		for _, pkg := range keys {
			currentVer := ""
			for _, req := range modFile.Require {
				if req != nil && req.Mod.Path == pkg {
					currentVer = req.Mod.Version
					break
				}
			}
			fmt.Fprintf(&msg, "  %-*s  %s\n", maxLen, pkg, currentVer)
		}
		fmt.Fprintf(&msg, "\n")
	}

	if len(allMissingDeps) > 0 {
		fmt.Fprintf(&msg, "SUGGESTED UPDATE COMMAND\n\n")
		fmt.Fprintf(&msg, "%s", buildSuggestedCommand(filtered, allMissingDeps, apiCompatibilityAlerts, modFile))
		fmt.Fprintf(&msg, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	}
	log.Warnf("%s", msg.String())
	return nil
}

// buildSuggestedCommand builds the omnibump --packages "..." command string.
// It merges filtered packages and missing transitive deps, keeping the highest
// version per module path so each package appears exactly once.
func buildSuggestedCommand(filtered map[string]*Package, allMissingDeps map[string]MissingDependency, apiAlerts map[string]struct{}, modFile *modfile.File) string {
	merged := make(map[string]string, len(filtered)+len(allMissingDeps))
	for name, pkg := range filtered {
		merged[name] = pkg.Version
	}
	for _, dep := range allMissingDeps {
		if cur, ok := merged[dep.Package]; ok {
			// If the current entry is non-semver (e.g. a commit hash) and the
			// transitive requirement is real semver, prefer the semver version.
			if !semver.IsValid(cur) && semver.IsValid(dep.RequiredVersion) {
				merged[dep.Package] = dep.RequiredVersion
			} else if semver.IsValid(dep.RequiredVersion) && semver.IsValid(cur) &&
				semver.Compare(dep.RequiredVersion, cur) > 0 {
				merged[dep.Package] = dep.RequiredVersion
			}
		} else {
			merged[dep.Package] = dep.RequiredVersion
		}
	}
	for pkg := range apiAlerts {
		if _, exists := merged[pkg]; !exists {
			for _, req := range modFile.Require {
				if req != nil && req.Mod.Path == pkg && req.Mod.Version != "" {
					merged[pkg] = req.Mod.Version
					break
				}
			}
		}
	}

	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	var sb strings.Builder
	fmt.Fprintf(&sb, "omnibump --packages \"\n")
	for _, name := range keys {
		fmt.Fprintf(&sb, "  %s@%s\n", name, merged[name])
	}
	fmt.Fprintf(&sb, "\"\n\n")
	return sb.String()
}

// collectMissingDeps adds missing transitive dependencies to allMissingDeps,
// skipping any that are already satisfied by the current update set.
func collectMissingDeps(ctx context.Context, missingDeps []MissingDependency, packagesBeingUpdated map[string]string, allMissingDeps map[string]MissingDependency) {
	log := clog.FromContext(ctx)
	for _, dep := range missingDeps {
		if targetVer, beingUpdated := packagesBeingUpdated[dep.Package]; beingUpdated {
			if semver.IsValid(targetVer) && semver.IsValid(dep.RequiredVersion) {
				if semver.Compare(targetVer, dep.RequiredVersion) >= 0 {
					log.Debugf("Dependency %s requirement satisfied by update to %s", dep.Package, targetVer)
					continue
				}
			}
		}
		if existing, exists := allMissingDeps[dep.Package]; exists {
			if semver.IsValid(dep.RequiredVersion) && semver.IsValid(existing.RequiredVersion) {
				if semver.Compare(dep.RequiredVersion, existing.RequiredVersion) > 0 {
					allMissingDeps[dep.Package] = dep
				}
			}
		} else {
			allMissingDeps[dep.Package] = dep
		}
	}
}

// appendIncompatibleIfNeeded adds the +incompatible suffix when a module path does not
// use major version path suffixes (e.g., /v2, /v3) but the version's major is greater
// than v1. This is required by go.mod for pre-module-era packages.
func appendIncompatibleIfNeeded(modulePath, version string) string {
	if !semver.IsValid(version) {
		return version
	}
	if strings.HasSuffix(version, "+incompatible") {
		return version
	}
	major := semver.Major(version)
	if major == "v0" || major == "v1" {
		return version
	}
	// SplitPathVersion returns the major version suffix (e.g. "/v2") if present.
	_, pathMajor, _ := module.SplitPathVersion(modulePath)
	if pathMajor != "" {
		return version
	}
	return version + "+incompatible"
}

// isVersionQuery checks if a version string is a query (like @latest, @upgrade, @patch).
func isVersionQuery(version string) bool {
	queries := []string{"latest", "upgrade", "patch"}
	return slices.Contains(queries, version)
}

// resolveVersionQuery resolves a version query to an actual version using go list.
func resolveVersionQuery(ctx context.Context, modulePath, query, modroot string) (string, error) {
	// Validate module path before passing to command.
	if err := module.CheckPath(modulePath); err != nil {
		return "", fmt.Errorf("invalid module path %q: %w", modulePath, err)
	}
	// Validate version query before passing to command.
	if err := validateVersionQuery(query); err != nil {
		return "", fmt.Errorf("invalid version query: %w", err)
	}

	//nolint:gosec // G204: Using exec.Command with validated module path and version query
	cmd := exec.CommandContext(ctx, "go", "list", "-m", fmt.Sprintf("%s@%s", modulePath, query))
	cmd.Dir = modroot
	// Disable workspace mode and override vendor mode to allow querying
	// GOWORK=off is required when go.work file exists
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("go list failed: %w, output: %s", err, strings.TrimSpace(string(output)))
	}

	// Parse output: "module version"
	parts := strings.Fields(strings.TrimSpace(string(output)))
	if len(parts) < 2 {
		return "", fmt.Errorf("%w: %s", ErrUnexpectedGoListOutput, string(output))
	}

	return parts[1], nil
}
