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
	// Only allows alphanumeric characters, dots, underscores, hyphens, and plus signs.
	// This prevents injection of quotes, braces, newlines, and other
	// characters that could be used for XML injection in Maven POM files.
	versionValidationRegex = regexp.MustCompile(`^[a-zA-Z0-9._+-]+$`)

	// ErrInvalidVersion is returned when a version string fails validation.
	ErrInvalidVersion = errors.New("invalid version string")
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

// Update performs dependency updates on a Maven project.
func (m *Maven) Update(ctx context.Context, cfg *languages.UpdateConfig) error {
	log := clog.FromContext(ctx)

	log.Infof("Updating Maven project at: %s", cfg.RootDir)
	log.Infof("Dependencies to update: %d", len(cfg.Dependencies))
	log.Infof("Properties to update: %d", len(cfg.Properties))

	// Validate all dependency versions before any file writes (fail-fast)
	for _, dep := range cfg.Dependencies {
		if err := validateVersion(dep.Version); err != nil {
			return fmt.Errorf("dependency %s: %w", dep.Name, err)
		}
	}

	// Validate all property values before any file writes (fail-fast)
	for propName, propValue := range cfg.Properties {
		if err := validateVersion(propValue); err != nil {
			return fmt.Errorf("property %s: %w", propName, err)
		}
	}

	// Find pom.xml
	pomPath := filepath.Join(cfg.RootDir, "pom.xml")
	if _, err := os.Stat(pomPath); os.IsNotExist(err) {
		return fmt.Errorf("pom.xml not found in: %s", cfg.RootDir)
	}

	// Convert unified dependencies to Maven patches
	patches := convertDependenciesToPatches(cfg.Dependencies)

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
					return fmt.Errorf("property %s has value %s, expected %s", propName, actualValue, expectedValue)
				}
			} else {
				log.Warnf("Property not found: %s", propName)
			}
		}
	}

	log.Infof("Validation completed successfully")
	return nil
}

// convertDependenciesToPatches converts unified dependencies to Maven-specific patches.
func convertDependenciesToPatches(deps []languages.Dependency) []Patch {
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
			// Use metadata if available
			if groupID, ok := dep.Metadata["groupID"].(string); ok {
				patch.GroupID = groupID
			}
			if artifactID, ok := dep.Metadata["artifactID"].(string); ok {
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

		patches = append(patches, patch)
	}

	return patches
}

// extractGroupID extracts groupID from a dependency.
func extractGroupID(dep languages.Dependency) string {
	if groupID, ok := dep.Metadata["groupID"].(string); ok {
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
	if artifactID, ok := dep.Metadata["artifactID"].(string); ok {
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
