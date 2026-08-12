/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package rust

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Test_featureSelection_args covers the cargo feature flags rendered for a
// discovery command: an explicit list becomes a single comma-joined --features,
// and the empty selection falls back to --all-features so discovery never silently
// resolves only default features (AUTO-1142).
func Test_featureSelection_args(t *testing.T) {
	tests := []struct {
		name string
		fs   featureSelection
		want []string
	}{
		{name: "empty defaults to all-features", fs: featureSelection{}, want: []string{"--all-features"}},
		{name: "nil slice defaults to all-features", fs: featureSelection{features: nil}, want: []string{"--all-features"}},
		{name: "single feature", fs: featureSelection{features: []string{"net"}}, want: []string{"--features", "net"}},
		{name: "multiple features are comma-joined", fs: featureSelection{features: []string{"fjall", "syslog", "journald"}}, want: []string{"--features", "fjall,syslog,journald"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.fs.args())
		})
	}
}

// Test_resolveFeatureSelection covers reading the feature list out of the
// language-agnostic Options map, from both the typed ([]string, from the CLI /
// config) and generic ([]any, from a raw YAML decode) shapes, dropping blanks and
// falling back to the zero value (which resolves to --all-features) for anything
// else.
func Test_resolveFeatureSelection(t *testing.T) {
	tests := []struct {
		name    string
		options map[string]any
		want    []string
	}{
		{name: "absent key", options: map[string]any{}, want: nil},
		{name: "nil map", options: nil, want: nil},
		{name: "string slice", options: map[string]any{"features": []string{"a", "b"}}, want: []string{"a", "b"}},
		{name: "any slice", options: map[string]any{"features": []any{"a", "b"}}, want: []string{"a", "b"}},
		{name: "blanks dropped", options: map[string]any{"features": []string{"a", "", "  ", "b"}}, want: []string{"a", "b"}},
		{name: "wrong type ignored", options: map[string]any{"features": "a,b"}, want: nil},
		{name: "any slice with non-strings", options: map[string]any{"features": []any{"a", 3, "b"}}, want: []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveFeatureSelection(tt.options)
			require.Equal(t, tt.want, got.features)
		})
	}
}

// Test_featureSelection_context checks the context round-trip and that a context
// with no selection set resolves to the all-features default rather than
// default-only resolution.
func Test_featureSelection_context(t *testing.T) {
	t.Run("round-trip", func(t *testing.T) {
		ctx := withFeatureSelection(context.Background(), featureSelection{features: []string{"x"}})
		require.Equal(t, []string{"x"}, featureSelectionFrom(ctx).features)
	})
	t.Run("unset resolves to all-features", func(t *testing.T) {
		require.Equal(t, []string{"--all-features"}, featureSelectionFrom(context.Background()).args())
	})
}

// scaffoldFeatureGatedWorkspace writes a workspace where crate `deep` is reachable
// only through `mid`'s non-default `withdeep` feature, which `app`'s non-default
// `extra` feature enables. Under default features cargo does not traverse into
// `deep`; only a feature-expanded resolve (--all-features or --features extra) does.
// This is the AUTO-1142 shape in miniature, built entirely from path crates so no
// network is required. It returns the workspace root.
func scaffoldFeatureGatedWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Cargo.toml"), `[workspace]
members = ["app"]
resolver = "2"
`)
	writeFile(t, filepath.Join(root, "app", "Cargo.toml"), `[package]
name = "app"
version = "0.1.0"
edition = "2021"

[dependencies]
mid = { path = "../mid" }

[features]
extra = ["mid/withdeep"]
`)
	writeFile(t, filepath.Join(root, "app", "src", "main.rs"), "fn main() {}\n")
	writeFile(t, filepath.Join(root, "mid", "Cargo.toml"), `[package]
name = "mid"
version = "0.1.0"
edition = "2021"

[dependencies]
deep = { path = "../deep", optional = true }

[features]
withdeep = ["dep:deep"]
`)
	writeFile(t, filepath.Join(root, "mid", "src", "lib.rs"), "\n")
	writeFile(t, filepath.Join(root, "deep", "Cargo.toml"), `[package]
name = "deep"
version = "0.1.0"
edition = "2021"
`)
	writeFile(t, filepath.Join(root, "deep", "src", "lib.rs"), "\n")

	// A committed lockfile makes the discovery commands deterministic; generating it
	// only touches local path crates, so it needs no network.
	cmd := exec.CommandContext(context.Background(), "cargo", "generate-lockfile")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "cargo generate-lockfile failed: %s", out)
	return root
}

// Test_cargoTreeInverted_featureGated is the regression test for AUTO-1142's
// reverse-dependency path: a crate gated behind a non-default feature has its
// dependents surfaced only when discovery runs with the feature enabled. Under
// default features `cargo tree -i deep` shows just the root (no dependents to
// widen); the feature-expanded query — as production now runs by default — shows
// the full inverted tree up to the workspace member.
func Test_cargoTreeInverted_featureGated(t *testing.T) {
	root := scaffoldFeatureGatedWorkspace(t)

	// Default features (the pre-fix behavior): the inverted tree stops at the root,
	// so the reverse-dependency engine sees no dependents.
	def, _, err := runCargoTree(context.Background(), root, "deep", nil)
	require.NoError(t, err)
	require.Contains(t, def, "deep")
	require.NotContains(t, def, "mid", "default-feature tree must not reach the gated dependent")

	// --all-features (the default selection) surfaces the gated dependents.
	all := withFeatureSelection(context.Background(), featureSelection{})
	treeAll, err := cargoTreeInverted(all, root, "deep")
	require.NoError(t, err)
	require.Contains(t, treeAll, "mid")
	require.Contains(t, treeAll, "app")

	// An explicit feature list that activates the gate is equivalent.
	extra := withFeatureSelection(context.Background(), featureSelection{features: []string{"extra"}})
	treeExtra, err := cargoTreeInverted(extra, root, "deep")
	require.NoError(t, err)
	require.Contains(t, treeExtra, "mid")
	require.Contains(t, treeExtra, "app")
}

// Test_presentVersions_featureGated checks the discovery-metadata path end to end:
// with the feature-expanded resolve (the default selection), the gated crate is
// reported as present, so its pin is no longer skipped as "not present in the
// dependency graph".
func Test_presentVersions_featureGated(t *testing.T) {
	root := scaffoldFeatureGatedWorkspace(t)

	ctx := withFeatureSelection(context.Background(), featureSelection{})
	got, err := presentVersions(ctx, root, "deep")
	require.NoError(t, err)
	require.Equal(t, []string{"0.1.0"}, got)
}
