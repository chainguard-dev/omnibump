/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package ruby

// Language and manifest constants.
const (
	// LanguageRuby is the language identifier.
	LanguageRuby = "ruby"

	// ManifestGemfile is the Gemfile manifest filename.
	ManifestGemfile = "Gemfile"

	// ManifestGemfileLock is the Gemfile.lock manifest filename.
	ManifestGemfileLock = "Gemfile.lock"
)

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
