/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/analyzer"
	"github.com/chainguard-dev/omnibump/pkg/gradlefile"
	"github.com/chainguard-dev/omnibump/pkg/languages"
)

// Gradle implements the BuildTool interface for Gradle projects.
type Gradle struct{}

const (
	// File permissions for writing updated build files.
	gradleFilePerms = 0o600

	// maxManifestSize limits manifest file size to prevent resource exhaustion.
	maxManifestSize = 10 * 1024 * 1024 // 10 MB

	// gradleToolName is the build tool identifier.
	gradleToolName = "gradle"

	// Well-known Gradle file names.
	buildGradleFile       = "build.gradle"
	buildGradleKtsFile    = "build.gradle.kts"
	settingsGradleFile    = "settings.gradle"
	settingsGradleKtsFile = "settings.gradle.kts"
	gradlePropertiesFile  = "gradle.properties"
)

var (
	// ErrNoBuildFiles is returned when no build.gradle files are found.
	ErrNoBuildFiles = errors.New("no build.gradle or build.gradle.kts files found")

	// ErrValidationFailed is returned when dependency validation fails.
	ErrValidationFailed = errors.New("validation failed")

	// ErrUnknownFileType is returned when an unknown Gradle file type is encountered.
	ErrUnknownFileType = errors.New("unknown Gradle file type")

	// ErrManifestTooLarge is returned when a manifest file exceeds size limits.
	ErrManifestTooLarge = errors.New("manifest file too large")
)

// skipDirs lists directories to skip when walking the file tree.
var skipDirs = map[string]struct{}{
	"vendor":       {},
	"node_modules": {},
}

// Name returns the build tool identifier.
func (g *Gradle) Name() string {
	return gradleToolName
}

// Detect checks if Gradle manifest files exist in the directory.
func (g *Gradle) Detect(ctx context.Context, dir string) (bool, error) {
	for _, file := range g.GetManifestFiles() {
		if _, err := os.Stat(filepath.Join(dir, file)); err == nil {
			clog.DebugContextf(ctx, "Detected Gradle project at %s (found %s)", dir, file)
			return true, nil
		}
	}

	clog.DebugContextf(ctx, "No Gradle project detected at %s", dir)
	return false, nil
}

// GetManifestFiles returns Gradle manifest files.
func (g *Gradle) GetManifestFiles() []string {
	return []string{
		buildGradleFile,
		buildGradleKtsFile,
		settingsGradleFile,
		settingsGradleKtsFile,
		gradlePropertiesFile,
		"gradle/libs.versions.toml",
	}
}

// GetAnalyzer returns the Gradle analyzer.
func (g *Gradle) GetAnalyzer() analyzer.Analyzer {
	return &GradleAnalyzer{}
}

// Update performs dependency updates on a Gradle project. It builds a model
// of every Gradle file that can define a version, routes each requested
// update to the mechanism that defines it (version catalog, version
// variable, direct declaration), pins dependencies found nowhere via a
// dependency constraint, and applies coordinate swaps via dependency
// substitution — both hosted in the managed block of the settings script so
// they apply before any configuration resolves.
func (g *Gradle) Update(ctx context.Context, cfg *languages.UpdateConfig) error {
	clog.InfoContextf(ctx, "Updating Gradle project at: %s", cfg.RootDir)
	clog.InfoContextf(ctx, "Dependencies to update: %d", len(cfg.Dependencies))
	clog.InfoContextf(ctx, "Properties to update: %d", len(cfg.Properties))

	// Validate all versions upfront to fail fast before any file writes.
	for _, dep := range cfg.Dependencies {
		if dep.Version == "" {
			continue
		}
		if err := gradlefile.ValidateVersion(dep.Version); err != nil {
			return fmt.Errorf("dependency %s: %w", depDisplayName(dep), err)
		}
	}
	for name, value := range cfg.Properties {
		if err := gradlefile.ValidateVersion(value); err != nil {
			return fmt.Errorf("property %s: %w", name, err)
		}
	}

	model, err := buildProjectModel(ctx, cfg.RootDir)
	if err != nil {
		return err
	}
	if len(model.sortedFiles) == 0 {
		return ErrNoBuildFiles
	}

	if extras := model.shipConfigurations(); len(extras) > 0 {
		clog.InfoContextf(ctx, "Gradle: also forcing managed pins on bundled non-classpath configuration(s): %s",
			strings.Join(extras, ", "))
	}
	for _, ref := range model.unresolvedShipConfigs() {
		clog.WarnContextf(ctx, "Gradle: a packaging task bundles a configuration omnibump could not resolve to a name (%s: %q); if a CVE is missed, pin it explicitly via the bump pipeline's gradle-force-configurations input",
			ref.Source, ref.Raw)
	}

	plan, err := resolveUpdates(ctx, model, cfg)
	if err != nil {
		return err
	}

	return plan.apply(ctx, cfg)
}

