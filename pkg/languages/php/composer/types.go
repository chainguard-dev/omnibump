/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package composer

// Package represents a Composer package dependency to update.
type Package struct {
	Name    string
	Version string
	Index   int // For ordering updates
}

// LockPackage represents a package from composer.lock.
type LockPackage struct {
	Name    string   `json:"name"`
	Version string   `json:"version"`
	Require []string `json:"require,omitempty"`
}

// UpdateConfig holds configuration for Composer project updates.
type UpdateConfig struct {
	ComposerRoot string
	Update       bool // Run 'composer update' before updating packages
	ShowDiff     bool
	NoInstall    bool // Skip package installation (useful for testing)
}
