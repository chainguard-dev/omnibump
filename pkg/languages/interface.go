/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package languages provides a plugin architecture for supporting multiple programming language ecosystems.
package languages

import "context"

// Language defines the interface that each language ecosystem must implement.
// This allows omnibump to support multiple languages (Rust, Go, Maven) with a unified interface.
type Language interface {
	// Name returns the language identifier (e.g., "maven", "go", "rust")
	Name() string

	// Detect checks if this language is present in the given directory.
	// Returns true if manifest files for this language are found.
	Detect(ctx context.Context, dir string) (bool, error)

	// Update performs the dependency update using the provided configuration.
	Update(ctx context.Context, cfg *UpdateConfig) error

	// Validate checks if the updates were applied successfully.
	// Returns an error if any dependency wasn't updated to the expected version.
	Validate(ctx context.Context, cfg *UpdateConfig) error

	// GetManifestFiles returns the list of manifest files for this language
	// (e.g., ["go.mod", "go.sum"] for Go, ["pom.xml"] for Maven)
	GetManifestFiles() []string

	// SupportsAnalysis returns true if this language supports dependency analysis.
	SupportsAnalysis() bool

	// ContainsPackage checks if the dependency manifest contains the specified package.
	// Returns true if the package is found in the manifest files.
	ContainsPackage(ctx context.Context, dir string, packageName string) (bool, error)
}

// UpdateConfig contains all configuration needed to perform a dependency update.
type UpdateConfig struct {
	// RootDir is the root directory of the project
	RootDir string

	// Dependencies is the list of dependencies to update
	Dependencies []Dependency

	// Properties contains property updates (for build systems that support them)
	Properties map[string]string

	// Tidy indicates whether to run the language's "tidy" command after updates
	Tidy bool

	// ShowDiff indicates whether to show a diff of changes
	ShowDiff bool

	// DryRun indicates whether to only simulate the update without making changes
	DryRun bool

	// Options contains language-specific options as a flexible map
	Options map[string]any
}

// Dependency represents a single dependency to be updated.
type Dependency struct {
	// Name is the primary package/module name
	Name string

	// OldName is used for module replacements (Go-specific)
	OldName string

	// Version is the target version to update to
	Version string

	// Scope is the dependency scope (Maven-specific: compile, test, runtime, etc.)
	Scope string

	// Type is the dependency type (Maven-specific: jar, pom, etc.)
	Type string

	// Replace indicates this is a replacement directive (Go-specific)
	Replace bool

	// Additional metadata can be stored here
	Metadata map[string]any
}
