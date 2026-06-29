/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package ruby implements omnibump support for Ruby projects using Bundler.
//
// Updates are performed via direct Gemfile.lock text editing — no Ruby or
// Bundler CLI is required at runtime. This matches the deployment model where
// omnibump runs in melange build sandboxes without guaranteed toolchain
// availability.
//
// # Manifest files
//
// The package detects Ruby projects by the presence of Gemfile.lock in the
// target directory. Both Gemfile and Gemfile.lock are listed as manifest files,
// but only Gemfile.lock is parsed and modified.
//
// # Gemfile.lock parsing
//
// The parser handles the three source section types found in Bundler lockfiles:
//
//   - GEM — gems from a rubygems remote (e.g. rubygems.org)
//   - GIT — gems from a git repository
//   - PATH — gems from a local filesystem path
//
// Within each section, spec lines (4-space indent) identify gems and their
// versions, while dependency constraint lines (6-space indent) are preserved
// but not modified during updates.
//
// # Update strategy
//
// All updates are direct text replacements: the version in "    gemname (oldversion)"
// is replaced with the new version. Dependency constraint lines are left untouched.
// Downgrade attempts are detected via semver comparison and skipped with a warning.
//
// # Gem-dir overlay mode
//
// When --gem-dir is specified, omnibump installs patched gems directly into
// an existing gem directory using `gem install --install-dir`. This is used
// for CVE remediation when a Ruby package bundles a transitive dependency
// that needs patching without rebuilding the entire bundle.
//
// Overlay mode validates gem names and versions, rejects downgrades, and
// verifies installed versions by scanning the specifications/ directory
// (no `gem list` command needed for validation). Each gem is installed
// individually with --force to overwrite existing versions.
//
// # Limitations
//
//   - No constraint validation: bumping a gem to a version that violates another
//     gem's constraints will produce an inconsistent lockfile.
//   - No checksum or integrity handling (Gemfile.lock has none, unlike go.sum).
//   - AnalyzeRemote is not yet implemented.
package ruby
