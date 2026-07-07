/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package rust

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Test_caretConstraint checks the reduction of a concrete version to the Cargo
// caret token written into a manifest constraint: major.minor for 0.x, major for
// >=1, with pre-release/build metadata stripped.
func Test_caretConstraint(t *testing.T) {
	tests := []struct {
		version string
		want    string
	}{
		{version: "0.3.4", want: "0.3"},
		{version: "0.2.0", want: "0.2"},
		{version: "0.2", want: "0.2"},
		{version: "2.5.1", want: "2"},
		{version: "1.0.0", want: "1"},
		{version: "1", want: "1"},
		{version: "0.3.4-beta.1", want: "0.3"},
		{version: "2.5.1+build7", want: "2"},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			require.Equal(t, tt.want, caretConstraint(tt.version))
		})
	}
}

// Test_isDirect verifies base-name matching against a classifyDependencies map,
// which is keyed "name@version".
func Test_isDirect(t *testing.T) {
	direct := map[string]bool{
		"rand@0.8.5":    true,
		"serde@1.0.228": true,
	}
	require.True(t, isDirect("rand", direct))
	require.True(t, isDirect("serde", direct))
	require.False(t, isDirect("tokio", direct))
	require.False(t, isDirect("ran", direct))
}

// Test_decodeManifestSections covers detection of every declaration form across
// the dependency tables: plain string, inline table with features, git/path
// (non-registry), workspace-inherited, dev/build sections, and a
// target-conditional table.
func Test_decodeManifestSections(t *testing.T) {
	manifest := []byte(`
[package]
name = "demo"

[dependencies]
serde = "1.0"
rand = { version = "0.8", features = ["std"] }
localdep = { path = "../localdep" }
gitdep = { git = "https://example.com/gitdep" }
inherited = { workspace = true }

[dev-dependencies]
rand = "0.8"

[build-dependencies]
cc = "1.0"

[target.'cfg(windows)'.dependencies]
winapi = "0.3"
`)

	t.Run("registry in two sections", func(t *testing.T) {
		got, err := decodeManifestSections(manifest, "demo", "rand")
		require.NoError(t, err)
		require.ElementsMatch(t, []manifestSection{
			{member: "demo", kind: "", target: "", inherited: false, registry: true},
			{member: "demo", kind: "dev", target: "", inherited: false, registry: true},
		}, got)
	})

	t.Run("workspace inherited", func(t *testing.T) {
		got, err := decodeManifestSections(manifest, "demo", "inherited")
		require.NoError(t, err)
		require.Equal(t, []manifestSection{{member: "demo", inherited: true, registry: false}}, got)
	})

	t.Run("path dependency is not registry", func(t *testing.T) {
		got, err := decodeManifestSections(manifest, "demo", "localdep")
		require.NoError(t, err)
		require.Equal(t, []manifestSection{{member: "demo", registry: false}}, got)
	})

	t.Run("git dependency is not registry", func(t *testing.T) {
		got, err := decodeManifestSections(manifest, "demo", "gitdep")
		require.NoError(t, err)
		require.Equal(t, []manifestSection{{member: "demo", registry: false}}, got)
	})

	t.Run("target-conditional dependency", func(t *testing.T) {
		got, err := decodeManifestSections(manifest, "demo", "winapi")
		require.NoError(t, err)
		require.Equal(t, []manifestSection{{member: "demo", kind: "", target: "cfg(windows)", registry: true}}, got)
	})

	t.Run("build dependency", func(t *testing.T) {
		got, err := decodeManifestSections(manifest, "demo", "cc")
		require.NoError(t, err)
		require.Equal(t, []manifestSection{{member: "demo", kind: "build", registry: true}}, got)
	})

	t.Run("crate not declared", func(t *testing.T) {
		got, err := decodeManifestSections(manifest, "demo", "absent")
		require.NoError(t, err)
		require.Empty(t, got)
	})
}

