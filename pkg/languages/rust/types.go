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

// IsLocal reports whether the package is the local/workspace crate itself
// rather than an external dependency. Local crates have no Source in Cargo.lock,
// whereas registry, git, and remote-path dependencies always carry one.
func (c CargoPackage) IsLocal() bool {
	return c.Source == ""
}

// classifyDependencies partitions Cargo.lock packages into the project's own
// (local/workspace) crates and its direct dependencies.
//
// A direct dependency is one declared in Cargo.toml; in Cargo.lock this is
// exactly the set of crates listed in a local crate's dependencies array.
// Everything reachable only transitively is indirect. Both returned maps are
// keyed by the "name@version" identifier produced by packageID.
func classifyDependencies(pkgs []CargoPackage) (direct, roots map[string]bool) {
	direct = make(map[string]bool)
	roots = make(map[string]bool)

	for _, pkg := range pkgs {
		if !pkg.IsLocal() {
			continue
		}
		roots[packageID(pkg.Name, pkg.Version)] = true
		for _, dep := range pkg.Dependencies {
			direct[dep] = true
		}
	}

	return direct, roots
}

// UpdateConfig holds configuration for Rust project updates.
type UpdateConfig struct {
	CargoRoot string
	Update    bool // Run 'cargo update' before updating packages
	ShowDiff  bool
}

// manifestSection records one place a crate is declared in a Cargo.toml manifest.
// A crate can be declared in several sections (e.g. both [dependencies] and
// [dev-dependencies], or under a [target.'cfg(...)'.dependencies] table), so a
// cross-SemVer bump must edit every one of them. The fields carry exactly what a
// `cargo add` invocation needs to target the same declaration.
type manifestSection struct {
	member    string // owning workspace member's package name, for `cargo add --package`
	kind      string // "" (normal), "dev", or "build"
	target    string // cfg/target string for a [target.'...'.dependencies] entry; "" otherwise
	inherited bool   // the member declares `dep.workspace = true`; the constraint lives in the root [workspace.dependencies]
	registry  bool   // a registry (crates.io) dependency; false for git/path deps, which cannot be bumped by version
}
