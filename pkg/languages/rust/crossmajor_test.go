/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package rust

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/chainguard-dev/clog"
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

// capturingHandler is a minimal slog.Handler that records the level and rendered
// message of each log call, for asserting log behavior.
type capturingHandler struct{ records *[]slog.Record }

func (h capturingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h capturingHandler) Handle(_ context.Context, r slog.Record) error {
	*h.records = append(*h.records, r)
	return nil
}
func (h capturingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h capturingHandler) WithGroup(string) slog.Handler      { return h }

// Test_verifyTransitiveUpgrade covers the outcome logging when a precise pin's
// original line is gone: success (info) when the crate reached the requested
// version — including when it is already exactly at it — and a warning only when it
// remains below the request.
func Test_verifyTransitiveUpgrade(t *testing.T) {
	tests := []struct {
		name      string
		present   []string
		requested string
		wantLevel slog.Level
		wantMsg   string
	}{
		{
			name: "already at requested", present: []string{"0.22.3"}, requested: "0.22.3",
			wantLevel: slog.LevelInfo, wantMsg: "satisfies the requested 0.22.3",
		},
		{
			name: "above requested", present: []string{"0.22.5"}, requested: "0.22.3",
			wantLevel: slog.LevelInfo, wantMsg: "satisfies the requested 0.22.3",
		},
		{
			name: "below requested", present: []string{"0.21.12"}, requested: "0.22.3",
			wantLevel: slog.LevelWarn, wantMsg: "did not pull in 0.22.3 as expected",
		},
		{
			name: "no longer present", present: nil, requested: "0.22.3",
			wantLevel: slog.LevelWarn, wantMsg: "did not pull in 0.22.3 as expected",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var recs []slog.Record
			ctx := clog.WithLogger(context.Background(), clog.New(capturingHandler{&recs}))
			verifyTransitiveUpgrade(ctx, "tract-nnef", tt.present, tt.requested)
			require.Len(t, recs, 1)
			require.Equal(t, tt.wantLevel, recs[0].Level)
			require.Contains(t, recs[0].Message, tt.wantMsg)
		})
	}
}

// Test_satisfiesFloor covers the cheap pre-flight skip: true when a present
// version is at or above the requested floor.
func Test_satisfiesFloor(t *testing.T) {
	tests := []struct {
		name    string
		present []string
		floor   string
		want    bool
	}{
		{name: "above floor", present: []string{"0.9.4"}, floor: "0.9.0", want: true},
		{name: "at floor", present: []string{"0.9.0"}, floor: "0.9.0", want: true},
		{name: "below floor", present: []string{"0.8.5"}, floor: "0.9.0", want: false},
		{name: "partial floor satisfied", present: []string{"0.9.4"}, floor: "0.9", want: true},
		{name: "newer major satisfies", present: []string{"2.0.0"}, floor: "1.5.0", want: true},
		{name: "nothing present", present: nil, floor: "0.9.0", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, satisfiesFloor(tt.present, tt.floor))
		})
	}
}

// Test_CrossSemverDirectPrecise is an end-to-end check that a precise-pin bump
// (name@from=to) of a DIRECT dependency across a SemVer boundary rewrites the
// Cargo.toml constraint before pinning, then lands the exact target in Cargo.lock.
// Without the manifest edit, `cargo update --precise` is rejected by the old
// constraint. Needs network access.
func Test_CrossSemverDirectPrecise(t *testing.T) {
	cargoRoot := scaffoldRandCrate(t)

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
	cargoRoot := scaffoldRandCrate(t)

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
	require.True(t, cargoCompatible("0.9", lockedVersion(updated, "rand")), "expected rand 0.9.x in Cargo.lock")
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
	require.True(t, cargoCompatible("0.9", lockedVersion(updated, "rand")), "expected rand 0.9.x in Cargo.lock")
}