// Test_bumpWorkspaceDependency verifies the scoped, section-anchored root-table
// edit: it rewrites only the target crate's version in [workspace.dependencies],
// handles both the plain-string and inline-table forms, preserves comments and
// sibling entries, and never touches a same-named key in another table.
func Test_bumpWorkspaceDependency(t *testing.T) {
	const manifest = `[workspace]
members = ["member"]

[workspace.dependencies]
# pinned for MSRV reasons
rand = "0.8"
serde = { version = "1.0", features = ["derive"] }

[dependencies]
rand = "0.8"
`

	t.Run("plain string form", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "Cargo.toml")
		require.NoError(t, os.WriteFile(path, []byte(manifest), 0o644))

		require.NoError(t, bumpWorkspaceDependency(path, "rand", "0.9"))

		got, err := os.ReadFile(path)
		require.NoError(t, err)
		want := `[workspace]
members = ["member"]

[workspace.dependencies]
# pinned for MSRV reasons
rand = "0.9"
serde = { version = "1.0", features = ["derive"] }

[dependencies]
rand = "0.8"
`
		require.Equal(t, want, string(got))
	})

	t.Run("inline table form preserves features", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "Cargo.toml")
		require.NoError(t, os.WriteFile(path, []byte(manifest), 0o644))

		require.NoError(t, bumpWorkspaceDependency(path, "serde", "2"))

		got, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Contains(t, string(got), `serde = { version = "2", features = ["derive"] }`)
		require.Contains(t, string(got), `rand = "0.8"`) // unrelated entries untouched
	})

	t.Run("missing crate errors", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "Cargo.toml")
		require.NoError(t, os.WriteFile(path, []byte(manifest), 0o644))
		require.ErrorIs(t, bumpWorkspaceDependency(path, "tokio", "1"), ErrNoCompatibleVersion)
	})
}

// Test_replaceManifestVersion covers the single-line version rewrite for both the
// plain-string and inline-table forms, and the no-match case.
func Test_replaceManifestVersion(t *testing.T) {
	tests := []struct {
		name        string
		line        string
		caret       string
		want        string
		wantChanged bool
	}{
		{name: "plain string", line: `rand = "0.8"`, caret: "0.9", want: `rand = "0.9"`, wantChanged: true},
		{name: "indented plain", line: `    rand = "0.8"`, caret: "0.9", want: `    rand = "0.9"`, wantChanged: true},
		{
			name: "inline table", line: `serde = { version = "1.0", features = ["derive"] }`, caret: "2",
			want: `serde = { version = "2", features = ["derive"] }`, wantChanged: true,
		},
		{name: "no assignment", line: `# comment`, caret: "1", want: `# comment`, wantChanged: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := replaceManifestVersion(tt.line, tt.caret)
			require.Equal(t, tt.wantChanged, changed)
			require.Equal(t, tt.want, got)
		})
	}
}

// Test_cargoToolchain covers the toolchain resolution: default "stable" when the
// env var is unset, an explicit override, and an empty value disabling it.
func Test_cargoToolchain(t *testing.T) {
	t.Run("defaults to stable when unset", func(t *testing.T) {
		orig, had := os.LookupEnv(cargoToolchainEnv)
		require.NoError(t, os.Unsetenv(cargoToolchainEnv))
		t.Cleanup(func() {
			if had {
				_ = os.Setenv(cargoToolchainEnv, orig)
			} else {
				_ = os.Unsetenv(cargoToolchainEnv)
			}
		})
		require.Equal(t, "stable", cargoToolchain())
	})

	t.Run("env overrides toolchain", func(t *testing.T) {
		t.Setenv(cargoToolchainEnv, "nightly-2024-01-01")
		require.Equal(t, "nightly-2024-01-01", cargoToolchain())
	})

	t.Run("empty env disables the override", func(t *testing.T) {
		t.Setenv(cargoToolchainEnv, "")
		require.Equal(t, "", cargoToolchain())
	})
}

// Test_cargoCommand verifies the toolchain is inserted as a leading "+<toolchain>"
// argument (before the subcommand) and omitted when disabled.
func Test_cargoCommand(t *testing.T) {
	t.Run("prepends toolchain override", func(t *testing.T) {
		t.Setenv(cargoToolchainEnv, "stable")
		cmd := cargoCommand(context.Background(), "/tmp/demo", "metadata", "--no-deps")
		require.Equal(t, []string{"cargo", "+stable", "metadata", "--no-deps"}, cmd.Args)
		require.Equal(t, "/tmp/demo", cmd.Dir)
	})

	t.Run("omits override when disabled", func(t *testing.T) {
		t.Setenv(cargoToolchainEnv, "")
		cmd := cargoCommand(context.Background(), "/tmp/demo", "update")
		require.Equal(t, []string{"cargo", "update"}, cmd.Args)
	})
}

