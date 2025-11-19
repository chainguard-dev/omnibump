/*
Copyright 2025 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package analyzer provides dependency analysis capabilities across different language ecosystems.
package analyzer

import "context"

// Analyzer defines the interface for analyzing project dependencies.
// Based on pombump's analyzer functionality - analyzes dependency structure
// and recommends update strategies, but does NOT perform vulnerability scanning.
type Analyzer interface {
	// Analyze performs dependency analysis on a project.
	// Returns detailed information about how dependencies are defined.
	Analyze(ctx context.Context, projectPath string) (*AnalysisResult, error)

	// RecommendStrategy suggests the best update strategy for given dependencies.
	// For example, Maven: should we update via properties or direct patches?
	RecommendStrategy(ctx context.Context, analysis *AnalysisResult, deps []Dependency) (*Strategy, error)
}

// AnalysisResult contains the results of dependency analysis.
type AnalysisResult struct {
	// Language is the detected language ecosystem
	Language string

	// Dependencies maps a unique identifier to dependency information
	// For Maven: "groupId:artifactId"
	// For Go: "module/path"
	// For Rust: "crate_name"
	Dependencies map[string]*DependencyInfo

	// Properties maps property names to their current values
	Properties map[string]string

	// PropertyUsage tracks how many dependencies use each property
	PropertyUsage map[string]int

	// Metadata stores language-specific analysis data
	Metadata map[string]any
}

// DependencyInfo contains detailed information about a single dependency.
type DependencyInfo struct {
	// Name is the dependency identifier
	Name string

	// Version is the current version
	Version string

	// UsesProperty indicates if this dependency's version comes from a property
	UsesProperty bool

	// PropertyName is the name of the property (if UsesProperty is true)
	PropertyName string

	// Transitive indicates if this is a transitive dependency
	Transitive bool

	// UpdateStrategy suggests how this dependency should be updated
	// Values: "direct", "property", "locked", "inherited"
	UpdateStrategy string

	// Metadata stores additional language-specific information
	Metadata map[string]any
}

// Dependency represents a dependency to be analyzed or updated.
type Dependency struct {
	Name     string
	Version  string
	Scope    string
	Type     string
	Metadata map[string]any
}

// Strategy contains the recommended update strategy.
type Strategy struct {
	// DirectUpdates lists dependencies that should be updated directly
	DirectUpdates []Dependency

	// PropertyUpdates maps property names to their new values
	PropertyUpdates map[string]string

	// Warnings contains any issues or recommendations
	Warnings []string

	// AffectedDependencies shows which dependencies will be affected by property updates
	AffectedDependencies map[string][]string
}
