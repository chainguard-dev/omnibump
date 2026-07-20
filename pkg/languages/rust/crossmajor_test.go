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
	"golang.org/x/mod/semver"
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
		// Pre-release versions keep their full form so the written constraint opts
		// into the pre-release line (a truncated "0.3"/"0.10" would never match).
		{version: "0.3.4-beta.1", want: "0.3.4-beta.1"},
		{version: "0.10.0-rc.18", want: "0.10.0-rc.18"},
		{version: "1.0.0-alpha.2", want: "1.0.0-alpha.2"},
		// Build metadata (no pre-release) is still stripped and truncated.
		{version: "2.5.1+build7", want: "2"},
		{version: "0.3.4-beta.1+build7", want: "0.3.4-beta.1"},
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

// withToolchainProbe overrides the cargo toolchain-support probe and resets its
// cached result for the duration of a test, restoring both afterward.
func withToolchainProbe(t *testing.T, probe func(context.Context, string) bool) {
	t.Helper()
	toolchainMu.Lock()
	origProbe, origArg, origDone := toolchainProbe, toolchainArg, toolchainDone
	toolchainProbe, toolchainArg, toolchainDone = probe, "", false
	toolchainMu.Unlock()
	t.Cleanup(func() {
		toolchainMu.Lock()
		toolchainProbe, toolchainArg, toolchainDone = origProbe, origArg, origDone
		toolchainMu.Unlock()
	})
}

