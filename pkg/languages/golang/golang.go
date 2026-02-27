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

// ContainsPackage checks if the package exists in go.mod.
func (g *Golang) ContainsPackage(ctx context.Context, dir string, packageName string) (bool, error) {
	log := clog.FromContext(ctx)

	goModPath := filepath.Join(dir, "go.mod")
	modFile, _, err := ParseGoModfile(goModPath)
	if err != nil {
		log.Debugf("Could not parse go.mod at %s: %v", goModPath, err)
		return false, nil
	}

	// Check require statements (both direct and indirect)
	for _, req := range modFile.Require {
		if req != nil && req.Mod.Path == packageName {
			log.Debugf("Found package %s in go.mod require statements", packageName)
			return true, nil
		}
	}

	// Check replace statements
	for _, rep := range modFile.Replace {
		if rep != nil && (rep.Old.Path == packageName || rep.New.Path == packageName) {
			log.Debugf("Found package %s in go.mod replace statements", packageName)
			return true, nil
		}
	}

	log.Debugf("Package %s not found in go.mod", packageName)
	return false, nil
}

// Update performs dependency updates on a Go project.
// If a go.work file is present, updates all modules in the workspace that contain the dependencies.
func (g *Golang) Update(ctx context.Context, cfg *languages.UpdateConfig) error {
	log := clog.FromContext(ctx)

	log.Infof("Updating Go project at: %s", cfg.RootDir)
	log.Infof("Dependencies to update: %d", len(cfg.Dependencies))

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
func (g *Golang) updateWorkspace(ctx context.Context, cfg *languages.UpdateConfig, workPath string) error {
	log := clog.FromContext(ctx)

	// Parse go.work file
	workFile, err := parseGoWork(workPath)
	if err != nil {
		return fmt.Errorf("failed to parse go.work: %w", err)
	}

	// Get all module paths from workspace
	modulePaths := getWorkspaceModulePaths(workFile)
	log.Infof("Found %d modules in workspace", len(modulePaths))

	// First, determine which modules contain the dependencies we want to update
	modulesToUpdate := make([]string, 0)
	dependencyNames := make(map[string]bool)
	for _, dep := range cfg.Dependencies {
		dependencyNames[dep.Name] = true
	}

	for _, modPath := range modulePaths {
		fullModPath := filepath.Join(cfg.RootDir, modPath)
		goModPath := filepath.Join(fullModPath, "go.mod")

		// Parse go.mod to see if it contains any of our target dependencies
		modFile, _, err := ParseGoModfile(goModPath)
		if err != nil {
			log.Warnf("Failed to parse %s: %v", goModPath, err)
			continue
		}

		// Check if this module contains any of the dependencies
		hasTargetDep := false
		for _, req := range modFile.Require {
			if req != nil && dependencyNames[req.Mod.Path] {
				hasTargetDep = true
				break
			}
		}

		if hasTargetDep {
			modulesToUpdate = append(modulesToUpdate, modPath)
		}
	}

	if len(modulesToUpdate) == 0 {
		log.Infof("None of the workspace modules contain the target dependencies")
		return nil
	}

	log.Infof("Will update %d modules that contain target dependencies", len(modulesToUpdate))

	// Update each module
	updatedCount := 0
	for _, modPath := range modulesToUpdate {
		fullModPath := filepath.Join(cfg.RootDir, modPath)
		log.Infof("Updating module: %s", modPath)

		// Create a copy of cfg with the module-specific directory
		moduleCfg := &languages.UpdateConfig{
			RootDir:      fullModPath,
			Dependencies: cfg.Dependencies,
			DryRun:       cfg.DryRun,
			Tidy:         cfg.Tidy,
			ShowDiff:     cfg.ShowDiff,
			Options:      cfg.Options,
		}

		// Update this module
		if err := g.updateSingleModule(ctx, moduleCfg, fullModPath); err != nil {
			log.Errorf("Failed to update module %s: %v", modPath, err)
			return fmt.Errorf("failed to update module %s: %w", modPath, err)
		}
		updatedCount++
	}

	log.Infof("Successfully updated %d modules in workspace", updatedCount)
	return nil
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
			log.Warnf("Dependency not found in go.mod: %s", dep.Name)
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

// resolveAndFilterPackages resolves version queries like @latest and filters out packages that don't need updating.
func resolveAndFilterPackages(ctx context.Context, packages map[string]*Package, modFile *modfile.File, modroot string) (map[string]*Package, error) {
	log := clog.FromContext(ctx)
	filtered := make(map[string]*Package)

	for name, pkg := range packages {
		// Resolve version if it's a query (@latest, @upgrade, etc.)
		resolvedVersion := pkg.Version
		if isVersionQuery(pkg.Version) {
			resolved, err := resolveVersionQuery(ctx, name, pkg.Version, modroot)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve %s@%s: %w", name, pkg.Version, err)
			}
			resolvedVersion = resolved
			log.Infof("Resolved %s@%s to %s", name, pkg.Version, resolvedVersion)
		}

		// Get current version from go.mod
		currentVersion := getVersion(modFile, name)

		if currentVersion == "" {
			// Package doesn't exist in go.mod, add it
			pkg.Version = resolvedVersion
			filtered[name] = pkg
			log.Infof("Package %s not found in go.mod, will add at %s", name, resolvedVersion)
			continue
		}

		// Compare versions using semver
		if semver.IsValid(currentVersion) && semver.IsValid(resolvedVersion) {
			cmp := semver.Compare(currentVersion, resolvedVersion)
			if cmp == 0 {
				log.Infof("Package %s is already at %s, skipping", name, currentVersion)
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

	return filtered, nil
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
	// Override vendor mode to allow querying
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")

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
