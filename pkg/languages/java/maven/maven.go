/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package maven implements Maven build tool support for Java projects.
// Ported from pombump with enhancements for the unified omnibump architecture.
package maven

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/analyzer"
	"github.com/chainguard-dev/omnibump/pkg/languages"
)

var (
	// versionValidationRegex defines the allowlist for valid version strings.
	// Allows alphanumeric characters, dots, underscores, hyphens, plus signs,
	// commas, and parentheses/brackets for Maven version ranges (e.g. [1.0,2.0)).
	// This prevents injection of quotes, braces, newlines, and other
	// characters that could be used for XML injection in Maven POM files.
	versionValidationRegex = regexp.MustCompile(`^[a-zA-Z0-9._+\-,()\[\]]+$`)

	// ErrInvalidVersion is returned when a version string fails validation.
	ErrInvalidVersion = errors.New("invalid version string")

	// ErrPomNotFound is returned when pom.xml is not found.
	ErrPomNotFound = errors.New("pom.xml not found")

	// ErrPropertyValidationFailed is returned when property validation fails.
	ErrPropertyValidationFailed = errors.New("property validation failed")

	// ErrRemoteAnalysisNotImplemented is returned when remote analysis is not implemented.
	ErrRemoteAnalysisNotImplemented = errors.New("remote analysis not yet implemented")

	// ErrMissingRequiredFields is returned when a dependency is missing groupId or artifactId.
	ErrMissingRequiredFields = errors.New("missing required fields for dependency")
)

// Maven implements the BuildTool interface for Maven projects.
type Maven struct{}

// Name returns the build tool identifier.
func (m *Maven) Name() string {
	return "maven"
}

// Detect checks if Maven manifest files exist in the directory.
func (m *Maven) Detect(ctx context.Context, dir string) (bool, error) {
	log := clog.FromContext(ctx)
	pomPath := filepath.Join(dir, "pom.xml")
	_, err := os.Stat(pomPath)
	if err == nil {
		log.Debugf("Detected Maven project at %s", dir)
		return true, nil
	}
	log.Debugf("No Maven project detected at %s", dir)
	return false, nil
}

// GetManifestFiles returns Maven manifest files.
func (m *Maven) GetManifestFiles() []string {
	return []string{"pom.xml"}
}

// validateVersion checks if a version string contains only safe characters.
// Returns an error if the version contains characters that could be used for
// XML injection (quotes, braces, newlines, etc.).
func validateVersion(version string) error {
	if !versionValidationRegex.MatchString(version) {
		return fmt.Errorf("%w: %q (allowed characters: a-zA-Z0-9._+-)", ErrInvalidVersion, version)
	}
	return nil
}

// GetAnalyzer returns the Maven analyzer.
func (m *Maven) GetAnalyzer() analyzer.Analyzer {
	return &MavenAnalyzer{}
}

// depDisplayName returns a human-readable identifier for a dependency,
// preferring groupId:artifactId from metadata over the generic Name field.
func depDisplayName(dep languages.Dependency) string {
	if gid, ok := dep.Metadata["groupId"].(string); ok {
		if aid, ok := dep.Metadata["artifactId"].(string); ok {
			return gid + ":" + aid
		}
	}
	if dep.Name != "" {
		return dep.Name
	}
	return "<unknown>"
}

