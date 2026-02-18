/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package gradle implements Gradle build tool support for Java projects.
// Uses text-based parsing/updating to match real-world usage patterns.
package gradle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/analyzer"
	"github.com/chainguard-dev/omnibump/pkg/languages"
)

// Gradle implements the BuildTool interface for Gradle projects.
type Gradle struct{}

const (
	// File permissions for writing updated build files.
	gradleFilePerms = 0o600

	// Version group index constants for regex patterns.
	versionGroupOne = 1
	versionGroupTwo = 2
)

var (
	// versionValidationRegex defines the allowlist for valid version strings.
	// Only allows alphanumeric characters, dots, underscores, hyphens, and plus signs.
	// This prevents injection of quotes, parentheses, newlines, braces, and other
	// characters that could be used for code injection in Gradle build files.
	versionValidationRegex = regexp.MustCompile(`^[a-zA-Z0-9._+-]+$`)

	// ErrInvalidVersion is returned when a version string fails validation.
	ErrInvalidVersion = errors.New("invalid version string: contains disallowed characters")
)

// gradleManifestFiles lists all files that can contain dependency versions.
var gradleManifestFiles = map[string]bool{
	"build.gradle.kts":    true,
	"build.gradle":        true,
	"settings.gradle.kts": true,
	"settings.gradle":     true,
	"libs.versions.toml":  true,
}

// skipDirs lists directories to skip when walking the file tree.
var skipDirs = map[string]bool{
	"vendor":       true,
	"node_modules": true,
}

// validateVersion checks if a version string contains only safe characters.
// Returns an error if the version contains characters that could be used for
// code injection (quotes, parentheses, newlines, braces, etc.).
func validateVersion(version string) error {
	if !versionValidationRegex.MatchString(version) {
		return fmt.Errorf("%w: %q (allowed characters: a-zA-Z0-9._+-)", ErrInvalidVersion, version)
	}
	return nil
}

// Name returns the build tool identifier.
func (g *Gradle) Name() string {
	return "gradle"
}

// Detect checks if Gradle manifest files exist in the directory.
func (g *Gradle) Detect(ctx context.Context, dir string) (bool, error) {
	log := clog.FromContext(ctx)
	// Check for build files in priority order
	buildFiles := []string{
		"build.gradle.kts", // Kotlin DSL (modern)
		"build.gradle",     // Groovy DSL (legacy)
		"settings.gradle.kts",
		"settings.gradle",
	}

	for _, file := range buildFiles {
		if _, err := os.Stat(filepath.Join(dir, file)); err == nil {
			log.Debugf("Detected Gradle project at %s (found %s)", dir, file)
			return true, nil
		}
	}

	log.Debugf("No Gradle project detected at %s", dir)
	return false, nil
}

// GetManifestFiles returns Gradle manifest files.
func (g *Gradle) GetManifestFiles() []string {
	return []string{
		"build.gradle",
		"build.gradle.kts",
		"settings.gradle",
		"settings.gradle.kts",
		"gradle/libs.versions.toml",
	}
}

// GetAnalyzer returns the Gradle analyzer.
func (g *Gradle) GetAnalyzer() analyzer.Analyzer {
	return &GradleAnalyzer{}
}

// Update performs dependency updates on a Gradle project.
func (g *Gradle) Update(ctx context.Context, cfg *languages.UpdateConfig) error {
	log := clog.FromContext(ctx)
	log.Infof("Updating Gradle project at: %s", cfg.RootDir)

	// Validate all dependency versions upfront to fail fast
	for _, dep := range cfg.Dependencies {
		if err := validateVersion(dep.Version); err != nil {
			return fmt.Errorf("dependency %s: %w", dep.Name, err)
		}
	}

	// Find build files
	buildFiles, err := findBuildFiles(cfg.RootDir)
	if err != nil {
		return fmt.Errorf("failed to find build files: %w", err)
	}

	if len(buildFiles) == 0 {
		return fmt.Errorf("no build.gradle or build.gradle.kts files found")
	}

	log.Infof("Found %d build file(s)", len(buildFiles))

	// Update each build file
	for _, buildFile := range buildFiles {
		if err := updateBuildFile(ctx, buildFile, cfg); err != nil {
			return fmt.Errorf("failed to update %s: %w", buildFile, err)
		}
	}

	return nil
}

