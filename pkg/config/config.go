/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package config handles configuration loading and normalization across different formats.
package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/languages"
	"github.com/chainguard-dev/omnibump/pkg/languages/js"
	"github.com/ghodss/yaml"
)

const (
	// MaxConfigFileSize limits configuration file size to prevent resource exhaustion.
	MaxConfigFileSize = 10 * 1024 * 1024 // 10 MB
)

var (
	// ErrConfigNotFound is returned when a configuration file is not found.
	ErrConfigNotFound = errors.New("configuration file not found")

	// ErrConfigTooLarge is returned when a configuration file exceeds size limits.
	ErrConfigTooLarge = errors.New("configuration file too large")

	// ErrConflictingLanguage is returned when merging configs with different language specs.
	ErrConflictingLanguage = errors.New("conflicting language specifications")

	// ErrConflictingManager is returned when merging configs with
	// different manager lists.
	ErrConflictingManager = errors.New("conflicting manager specifications")

	// ErrInvalidPackageFormat is returned when a package string has invalid format.
	ErrInvalidPackageFormat = errors.New("invalid package format")
)

// Config represents the unified configuration for omnibump.
type Config struct {
	// Language specifies the language ecosystem (auto, maven, go, rust, js)
	Language string `json:"language,omitempty" yaml:"language,omitempty"`

	// Manager names the build tool(s) within a language where the
	// language alone is ambiguous. Currently used by JS to choose between
	// pnpm/yarn/npm/bun.
	//
	// In YAML this may be a scalar or a sequence: `manager: pnpm` is
	// equivalent to `manager: [pnpm]`. A sequence is meaningful when a
	// single package.json needs overrides written under more than one
	// manager's field (for example, pnpm reads both .pnpm.overrides and
	// .resolutions, so a project mid-migration may want both kept in
	// sync).
	Manager js.Managers `json:"manager,omitempty" yaml:"manager,omitempty"`

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

	// Reason is a free-form note (typically a CVE/GHSA list) recorded
	// in build logs alongside the update. It is not written to any
	// manifest file.
	Reason string `json:"reason,omitempty" yaml:"reason,omitempty"`

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

	// Check if file exists and validate size
	fileInfo, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: %s", ErrConfigNotFound, path)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to stat configuration file: %w", err)
	}

	// Prevent resource exhaustion from reading huge files
	if fileInfo.Size() > MaxConfigFileSize {
		return nil, fmt.Errorf("%w: %d bytes (max: %d)", ErrConfigTooLarge, fileInfo.Size(), MaxConfigFileSize)
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
		if err := merged.Merge(cfg); err != nil {
			return nil, err
		}
	}

	return merged, nil
}