// Test_cargoCommand verifies the toolchain is inserted as a leading "+<toolchain>"
// argument only when cargo supports it, and omitted when unsupported or disabled.
func Test_cargoCommand(t *testing.T) {
	t.Run("prepends toolchain when supported", func(t *testing.T) {
		t.Setenv(cargoToolchainEnv, "stable")
		withToolchainProbe(t, func(context.Context, string) bool { return true })
		cmd := cargoCommand(context.Background(), "/tmp/demo", "metadata", "--no-deps")
		require.Equal(t, []string{"cargo", "+stable", "metadata", "--no-deps"}, cmd.Args)
		require.Equal(t, "/tmp/demo", cmd.Dir)
	})

	t.Run("omits toolchain when cargo does not support it", func(t *testing.T) {
		t.Setenv(cargoToolchainEnv, "stable")
		withToolchainProbe(t, func(context.Context, string) bool { return false })
		cmd := cargoCommand(context.Background(), "/tmp/demo", "update")
		require.Equal(t, []string{"cargo", "update"}, cmd.Args)
	})

	t.Run("omits toolchain when disabled", func(t *testing.T) {
		t.Setenv(cargoToolchainEnv, "")
		withToolchainProbe(t, func(context.Context, string) bool { return true })
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

// Test_invertedTreeSpec covers scoping the `cargo tree -i` query when a crate is
// locked at multiple versions: single version needs no scoping, a multi-version
// request scopes to the in-line instance, and one that matches no line is ambiguous.
func Test_invertedTreeSpec(t *testing.T) {
	t.Run("single version uses the bare name", func(t *testing.T) {
		spec, err := invertedTreeSpec("rand", parseTarget("rand@0.9.0"), []string{"0.8.5"})
		require.NoError(t, err)
		require.Equal(t, "rand", spec)
	})

	t.Run("multiple versions scope to the requested line (@version)", func(t *testing.T) {
		spec, err := invertedTreeSpec("rand", parseTarget("rand@0.8.6"), []string{"0.7.3", "0.8.5"})
		require.NoError(t, err)
		require.Equal(t, "rand@0.8.5", spec)
	})

	t.Run("multiple versions scope to the from line (=precise)", func(t *testing.T) {
		spec, err := invertedTreeSpec("rand", parseTarget("rand@0.7.3=0.7.9"), []string{"0.7.3", "0.8.5"})
		require.NoError(t, err)
		require.Equal(t, "rand@0.7.3", spec)
	})

	t.Run("multiple versions matching no line is ambiguous", func(t *testing.T) {
		_, err := invertedTreeSpec("rand", parseTarget("rand@0.9.0"), []string{"0.7.3", "0.8.5"})
		require.ErrorIs(t, err, ErrAmbiguousTarget)
	})
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

// Test_floorSatisfiedInLine covers the skip decision when a crate is locked at
// several SemVer-incompatible lines: the target's own line must be judged on its
// own, so a higher unrelated line cannot mask a stale target line.
func Test_floorSatisfiedInLine(t *testing.T) {
	tests := []struct {
		name    string
		target  target
		present []string
		floor   string
		want    bool
	}{
		{
			name:    "unrelated higher line does not satisfy target line",
			target:  target{name: "rand", version: "0.9.4", hasVersion: true},
			present: []string{"0.9.0", "0.10.1"}, floor: "0.9.4", want: false,
		},
		{
			name:    "target line at floor",
			target:  target{name: "rand", version: "0.9.4", hasVersion: true},
			present: []string{"0.9.4", "0.10.1"}, floor: "0.9.4", want: true,
		},
		{
			name:    "target line above floor",
			target:  target{name: "rand", version: "0.9.4", hasVersion: true},
			present: []string{"0.9.6", "0.10.1"}, floor: "0.9.4", want: true,
		},
		{
			// The requested line is gone (the crate moved to a higher line, e.g.
			// anstream 0.6 -> 1.0 pulled up by a dependent). A higher line already
			// meets the floor, so skip rather than force a cross-line downgrade.
			name:    "target line absent, higher line satisfies",
			target:  target{name: "anstream", version: "0.6.8", hasVersion: true},
			present: []string{"1.0.1"}, floor: "0.6.8", want: true,
		},
		{
			// The requested line is gone and only a lower line remains: a genuine
			// cross-line upgrade is still needed, so do not skip.
			name:    "target line absent, only lower line",
			target:  target{name: "anstream", version: "0.6.8", hasVersion: true},
			present: []string{"0.5.0"}, floor: "0.6.8", want: false,
		},
		{
			name:    "bare name single present at floor",
			target:  target{name: "rand"},
			present: []string{"0.9.4"}, floor: "0.9.4", want: true,
		},
		{
			name:    "bare name single present below floor",
			target:  target{name: "rand"},
			present: []string{"0.9.0"}, floor: "0.9.4", want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, floorSatisfiedInLine(tt.target, tt.present, tt.floor))
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

// scaffoldRand09Crate scaffolds a crate depending on rand ^0.9 with rand,
// rand_chacha and rand_core all pinned down to 0.9.0, establishing an old baseline
// so a subsequent floor upgrade has room to advance the target. Needs
// network/registry access.
func scaffoldRand09Crate(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Cargo.toml"), `[package]
name = "demo"
version = "0.1.0"
edition = "2021"

[dependencies]
rand = "0.9"
`)
	writeFile(t, filepath.Join(root, "src", "lib.rs"), "")
	generateLockfile(t, root)

	// Pin the family down to 0.9.0 (released together) to establish an old baseline.
	for _, c := range []string{"rand", "rand_chacha", "rand_core"} {
		require.NoError(t, runCargoUpdate(context.Background(), root, []string{c}, "--precise", "0.9.0"))
	}
	return root
}

// Test_FloorAdvancesToLatestInLine is an end-to-end check of floor semantics for the
// @version form: requesting rand@0.9.3 against a 0.9.0 baseline advances rand to the
// latest version in its 0.9 line (past 0.9.0, >= 0.9.3) without editing the in-line
// manifest constraint. (Coordinated advancement of rand's family siblings is covered
// by Test_FamilyRefresh.) Needs network access.
func Test_FloorAdvancesToLatestInLine(t *testing.T) {
	cargoRoot := scaffoldRand09Crate(t)

	base, err := GetCurrentPackages(context.Background(), cargoRoot)
	require.NoError(t, err)
	require.Equal(t, "0.9.0", lockedVersion(base, "rand"))

	packages := map[string]*Package{"rand": {Name: "rand", Version: "0.9.3", Index: 0}}
	require.NoError(t, DoUpdate(context.Background(), packages, base, &UpdateConfig{CargoRoot: cargoRoot}))

	updated, err := GetCurrentPackages(context.Background(), cargoRoot)
	require.NoError(t, err)

	gotRand := lockedVersion(updated, "rand")
	require.True(t, cargoCompatible("0.9", gotRand), "rand must stay in the 0.9 line, got %s", gotRand)
	require.True(t, satisfiesFloor([]string{gotRand}, "0.9.3"), "rand must satisfy the floor, got %s", gotRand)
	require.Positive(t, semver.Compare("v"+gotRand, "v0.9.0"), "rand must advance past the baseline, got %s", gotRand)

	manifest, err := os.ReadFile(filepath.Join(cargoRoot, "Cargo.toml"))
	require.NoError(t, err)
	require.Contains(t, string(manifest), `rand = "0.9"`, "an in-line bump must not edit the manifest constraint")
}

// Test_FamilyRefresh is an end-to-end check that bumping a family member advances its
// curated siblings in place. Against a 0.9.0 baseline (rand, rand_core, rand_chacha
// all pinned to 0.9.0), requesting rand@0.9.3 must advance rand_core past 0.9.0 via
// the family refresh — it is not touched by landing rand itself. rand_chacha 0.9.0 is
// the latest in its line, so assert only that it stayed in-line and did not regress.
// Needs network access.
func Test_FamilyRefresh(t *testing.T) {
	cargoRoot := scaffoldRand09Crate(t)

	base, err := GetCurrentPackages(context.Background(), cargoRoot)
	require.NoError(t, err)
	baseCore := lockedVersion(base, "rand_core")
	baseChacha := lockedVersion(base, "rand_chacha")
	require.Equal(t, "0.9.0", baseCore)
	require.Equal(t, "0.9.0", baseChacha)

	packages := map[string]*Package{"rand": {Name: "rand", Version: "0.9.3", Index: 0}}
	require.NoError(t, DoUpdate(context.Background(), packages, base, &UpdateConfig{CargoRoot: cargoRoot}))

	updated, err := GetCurrentPackages(context.Background(), cargoRoot)
	require.NoError(t, err)

	require.Positive(t, semver.Compare("v"+lockedVersion(updated, "rand_core"), "v"+baseCore),
		"rand_core sibling must advance past %s", baseCore)
	gotChacha := lockedVersion(updated, "rand_chacha")
	require.True(t, cargoCompatible("0.9", gotChacha), "rand_chacha must stay in the 0.9 line, got %s", gotChacha)
	require.True(t, satisfiesFloor([]string{gotChacha}, baseChacha), "rand_chacha must not regress below %s", baseChacha)
}

// Test_InactiveDepPinsDirectly checks that bumping a crate that is locked but not
// activated in the inverted tree does not abort the run. quinn-proto is declared
// only under a windows-only target table, so on a non-Windows host it is locked
// (listed by cargo metadata) yet `cargo tree -i` returns "nothing to print". The
// bump must fall back to a direct lockfile pin and land the requested version.
// Needs network access and a non-Windows host.
func Test_InactiveDepPinsDirectly(t *testing.T) {
	cargoRoot := t.TempDir()
	writeFile(t, filepath.Join(cargoRoot, "Cargo.toml"), `[package]
name = "demo"
version = "0.1.0"
edition = "2021"

[target.'cfg(windows)'.dependencies]
quinn-proto = "0.11"
`)
	writeFile(t, filepath.Join(cargoRoot, "src", "lib.rs"), "")
	generateLockfile(t, cargoRoot)

	// The latest 0.11.x that resolved is our upgrade target; pin the lock down so a
	// floor bump has room to move.
	initial, err := GetCurrentPackages(context.Background(), cargoRoot)
	require.NoError(t, err)
	target := lockedVersion(initial, "quinn-proto")
	require.NotEmpty(t, target, "expected quinn-proto locked via the windows-only edge")
	require.NoError(t, runCargoUpdate(context.Background(), cargoRoot, []string{"quinn-proto"}, "--precise", "0.11.0"))

	base, err := GetCurrentPackages(context.Background(), cargoRoot)
	require.NoError(t, err)
	require.Equal(t, "0.11.0", lockedVersion(base, "quinn-proto"))

	packages := map[string]*Package{"quinn-proto": {Name: "quinn-proto", Version: target, Index: 0}}
	// Must not be fatal: the inactive-but-locked crate falls back to a direct pin.
	require.NoError(t, DoUpdate(context.Background(), packages, base, &UpdateConfig{CargoRoot: cargoRoot}))

	updated, err := GetCurrentPackages(context.Background(), cargoRoot)
	require.NoError(t, err)
	got := lockedVersion(updated, "quinn-proto")
	require.True(t, satisfiesFloor([]string{got}, target), "quinn-proto must be pinned to >= %s, got %s", target, got)
}

// Test_CrossSemverDirectPreRelease checks the pre-release manifest-edit path: a
// direct dependency bumped across a SemVer boundary into a pre-release-only line
// must have its Cargo.toml constraint rewritten to the full pre-release version, not
// a truncated caret line. rsa's 0.10 line is pre-release only (0.10.0-rc.*, no stable
// 0.10.0), so caretConstraint must keep "0.10.0-rc.18" — a truncated "0.10" would be
// read as ^0.10 and never match a pre-release, so `cargo add` would fail to resolve.
// The build check is stubbed (the fixture has no source); this verifies the manifest
// edit and lock resolution. Needs network/registry access.
func Test_CrossSemverDirectPreRelease(t *testing.T) {
	cargoRoot := t.TempDir()
	writeFile(t, filepath.Join(cargoRoot, "Cargo.toml"), `[package]
name = "demo"
version = "0.1.0"
edition = "2021"

[dependencies]
rsa = "0.9"
`)
	writeFile(t, filepath.Join(cargoRoot, "src", "lib.rs"), "")
	generateLockfile(t, cargoRoot)

	restore := checkBuild
	checkBuild = func(context.Context, string) (string, error) { return "", nil }
	defer func() { checkBuild = restore }()

	original, err := GetCurrentPackages(context.Background(), cargoRoot)
	require.NoError(t, err)
	require.True(t, cargoCompatible("0.9", lockedVersion(original, "rsa")), "expected rsa locked in the 0.9 line")

	packages := map[string]*Package{"rsa": {Name: "rsa", Version: "0.10.0-rc.18", Index: 0}}
	require.NoError(t, DoUpdate(context.Background(), packages, original, &UpdateConfig{CargoRoot: cargoRoot}))

	manifest, err := os.ReadFile(filepath.Join(cargoRoot, "Cargo.toml"))
	require.NoError(t, err)
	require.Contains(t, string(manifest), `rsa = "0.10.0-rc.18"`,
		"constraint must keep the pre-release tag, not truncate to 0.10")

	updated, err := GetCurrentPackages(context.Background(), cargoRoot)
	require.NoError(t, err)
	got := lockedVersion(updated, "rsa")
	require.True(t, satisfiesFloor([]string{got}, "0.10.0-rc.18"), "rsa must be >= 0.10.0-rc.18, got %s", got)
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

// Test_InRangeIndirectRefreshesLock covers the empty-plan case: a transitively
// pulled crate (rand_core, via rand ^0.8) whose dependents already permit the
// requested version, so revdep computes no manifest edit or boundary pin. The lock
// is nonetheless stale (pinned below the target), so the upgrade must still advance
// it via `cargo update --precise` rather than reporting "nothing to upgrade". Needs
// network access.
func Test_InRangeIndirectRefreshesLock(t *testing.T) {
	cargoRoot := scaffoldRandCrate(t)

	// Force the transitive rand_core lock entry behind the latest 0.6.x so an
	// in-range upgrade is available without any manifest constraint change.
	require.NoError(t, runCargoUpdate(context.Background(), cargoRoot, []string{"rand_core"}, "--precise", "0.6.3"))

	original, err := GetCurrentPackages(context.Background(), cargoRoot)
	require.NoError(t, err)
	require.Equal(t, "0.6.3", lockedVersion(original, "rand_core"), "expected rand_core pinned behind the target")

	// rand's ^0.6 requirement already permits 0.6.4, so the revdep plan is empty.
	packages := map[string]*Package{"rand_core": {Name: "rand_core", Version: "0.6.4", Index: 0}}
	err = DoUpdate(context.Background(), packages, original, &UpdateConfig{CargoRoot: cargoRoot})
	require.NoError(t, err)

	updated, err := GetCurrentPackages(context.Background(), cargoRoot)
	require.NoError(t, err)
	// Floor semantics: the lock must advance to at least the requested 0.6.4 within
	// the 0.6 line (the floor landing moves it to the latest compatible release).
	gotCore := lockedVersion(updated, "rand_core")
	require.True(t, cargoCompatible("0.6", gotCore), "rand_core must stay in the 0.6 line, got %s", gotCore)
	require.True(t, satisfiesFloor([]string{gotCore}, "0.6.4"), "in-range lock must advance to >= 0.6.4, got %s", gotCore)

	// The upgrade needed no manifest edit; the indirect target must not be added.
	manifest, err := os.ReadFile(filepath.Join(cargoRoot, "Cargo.toml"))
	require.NoError(t, err)
	require.NotContains(t, string(manifest), "rand_core", "in-range upgrade must not edit Cargo.toml")
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
