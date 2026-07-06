/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradle

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/analyzer"
	"github.com/chainguard-dev/omnibump/pkg/gradlefile"
)

// GradleAnalyzer implements dependency analysis for Gradle projects.
//
//nolint:revive // Explicit name preferred for clarity
type GradleAnalyzer struct{}

var _ analyzer.Analyzer = (*GradleAnalyzer)(nil)

// Analyze analyzes a Gradle project's dependencies. Version catalog keys and
// version variables are surfaced as properties (with their defining file in
// PropertySources); dependencies declared through a catalog reference or a
// variable interpolation report UsesProperty with the key they resolve
// through, mirroring how the Maven analyzer reports ${property} versions.
func (ga *GradleAnalyzer) Analyze(ctx context.Context, projectPath string) (*analyzer.AnalysisResult, error) {
	absPath, err := filepath.Abs(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	clog.DebugContextf(ctx, "Analyzing Gradle project: %s", absPath)

	model, err := buildProjectModel(ctx, absPath)
	if err != nil {
		return nil, err
	}

	result := analyzeModel(model)

	clog.InfoContextf(ctx, "Analysis complete: found %d dependencies, %d using version catalogs or variables",
		len(result.Dependencies), countPropertyUsage(result))

	return result, nil
}

// AnalyzeRemote performs dependency analysis on remotely-fetched Gradle files.
// It mirrors Analyze, building the project model from the in-memory file
// contents instead of disk, and returns a single aggregated FileAnalysis the
// way the Maven analyzer aggregates all pom.xml files into one.
func (ga *GradleAnalyzer) AnalyzeRemote(ctx context.Context, files map[string][]byte) (*analyzer.RemoteAnalysisResult, error) {
	model := buildModelFromFiles(ctx, files)
	if len(model.sortedFiles) == 0 {
		return nil, ErrNoBuildFiles
	}

	result := analyzeModel(model)

	clog.InfoContextf(ctx, "Remote analysis: %d Gradle files, %d dependencies, %d using version catalogs or variables",
		len(model.sortedFiles), len(result.Dependencies), countPropertyUsage(result))

	return &analyzer.RemoteAnalysisResult{
		Language: result.Language,
		FileAnalyses: []analyzer.FileAnalysis{{
			FilePath: remoteFilePath(model),
			Analysis: result,
		}},
		Metadata: map[string]any{"buildTool": gradleToolName},
	}, nil
}

// analyzeModel builds the AnalysisResult for a fully-indexed project model.
// Shared by Analyze and AnalyzeRemote so both surface dependencies, version
// catalog keys and variables identically.
func analyzeModel(model *projectModel) *analyzer.AnalysisResult {
	result := &analyzer.AnalysisResult{
		Language:        "java",
		Dependencies:    make(map[string]*analyzer.DependencyInfo),
		Properties:      make(map[string]string),
		PropertySources: make(map[string]string),
		PropertyUsage:   make(map[string]int),
		Metadata:        map[string]any{"buildTool": gradleToolName},
	}

	collectProperties(model, result)
	collectCatalogDependencies(model, result)
	collectDeclaredDependencies(model, result)
	countCatalogReferences(model, result)

	// Surface configurations bundled into shipped artifacts beyond the
	// compile/runtime classpaths (e.g. a shadowJar's custom configuration), so
	// callers know the managed pins will also be forced there — and which
	// bundling sites omnibump could not resolve to a name (operator must pin
	// those explicitly). Carried in Metadata to keep AnalysisResult stable.
	if ship := model.shipConfigurations(); len(ship) > 0 {
		result.Metadata["ship_configurations"] = ship
	}
	if unresolved := model.unresolvedShipConfigs(); len(unresolved) > 0 {
		raws := make([]string, 0, len(unresolved))
		for _, ref := range unresolved {
			raws = append(raws, ref.Raw)
		}
		result.Metadata["unresolved_ship_configurations"] = raws
	}

	return result
}

// remoteFilePath picks a representative path for the single aggregated remote
// FileAnalysis: the root build script when the model has one, else the first
// parsed file, else "build.gradle".
func remoteFilePath(model *projectModel) string {
	if build := model.rootBuildFile(); build != nil {
		return relativeTo(model.rootDir, build.Path())
	}
	if len(model.sortedFiles) > 0 {
		return relativeTo(model.rootDir, model.sortedFiles[0])
	}
	return buildGradleFile
}

// collectProperties surfaces catalog version keys and version variables as
// analysis properties with their defining files.
func collectProperties(model *projectModel, result *analyzer.AnalysisResult) {
	for _, key := range slices.Sorted(maps.Keys(model.catalogVersionSites)) {
		site := model.catalogVersionSites[key][0]
		result.Properties[key] = site.version.Value
		result.PropertySources[key] = relativeTo(model.rootDir, site.path())
	}
	for _, name := range slices.Sorted(maps.Keys(model.variableSites)) {
		if _, exists := result.Properties[name]; exists {
			continue
		}
		site := model.variableSites[name][0]
		result.Properties[name] = site.value()
		result.PropertySources[name] = relativeTo(model.rootDir, site.path())
	}
}

// collectCatalogDependencies records one dependency per catalog library.
func collectCatalogDependencies(model *projectModel, result *analyzer.AnalysisResult) {
	for _, module := range slices.Sorted(maps.Keys(model.catalogLibrarySites)) {
		library := model.catalogLibrarySites[module][0].library
		info := &analyzer.DependencyInfo{
			Name:           module,
			Version:        model.resolveCatalogValue(library),
			UpdateStrategy: "catalog",
			Metadata: map[string]any{
				"catalog_alias": library.Alias,
			},
		}
		if library.VersionRef != "" {
			info.UsesProperty = true
			info.PropertyName = library.VersionRef
			result.PropertyUsage[library.VersionRef]++
		}
		result.Dependencies[module] = info
	}
}

// collectDeclaredDependencies records dependencies declared directly in
// build scripts. Catalog entries win when the same module appears in both.
func collectDeclaredDependencies(model *projectModel, result *analyzer.AnalysisResult) {
	for _, module := range slices.Sorted(maps.Keys(model.declarationSites)) {
		if _, exists := result.Dependencies[module]; exists {
			continue
		}
		decl := model.declarationSites[module][0].decl
		info := &analyzer.DependencyInfo{
			Name:           module,
			Version:        decl.Version,
			UpdateStrategy: "direct",
			Metadata: map[string]any{
				"groupId":    decl.Group,
				"artifactId": decl.Artifact,
			},
		}
		if decl.VarRef != "" {
			info.UsesProperty = true
			info.PropertyName = decl.VarRef
			info.UpdateStrategy = "property"
			if sites := model.variableSites[decl.VarRef]; len(sites) > 0 {
				info.Version = sites[0].value()
			}
			result.PropertyUsage[decl.VarRef]++
		}
		result.Dependencies[module] = info
	}
}

// countCatalogReferences counts libs.x.y accessor usages in build scripts
// against the version key each referenced library resolves through.
func countCatalogReferences(model *projectModel, result *analyzer.AnalysisResult) {
	for _, path := range model.sortedFiles {
		build, ok := model.builds[path]
		if !ok {
			continue
		}
		for _, decl := range build.Dependencies() {
			if decl.Kind != gradlefile.CatalogRef {
				continue
			}
			module, ok := model.aliasModules[gradlefile.NormalizeAlias(decl.CatalogAlias)]
			if !ok {
				continue
			}
			for _, site := range model.catalogLibrarySites[module] {
				if site.library.VersionRef != "" {
					result.PropertyUsage[site.library.VersionRef]++
				}
			}
		}
	}
}

// RecommendStrategy recommends an update strategy for given dependencies.
func (ga *GradleAnalyzer) RecommendStrategy(ctx context.Context, analysis *analyzer.AnalysisResult, deps []analyzer.Dependency) (*analyzer.Strategy, error) {
	clog.DebugContextf(ctx, "Determining update strategy for %d dependencies", len(deps))

	strategy := &analyzer.Strategy{
		DirectUpdates:        []analyzer.Dependency{},
		PropertyUpdates:      make(map[string]string), // Catalog key -> version
		Warnings:             []string{},
		AffectedDependencies: make(map[string][]string),
	}

	var missingCatalogKeys []string

	for _, dep := range deps {
		depKey := dep.Name
		clog.DebugContextf(ctx, "Checking dependency: %s @ %s", depKey, dep.Version)

		// Check if this dependency uses a version catalog
		depInfo, exists := analysis.Dependencies[depKey]
		if exists && depInfo.UsesProperty {
			handleCatalogUpdate(ctx, depKey, dep, depInfo, analysis, strategy, &missingCatalogKeys)
		} else {
			handleDirectUpdate(ctx, depKey, dep, exists, strategy)
		}
	}

	if len(missingCatalogKeys) > 0 {
		strategy.Warnings = append(strategy.Warnings,
			fmt.Sprintf("Catalog keys referenced but not found: %s (may be in external version catalog)",
				strings.Join(missingCatalogKeys, ", ")))
	}

	clog.InfoContextf(ctx, "Strategy: %d direct updates, %d version catalog updates",
		len(strategy.DirectUpdates), len(strategy.PropertyUpdates))

	return strategy, nil
}

// handleCatalogUpdate processes a dependency that uses a version catalog.
func handleCatalogUpdate(ctx context.Context, depKey string, dep analyzer.Dependency, depInfo *analyzer.DependencyInfo, analysis *analyzer.AnalysisResult, strategy *analyzer.Strategy, missingKeys *[]string) {
	catalogKey := depInfo.PropertyName
	clog.DebugContextf(ctx, "  -> Dependency uses version catalog key: %s", catalogKey)

	// Check if we already have this catalog key
	if existingVersion, exists := strategy.PropertyUpdates[catalogKey]; exists {
		clog.WarnContextf(ctx, "Catalog key %s already set to %s, requested %s for %s",
			catalogKey, existingVersion, dep.Version, depKey)
		return
	}

	strategy.PropertyUpdates[catalogKey] = dep.Version

	// Track affected dependencies
	affected := getAffectedDependenciesGradle(analysis, catalogKey)
	strategy.AffectedDependencies[catalogKey] = affected

	// Check if catalog key is actually defined
	if currentValue, exists := analysis.Properties[catalogKey]; exists {
		clog.InfoContextf(ctx, "Will update version catalog key %s from %s to %s", catalogKey, currentValue, dep.Version)
	} else {
		clog.WarnContextf(ctx, "Catalog key %s is referenced but not found in version catalogs", catalogKey)
		*missingKeys = append(*missingKeys, catalogKey)
	}
}

// handleDirectUpdate processes a dependency that requires a direct update.
func handleDirectUpdate(ctx context.Context, depKey string, dep analyzer.Dependency, exists bool, strategy *analyzer.Strategy) {
	// Direct update in build file
	if exists {
		clog.DebugContextf(ctx, "  -> Dependency found but doesn't use version catalogs")
	} else {
		clog.DebugContextf(ctx, "  -> Dependency not found (may be transitive or new)")
	}
	strategy.DirectUpdates = append(strategy.DirectUpdates, dep)
	clog.InfoContextf(ctx, "Will directly update %s to %s", depKey, dep.Version)
}

// countPropertyUsage counts how many dependencies resolve their version
// through a property (catalog key or variable).
func countPropertyUsage(result *analyzer.AnalysisResult) int {
	count := 0
	for _, dep := range result.Dependencies {
		if dep.UsesProperty {
			count++
		}
	}
	return count
}

// getAffectedDependenciesGradle returns dependency keys that use a given catalog key.
func getAffectedDependenciesGradle(analysis *analyzer.AnalysisResult, catalogKey string) []string {
	var affected []string
	for key, dep := range analysis.Dependencies {
		if dep.UsesProperty && dep.PropertyName == catalogKey {
			affected = append(affected, key)
		}
	}
	return affected
}

// relativeTo returns path relative to root, falling back to path itself.
func relativeTo(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}