// Merge appends other's packages, properties and replaces into c and adopts
// its language and manager, erroring on conflicting specifications.
func (c *Config) Merge(other *Config) error {
	if other == nil {
		return nil
	}
	c.Packages = append(c.Packages, other.Packages...)
	c.Properties = append(c.Properties, other.Properties...)
	c.Replaces = append(c.Replaces, other.Replaces...)

	// Language should be consistent or auto
	if other.Language != "" && other.Language != "auto" {
		if c.Language != "" && c.Language != other.Language {
			return fmt.Errorf("%w: %q vs %q", ErrConflictingLanguage, c.Language, other.Language)
		}
		c.Language = other.Language
	}

	if len(other.Manager) > 0 {
		if len(c.Manager) > 0 && !slices.Equal(c.Manager, other.Manager) {
			return fmt.Errorf("%w: %v vs %v", ErrConflictingManager, c.Manager, other.Manager)
		}
		c.Manager = other.Manager
	}

	return nil
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
			if !slices.Contains(found, filepath.Join(dir, StandardFileNames[oldName])) {
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

// ToUpdateConfig projects this Config into the UpdateConfig consumed by
// languages.Language.
func (c *Config) ToUpdateConfig() *languages.UpdateConfig {
	uc := &languages.UpdateConfig{
		Dependencies: make([]languages.Dependency, 0, len(c.Packages)+len(c.Replaces)),
		Properties:   make(map[string]string, len(c.Properties)),
		Options:      make(map[string]any),
	}

	for _, pkg := range c.Packages {
		dep := languages.Dependency{
			Name:     pkg.Name,
			Version:  pkg.Version,
			Scope:    pkg.Scope,
			Type:     pkg.Type,
			Metadata: make(map[string]any),
		}

		if pkg.GroupID != "" {
			dep.Metadata["groupId"] = pkg.GroupID
		}
		if pkg.ArtifactID != "" {
			dep.Metadata["artifactId"] = pkg.ArtifactID
		}
		if pkg.Reason != "" {
			dep.Metadata["reason"] = pkg.Reason
		}

		uc.Dependencies = append(uc.Dependencies, dep)
	}

	for _, prop := range c.Properties {
		uc.Properties[prop.Property] = prop.Value
	}

	for _, repl := range c.Replaces {
		uc.Dependencies = append(uc.Dependencies, languages.Dependency{
			OldName: repl.OldName,
			Name:    repl.Name,
			Version: repl.Version,
			Replace: true,
		})
	}

	if len(c.Manager) > 0 {
		uc.Options["manager"] = []js.Manager(c.Manager)
	}

	return uc
}

// ParseInlinePackages parses inline package specifications from command line.
//
// Three input shapes are accepted:
//
//   - Go/Rust:  name@version
//   - Maven:    groupId@artifactId@version[@scope[@type]]
//   - JS:       selector=version (use this form when the selector contains
//     '@' or '/', e.g. "@scope/name"=1.0.0 or "undici@^6"=6.24.0)
//
// The input may span multiple lines and include shell-style line comments
// introduced by '#'. Each non-empty line is tokenised on whitespace; the
// JS form is identified by the presence of '=' in a token, in which case
// the token is split on its last '=' with the left-hand side optionally
// surrounded by double quotes.
func ParseInlinePackages(packagesStr string) ([]Package, error) {
	if strings.TrimSpace(packagesStr) == "" {
		return nil, nil
	}

	var packages []Package
	for pkgStr := range strings.FieldsSeq(stripLineComments(packagesStr)) {
		if pkgStr == "" {
			continue
		}

		// JS form: selector=version. Match this first so that selectors
		// containing '@' (scoped names, version ranges) parse cleanly.
		if eq := strings.LastIndexByte(pkgStr, '='); eq >= 0 {
			selector := strings.Trim(pkgStr[:eq], `"`)
			version := strings.Trim(pkgStr[eq+1:], `"`)
			if selector == "" || version == "" {
				return nil, fmt.Errorf("%w: %s (expected selector=version)", ErrInvalidPackageFormat, pkgStr)
			}
			packages = append(packages, Package{
				Name:    selector,
				Version: version,
			})
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
			return nil, fmt.Errorf("%w: %s (expected name@version, groupId@artifactId@version, or selector=version)", ErrInvalidPackageFormat, pkgStr)
		}
	}

	return packages, nil
}

// stripLineComments removes '#'-prefixed trailing comments from each line
// in s and returns the lines rejoined with single spaces. Empty lines
// (including lines that are entirely a comment) are dropped. This lets
// callers pass multi-line YAML literal blocks with inline GHSA/CVE notes,
// e.g.
//
//	"fast-xml-parser"=5.5.7  # GHSA-37qj-frw5-hhjh
//	"simple-git"=3.36.0      # GHSA-hffm-xvc3-vprc
func stripLineComments(s string) string {
	if !strings.ContainsAny(s, "\n#") {
		return s
	}

	var b strings.Builder
	first := true
	for line := range strings.SplitSeq(s, "\n") {
		if hash := strings.IndexByte(line, '#'); hash >= 0 {
			line = line[:hash]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !first {
			b.WriteByte(' ')
		}
		b.WriteString(line)
		first = false
	}
	return b.String()
}

// ParseInlineReplaces parses inline replace specifications from command line.
// Format: space-separated "oldpkg=newpkg@version" entries.
func ParseInlineReplaces(replacesStr string) ([]Replace, error) {
	if replacesStr == "" {
		return nil, nil
	}

	var replaces []Replace
	for replStr := range strings.FieldsSeq(replacesStr) {
		if replStr == "" {
			continue
		}

		eqIdx := strings.Index(replStr, "=")
		if eqIdx < 0 {
			return nil, fmt.Errorf("%w: %s (expected oldpkg=newpkg@version)", ErrInvalidPackageFormat, replStr)
		}

		oldName := replStr[:eqIdx]
		rest := replStr[eqIdx+1:]

		atIdx := strings.LastIndex(rest, "@")
		if atIdx < 0 {
			return nil, fmt.Errorf("%w: %s (expected oldpkg=newpkg@version)", ErrInvalidPackageFormat, replStr)
		}

		replaces = append(replaces, Replace{
			OldName: oldName,
			Name:    rest[:atIdx],
			Version: rest[atIdx+1:],
		})
	}

	return replaces, nil
}

// ParseInlineProperties parses inline property specifications from command line.
// Format: "property=value" pairs separated by spaces.
func ParseInlineProperties(propsStr string) ([]Property, error) {
	if propsStr == "" {
		return nil, nil
	}

	var properties []Property
	for propStr := range strings.FieldsSeq(propsStr) {
		if propStr == "" {
			continue
		}

		parts := strings.SplitN(propStr, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("%w: %s (expected property=value)", ErrInvalidPackageFormat, propStr)
		}

		// Skip properties with empty name or value
		if parts[0] == "" || parts[1] == "" {
			continue
		}

		properties = append(properties, Property{
			Property: parts[0],
			Value:    parts[1],
		})
	}

	return properties, nil
}
