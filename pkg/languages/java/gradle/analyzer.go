/*
Copyright 2025 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/analyzer"
)

// GradleAnalyzer implements dependency analysis for Gradle projects.
type GradleAnalyzer struct{}

// Regex patterns for parsing Gradle files
var (
	// Pattern for inline version catalog declarations: version("key", "version")
	inlineVersionCatalogPattern = regexp.MustCompile(`version\s*\(\s*["']([^"']+)["']\s*,\s*["']([^"']+)["']\s*\)`)

	// Pattern for string notation dependencies: implementation("group:artifact:version")
	stringDependencyPattern = regexp.MustCompile(`[a-zA-Z]+\s*\(\s*["']([^:]+):([^:]+):([^"']+)["']\s*\)`)

	// Pattern for version catalog references: implementation(libs.foo.bar)
	catalogReferencePattern = regexp.MustCompile(`[a-zA-Z]+\s*\(\s*libs\.([a-zA-Z0-9._-]+)\s*\)`)
)

// readFileContent is a helper to read file content with consistent error handling.
func readFileContent(ctx context.Context, path string) ([]byte, error) {
	log := clog.FromContext(ctx)
	log.Debugf("Reading file: %s", path)

	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	return content, nil
}

// Analyze analyzes a Gradle project's dependencies.
func (ga *GradleAnalyzer) Analyze(ctx context.Context, projectPath string) (*analyzer.AnalysisResult, error) {
	log := clog.FromContext(ctx)

	// Get absolute path
	absPath, err := filepath.Abs(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	log.Debugf("Analyzing Gradle project: %s", absPath)

	result := &analyzer.AnalysisResult{
		Language:      "java",
		Dependencies:  make(map[string]*analyzer.DependencyInfo),
		Properties:    make(map[string]string), // Version catalog keys
		PropertyUsage: make(map[string]int),    // Catalog key usage count
		Metadata: map[string]any{
			"build_tool": "gradle",
		},
	}

	// Find all Gradle manifest files
	files, err := findBuildFiles(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to find build files: %w", err)
	}

	log.Infof("Found %d Gradle file(s) to analyze", len(files))

	// Parse version catalogs first (TOML and inline)
	for _, file := range files {
		filename := filepath.Base(file)
		switch filename {
		case "libs.versions.toml":
			if err := analyzeVersionCatalogToml(ctx, file, result); err != nil {
				log.Warnf("Failed to analyze %s: %v", file, err)
			}
		case "settings.gradle", "settings.gradle.kts":
			if err := analyzeSettingsGradle(ctx, file, result); err != nil {
				log.Warnf("Failed to analyze %s: %v", file, err)
			}
		}
	}

	// Parse build files for dependencies
	for _, file := range files {
		filename := filepath.Base(file)
		if filename == "build.gradle" || filename == "build.gradle.kts" {
			if err := analyzeBuildGradle(ctx, file, result); err != nil {
				log.Warnf("Failed to analyze %s: %v", file, err)
			}
		}
	}

	log.Infof("Analysis complete: found %d dependencies, %d using version catalogs",
		len(result.Dependencies), countCatalogUsage(result))

	return result, nil
}

// RecommendStrategy recommends an update strategy for given dependencies.
func (ga *GradleAnalyzer) RecommendStrategy(ctx context.Context, analysis *analyzer.AnalysisResult, deps []analyzer.Dependency) (*analyzer.Strategy, error) {
	log := clog.FromContext(ctx)

	log.Debugf("Determining update strategy for %d dependencies", len(deps))

	strategy := &analyzer.Strategy{
		DirectUpdates:        []analyzer.Dependency{},
		PropertyUpdates:      make(map[string]string), // Catalog key -> version
		Warnings:             []string{},
		AffectedDependencies: make(map[string][]string),
	}

	var missingCatalogKeys []string

	for _, dep := range deps {
		depKey := dep.Name
		log.Debugf("Checking dependency: %s @ %s", depKey, dep.Version)

		// Check if this dependency uses a version catalog
		if depInfo, exists := analysis.Dependencies[depKey]; exists && depInfo.UsesProperty {
			catalogKey := depInfo.PropertyName
			log.Debugf("  -> Dependency uses version catalog key: %s", catalogKey)

			// Check if we already have this catalog key
			if existingVersion, exists := strategy.PropertyUpdates[catalogKey]; exists {
				log.Warnf("Catalog key %s already set to %s, requested %s for %s",
					catalogKey, existingVersion, dep.Version, depKey)
			} else {
				strategy.PropertyUpdates[catalogKey] = dep.Version

				// Track affected dependencies
				affected := getAffectedDependenciesGradle(analysis, catalogKey)
				strategy.AffectedDependencies[catalogKey] = affected

				// Check if catalog key is actually defined
				if currentValue, exists := analysis.Properties[catalogKey]; exists {
					log.Infof("Will update version catalog key %s from %s to %s", catalogKey, currentValue, dep.Version)
				} else {
					log.Warnf("Catalog key %s is referenced but not found in version catalogs", catalogKey)
					missingCatalogKeys = append(missingCatalogKeys, catalogKey)
				}
			}
		} else {
			// Direct update in build file
			if _, exists := analysis.Dependencies[depKey]; exists {
				log.Debugf("  -> Dependency found but doesn't use version catalogs")
			} else {
				log.Debugf("  -> Dependency not found (may be transitive or new)")
			}
			strategy.DirectUpdates = append(strategy.DirectUpdates, dep)
			log.Infof("Will directly update %s to %s", depKey, dep.Version)
		}
	}

	if len(missingCatalogKeys) > 0 {
		strategy.Warnings = append(strategy.Warnings,
			fmt.Sprintf("Catalog keys referenced but not found: %s (may be in external version catalog)",
				strings.Join(missingCatalogKeys, ", ")))
	}

	log.Infof("Strategy: %d direct updates, %d version catalog updates",
		len(strategy.DirectUpdates), len(strategy.PropertyUpdates))

	return strategy, nil
}

// analyzeVersionCatalogToml parses a TOML version catalog file.
func analyzeVersionCatalogToml(ctx context.Context, path string, result *analyzer.AnalysisResult) error {
	log := clog.FromContext(ctx)

	content, err := readFileContent(ctx, path)
	if err != nil {
		return err
	}

	var catalog map[string]any
	if err := toml.Unmarshal(content, &catalog); err != nil {
		return fmt.Errorf("failed to parse TOML: %w", err)
	}

	extractVersionCatalogKeys(catalog, result, log)
	extractLibraryDefinitions(catalog, result, log)

	return nil
}

// extractVersionCatalogKeys extracts version keys from TOML catalog.
func extractVersionCatalogKeys(catalog map[string]any, result *analyzer.AnalysisResult, log *clog.Logger) {
	versions, ok := catalog["versions"].(map[string]any)
	if !ok {
		return
	}

	for key, value := range versions {
		if version, ok := value.(string); ok {
			result.Properties[key] = version
			log.Debugf("Found version catalog key: %s = %s", key, version)
		}
	}
}

// extractLibraryDefinitions extracts library definitions from TOML catalog.
func extractLibraryDefinitions(catalog map[string]any, result *analyzer.AnalysisResult, log *clog.Logger) {
	libraries, ok := catalog["libraries"].(map[string]any)
	if !ok {
		return
	}

	for alias, libDef := range libraries {
		libMap, ok := libDef.(map[string]any)
		if !ok {
			continue
		}

		groupID, artifactID := parseLibraryModule(libMap)
		if groupID == "" || artifactID == "" {
			continue
		}

		version := parseLibraryVersion(libMap, log, alias)
		depKey := fmt.Sprintf("%s:%s", groupID, artifactID)

		result.Dependencies[depKey] = &analyzer.DependencyInfo{
			Name:           depKey,
			Version:        version,
			UsesProperty:   false, // This is the catalog definition itself
			UpdateStrategy: "catalog",
			Metadata: map[string]any{
				"catalog_alias": alias,
			},
		}
	}
}

// parseLibraryModule extracts groupId and artifactId from library module definition.
func parseLibraryModule(libMap map[string]any) (groupID, artifactID string) {
	module, ok := libMap["module"].(string)
	if !ok {
		return "", ""
	}

	parts := strings.Split(module, ":")
	if len(parts) != 2 {
		return "", ""
	}

	return parts[0], parts[1]
}

// parseLibraryVersion extracts version from library definition.
func parseLibraryVersion(libMap map[string]any, log *clog.Logger, alias string) string {
	// Check for direct version
	if v, ok := libMap["version"].(string); ok {
		return v
	}

	// Check for version reference
	if vRef, ok := libMap["version"].(map[string]any); ok {
		if ref, ok := vRef["ref"].(string); ok {
			log.Debugf("Library %s references version key: %s", alias, ref)
		}
	}

	return ""
}

// analyzeSettingsGradle parses settings.gradle for inline version catalogs.
func analyzeSettingsGradle(ctx context.Context, path string, result *analyzer.AnalysisResult) error {
	log := clog.FromContext(ctx)

	content, err := readFileContent(ctx, path)
	if err != nil {
		return err
	}

	matches := inlineVersionCatalogPattern.FindAllStringSubmatch(string(content), -1)

	for _, match := range matches {
		if len(match) >= 3 {
			key := match[1]
			version := match[2]
			result.Properties[key] = version
			log.Debugf("Found inline version catalog key: %s = %s", key, version)
		}
	}

	return nil
}

// analyzeBuildGradle parses build.gradle files for dependency declarations.
func analyzeBuildGradle(ctx context.Context, path string, result *analyzer.AnalysisResult) error {
	log := clog.FromContext(ctx)

	content, err := readFileContent(ctx, path)
	if err != nil {
		return err
	}

	text := string(content)

	parseStringDependencies(text, result, log)
	parseCatalogReferences(text, result, log)

	return nil
}

// parseStringDependencies extracts direct dependency declarations.
func parseStringDependencies(text string, result *analyzer.AnalysisResult, log *clog.Logger) {
	matches := stringDependencyPattern.FindAllStringSubmatch(text, -1)

	for _, match := range matches {
		if len(match) < 4 {
			continue
		}

		groupID := match[1]
		artifactID := match[2]
		version := match[3]
		depKey := fmt.Sprintf("%s:%s", groupID, artifactID)

		result.Dependencies[depKey] = &analyzer.DependencyInfo{
			Name:           depKey,
			Version:        version,
			UsesProperty:   false,
			UpdateStrategy: "direct",
			Metadata: map[string]any{
				"groupId":    groupID,
				"artifactId": artifactID,
			},
		}
		log.Debugf("Found dependency: %s @ %s", depKey, version)
	}
}

// parseCatalogReferences extracts version catalog references and links them to definitions.
func parseCatalogReferences(text string, result *analyzer.AnalysisResult, log *clog.Logger) {
	matches := catalogReferencePattern.FindAllStringSubmatch(text, -1)

	for _, match := range matches {
		if len(match) < 2 {
			continue
		}

		catalogRef := match[1]
		catalogKey := strings.ReplaceAll(catalogRef, ".", "-")

		log.Debugf("Found catalog reference: libs.%s (key: %s)", catalogRef, catalogKey)
		result.PropertyUsage[catalogKey]++

		linkCatalogToDependency(result, catalogRef, catalogKey, log)
	}
}

// linkCatalogToDependency links a catalog reference to its dependency definition.
func linkCatalogToDependency(result *analyzer.AnalysisResult, catalogRef, catalogKey string, log *clog.Logger) {
	for depKey, depInfo := range result.Dependencies {
		alias, ok := depInfo.Metadata["catalog_alias"].(string)
		if !ok {
			continue
		}

		if alias == catalogKey || alias == catalogRef {
			depInfo.UsesProperty = true
			depInfo.PropertyName = catalogKey
			depInfo.UpdateStrategy = "catalog"
			result.PropertyUsage[catalogKey]++
			log.Debugf("Linked dependency %s to catalog key %s", depKey, catalogKey)
		}
	}
}

// countCatalogUsage counts how many dependencies use version catalogs.
func countCatalogUsage(result *analyzer.AnalysisResult) int {
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
