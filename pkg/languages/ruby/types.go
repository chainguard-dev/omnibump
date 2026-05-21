/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package ruby implements omnibump support for Ruby projects using Bundler.
package ruby

// Package represents a target gem dependency to update.
type Package struct {
	Name    string
	Version string
	Index   int // For ordering updates
}

// GemPackage represents a gem from Gemfile.lock.
type GemPackage struct {
	Name         string
	Version      string
	Source       string
	Dependencies []string // Dependency constraints (e.g., "rack (~> 3.0)")
}
