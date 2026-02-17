/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package rust implements omnibump support for Rust projects.
// Ported from cargobump with enhancements for the unified omnibump architecture.
package rust

// Package represents a Cargo package dependency.
type Package struct {
	Name    string
	Version string
	Index   int // For ordering updates
}

// CargoPackage represents a package from Cargo.lock.
type CargoPackage struct {
	Name         string
	Version      string
	Source       string
	Dependencies []string
}

// UpdateConfig holds configuration for Rust project updates.
type UpdateConfig struct {
	CargoRoot string
	Update    bool // Run 'cargo update' before updating packages
	ShowDiff  bool
}
