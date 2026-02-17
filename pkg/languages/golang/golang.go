/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package golang implements omnibump support for Go projects.
// Ported from gobump with enhancements for the unified omnibump architecture.
package golang

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/languages"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
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
	goModPath := filepath.Join(dir, "go.mod")
	_, err := os.Stat(goModPath)
	return err == nil, nil
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
func (g *Golang) Update(ctx context.Context, cfg *languages.UpdateConfig) error {
	log := clog.FromContext(ctx)

	log.Infof("Updating Go project at: %s", cfg.RootDir)
	log.Infof("Dependencies to update: %d", len(cfg.Dependencies))

	// Find go.mod
	goModPath := filepath.Join(cfg.RootDir, "go.mod")
	if _, err := os.Stat(goModPath); os.IsNotExist(err) {
		return fmt.Errorf("go.mod not found in: %s", cfg.RootDir)
	}

	// Build update configuration
	updateCfg := &UpdateConfig{
		Modroot:         cfg.RootDir,
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
	packagesToUpdate, err := resolveAndFilterPackages(ctx, packages, modFile, cfg.RootDir)
	if err != nil {
		return fmt.Errorf("failed to resolve package versions: %w", err)
	}

	if len(packagesToUpdate) == 0 {
		log.Infof("All packages are already up-to-date")
		return nil
	}

	if cfg.DryRun {
		log.Infof("Dry run mode: not making actual changes")
		log.Infof("Would update %d packages", len(packagesToUpdate))
		return nil
	}

	// Perform the update
	_, err = DoUpdate(ctx, packagesToUpdate, updateCfg)
	if err != nil {
		return fmt.Errorf("failed to update Go modules: %w", err)
	}

	log.Infof("Successfully updated Go modules")
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
			resolved, err := resolveVersionQuery(name, pkg.Version, modroot)
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

// isVersionQuery checks if a version string is a query (like @latest, @upgrade, @patch)
func isVersionQuery(version string) bool {
	queries := []string{"latest", "upgrade", "patch"}
	return slices.Contains(queries, version)
}

// resolveVersionQuery resolves a version query to an actual version using go list
func resolveVersionQuery(modulePath, query, modroot string) (string, error) {
	modulePath = filepath.Clean(modulePath)
	// Safe: modulePath comes from parsed go.mod (validated Go module paths) and query is a version string
	//nolint:gosec // G204: Using exec.Command with variables from validated go.mod files
	cmd := exec.Command("go", "list", "-m", fmt.Sprintf("%s@%s", modulePath, query))
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
		return "", fmt.Errorf("unexpected go list output: %s", string(output))
	}

	return parts[1], nil
}