// Update performs dependency updates on a Maven project.
func (m *Maven) Update(ctx context.Context, cfg *languages.UpdateConfig) error {
	log := clog.FromContext(ctx)

	log.Infof("Updating Maven project at: %s", cfg.RootDir)
	log.Infof("Dependencies to update: %d", len(cfg.Dependencies))
	log.Infof("Properties to update: %d", len(cfg.Properties))

	// Validate all dependency versions before any file writes (fail-fast).
	// Deps with no version are allowed — they are used to add scope-only entries to
	// DependencyManagement (e.g. scope: provided with no version to suppress a
	// relocated artifact via the Maven exclusion-by-provided-scope trick).
	for _, dep := range cfg.Dependencies {
		if dep.Version == "" {
			log.Infof("Dependency %s has no version: will be written to DependencyManagement without <version>", depDisplayName(dep))
			continue
		}
		if err := validateVersion(dep.Version); err != nil {
			return fmt.Errorf("dependency %s: %w", depDisplayName(dep), err)
		}
	}

	deps := cfg.Dependencies

	// Validate all property values before any file writes (fail-fast)
	for propName, propValue := range cfg.Properties {
		if err := validateVersion(propValue); err != nil {
			return fmt.Errorf("property %s: %w", propName, err)
		}
	}

	// Find pom.xml
	pomPath := filepath.Join(cfg.RootDir, "pom.xml")
	if _, err := os.Stat(pomPath); os.IsNotExist(err) {
		return fmt.Errorf("%w in: %s", ErrPomNotFound, cfg.RootDir)
	}

	// Apply precedence: properties take precedence over direct dependency patches
	// If a dependency uses a property that's being updated, skip the direct patch
	var patches []Patch
	var err error
	if len(cfg.Properties) > 0 {
		patches, err = applyPrecedenceRules(ctx, pomPath, deps, cfg.Properties)
		if err != nil {
			return fmt.Errorf("failed to apply precedence rules: %w", err)
		}
	} else {
		// No properties, convert all dependencies to patches
		patches, err = convertDependenciesToPatches(deps)
		if err != nil {
			return fmt.Errorf("failed to convert dependencies to patches: %w", err)
		}
	}

	// Perform the update
	updatedPom, err := UpdatePom(ctx, pomPath, patches, cfg.Properties)
	if err != nil {
		return fmt.Errorf("failed to update pom.xml: %w", err)
	}

	if cfg.DryRun {
		log.Infof("Dry run mode: not writing changes to %s", pomPath)
		return nil
	}

	// Write updated POM back to file
	if err := os.WriteFile(pomPath, updatedPom, 0o600); err != nil {
		return fmt.Errorf("failed to write updated pom.xml: %w", err)
	}

	log.Infof("Successfully updated %s", pomPath)

	return nil
}

// Validate checks if the updates were applied successfully.
func (m *Maven) Validate(ctx context.Context, cfg *languages.UpdateConfig) error {
	log := clog.FromContext(ctx)

	pomPath := filepath.Join(cfg.RootDir, "pom.xml")

	// Parse the updated POM
	project, err := ParsePom(pomPath)
	if err != nil {
		return fmt.Errorf("failed to parse updated pom.xml: %w", err)
	}

	// Validate dependencies
	for _, dep := range cfg.Dependencies {
		found := false

		// Determine the key to search for
		var searchKey string
		if dep.Name != "" {
			searchKey = dep.Name
		} else {
			// Maven format: groupID:artifactID
			searchKey = fmt.Sprintf("%s:%s", extractGroupID(dep), extractArtifactID(dep))
		}

		// Check in dependencies
		if project.Dependencies != nil {
			for _, pomDep := range *project.Dependencies {
				key := fmt.Sprintf("%s:%s", pomDep.GroupID, pomDep.ArtifactID)
				if key == searchKey && pomDep.Version == dep.Version {
					found = true
					break
				}
			}
		}

		// Check in dependency management
		if !found && project.DependencyManagement != nil && project.DependencyManagement.Dependencies != nil {
			for _, pomDep := range *project.DependencyManagement.Dependencies {
				key := fmt.Sprintf("%s:%s", pomDep.GroupID, pomDep.ArtifactID)
				if key == searchKey && pomDep.Version == dep.Version {
					found = true
					break
				}
			}
		}

		if !found {
			log.Warnf("Dependency not found or not at expected version: %s@%s", searchKey, dep.Version)
		}
	}

	// Validate properties
	if project.Properties != nil {
		for propName, expectedValue := range cfg.Properties {
			if actualValue, exists := project.Properties.Entries[propName]; exists {
				if actualValue != expectedValue {
					return fmt.Errorf("%w: property %s has value %s, expected %s", ErrPropertyValidationFailed, propName, actualValue, expectedValue)
				}
			} else {
				log.Warnf("Property not found: %s", propName)
			}
		}
	}

	log.Infof("Validation completed successfully")
	return nil
}