// Test_crossesSemverBoundary covers the detector that routes precise pins through
// the manifest edit: true only when the target is outside every present caret line
// AND above the highest present version (an upgrade cargo cannot make on its own).
func Test_crossesSemverBoundary(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		present []string
		want    bool
	}{
		{name: "0.x cross-boundary upgrade", target: "0.9.0", present: []string{"0.8.5"}, want: true},
		{name: "0.x in-line patch", target: "0.8.6", present: []string{"0.8.5"}, want: false},
		{name: "0.x downgrade across boundary", target: "0.7.0", present: []string{"0.8.5"}, want: false},
		{name: "major cross-boundary upgrade", target: "2.0.0", present: []string{"1.5.0"}, want: true},
		{name: "major same line", target: "1.6.0", present: []string{"1.5.0"}, want: false},
		{name: "nothing present", target: "0.9.0", present: nil, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, crossesSemverBoundary(tt.target, tt.present))
		})
	}
}

// Test_CrossSemverDirectPrecise is an end-to-end check that a precise-pin bump
// (name@from=to) of a DIRECT dependency across a SemVer boundary rewrites the
// Cargo.toml constraint before pinning, then lands the exact target in Cargo.lock.
// Without the manifest edit, `cargo update --precise` is rejected by the old
// constraint. Needs network access.
func Test_CrossSemverDirectPrecise(t *testing.T) {
	cargoRoot := scaffoldCrate(t, `[package]
name = "demo"
version = "0.1.0"
edition = "2021"

[dependencies]
rand = "0.8"
`)

	original, err := GetCurrentPackages(context.Background(), cargoRoot)
	require.NoError(t, err)
	from := lockedVersion(original, "rand")
	require.NotEmpty(t, from, "expected rand to be locked in the 0.8 line")

	// A precise pin arrives keyed "name@from" with the target as the version, which
	// DoUpdate reconstructs into the "name@from=to" precise spec.
	key := "rand@" + from
	packages := map[string]*Package{key: {Name: key, Version: "0.9.0", Index: 0}}

	err = DoUpdate(context.Background(), packages, original, &UpdateConfig{CargoRoot: cargoRoot})
	require.NoError(t, err)

	manifest, err := os.ReadFile(filepath.Join(cargoRoot, "Cargo.toml"))
	require.NoError(t, err)
	require.Contains(t, string(manifest), `rand = "0.9"`)

	updated, err := GetCurrentPackages(context.Background(), cargoRoot)
	require.NoError(t, err)
	require.Equal(t, "0.9.0", lockedVersion(updated, "rand"), "expected rand pinned exactly to 0.9.0")
}

// Test_CrossSemverDirect is an end-to-end check that a SemVer-breaking bump of a
// DIRECT dependency rewrites its Cargo.toml constraint (replacing the old `sed`
// hack) and moves Cargo.lock onto the new line. It scaffolds a minimal crate and
// invokes real cargo, so it needs network/registry access.
func Test_CrossSemverDirect(t *testing.T) {
	cargoRoot := scaffoldCrate(t, `[package]
name = "demo"
version = "0.1.0"
edition = "2021"

[dependencies]
rand = "0.8"
`)

	packages := map[string]*Package{"rand": {Name: "rand", Version: "0.9", Index: 0}}
	original, err := GetCurrentPackages(context.Background(), cargoRoot)
	require.NoError(t, err)

	err = DoUpdate(context.Background(), packages, original, &UpdateConfig{CargoRoot: cargoRoot})
	require.NoError(t, err)

	manifest, err := os.ReadFile(filepath.Join(cargoRoot, "Cargo.toml"))
	require.NoError(t, err)
	require.Contains(t, string(manifest), `rand = "0.9"`)
	require.NotContains(t, string(manifest), `rand = "0.8"`)

	updated, err := GetCurrentPackages(context.Background(), cargoRoot)
	require.NoError(t, err)
	require.True(t, hasVersionInLine(updated, "rand", "0.9"), "expected rand 0.9.x in Cargo.lock")
}

