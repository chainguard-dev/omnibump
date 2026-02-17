/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package maven implements Maven build tool support for Java projects.
// Ported from pombump with enhancements for the unified omnibump architecture.
package maven

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/analyzer"
	"github.com/chainguard-dev/omnibump/pkg/languages"
)

// Maven implements the BuildTool interface for Maven projects.
type Maven struct{}

// Name returns the build tool identifier.
func (m *Maven) Name() string {
	return "maven"
}

// Detect checks if Maven manifest files exist in the directory.
func (m *Maven) Detect(ctx context.Context, dir string) (bool, error) {
	pomPath := filepath.Join(dir, "pom.xml")
	_, err := os.Stat(pomPath)
	return err == nil, nil
}

// GetManifestFiles returns Maven manifest files.
func (m *Maven) GetManifestFiles() []string {
	return []string{"pom.xml"}
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
	if err := os.WriteFile(pomPath, updatedPom, 0600); err != nil {
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
			// Maven format: groupId:artifactId
			searchKey = fmt.Sprintf("%s:%s", extractGroupId(dep), extractArtifactId(dep))
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
			// Simple name format (might be groupId:artifactId)
			patch.GroupID = extractGroupId(dep)
			patch.ArtifactID = extractArtifactId(dep)
		} else {
			// Use metadata if available
			if groupId, ok := dep.Metadata["groupId"].(string); ok {
				patch.GroupID = groupId
			}
			if artifactId, ok := dep.Metadata["artifactId"].(string); ok {
				patch.ArtifactID = artifactId
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

// extractGroupId extracts groupId from a dependency.
func extractGroupId(dep languages.Dependency) string {
	if groupId, ok := dep.Metadata["groupId"].(string); ok {
		return groupId
	}
	// Try to extract from Name if it's in groupId:artifactId format
	if dep.Name != "" {
		parts := splitMavenCoordinate(dep.Name)
		if len(parts) >= 1 {
			return parts[0]
		}
	}
	return ""
}

// extractArtifactId extracts artifactId from a dependency.
func extractArtifactId(dep languages.Dependency) string {
	if artifactId, ok := dep.Metadata["artifactId"].(string); ok {
		return artifactId
	}
	// Try to extract from Name if it's in groupId:artifactId format
	if dep.Name != "" {
		parts := splitMavenCoordinate(dep.Name)
		if len(parts) >= 2 {
			return parts[1]
		}
	}
	return ""
}

// splitMavenCoordinate splits a Maven coordinate like "groupId:artifactId" or "groupId:artifactId:version".
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