// Validate checks if the updates were applied successfully.
// Re-parses build files and verifies that requested versions were actually updated.
func (g *Gradle) Validate(ctx context.Context, cfg *languages.UpdateConfig) error {
	log := clog.FromContext(ctx)
	log.Infof("Validating Gradle updates in: %s", cfg.RootDir)

	// Re-analyze the project to get current state
	analyzer := g.GetAnalyzer()
	analysis, err := analyzer.Analyze(ctx, cfg.RootDir)
	if err != nil {
		return fmt.Errorf("failed to analyze project for validation: %w", err)
	}

	// Verify each requested dependency update
	var failures []string
	for _, dep := range cfg.Dependencies {
		if failure := validateDependency(ctx, dep, analysis); failure != "" {
			failures = append(failures, failure)
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("validation failed for %d dependencies:\n  - %s",
			len(failures), strings.Join(failures, "\n  - "))
	}

	log.Infof("Validation successful: all %d dependencies updated correctly", len(cfg.Dependencies))
	return nil
}

// validateDependency validates a single dependency update.
// Returns an error message if validation fails, empty string if successful.
func validateDependency(ctx context.Context, dep languages.Dependency, analysis *analyzer.AnalysisResult) string {
	depKey := dep.Name
	expectedVersion := dep.Version

	// Check if dependency exists in analysis
	depInfo, exists := analysis.Dependencies[depKey]
	if !exists {
		return fmt.Sprintf("%s: not found in project after update", depKey)
	}

	// Validate based on dependency type
	if depInfo.UsesProperty {
		return validateCatalogDependency(ctx, depKey, expectedVersion, depInfo, analysis.Properties)
	}
	return validateDirectDependency(ctx, depKey, expectedVersion, depInfo)
}

// validateCatalogDependency validates a dependency that uses a version catalog.
func validateCatalogDependency(ctx context.Context, depKey, expectedVersion string, depInfo *analyzer.DependencyInfo, properties map[string]string) string {
	log := clog.FromContext(ctx)
	catalogKey := depInfo.PropertyName

	actualVersion, exists := properties[catalogKey]
	if !exists {
		return fmt.Sprintf("%s: catalog key %s not found", depKey, catalogKey)
	}

	if actualVersion != expectedVersion {
		return fmt.Sprintf("%s: catalog key %s has version %s, expected %s",
			depKey, catalogKey, actualVersion, expectedVersion)
	}

	log.Debugf("Verified %s via catalog key %s = %s", depKey, catalogKey, actualVersion)
	return ""
}

// validateDirectDependency validates a dependency with a direct version declaration.
func validateDirectDependency(ctx context.Context, depKey, expectedVersion string, depInfo *analyzer.DependencyInfo) string {
	log := clog.FromContext(ctx)

	if depInfo.Version != expectedVersion {
		return fmt.Sprintf("%s: has version %s, expected %s",
			depKey, depInfo.Version, expectedVersion)
	}

	log.Debugf("Verified %s = %s", depKey, depInfo.Version)
	return ""
}

// findBuildFiles finds all Gradle files that can contain dependency versions.
// Walks subdirectories to support multi-module Gradle projects.
// Finds:
// - build.gradle[.kts] - Direct dependency declarations
// - settings.gradle[.kts] - Inline version catalogs
// - gradle/libs.versions.toml - TOML version catalogs.
func findBuildFiles(root string) ([]string, error) {
	var files []string

	// Walk directory tree looking for build files
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden directories and common non-build directories
		if info.IsDir() {
			name := info.Name()
			if name[0] == '.' || skipDirs[name] {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip symlinks to prevent arbitrary file corruption via symlink attacks
		// Use Lstat to check the file itself, not what it points to
		fileInfo, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if fileInfo.Mode()&os.ModeSymlink != 0 {
			return nil // Skip symlinks
		}

		// Check if this is a file that can contain dependency versions
		if gradleManifestFiles[info.Name()] {
			files = append(files, path)
		}

		return nil
	})

	return files, err
}

// updateBuildFile updates dependencies in a single Gradle file.
// Routes to the appropriate handler based on file type.
func updateBuildFile(ctx context.Context, path string, cfg *languages.UpdateConfig) error {
	log := clog.FromContext(ctx)
	log.Infof("Updating %s", path)

	// Determine file type and route to appropriate handler
	filename := filepath.Base(path)
	switch filename {
	case "libs.versions.toml":
		return updateVersionCatalogToml(ctx, path, cfg)
	case "settings.gradle", "settings.gradle.kts":
		return updateSettingsGradle(ctx, path, cfg)
	case "build.gradle", "build.gradle.kts":
		return updateBuildGradle(ctx, path, cfg)
	default:
		return fmt.Errorf("unknown Gradle file type: %s", filename)
	}
}

// updateFileFunc is a function that updates file content and returns the updated content and change count.
type updateFileFunc func(ctx context.Context, content string, cfg *languages.UpdateConfig) (updated string, changeCount int, err error)

// replaceRegexMatches replaces all regex matches with a new version in reverse order.
// versionGroupIdx specifies which capture group contains the version (0-indexed).
// Returns the updated content and the number of replacements made.
func replaceRegexMatches(content string, matches [][]int, versionGroupIdx int, newVersion string) (string, int) {
	updated := content
	changeCount := 0

	// Replace all occurrences (in reverse to maintain indices)
	for i := len(matches) - 1; i >= 0; i-- {
		match := matches[i]
		versionSubmatchIdx := versionGroupIdx * 2 // submatch indices are pairs
		if versionSubmatchIdx+1 < len(match) {
			oldVersion := updated[match[versionSubmatchIdx]:match[versionSubmatchIdx+1]]
			replacement := strings.Replace(
				updated[match[0]:match[1]],
				oldVersion,
				newVersion,
				1,
			)
			updated = updated[:match[0]] + replacement + updated[match[1]:]
			changeCount++
		}
	}

	return updated, changeCount
}

// processFileUpdate handles the common pattern of reading a file, updating it, and writing it back.
func processFileUpdate(ctx context.Context, path string, cfg *languages.UpdateConfig, updater updateFileFunc) error {
	log := clog.FromContext(ctx)

	// Read the file
	content, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", path, err)
	}

	// Call the updater function
	updated, changeCount, err := updater(ctx, string(content), cfg)
	if err != nil {
		return err
	}

	if changeCount == 0 {
		log.Warnf("No dependencies were updated in %s", path)
		return nil
	}

	if cfg.DryRun {
		log.Infof("Dry run mode: would write %d change(s) to %s", changeCount, path)
		return nil
	}

	// Write updated content back
	if err := os.WriteFile(path, []byte(updated), gradleFilePerms); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}

	log.Infof("Successfully updated %s with %d change(s)", path, changeCount)
	return nil
}

