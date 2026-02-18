/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package maven

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/gopom"
	"github.com/ghodss/yaml"
)

// Default scope and type for a dependency.
const (
	defaultScope = "import"
	defaultType  = "jar"

	// MaxPatchFileSize limits patch/properties file size to prevent resource exhaustion.
	MaxPatchFileSize = 10 * 1024 * 1024 // 10 MB
)

// Patch represents a Maven dependency patch.
// Ported from pombump/pkg/patch.go.
type Patch struct {
	GroupID    string `json:"groupId" yaml:"groupId"`
	ArtifactID string `json:"artifactId" yaml:"artifactId"`
	Version    string `json:"version" yaml:"version"`
	Scope      string `json:"scope,omitempty" yaml:"scope,omitempty"`
	Type       string `json:"type,omitempty" yaml:"type,omitempty"`
}

// PatchList represents a list of patches from a YAML file.
type PatchList struct {
	Patches []Patch `json:"patches" yaml:"patches"`
}

// PropertyPatch represents a property override.
type PropertyPatch struct {
	Property string `json:"property" yaml:"property"`
	Value    string `json:"value" yaml:"value"`
}

// PropertyList represents a list of property patches from a YAML file.
type PropertyList struct {
	Properties []PropertyPatch `json:"properties" yaml:"properties"`
}

// UpdatePom updates a POM file with the given patches and properties.
// Returns the marshaled XML content of the updated POM.
func UpdatePom(ctx context.Context, pomPath string, patches []Patch, properties map[string]string) ([]byte, error) {
	log := clog.FromContext(ctx)

	// Parse the POM
	project, err := ParsePom(pomPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse POM: %w", err)
	}

	// Apply patches
	project, err = PatchProject(ctx, project, patches, properties)
	if err != nil {
		return nil, fmt.Errorf("failed to patch project: %w", err)
	}

	// Marshal back to XML
	xmlBytes, err := project.Marshal()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal POM: %w", err)
	}

	log.Infof("Successfully updated POM file")
	return xmlBytes, nil
}

// PatchProject updates a gopom.Project with the given patches and properties.
// Ported from pombump/pkg/patch.go:PatchProject.
func PatchProject(ctx context.Context, project *gopom.Project, patches []Patch, propertyPatches map[string]string) (*gopom.Project, error) {
	log := clog.FromContext(ctx)

	if project == nil {
		return nil, fmt.Errorf("project is nil")
	}

	// Track dependencies that weren't found (will be added to DependencyManagement)
	missingDeps := make(map[Patch]Patch)
	for _, p := range patches {
		log.Infof("Processing patch: %s:%s @ %s", p.GroupID, p.ArtifactID, p.Version)
		missingDeps[p] = p
	}

	// Patch direct dependencies
	if project.Dependencies != nil {
		for i, dep := range *project.Dependencies {
			log.Debugf("Checking dependency: %s:%s @ %s", dep.GroupID, dep.ArtifactID, dep.Version)
			for _, patch := range patches {
				if dep.ArtifactID == patch.ArtifactID && dep.GroupID == patch.GroupID {
					log.Infof("Patching %s:%s from %s to %s (scope: %s)",
						patch.GroupID, patch.ArtifactID, dep.Version, patch.Version, patch.Scope)
					(*project.Dependencies)[i].Version = patch.Version
					delete(missingDeps, patch)
				}
			}
		}
	}

	// Patch dependency management
	if project.DependencyManagement != nil && project.DependencyManagement.Dependencies != nil {
		for i, dep := range *project.DependencyManagement.Dependencies {
			log.Debugf("Checking DM dependency: %s:%s @ %s", dep.GroupID, dep.ArtifactID, dep.Version)
			for _, patch := range patches {
				if dep.ArtifactID == patch.ArtifactID && dep.GroupID == patch.GroupID {
					log.Infof("Patching DM dependency %s:%s from %s to %s (scope: %s)",
						patch.GroupID, patch.ArtifactID, dep.Version, patch.Version, patch.Scope)
					(*project.DependencyManagement.Dependencies)[i].Version = patch.Version
					delete(missingDeps, patch)
				}
			}
		}
	}

	// Add missing dependencies to DependencyManagement
	if len(missingDeps) > 0 {
		if project.DependencyManagement == nil {
			project.DependencyManagement = &gopom.DependencyManagement{
				Dependencies: &[]gopom.Dependency{},
			}
		} else if project.DependencyManagement.Dependencies == nil {
			project.DependencyManagement.Dependencies = &[]gopom.Dependency{}
		}

		for _, md := range missingDeps {
			log.Infof("Adding missing dependency: %s:%s @ %s", md.GroupID, md.ArtifactID, md.Version)
			*project.DependencyManagement.Dependencies = append(*project.DependencyManagement.Dependencies, gopom.Dependency{
				GroupID:    md.GroupID,
				ArtifactID: md.ArtifactID,
				Version:    md.Version,
				Scope:      md.Scope,
				Type:       md.Type,
			})
		}
	}

	// Update properties
	if len(propertyPatches) > 0 {
		if project.Properties == nil {
			project.Properties = &gopom.Properties{Entries: propertyPatches}
		} else {
			for k, v := range propertyPatches {
				val, exists := project.Properties.Entries[k]
				if exists {
					log.Infof("Updating property: %s from %s to %s", k, val, v)
				} else {
					log.Infof("Creating property: %s = %s", k, v)
				}
				project.Properties.Entries[k] = v
			}
		}
	}

	return project, nil
}