// Test_CrossSemverWorkspaceInherited checks that a SemVer-breaking bump of a
// workspace-inherited direct dependency rewrites the root [workspace.dependencies]
// table (which cargo add cannot manage) and moves the lock. Needs network access.
func Test_CrossSemverWorkspaceInherited(t *testing.T) {
	cargoRoot := t.TempDir()
	writeFile(t, filepath.Join(cargoRoot, "Cargo.toml"), `[workspace]
members = ["member"]
resolver = "2"

[workspace.dependencies]
rand = "0.8"
`)
	writeFile(t, filepath.Join(cargoRoot, "member", "Cargo.toml"), `[package]
name = "member"
version = "0.1.0"
edition = "2021"

[dependencies]
rand = { workspace = true }
`)
	writeFile(t, filepath.Join(cargoRoot, "member", "src", "lib.rs"), "")
	generateLockfile(t, cargoRoot)

	packages := map[string]*Package{"rand": {Name: "rand", Version: "0.9", Index: 0}}
	original, err := GetCurrentPackages(context.Background(), cargoRoot)
	require.NoError(t, err)

	err = DoUpdate(context.Background(), packages, original, &UpdateConfig{CargoRoot: cargoRoot})
	require.NoError(t, err)

	root, err := os.ReadFile(filepath.Join(cargoRoot, "Cargo.toml"))
	require.NoError(t, err)
	require.Contains(t, string(root), `rand = "0.9"`)

	updated, err := GetCurrentPackages(context.Background(), cargoRoot)
	require.NoError(t, err)
	require.True(t, hasVersionInLine(updated, "rand", "0.9"), "expected rand 0.9.x in Cargo.lock")
}

// Test_CrossSemverIndirectHardFails confirms scope decision #1: a SemVer-breaking
// bump of an INDIRECT dependency is not satisfied by a manifest edit (there is
// nothing to edit — it is not declared in Cargo.toml) and keeps the pre-existing
// hard failure. Only direct deps get the constraint rewrite. Needs network access.
func Test_CrossSemverIndirectHardFails(t *testing.T) {
	cargoRoot := scaffoldCrate(t, `[package]
name = "demo"
version = "0.1.0"
edition = "2021"

[dependencies]
rand = "0.8"
`)

	// rand_core is pulled in transitively by rand ^0.8 (locked at 0.6.x); it is
	// not a direct dependency, so requesting a cross-line 0.9 must fail.
	packages := map[string]*Package{"rand_core": {Name: "rand_core", Version: "0.9", Index: 0}}
	original, err := GetCurrentPackages(context.Background(), cargoRoot)
	require.NoError(t, err)

	err = DoUpdate(context.Background(), packages, original, &UpdateConfig{CargoRoot: cargoRoot})
	require.ErrorIs(t, err, ErrNoCompatibleVersion)

	manifest, err := os.ReadFile(filepath.Join(cargoRoot, "Cargo.toml"))
	require.NoError(t, err)
	require.NotContains(t, string(manifest), "rand_core", "indirect dep must not be added to Cargo.toml")
}

// scaffoldCrate writes a single-crate project (manifest + empty lib) into a temp
// dir and generates its Cargo.lock, returning the crate root.
func scaffoldCrate(t *testing.T, manifest string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Cargo.toml"), manifest)
	writeFile(t, filepath.Join(root, "src", "lib.rs"), "")
	generateLockfile(t, root)
	return root
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func generateLockfile(t *testing.T, cargoRoot string) {
	t.Helper()
	cmd := cargoCommand(context.Background(), cargoRoot, "generate-lockfile")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cargo generate-lockfile: %v\n%s", err, out)
	}
}

// hasVersionInLine reports whether any locked instance of name falls in the same
// caret line as want.
func hasVersionInLine(pkgs []CargoPackage, name, want string) bool {
	for _, p := range pkgs {
		if p.Name == name && cargoCompatible(want, p.Version) {
			return true
		}
	}
	return false
}

// lockedVersion returns the version of the first locked instance of name, or ""
// if it is not present.
func lockedVersion(pkgs []CargoPackage, name string) string {
	for _, p := range pkgs {
		if p.Name == name {
			return p.Version
		}
	}
	return ""
}