// applyPrecedenceRules filters dependencies based on precedence rules:
// - If a dependency uses a property that's being updated, skip the direct patch.
// - Properties take precedence over direct dependency patches.
func applyPrecedenceRules(ctx context.Context, pomPath string, deps []languages.Dependency, properties map[string]string) ([]Patch, error) {
	log := clog.FromContext(ctx)

	// Analyze the POM to understand which dependencies use properties
	analyzer := &MavenAnalyzer{}
	analysis, err := analyzer.Analyze(ctx, pomPath)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze POM for precedence rules: %w", err)
	}

	// Filter dependencies based on property precedence
	var filteredDeps []languages.Dependency
	for _, dep := range deps {
		// Determine the dependency key
		depKey := dep.Name
		if depKey == "" && dep.Metadata != nil {
			if groupID, ok := dep.Metadata["groupId"].(string); ok {
				if artifactID, ok := dep.Metadata["artifactId"].(string); ok {
					depKey = fmt.Sprintf("%s:%s", groupID, artifactID)
				}
			}
		}

		// Check if this dependency uses a property
		if depInfo, exists := analysis.Dependencies[depKey]; exists && depInfo.UsesProperty {
			// Check if the property is being updated
			if _, propertyBeingUpdated := properties[depInfo.PropertyName]; propertyBeingUpdated {
				log.Infof("Skipping direct patch for %s (property %s takes precedence)", depKey, depInfo.PropertyName)
				continue // Skip this dependency, property wins
			}
		}

		// Include this dependency in patches
		filteredDeps = append(filteredDeps, dep)
	}

	log.Infof("After precedence filtering: %d patches (skipped %d property-managed)", len(filteredDeps), len(deps)-len(filteredDeps))

	// Convert filtered dependencies to patches
	return convertDependenciesToPatches(filteredDeps)
}

// convertDependenciesToPatches converts unified dependencies to Maven-specific patches.
func convertDependenciesToPatches(deps []languages.Dependency) ([]Patch, error) {
	patches := make([]Patch, 0, len(deps))

	for _, dep := range deps {
		patch := Patch{
			Version: dep.Version,
			Scope:   dep.Scope,
			Type:    dep.Type,
		}

		// Handle different input formats
		if dep.Name != "" {
			// Simple name format (might be groupID:artifactID)
			patch.GroupID = extractGroupID(dep)
			patch.ArtifactID = extractArtifactID(dep)
		} else {
			// Use metadata if available (lowercase 'd' to match Maven XML)
			if groupID, ok := dep.Metadata["groupId"].(string); ok {
				patch.GroupID = groupID
			}
			if artifactID, ok := dep.Metadata["artifactId"].(string); ok {
				patch.ArtifactID = artifactID
			}
		}

		// Set defaults if not specified
		if patch.Scope == "" {
			patch.Scope = "import"
		}
		if patch.Type == "" {
			patch.Type = "jar"
		}

		// Validate required fields to prevent malformed XML
		if patch.GroupID == "" || patch.ArtifactID == "" {
			return nil, fmt.Errorf("%w: groupId=%q, artifactId=%q, version=%q (dependency name=%q)",
				ErrMissingRequiredFields, patch.GroupID, patch.ArtifactID, patch.Version, dep.Name)
		}

		patches = append(patches, patch)
	}

	return patches, nil
}

// extractGroupID extracts groupID from a dependency.
func extractGroupID(dep languages.Dependency) string {
	// Use lowercase 'd' to match Maven XML naming
	if groupID, ok := dep.Metadata["groupId"].(string); ok {
		return groupID
	}
	// Try to extract from Name if it's in groupID:artifactID format
	if dep.Name != "" {
		parts := splitMavenCoordinate(dep.Name)
		if len(parts) >= 1 {
			return parts[0]
		}
	}
	return ""
}

// extractArtifactID extracts artifactID from a dependency.
func extractArtifactID(dep languages.Dependency) string {
	// Use lowercase 'd' to match Maven XML naming
	if artifactID, ok := dep.Metadata["artifactId"].(string); ok {
		return artifactID
	}
	// Try to extract from Name if it's in groupID:artifactID format
	if dep.Name != "" {
		parts := splitMavenCoordinate(dep.Name)
		if len(parts) >= 2 {
			return parts[1]
		}
	}
	return ""
}

// splitMavenCoordinate splits a Maven coordinate like "groupID:artifactID" or "groupID:artifactID:version".
func splitMavenCoordinate(coordinate string) []string {
	// Use a simple colon split for Maven coordinates
	var result []string
	for _, part := range coordinate {
		if part == ':' {
			result = append(result, "")
		} else {
			if len(result) == 0 {
				result = append(result, "")
			}
			result[len(result)-1] += string(part)
		}
	}
	return result
}
