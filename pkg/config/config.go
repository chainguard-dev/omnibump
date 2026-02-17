/*
Copyright 2025 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package config handles configuration loading and normalization across different formats.
package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/chainguard-dev/clog"
	"github.com/ghodss/yaml"
)

// Config represents the unified configuration for omnibump.
type Config struct {
	// Language specifies the language ecosystem (auto, maven, go, rust)
	Language string `json:"language,omitempty" yaml:"language,omitempty"`

	// Packages lists dependencies to update
	Packages []Package `json:"packages,omitempty" yaml:"packages,omitempty"`

	// Properties lists properties to update (Maven, etc.)
	Properties []Property `json:"properties,omitempty" yaml:"properties,omitempty"`

	// Replaces lists module replacements (Go-specific)
	Replaces []Replace `json:"replaces,omitempty" yaml:"replaces,omitempty"`
}

// Package represents a dependency package to update.
type Package struct {
	// Common fields
	Name    string `json:"name,omitempty" yaml:"name,omitempty"`
	Version string `json:"version,omitempty" yaml:"version,omitempty"`

	// Maven-specific
	GroupID    string `json:"groupId,omitempty" yaml:"groupId,omitempty"`
	ArtifactID string `json:"artifactId,omitempty" yaml:"artifactId,omitempty"`
	Scope      string `json:"scope,omitempty" yaml:"scope,omitempty"`
	Type       string `json:"type,omitempty" yaml:"type,omitempty"`
}

// Property represents a build property to update.
type Property struct {
	Property string `json:"property" yaml:"property"`
	Value    string `json:"value" yaml:"value"`
}

// Replace represents a module replacement (Go-specific).
type Replace struct {
	OldName string `json:"oldName" yaml:"oldName"`
	Name    string `json:"name" yaml:"name"`
	Version string `json:"version" yaml:"version"`
}

// StandardFileNames maps old configuration file names to the new standard names.
var StandardFileNames = map[string]string{
	"cargobump-deps.yaml":     "deps.yaml",
	"gobump-deps.yaml":        "deps.yaml",
	"pombump-deps.yaml":       "deps.yaml",
	"pombump-properties.yaml": "properties.yaml",
	"gobump-replaces.yaml":    "replaces.yaml",
}

// LoadConfig loads configuration from a file, supporting both old and new naming conventions.
func LoadConfig(ctx context.Context, path string) (*Config, error) {
	log := clog.FromContext(ctx)

	// Normalize the path (support both old and new names)
	normalizedPath, isOldFormat := normalizePath(path)

	if isOldFormat {
		log.Warnf("Using deprecated configuration file name: %s", filepath.Base(path))
		log.Warnf("Please migrate to: %s", filepath.Base(normalizedPath))
	}

	// Check if file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("configuration file not found: %s", path)
	}

	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("failed to read configuration file: %w", err)
	}

	// Detect file type based on name and content
	var cfg *Config
	switch {
	case isPropertiesFile(path):
		cfg, err = loadPropertiesFile(data)
	case isReplaceFile(path):
		cfg, err = loadReplacesFile(data)
	default:
		cfg, err = loadDepsFile(data)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to parse configuration file: %w", err)
	}

	return cfg, nil
}

// LoadMultipleConfigs loads and merges multiple configuration files.
// This is useful for Maven projects that have separate deps and properties files.
func LoadMultipleConfigs(ctx context.Context, paths []string) (*Config, error) {
	merged := &Config{}

	for _, path := range paths {
		cfg, err := LoadConfig(ctx, path)
		if err != nil {
			return nil, err
		}

		// Merge configurations
		merged.Packages = append(merged.Packages, cfg.Packages...)
		merged.Properties = append(merged.Properties, cfg.Properties...)
		merged.Replaces = append(merged.Replaces, cfg.Replaces...)

		// Language should be consistent or auto
		if cfg.Language != "" && cfg.Language != "auto" {
			if merged.Language != "" && merged.Language != cfg.Language {
				return nil, fmt.Errorf("conflicting language specifications: %s vs %s", merged.Language, cfg.Language)
			}
			merged.Language = cfg.Language
		}
	}

	return merged, nil
}

// DiscoverConfigFiles searches for configuration files in a directory.
// Returns paths to all found configuration files (both old and new formats).
func DiscoverConfigFiles(ctx context.Context, dir string) ([]string, error) {
	log := clog.FromContext(ctx)
	var found []string

	// Check for new standard names first
	standardNames := []string{"deps.yaml", "properties.yaml", "replaces.yaml"}
	for _, name := range standardNames {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			found = append(found, path)
			log.Debugf("Found configuration file: %s", name)
		}
	}

	// Check for old names (for backward compatibility)
	for oldName := range StandardFileNames {
		path := filepath.Join(dir, oldName)
		if _, err := os.Stat(path); err == nil {
			// Only add if we haven't already found the standard equivalent
			if !contains(found, filepath.Join(dir, StandardFileNames[oldName])) {
				found = append(found, path)
				log.Warnf("Found deprecated configuration file: %s", oldName)
			}
		}
	}

	return found, nil
}

// normalizePath converts old file names to new standard names for internal processing.
func normalizePath(path string) (string, bool) {
	base := filepath.Base(path)
	dir := filepath.Dir(path)

	if newName, isOld := StandardFileNames[base]; isOld {
		return filepath.Clean(filepath.Join(dir, newName)), true
	}

	return filepath.Clean(path), false
}

// isPropertiesFile checks if the file is a properties configuration.
func isPropertiesFile(path string) bool {
	base := filepath.Base(path)
	return base == "properties.yaml" || base == "pombump-properties.yaml"
}

// isReplaceFile checks if the file is a replaces configuration (Go).
func isReplaceFile(path string) bool {
	base := filepath.Base(path)
	return base == "replaces.yaml" || base == "gobump-replaces.yaml"
}

// loadDepsFile loads a dependencies configuration file.
func loadDepsFile(data []byte) (*Config, error) {
	// Try new unified format first
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// If packages is populated, we're good
	if len(cfg.Packages) > 0 {
		return &cfg, nil
	}

	// Try old pombump format (with "patches" key)
	var pombumpFormat struct {
		Patches []Package `json:"patches" yaml:"patches"`
	}
	if err := yaml.Unmarshal(data, &pombumpFormat); err == nil && len(pombumpFormat.Patches) > 0 {
		cfg.Packages = pombumpFormat.Patches
		return &cfg, nil
	}

	return &cfg, nil
}

// loadPropertiesFile loads a properties configuration file.
func loadPropertiesFile(data []byte) (*Config, error) {
	var cfg Config

	var propList struct {
		Properties []Property `json:"properties" yaml:"properties"`
	}

	if err := yaml.Unmarshal(data, &propList); err != nil {
		return nil, err
	}

	cfg.Properties = propList.Properties
	return &cfg, nil
}

// loadReplacesFile loads a replaces configuration file (Go-specific).
func loadReplacesFile(data []byte) (*Config, error) {
	var cfg Config

	var replaceList struct {
		Replaces []Replace `json:"replaces" yaml:"replaces"`
	}

	if err := yaml.Unmarshal(data, &replaceList); err != nil {
		return nil, err
	}

	cfg.Replaces = replaceList.Replaces
	return &cfg, nil
}

// contains checks if a string is in a slice.
func contains(slice []string, item string) bool {
	return slices.Contains(slice, item)
}

// ParseInlinePackages parses inline package specifications from command line.
// Format: "name@version" or "groupId@artifactId@version" (Maven)
func ParseInlinePackages(packagesStr string) ([]Package, error) {
	if packagesStr == "" {
		return nil, nil
	}

	var packages []Package
	for pkgStr := range strings.FieldsSeq(packagesStr) {
		if pkgStr == "" {
			continue
		}

		parts := strings.Split(pkgStr, "@")

		// Determine format based on number of parts
		switch len(parts) {
		case 2:
			// Simple format: name@version (Rust, Go)
			packages = append(packages, Package{
				Name:    parts[0],
				Version: parts[1],
			})
		case 3:
			// Maven format: groupId@artifactId@version
			packages = append(packages, Package{
				GroupID:    parts[0],
				ArtifactID: parts[1],
				Version:    parts[2],
			})
		case 4:
			// Maven with scope: groupId@artifactId@version@scope
			packages = append(packages, Package{
				GroupID:    parts[0],
				ArtifactID: parts[1],
				Version:    parts[2],
				Scope:      parts[3],
			})
		case 5:
			// Maven with scope and type: groupId@artifactId@version@scope@type
			packages = append(packages, Package{
				GroupID:    parts[0],
				ArtifactID: parts[1],
				Version:    parts[2],
				Scope:      parts[3],
				Type:       parts[4],
			})
		default:
			return nil, fmt.Errorf("invalid package format: %s (expected name@version or groupId@artifactId@version)", pkgStr)
		}
	}

	return packages, nil
}
