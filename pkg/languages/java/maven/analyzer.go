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

// pomFileProperties holds the properties declared in a single POM file.
type pomFileProperties struct {
	// PomFile is the path to the POM file (absolute when returned by searchForProperties,
	// relative to the analysis root when returned by resolveUnknownProperties).
	PomFile string
	// Properties maps property names to their current values for this file.
	Properties map[string]string
}

// Analyze performs dependency analysis on a Maven project.
func (ma *MavenAnalyzer) Analyze(ctx context.Context, projectPath string) (*analyzer.AnalysisResult, error) {
	log := clog.FromContext(ctx)

	// Get absolute path
	absPath, err := filepath.Abs(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	log.Debugf("Analyzing Maven project: %s", absPath)

	if info, err := os.Stat(absPath); err == nil && info.IsDir() {
		return ma.analyzeAllPoms(ctx, absPath)
	}

	// Parse the POM
	project, err := gopom.Parse(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse POM file: %w", err)
	}

	baseDir := filepath.Dir(absPath)

	result := &analyzer.AnalysisResult{
		Language:        mavenLanguageName,
		Dependencies:    make(map[string]*analyzer.DependencyInfo),
		Properties:      make(map[string]string),
		PropertySources: make(map[string]string),
		PropertyUsage:   make(map[string]int),
		Metadata:        make(map[string]any),
	}

	// Extract properties from the target POM and record their source file.
	result.Properties = extractPropertiesFromProject(project)
	propertySource := filepath.Base(absPath)
	for k := range result.Properties {
		result.PropertySources[k] = propertySource
	}

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

	// Search for additional properties in project tree and record their source files.
	// baseDir bounds the upward walk so we do not read outside the analyzed project.
	additionalPomFiles := searchForProperties(ctx, filepath.Dir(absPath), absPath, baseDir)
	log.Debugf("Property search found properties in %d nearby POMs", len(additionalPomFiles))

	// Merge additional properties; first definition wins.
	for _, pf := range additionalPomFiles {
		for k, v := range pf.Properties {
			if mergeProperty(ctx, result, k, v, relPath(baseDir, pf.PomFile)) {
				log.Infof("Found property %s = %s in nearby POM", k, v)
			}
		}
	}

	// For any property referenced by dependencies but still not found, follow the
	// <parent><relativePath> chain using the same resolver the updater uses — so
	// the analyzer and updater always agree on where a property lives.
	for _, pf := range resolveUnknownProperties(ctx, result.PropertyUsage, result.Properties, absPath, baseDir) {
		for propName, value := range pf.Properties {
			result.Properties[propName] = value
			result.PropertySources[propName] = pf.PomFile
			log.Infof("Found property %s = %s in parent POM %s", propName, value, pf.PomFile)
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
		depInfo, exists := analysis.Dependencies[depKey]
		if exists && depInfo.UsesProperty {
			handlePropertyUpdate(log, depKey, dep, depInfo, analysis, strategy, &missingProperties)
		} else {
			handleDirectPatch(log, depKey, dep, exists, strategy)
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

// handlePropertyUpdate processes a dependency that uses a Maven property.
func handlePropertyUpdate(log *clog.Logger, depKey string, dep analyzer.Dependency, depInfo *analyzer.DependencyInfo, analysis *analyzer.AnalysisResult, strategy *analyzer.Strategy, missingProps *[]string) {
	propertyName := depInfo.PropertyName
	log.Debugf("  -> Dependency uses property ${%s}", propertyName)

	// Check if we already have this property
	if existingVersion, exists := strategy.PropertyUpdates[propertyName]; exists {
		log.Warnf("Property %s already set to %s, requested %s for %s",
			propertyName, existingVersion, dep.Version, depKey)
		return
	}

	strategy.PropertyUpdates[propertyName] = dep.Version

	// Track affected dependencies
	affected := getAffectedDependencies(analysis, propertyName)
	strategy.AffectedDependencies[propertyName] = affected

	// Check if property is actually defined
	if currentValue, exists := analysis.Properties[propertyName]; exists {
		log.Infof("Will update property %s from %s to %s", propertyName, currentValue, dep.Version)
	} else {
		log.Warnf("Property %s is referenced but not found - may be in external parent POM", propertyName)
		*missingProps = append(*missingProps, propertyName)
	}
}

// handleDirectPatch processes a dependency that requires a direct patch.
func handleDirectPatch(log *clog.Logger, depKey string, dep analyzer.Dependency, exists bool, strategy *analyzer.Strategy) {
	// Direct patch
	if exists {
		log.Debugf("  -> Dependency found but doesn't use properties")
	} else {
		log.Debugf("  -> Dependency not found (may be from BOM or new)")
	}
	strategy.DirectUpdates = append(strategy.DirectUpdates, dep)
	log.Infof("Will directly patch %s to %s", depKey, dep.Version)
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
// Returns one entry per POM file that declares at least one property, each carrying
// the file's absolute path and its full property map. rootDir bounds the upward walk.
func searchForProperties(ctx context.Context, startDir, excludePath, rootDir string) []pomFileProperties {
	log := clog.FromContext(ctx)
	var results []pomFileProperties

	projectRoot := findProjectRoot(startDir, rootDir)
	log.Debugf("Starting property search from project root: %s", projectRoot)

	pomFilesChecked := 0
	for _, path := range walkXMLFiles(projectRoot) {
		absPath, _ := filepath.Abs(path)
		if absPath == excludePath {
			continue
		}
		project, err := gopom.Parse(path)
		if err != nil {
			continue
		}
		props := extractPropertiesFromProject(project)
		if len(props) == 0 {
			continue
		}
		pomFilesChecked++
		results = append(results, pomFileProperties{PomFile: absPath, Properties: props})
	}

	if log.Enabled(context.Background(), slog.LevelDebug) {
		log.Debugf("Property search checked %d POMs, found properties in %d", pomFilesChecked, len(results))
	}

	return results
}

// relPath returns filePath relative to baseDir, falling back to the absolute
// path if the relative form cannot be computed.
func relPath(baseDir, filePath string) string {
	rel, err := filepath.Rel(baseDir, filePath)
	if err != nil {
		return filePath
	}
	return rel
}

// mergeProperty adds key→value/sourceFile to result if key is not already present.
// When the key already exists with a different value, a warning is logged and the
// existing definition is kept. Returns true when the property was newly added.
func mergeProperty(ctx context.Context, result *analyzer.AnalysisResult, key, value, sourceFile string) bool {
	if existing, exists := result.Properties[key]; exists {
		if existing != value {
			clog.WarnContextf(ctx, "Property %s is defined with different values in %s (%s) and %s (%s); keeping first definition",
				key, result.PropertySources[key], existing, sourceFile, value)
		}
		return false
	}
	result.Properties[key] = value
	result.PropertySources[key] = sourceFile
	return true
}

// resolveUnknownProperties looks up every property in usage that is not already
// in known, following the <parent><relativePath> chain from startPom. Returns
// one pomFileProperties per resolved property (PomFile relative to baseDir).
// Individual lookup failures are logged at debug level and skipped.
func resolveUnknownProperties(ctx context.Context, usage map[string]int, known map[string]string, startPom, baseDir string) []pomFileProperties {
	var results []pomFileProperties
	for propName := range usage {
		if _, found := known[propName]; found {
			continue
		}
		ownerPath, err := resolvePropertyPomPath(ctx, startPom, propName, baseDir)
		if err != nil {
			clog.FromContext(ctx).Debugf("Property %s not found in parent chain: %v", propName, err)
			continue
		}
		ownerProject, err := ParsePom(ownerPath)
		if err != nil {
			clog.FromContext(ctx).Debugf("Could not parse parent POM %s for property %s: %v", ownerPath, propName, err)
			continue
		}
		// resolvePropertyPomPath guarantees the property exists in ownerPath via
		// projectHasProperty, so Properties is non-nil and the key is present.
		results = append(results, pomFileProperties{
			PomFile:    relPath(baseDir, ownerPath),
			Properties: map[string]string{propName: ownerProject.Properties.Entries[propName]},
		})
	}
	return results
}

// findProjectRoot finds the root of the Maven project by walking up the directory
// tree as long as each parent contains a pom.xml. The walk stops when it would
// escape rootDir, preventing reads outside the project boundary.
func findProjectRoot(startDir, rootDir string) string {
	current := startDir
	projectRoot := startDir

	for {
		parent := filepath.Dir(current)
		if parent == current {
			break
		}

		// Stop climbing if the parent escapes the project root boundary.
		if err := validatePathWithinRoot(rootDir, parent); err != nil {
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

// isSkippableDirectory checks if a directory should be skipped during tree walks.
// Explicit VCS and build-output directories are skipped; other dot-prefixed directories
// (e.g. .build/) are intentionally allowed so non-standard project layouts are scanned.
func isSkippableDirectory(name string) bool {
	switch name {
	case ".git", ".svn", ".hg", ".bzr",
		"target", "node_modules",
		"build", "dist", "out",
		"testdata", "vendor", "test":
		return true
	}
	return false
}

// walkXMLFiles returns paths of all non-symlink .xml files under rootDir,
// skipping directories matched by isSkippableDirectory.
func walkXMLFiles(rootDir string) []string {
	var files []string
	_ = filepath.WalkDir(rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // skip unreadable entries
		}
		if d.IsDir() {
			if path != rootDir && isSkippableDirectory(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".xml") {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		files = append(files, path)
		return nil
	})
	return files
}

// findMavenPoms walks rootDir and returns the paths of all valid Maven POM XML files.
func findMavenPoms(rootDir string) []string {
	var poms []string
	for _, path := range walkXMLFiles(rootDir) {
		if ok, _ := IsMavenPom(path); ok {
			poms = append(poms, path)
		}
	}
	return poms
}

// hasMavenPom reports whether any valid Maven POM XML file exists under dir.
func hasMavenPom(dir string) bool {
	for _, path := range walkXMLFiles(dir) {
		if ok, _ := IsMavenPom(path); ok {
			return true
		}
	}
	return false
}

// analyzeAllPoms collects every Maven POM in the project tree via findMavenPoms,
// then parses each one and aggregates their dependencies and properties.
func (ma *MavenAnalyzer) analyzeAllPoms(ctx context.Context, rootDir string) (*analyzer.AnalysisResult, error) {
	log := clog.FromContext(ctx)
	log.Debugf("Scanning %s for Maven POMs", rootDir)

	pomPaths := findMavenPoms(rootDir)
	if len(pomPaths) == 0 {
		return nil, fmt.Errorf("%w in %s", ErrNoPOMsFound, rootDir)
	}

	result := &analyzer.AnalysisResult{
		Language:        mavenLanguageName,
		Dependencies:    make(map[string]*analyzer.DependencyInfo),
		Properties:      make(map[string]string),
		PropertySources: make(map[string]string),
		PropertyUsage:   make(map[string]int),
		Metadata:        make(map[string]any),
	}

	for _, pomPath := range pomPaths {
		log.Debugf("Analyzing Maven POM: %s", pomPath)
		project, err := gopom.Parse(pomPath)
		if err != nil {
			log.Debugf("Skipping %s: %v", pomPath, err)
			continue
		}
		for k, v := range extractPropertiesFromProject(project) {
			mergeProperty(ctx, result, k, v, relPath(rootDir, pomPath))
		}
		if project.Dependencies != nil {
			for _, dep := range *project.Dependencies {
				analyzeDependency(ctx, dep, result)
			}
		}
		if project.DependencyManagement != nil && project.DependencyManagement.Dependencies != nil {
			for _, dep := range *project.DependencyManagement.Dependencies {
				analyzeDependency(ctx, dep, result)
			}
		}
	}

	log.Infof("Analysis complete: found %d Maven POMs, %d dependencies, %d using properties",
		len(pomPaths), len(result.Dependencies), countPropertiesUsage(result))

	// For any property referenced by dependencies but not found in the scanned
	// POMs, follow the <parent><relativePath> chain from the root pom.xml using
	// the same resolver the updater uses. This surfaces properties in parent POMs
	// outside the project tree (e.g. ../pom.xml).
	rootPom := filepath.Join(rootDir, DefaultManifestFile)
	for _, pf := range resolveUnknownProperties(ctx, result.PropertyUsage, result.Properties, rootPom, rootDir) {
		for propName, value := range pf.Properties {
			result.Properties[propName] = value
			result.PropertySources[propName] = pf.PomFile
			log.Infof("Found property %s = %s in parent POM %s", propName, value, pf.PomFile)
		}
	}

	return result, nil
}

// extractPropertiesFromProject extracts properties from a parsed POM.
func extractPropertiesFromProject(project *gopom.Project) map[string]string {
	properties := make(map[string]string)
	if project != nil && project.Properties != nil && project.Properties.Entries != nil {
		maps.Copy(properties, project.Properties.Entries)
	}
	return properties
}