// updateBuildGradle updates dependencies in build.gradle or build.gradle.kts files.
func updateBuildGradle(ctx context.Context, path string, cfg *languages.UpdateConfig) error {
	return processFileUpdate(ctx, path, cfg, updateBuildGradleContent)
}

// updateBuildGradleContent performs the actual update logic for build.gradle files.
func updateBuildGradleContent(ctx context.Context, content string, cfg *languages.UpdateConfig) (string, int, error) {
	log := clog.FromContext(ctx)

	updated := content
	changeCount := 0

	// Update each dependency
	for _, dep := range cfg.Dependencies {
		// Parse dependency name (format: "groupId:artifactId" or just "artifactId")
		groupID, artifactID := parseDependencyName(dep.Name)

		if groupID == "" {
			log.Warnf("Skipping dependency with invalid name format: %s (expected groupId:artifactID)", dep.Name)
			continue
		}

		// Try different Gradle dependency patterns
		patterns := buildDependencyPatterns(groupID, artifactID)

		for _, pattern := range patterns {
			regex := regexp.MustCompile(pattern.regex)
			matches := regex.FindAllStringSubmatchIndex(updated, -1)

			if len(matches) > 0 {
				log.Infof("Found %d occurrence(s) of %s:%s using pattern: %s", len(matches), groupID, artifactID, pattern.name)

				var count int
				updated, count = replaceRegexMatches(updated, matches, pattern.versionGroup, dep.Version)
				changeCount += count

				if count > 0 {
					log.Infof("Updated %d occurrence(s) of %s:%s to version %s", count, groupID, artifactID, dep.Version)
				}
				break // Found and updated, don't try other patterns
			}
		}
	}

	return updated, changeCount, nil
}