// Test_CrossSemverIndirectResolves exercises the reverse-dependency engine on an
// INDIRECT target: rand_core is pulled in transitively by rand ^0.8 (locked in the
// 0.6 line). Requesting rand_core 0.9 is satisfied by bumping the direct dependency
// rand to 0.9 (which requires rand_core 0.9) and editing demo's Cargo.toml — not by
// adding rand_core directly. Needs network access.
func Test_CrossSemverIndirectResolves(t *testing.T) {
	cargoRoot := scaffoldRandCrate(t)

	packages := map[string]*Package{"rand_core": {Name: "rand_core", Version: "0.9", Index: 0}}
	original, err := GetCurrentPackages(context.Background(), cargoRoot)
	require.NoError(t, err)

	err = DoUpdate(context.Background(), packages, original, &UpdateConfig{CargoRoot: cargoRoot})
	require.NoError(t, err)

	// The gating direct dependency (rand) is the one edited, not the indirect target.
	manifest, err := os.ReadFile(filepath.Join(cargoRoot, "Cargo.toml"))
	require.NoError(t, err)
	require.Contains(t, string(manifest), `rand = "0.9"`)
	require.NotContains(t, string(manifest), "rand_core", "indirect dep must not be added to Cargo.toml")

	updated, err := GetCurrentPackages(context.Background(), cargoRoot)
	require.NoError(t, err)
	require.True(t, cargoCompatible("0.9", lockedVersion(updated, "rand")), "expected rand 0.9.x in Cargo.lock")
	require.True(t, cargoCompatible("0.9", lockedVersion(updated, "rand_core")), "expected rand_core 0.9.x in Cargo.lock")
}

// Test_CargoCheck_ReportsFailure verifies CargoCheck surfaces a compile failure
// (error plus captured output). It uses a crate with invalid Rust so the check
// fails deterministically without network access.
func Test_CargoCheck_ReportsFailure(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Cargo.toml"), `[package]
name = "demo"
version = "0.1.0"
edition = "2021"
`)
	writeFile(t, filepath.Join(root, "src", "lib.rs"), "fn broken( { this is not valid rust")

	out, err := CargoCheck(context.Background(), root)
	require.Error(t, err)
	require.NotEmpty(t, out, "expected cargo check to surface compiler output")
}

// errStubCheckFailed is the failure returned by the stubbed build check in
// Test_CrossSemver_CheckFailureReturnsError.
var errStubCheckFailed = errors.New("cargo check exit 101")

// Test_CrossSemver_CheckFailureReturnsError verifies that when the post-edit
// cargo check fails, the SemVer-breaking upgrade is rejected with
// ErrUpgradeBrokeBuild and the edits are left on disk for the caller to discard.
// The check is stubbed so the failure is deterministic. Needs network access for
// the manifest edit / lock reconcile.
func Test_CrossSemver_CheckFailureReturnsError(t *testing.T) {
	cargoRoot := scaffoldRandCrate(t)

	orig := checkBuild
	checkBuild = func(_ context.Context, _ string) (string, error) {
		return "simulated compile error", errStubCheckFailed
	}
	defer func() { checkBuild = orig }()

	packages := map[string]*Package{"rand": {Name: "rand", Version: "0.9", Index: 0}}
	original, err := GetCurrentPackages(context.Background(), cargoRoot)
	require.NoError(t, err)

	err = DoUpdate(context.Background(), packages, original, &UpdateConfig{CargoRoot: cargoRoot})
	require.ErrorIs(t, err, ErrUpgradeBrokeBuild)

	// Per the "leave edits in place" contract, the widened constraint remains.
	manifest, rerr := os.ReadFile(filepath.Join(cargoRoot, "Cargo.toml"))
	require.NoError(t, rerr)
	require.Contains(t, string(manifest), `rand = "0.9"`)
}

// scaffoldRandCrate writes a single-crate project pinned at `rand = "0.8"` (the
// shared cross-SemVer fixture: 0.8 -> 0.9 is a boundary-crossing bump) plus an
// empty lib, generates its Cargo.lock, and returns the crate root.
func scaffoldRandCrate(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Cargo.toml"), `[package]
name = "demo"
version = "0.1.0"
edition = "2021"

[dependencies]
rand = "0.8"
`)
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