// ParsePom parses a POM file and returns a gopom.Project.
func ParsePom(pomPath string) (*gopom.Project, error) {
	project, err := gopom.Parse(pomPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse POM file %s: %w", pomPath, err)
	}
	return project, nil
}

// parsePatches parses Maven patches from a file or inline string.
// Ported from pombump/pkg/patch.go.
func parsePatches(ctx context.Context, patchFile, patchFlag string) ([]Patch, error) {
	if patchFile != "" {
		var patchList PatchList
		// filepath.Clean sanitizes the path to prevent traversal attacks
		file, err := os.Open(filepath.Clean(patchFile)) //nolint:gosec // G703: filepath.Clean() sanitizes user input
		if err != nil {
			return nil, fmt.Errorf("failed reading file: %w", err)
		}
		// Ensure we handle err from file.Close()
		defer func() {
			if err := file.Close(); err != nil {
				clog.FromContext(ctx).Warnf("failed to close file: %v", err)
			}
		}()
		// Limit file size to prevent resource exhaustion
		byteValue, err := io.ReadAll(io.LimitReader(file, MaxPatchFileSize))
		if err != nil {
			return nil, fmt.Errorf("reading file: %w", err)
		}
		// Check if file was truncated (too large)
		if len(byteValue) >= MaxPatchFileSize {
			return nil, fmt.Errorf("patch file too large (max: %d bytes)", MaxPatchFileSize)
		}
		if err := yaml.Unmarshal(byteValue, &patchList); err != nil {
			return nil, err
		}
		for i := range patchList.Patches {
			if patchList.Patches[i].Scope == "" {
				patchList.Patches[i].Scope = defaultScope
			}
			if patchList.Patches[i].Type == "" {
				patchList.Patches[i].Type = defaultType
			}
		}
		return patchList.Patches, nil
	}
	dependencies := strings.Split(patchFlag, " ")
	patches := []Patch{}
	for _, dep := range dependencies {
		if dep == "" {
			continue
		}
		parts := strings.Split(dep, "@")
		if len(parts) < 3 {
			return nil, fmt.Errorf("invalid dependencies format (%s). Each dependency should be in the format <groupID@artifactID@version[@scope]>", dep)
		}
		// Default scope
		scope := defaultScope
		if len(parts) >= 4 {
			scope = parts[3]
		}
		depType := defaultType
		if len(parts) >= 5 {
			depType = parts[4]
		}
		patches = append(patches, Patch{GroupID: parts[0], ArtifactID: parts[1], Version: parts[2], Scope: scope, Type: depType})
	}
	return patches, nil
}

// parseProperties parses Maven properties from a file or inline string.
// Ported from pombump/pkg/patch.go.
func parseProperties(ctx context.Context, propertyFile, propertiesFlag string) (map[string]string, error) {
	propertiesPatches := map[string]string{}
	if propertyFile != "" {
		var propertyList PropertyList
		// filepath.Clean sanitizes the path to prevent traversal attacks
		file, err := os.Open(filepath.Clean(propertyFile)) //nolint:gosec // G703: filepath.Clean() sanitizes user input
		if err != nil {
			return nil, fmt.Errorf("failed reading file: %w", err)
		}
		// Ensure we handle err from file.Close()
		defer func() {
			if err := file.Close(); err != nil {
				clog.FromContext(ctx).Warnf("failed to close file: %v", err)
			}
		}()
		// Limit file size to prevent resource exhaustion
		byteValue, err := io.ReadAll(io.LimitReader(file, MaxPatchFileSize))
		if err != nil {
			return nil, fmt.Errorf("reading file: %w", err)
		}
		// Check if file was truncated (too large)
		if len(byteValue) >= MaxPatchFileSize {
			return nil, fmt.Errorf("properties file too large (max: %d bytes)", MaxPatchFileSize)
		}
		if err := yaml.Unmarshal(byteValue, &propertyList); err != nil {
			return nil, err
		}
		for _, v := range propertyList.Properties {
			propertiesPatches[v.Property] = v.Value
		}
		return propertiesPatches, nil
	}

	for prop := range strings.SplitSeq(propertiesFlag, " ") {
		if prop == "" {
			continue
		}
		parts := strings.Split(prop, "@")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid properties format. Each dependency should be in the format <property@value>")
		}
		propertiesPatches[parts[0]] = parts[1]
	}

	return propertiesPatches, nil
}