// parseDependencyName parses "groupId:artifactId" format.
func parseDependencyName(name string) (groupID, artifactID string) {
	parts := strings.Split(name, ":")
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	if len(parts) == 1 {
		// Assume it's just artifactID (less common)
		return "", parts[0]
	}
	return "", ""
}

// dependencyPattern represents a regex pattern for matching dependencies.
type dependencyPattern struct {
	name         string
	regex        string
	versionGroup int // which capture group contains the version
}

// buildDependencyPatterns builds regex patterns for different Gradle dependency formats.
func buildDependencyPatterns(groupID, artifactID string) []dependencyPattern {
	// Escape special regex characters
	g := regexp.QuoteMeta(groupID)
	a := regexp.QuoteMeta(artifactID)

	return []dependencyPattern{
		// Pattern 1: String notation - implementation("group:artifact:version")
		{
			name:         "string-notation",
			regex:        fmt.Sprintf(`([a-zA-Z]+)\s*\(\s*["']%s:%s:([^"']+)["']\s*\)`, g, a),
			versionGroup: versionGroupTwo,
		},
		// Pattern 2: Spring Boot library() - library("name", "version") { ... }
		// Handles both library("name", "version") and library("name", "version") { config }
		{
			name:         "library-function",
			regex:        fmt.Sprintf(`library\s*\(\s*["']%s["']\s*,\s*["']([^"']+)["']\s*\)`, a),
			versionGroup: versionGroupOne,
		},
		// Pattern 3: Map notation - group = "...", name = "...", version = "..."
		{
			name: "map-notation",
			regex: fmt.Sprintf(
				`(?s)group\s*=\s*["']%s["']\s*,\s*name\s*=\s*["']%s["']\s*,\s*version\s*=\s*["']([^"']+)["']`,
				g, a,
			),
			versionGroup: versionGroupOne,
		},
	}
}

// updateVersionCatalogToml updates dependencies in gradle/libs.versions.toml files.
// TOML version catalogs use a [versions] section with key-value pairs.
func updateVersionCatalogToml(ctx context.Context, path string, cfg *languages.UpdateConfig) error {
	return processFileUpdate(ctx, path, cfg, updateVersionCatalogTomlContent)
}

