/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package php implements omnibump support for PHP projects.
// Supports multiple build tools (Composer, etc.)
package php

import (
	"context"

	"github.com/chainguard-dev/omnibump/pkg/analyzer"
	"github.com/chainguard-dev/omnibump/pkg/languages"
)

// BuildTool defines the interface that each PHP build tool must implement.
// This allows omnibump to support Composer and other PHP build systems
// with a unified interface.
type BuildTool interface {
	// Name returns the build tool identifier (e.g., "composer")
	Name() string

	// Detect checks if this build tool is present in the given directory.
	// Returns true if manifest files for this build tool are found.
	Detect(ctx context.Context, dir string) (bool, error)

	// Update performs the dependency update using the provided configuration.
	Update(ctx context.Context, cfg *languages.UpdateConfig) error

	// Validate checks if the updates were applied successfully.
	Validate(ctx context.Context, cfg *languages.UpdateConfig) error

	// GetManifestFiles returns the list of manifest files for this build tool
	// (e.g., ["composer.json", "composer.lock"] for Composer)
	GetManifestFiles() []string

	// GetAnalyzer returns an analyzer for this build tool, or nil if not supported.
	GetAnalyzer() analyzer.Analyzer
}