// Validate checks if the updates were applied successfully by re-scanning
// the project and verifying every dependency's effective version and every
// property's definition sites.
func (g *Gradle) Validate(ctx context.Context, cfg *languages.UpdateConfig) error {
	clog.InfoContextf(ctx, "Validating Gradle updates in: %s", cfg.RootDir)

	model, err := buildProjectModel(ctx, cfg.RootDir)
	if err != nil {
		return fmt.Errorf("failed to re-scan project for validation: %w", err)
	}

	var failures []string
	for _, dep := range cfg.Dependencies {
		if failure := validateDependency(model, dep); failure != "" {
			failures = append(failures, failure)
		}
	}
	for name, expected := range cfg.Properties {
		if failure := validateProperty(model, name, expected); failure != "" {
			failures = append(failures, failure)
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("%w for %d update(s):\n  - %s",
			ErrValidationFailed, len(failures), strings.Join(failures, "\n  - "))
	}

	clog.InfoContextf(ctx, "Validation successful: all %d dependencies and %d properties updated correctly",
		len(cfg.Dependencies), len(cfg.Properties))
	return nil
}

// effectiveVersion is one resolved version of a module together with a
// description of the mechanism it came from, for validation messages.
type effectiveVersion struct {
	version string
	desc    string
}

// validateDependency verifies that at least one mechanism resolves the
// dependency to the expected version. Returns a failure description or "".
func validateDependency(model *projectModel, dep languages.Dependency) string {
	if dep.Version == "" {
		return ""
	}
	group, artifact, err := depCoordinates(dep)
	if err != nil {
		return err.Error()
	}
	module := group + ":" + artifact

	versions := effectiveVersions(model, module, group, artifact)
	if len(versions) == 0 {
		return fmt.Sprintf("%s: not found in project after update", module)
	}
	for _, v := range versions {
		if v.version == dep.Version {
			return ""
		}
	}
	return fmt.Sprintf("%s: %s, expected %s", module, versions[0].desc, dep.Version)
}

// effectiveVersions collects every version the project resolves module to,
// across catalogs, declarations, referenced variables and force blocks.
func effectiveVersions(model *projectModel, module, group, artifact string) []effectiveVersion {
	var versions []effectiveVersion

	for _, site := range model.catalogLibrarySites[module] {
		value := model.resolveCatalogValue(site.library)
		if value == "" {
			continue
		}
		key := site.library.VersionRef
		if key == "" {
			key = site.library.Alias
		}
		versions = append(versions, effectiveVersion{
			version: value,
			desc:    fmt.Sprintf("catalog key %s has version %s", key, value),
		})
	}
	for _, site := range model.declarationSites[module] {
		switch {
		case site.decl.Version != "":
			versions = append(versions, effectiveVersion{
				version: site.decl.Version,
				desc:    fmt.Sprintf("has version %s", site.decl.Version),
			})
		case site.decl.VarRef != "":
			versions = append(versions, variableEffectiveVersions(model, site.decl.VarRef)...)
		}
	}
	for _, site := range model.libraryFnSites[artifact] {
		versions = append(versions, effectiveVersion{
			version: site.decl.Version,
			desc:    fmt.Sprintf("has version %s", site.decl.Version),
		})
	}
	for _, site := range model.resolutionRuleSites[group] {
		rule := site.rule
		if rule.Artifact != "" && rule.Artifact != artifact {
			continue
		}
		switch {
		case rule.CatalogKey != "":
			for _, keySite := range model.catalogVersionSites[rule.CatalogKey] {
				versions = append(versions, effectiveVersion{
					version: keySite.version.Value,
					desc:    fmt.Sprintf("catalog key %s has version %s", rule.CatalogKey, keySite.version.Value),
				})
			}
		case rule.VarRef != "":
			versions = append(versions, variableEffectiveVersions(model, rule.VarRef)...)
		case rule.Version != "":
			versions = append(versions, effectiveVersion{
				version: rule.Version,
				desc:    fmt.Sprintf("resolution rule has version %s", rule.Version),
			})
		}
	}
	for _, version := range model.pinnedSites[module] {
		versions = append(versions, effectiveVersion{
			version: version,
			desc:    fmt.Sprintf("pinned to version %s", version),
		})
	}
	return versions
}

// variableEffectiveVersions resolves a variable reference the same way the
// updater routes it: definition sites first, then the catalog-accessor
// bridge ("${versions.x}" resolving to the catalog version key x).
func variableEffectiveVersions(model *projectModel, varPath string) []effectiveVersion {
	varSites := model.variableSites[varPath]
	if len(varSites) == 0 {
		if key, ok := model.catalogKeyForVarPath(varPath); ok {
			versions := make([]effectiveVersion, 0, len(model.catalogVersionSites[key]))
			for _, keySite := range model.catalogVersionSites[key] {
				versions = append(versions, effectiveVersion{
					version: keySite.version.Value,
					desc:    fmt.Sprintf("catalog key %s has version %s", key, keySite.version.Value),
				})
			}
			return versions
		}
	}
	versions := make([]effectiveVersion, 0, len(varSites))
	for _, varSite := range varSites {
		versions = append(versions, effectiveVersion{
			version: varSite.value(),
			desc:    fmt.Sprintf("variable %s has version %s", varPath, varSite.value()),
		})
	}
	return versions
}

// validateProperty verifies that every definition site of the property
// carries the expected value. Returns a failure description or "".
func validateProperty(model *projectModel, name, expected string) string {
	catalogSites := model.catalogVersionSites[name]
	variableSites := model.variableSitesFor(name)
	if len(catalogSites) == 0 && len(variableSites) == 0 {
		return fmt.Sprintf("property %s: %v", name, ErrPropertyNotFound)
	}
	for _, site := range catalogSites {
		if site.version.Value != expected {
			return fmt.Sprintf("property %s: catalog key has value %s in %s, expected %s",
				name, site.version.Value, site.path(), expected)
		}
	}
	for _, site := range variableSites {
		if site.value() != expected {
			return fmt.Sprintf("property %s: has value %s in %s, expected %s",
				name, site.value(), site.path(), expected)
		}
	}
	return ""
}

// findBuildFiles finds all Gradle files that can contain dependency versions:
// build and settings scripts (any *.gradle / *.gradle.kts, including files
// like gradle/dependencies.gradle), gradle.properties, version.properties
// files, and *.versions.toml version catalogs. Walks subdirectories to
// support multi-module Gradle projects.
func findBuildFiles(root string) ([]string, error) {
	var files []string

	// Use WalkDir instead of Walk - it doesn't follow symlinks and provides type info directly
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden directories and common non-build directories
		if d.IsDir() {
			name := d.Name()
			if _, skip := skipDirs[name]; path != root && (name[0] == '.' || skip) {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip symlinks to prevent arbitrary file corruption via symlink attacks
		// WalkDir provides type info directly without needing Lstat
		if d.Type()&os.ModeSymlink != 0 {
			return nil // Skip symlinks
		}

		if classifyFile(path) != fileKindUnknown {
			files = append(files, path)
		}

		return nil
	})

	return files, err
}