// updateVersionCatalogTomlContent performs the actual update logic for TOML version catalogs.
func updateVersionCatalogTomlContent(ctx context.Context, content string, cfg *languages.UpdateConfig) (string, int, error) {
	log := clog.FromContext(ctx)

	// Parse TOML structure
	var catalog map[string]any
	if err := toml.Unmarshal([]byte(content), &catalog); err != nil {
		return "", 0, fmt.Errorf("failed to parse TOML: %w", err)
	}

	// Get versions section
	versions, ok := catalog["versions"].(map[string]any)
	if !ok {
		log.Warnf("No [versions] section found in TOML file")
		return content, 0, nil
	}

	updated := content
	changeCount := 0

	// Update each dependency
	for _, dep := range cfg.Dependencies {
		_, artifactID := parseDependencyName(dep.Name)
		if artifactID == "" {
			log.Warnf("Skipping dependency with invalid name format: %s", dep.Name)
			continue
		}

		// Find and update version key
		versionKey := findVersionKeyForArtifact(artifactID, versions)
		if versionKey == "" {
			log.Debugf("No version key found for %s", artifactID)
			continue
		}

		// Verify version value is a string
		if _, ok := versions[versionKey].(string); !ok {
			log.Warnf("Version key %s has non-string value", versionKey)
			continue
		}

		// Update the version line in the content
		newContent, changed := updateTomlVersionLine(updated, versionKey, dep.Version)
		if changed {
			updated = newContent
			changeCount++
			log.Infof("Updated %s (key: %s) to %s", artifactID, versionKey, dep.Version)
		}
	}

	return updated, changeCount, nil
}

// findVersionKeyForArtifact finds the version key for an artifact in the versions map.
// Only uses exact matching to avoid ambiguous matches.
// For example, "netty-codec-http" could match both "netty" and "netty-codec",
// leading to nondeterministic behavior due to map iteration order.
func findVersionKeyForArtifact(artifactID string, versions map[string]any) string {
	// Only use exact match on artifactId
	if _, exists := versions[artifactID]; exists {
		return artifactID
	}

	return ""
}

// updateTomlVersionLine updates a version line in TOML content.
// Returns the updated content and whether a change was made.
func updateTomlVersionLine(content, versionKey, newVersion string) (string, bool) {
	// Build regex to match the version line in TOML
	// Format: key = "version"
	pattern := regexp.MustCompile(fmt.Sprintf(`(?m)^(\s*)%s\s*=\s*"([^"]+)"`, regexp.QuoteMeta(versionKey)))
	matches := pattern.FindStringSubmatchIndex(content)

	if len(matches) == 0 {
		return content, false
	}

	oldVersion := content[matches[4]:matches[5]]
	replacement := strings.Replace(
		content[matches[0]:matches[1]],
		oldVersion,
		newVersion,
		1,
	)
	updated := content[:matches[0]] + replacement + content[matches[1]:]
	return updated, true
}

// updateSettingsGradle updates dependencies in settings.gradle or settings.gradle.kts files.
// These files can contain inline version catalogs using versionCatalogs { } blocks.
func updateSettingsGradle(ctx context.Context, path string, cfg *languages.UpdateConfig) error {
	return processFileUpdate(ctx, path, cfg, updateSettingsGradleContent)
}

// updateSettingsGradleContent performs the actual update logic for settings.gradle files.
func updateSettingsGradleContent(ctx context.Context, content string, cfg *languages.UpdateConfig) (string, int, error) {
	log := clog.FromContext(ctx)

	updated := content
	changeCount := 0

	// Update each dependency
	for _, dep := range cfg.Dependencies {
		_, artifactID := parseDependencyName(dep.Name)
		if artifactID == "" {
			log.Warnf("Skipping dependency with invalid name format: %s", dep.Name)
			continue
		}

		// Pattern for version catalog version() declarations
		// Format: version("key", "version")
		pattern := regexp.MustCompile(fmt.Sprintf(
			`version\s*\(\s*["']%s["']\s*,\s*["']([^"']+)["']\s*\)`,
			regexp.QuoteMeta(artifactID),
		))
		matches := pattern.FindAllStringSubmatchIndex(updated, -1)

		if len(matches) > 0 {
			log.Infof("Found %d version catalog declaration(s) for %s", len(matches), artifactID)

			var count int
			updated, count = replaceRegexMatches(updated, matches, versionGroupOne, dep.Version)
			changeCount += count

			if count > 0 {
				log.Infof("Updated %d occurrence(s) of %s to version %s", count, artifactID, dep.Version)
			}
		}
	}

	return updated, changeCount, nil
}
