/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package maven

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/gopom"
	"github.com/chainguard-dev/omnibump/pkg/analyzer"
)

// MavenAnalyzer implements the Analyzer interface for Maven projects.
// Ported from pombump/pkg/analyzer.go.
//
//nolint:revive // Explicit name preferred for clarity
type MavenAnalyzer struct{}

// Analyze performs dependency analysis on a Maven project.
func (ma *MavenAnalyzer) Analyze(ctx context.Context, projectPath string) (*analyzer.AnalysisResult, error) {
	log := clog.FromContext(ctx)

	// Get absolute path
	absPath, err := filepath.Abs(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	log.Debugf("Analyzing Maven project: %s", absPath)

	// Determine POM file path
	pomPath := absPath
	if info, err := os.Stat(absPath); err == nil && info.IsDir() {
		pomPath = filepath.Join(absPath, "pom.xml")
	}

	// Parse the POM
	project, err := gopom.Parse(pomPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse POM file: %w", err)
	}

	result := &analyzer.AnalysisResult{
		Language:      "maven",
		Dependencies:  make(map[string]*analyzer.DependencyInfo),
		Properties:    make(map[string]string),
		PropertyUsage: make(map[string]int),
		Metadata:      make(map[string]any),
	}

	// Extract properties
	result.Properties = extractPropertiesFromProject(project)

	// Analyze regular dependencies
	if project.Dependencies != nil {
		for _, dep := range *project.Dependencies {
			analyzeDependency(ctx, dep, result)
		}
	}

	// Analyze dependency management
	if project.DependencyManagement != nil && project.DependencyManagement.Dependencies != nil {
		for _, dep := range *project.DependencyManagement.Dependencies {
			analyzeDependency(ctx, dep, result)
		}
	}

	log.Infof("Analysis complete: found %d dependencies, %d using properties",
		len(result.Dependencies), countPropertiesUsage(result))

	// Search for additional properties in project tree
	dir := filepath.Dir(absPath)
	additionalProps := searchForProperties(ctx, dir, absPath)
	log.Debugf("Property search found %d additional properties", len(additionalProps))

	// Merge additional properties
	for k, v := range additionalProps {
		if _, exists := result.Properties[k]; !exists {
			result.Properties[k] = v
			log.Infof("Found property %s = %s in nearby POM", k, v)
		}
	}

	return result, nil
}

// AnalyzeRemote performs dependency analysis on remotely-fetched Maven files.
// Not yet implemented for Maven - returns error.
// TODO: Implement this function and use ctx for logging and files for analysis.
//
//nolint:revive // Parameters will be used when implementation is added
func (ma *MavenAnalyzer) AnalyzeRemote(ctx context.Context, files map[string][]byte) (*analyzer.RemoteAnalysisResult, error) {
	return nil, fmt.Errorf("%w for Maven", ErrRemoteAnalysisNotImplemented)
}

// RecommendStrategy suggests whether to use properties or direct patches.
func (ma *MavenAnalyzer) RecommendStrategy(ctx context.Context, analysis *analyzer.AnalysisResult, deps []analyzer.Dependency) (*analyzer.Strategy, error) {
	log := clog.FromContext(ctx)

	log.Debugf("Determining patch strategy for %d dependencies", len(deps))

	strategy := &analyzer.Strategy{
		DirectUpdates:        []analyzer.Dependency{},
		PropertyUpdates:      make(map[string]string),
		Warnings:             []string{},
		AffectedDependencies: make(map[string][]string),
	}

	var missingProperties []string

	for _, dep := range deps {
		depKey := dep.Name
		if depKey == "" {
			// Try to construct from metadata
			if groupID, ok := dep.Metadata["groupId"].(string); ok {
				if artifactID, ok := dep.Metadata["artifactId"].(string); ok {
					depKey = fmt.Sprintf("%s:%s", groupID, artifactID)
				}
			}
		}

		log.Debugf("Checking dependency: %s @ %s", depKey, dep.Version)

		// Check if this dependency uses a property
		if depInfo, exists := analysis.Dependencies[depKey]; exists && depInfo.UsesProperty {
			propertyName := depInfo.PropertyName
			log.Debugf("  -> Dependency uses property ${%s}", propertyName)

			// Check if we already have this property
			if existingVersion, exists := strategy.PropertyUpdates[propertyName]; exists {
				log.Warnf("Property %s already set to %s, requested %s for %s",
					propertyName, existingVersion, dep.Version, depKey)
			} else {
				strategy.PropertyUpdates[propertyName] = dep.Version

				// Track affected dependencies
				affected := getAffectedDependencies(analysis, propertyName)
				strategy.AffectedDependencies[propertyName] = affected

				// Check if property is actually defined
				if currentValue, exists := analysis.Properties[propertyName]; exists {
					log.Infof("Will update property %s from %s to %s", propertyName, currentValue, dep.Version)
				} else {
					log.Warnf("Property %s is referenced but not found - may be in external parent POM", propertyName)
					missingProperties = append(missingProperties, propertyName)
				}
			}
		} else {
			// Direct patch
			if _, exists := analysis.Dependencies[depKey]; exists {
				log.Debugf("  -> Dependency found but doesn't use properties")
			} else {
				log.Debugf("  -> Dependency not found (may be from BOM or new)")
			}
			strategy.DirectUpdates = append(strategy.DirectUpdates, dep)
			log.Infof("Will directly patch %s to %s", depKey, dep.Version)
		}
	}

	if len(missingProperties) > 0 {
		strategy.Warnings = append(strategy.Warnings,
			fmt.Sprintf("Properties referenced but not found: %s (may be in external parent POM)",
				strings.Join(missingProperties, ", ")))
	}

	log.Infof("Strategy: %d direct patches, %d property updates", len(strategy.DirectUpdates), len(strategy.PropertyUpdates))

	return strategy, nil
}

// analyzeDependency analyzes a single Maven dependency.
func analyzeDependency(ctx context.Context, dep gopom.Dependency, result *analyzer.AnalysisResult) {
	log := clog.FromContext(ctx)

	depKey := fmt.Sprintf("%s:%s", dep.GroupID, dep.ArtifactID)

	info := &analyzer.DependencyInfo{
		Name:     depKey,
		Version:  dep.Version,
		Metadata: make(map[string]any),
	}

	// Store Maven-specific metadata
	info.Metadata["groupId"] = dep.GroupID
	info.Metadata["artifactId"] = dep.ArtifactID
	if dep.Scope != "" {
		info.Metadata["scope"] = dep.Scope
	}
	if dep.Type != "" {
		info.Metadata["type"] = dep.Type
	}

	// Check if version uses a property reference
	if strings.HasPrefix(dep.Version, "${") && strings.HasSuffix(dep.Version, "}") {
		propertyName := strings.TrimSuffix(strings.TrimPrefix(dep.Version, "${"), "}")
		info.UsesProperty = true
		info.PropertyName = propertyName
		info.UpdateStrategy = "property"
		result.PropertyUsage[propertyName]++

		log.Debugf("Dependency %s uses property %s (total usage: %d)",
			depKey, propertyName, result.PropertyUsage[propertyName])
	} else {
		info.UpdateStrategy = "direct"
	}

	result.Dependencies[depKey] = info
}

// countPropertiesUsage counts how many dependencies use properties.
func countPropertiesUsage(result *analyzer.AnalysisResult) int {
	count := 0
	for _, dep := range result.Dependencies {
		if dep.UsesProperty {
			count++
		}
	}
	return count
}

// getAffectedDependencies returns dependency keys that use a given property.
func getAffectedDependencies(analysis *analyzer.AnalysisResult, propertyName string) []string {
	var affected []string
	for key, dep := range analysis.Dependencies {
		if dep.UsesProperty && dep.PropertyName == propertyName {
			affected = append(affected, key)
		}
	}
	return affected
}

// searchForProperties recursively searches for properties in the Maven project tree.
func searchForProperties(ctx context.Context, startDir string, excludePath string) map[string]string {
	log := clog.FromContext(ctx)
	properties := make(map[string]string)

	projectRoot := findProjectRoot(startDir)
	log.Debugf("Starting property search from project root: %s", projectRoot)

	pomFilesChecked := 0

	// Use WalkDir instead of Walk - it doesn't follow symlinks and provides type info directly
	err := filepath.WalkDir(projectRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // Intentionally skip directories with errors
		}

		// Skip directories
		if d.IsDir() {
			if isSkippableDirectory(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip symlinks to prevent following malicious symlinks
		// WalkDir provides type info directly without needing Lstat
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}

		// Only process XML files
		if !strings.HasSuffix(d.Name(), ".xml") {
			return nil
		}

		// Skip the file we're analyzing
		if absPath, _ := filepath.Abs(path); absPath == excludePath {
			return nil
		}

		// Try to parse as POM
		project, err := gopom.Parse(path)
		if err != nil {
			// Not a valid POM file, skip it
			return nil //nolint:nilerr // Intentionally skip non-POM XML files
		}

		pomFilesChecked++
		pomProperties := extractPropertiesFromProject(project)
		for k, v := range pomProperties {
			if _, exists := properties[k]; !exists {
				properties[k] = v
			}
		}

		return nil
	})
	if err != nil {
		log.Warnf("Error walking project tree: %v", err)
	}

	if log.Enabled(context.Background(), slog.LevelDebug) {
		log.Debugf("Property search checked %d POMs, found %d properties", pomFilesChecked, len(properties))
	}

	return properties
}

// findProjectRoot finds the root of the Maven project.
func findProjectRoot(startDir string) string {
	current := startDir
	projectRoot := startDir

	for {
		parent := filepath.Dir(current)
		if parent == current {
			break
		}

		parentPom := filepath.Join(parent, "pom.xml")
		if _, err := os.Stat(parentPom); err == nil {
			projectRoot = parent
			current = parent
		} else {
			break
		}
	}

	return projectRoot
}

// isSkippableDirectory checks if a directory should be skipped.
func isSkippableDirectory(name string) bool {
	return strings.HasPrefix(name, ".") ||
		name == "target" ||
		name == "node_modules" ||
		name == "build" ||
		name == "dist" ||
		name == "out"
}

// extractPropertiesFromProject extracts properties from a parsed POM.
func extractPropertiesFromProject(project *gopom.Project) map[string]string {
	properties := make(map[string]string)
	if project != nil && project.Properties != nil && project.Properties.Entries != nil {
		maps.Copy(properties, project.Properties.Entries)
	}
	return properties
}
