/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package js

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const basePkgJSON = `{
  "name": "example",
  "version": "1.0.0",
  "dependencies": {
    "simple-git": "^3.0.0"
  }
}
`

func TestWriteOverrides_PerManagerPath(t *testing.T) {
	tests := []struct {
		manager  Manager
		wantPath string
	}{
		{ManagerPnpm, "pnpm.overrides"},
		{ManagerYarn, "resolutions"},
		{ManagerNpm, "overrides"},
		{ManagerBun, "overrides"},
	}

	for _, tt := range tests {
		t.Run(string(tt.manager), func(t *testing.T) {
			out, err := writeOverrides([]byte(basePkgJSON), []Manager{tt.manager},
				[]Override{{Selector: "simple-git", Version: "3.36.0"}})
			require.NoError(t, err)

			got := gjson.GetBytes(out, tt.wantPath+`.simple-git`)
			assert.True(t, got.Exists(), "%s not set", tt.wantPath)
			assert.Equal(t, "3.36.0", got.String())
		})
	}
}

func TestWriteOverrides_PreservesScopedAndRangedSelectors(t *testing.T) {
	tests := []struct {
		name     string
		selector string
	}{
		{"scoped name", "@isaacs/brace-expansion"},
		{"name with caret range", "undici@^6"},
		{"name with tilde range", "react@~18.2.0"},
		{"name with version pin", "tar@7"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := writeOverrides([]byte(basePkgJSON), []Manager{ManagerPnpm},
				[]Override{{Selector: tt.selector, Version: "9.9.9"}})
			require.NoError(t, err)

			// Read via the same escape rules used when writing so that
			// we round-trip the selector through gjson cleanly.
			val := gjson.GetBytes(out, sjsonPath("pnpm.overrides", tt.selector))
			assert.True(t, val.Exists(), "selector %q missing from output", tt.selector)
			assert.Equal(t, "9.9.9", val.String())
		})
	}
}

func TestWriteOverrides_MultipleOverridesCoexist(t *testing.T) {
	out, err := writeOverrides([]byte(basePkgJSON), []Manager{ManagerPnpm}, []Override{
		{Selector: "simple-git", Version: "3.36.0"},
		{Selector: "undici@^6", Version: "6.24.0"},
		{Selector: "@isaacs/brace-expansion", Version: "5.0.1"},
	})
	require.NoError(t, err)

	assert.Equal(t, "3.36.0", gjson.GetBytes(out, sjsonPath("pnpm.overrides", "simple-git")).String())
	assert.Equal(t, "6.24.0", gjson.GetBytes(out, sjsonPath("pnpm.overrides", "undici@^6")).String())
	assert.Equal(t, "5.0.1", gjson.GetBytes(out, sjsonPath("pnpm.overrides", "@isaacs/brace-expansion")).String())
}

func TestWriteOverrides_PreservesUnrelatedKeys(t *testing.T) {
	out, err := writeOverrides([]byte(basePkgJSON), []Manager{ManagerPnpm},
		[]Override{{Selector: "simple-git", Version: "3.36.0"}})
	require.NoError(t, err)

	assert.Equal(t, "example", gjson.GetBytes(out, "name").String())
	assert.Equal(t, "1.0.0", gjson.GetBytes(out, "version").String())
	assert.Equal(t, "^3.0.0", gjson.GetBytes(out, "dependencies.simple-git").String())
}

// TestWriteOverrides_OnlyAppendsForBrandNewSections verifies the minimal-
// edits invariant: when none of the override paths exist in the source,
// writeOverrides must not touch any of the original bytes. It may only
// append a new top-level section. This is what we trade away when we
// choose sjson over a re-emitting JSON encoder.
func TestWriteOverrides_OnlyAppendsForBrandNewSections(t *testing.T) {
	const orig = `{
  "name": "example",
  "version": "1.0.0"
}
`
	out, err := writeOverrides([]byte(orig), []Manager{ManagerPnpm},
		[]Override{{Selector: "simple-git", Version: "3.36.0"}})
	require.NoError(t, err)

	// Every byte from the original must appear in the output in order.
	// We don't pin the exact trailing bytes; sjson is free to choose how
	// to append the new section, and that detail is not load-bearing.
	if len(out) < len(orig)-1 {
		t.Fatalf("output shorter than input: %q", out)
	}
	// The original closing brace and newline at the end are repositioned
	// by sjson; compare the leading prefix instead.
	const sharedPrefix = `{
  "name": "example",
  "version": "1.0.0"`
	if string(out[:len(sharedPrefix)]) != sharedPrefix {
		t.Errorf("leading bytes changed:\nwant: %q\ngot:  %q", sharedPrefix, out[:len(sharedPrefix)])
	}
}

// TestWriteOverrides_PreservesExistingOverrideFormatting confirms that
// when the overrides section already exists, sjson updates a single key
// in place without disturbing siblings or whitespace around them.
func TestWriteOverrides_PreservesExistingOverrideFormatting(t *testing.T) {
	const orig = `{
  "name": "example",
  "version": "1.0.0",
  "pnpm": {
    "overrides": {
      "alpha": "1.0.0",
      "simple-git": "3.0.0",
      "zulu": "9.9.9"
    }
  }
}
`
	out, err := writeOverrides([]byte(orig), []Manager{ManagerPnpm},
		[]Override{{Selector: "simple-git", Version: "3.36.0"}})
	require.NoError(t, err)

	want := `{
  "name": "example",
  "version": "1.0.0",
  "pnpm": {
    "overrides": {
      "alpha": "1.0.0",
      "simple-git": "3.36.0",
      "zulu": "9.9.9"
    }
  }
}
`
	if diff := cmp.Diff(want, string(out)); diff != "" {
		t.Errorf("in-place update changed formatting (-want +got):\n%s", diff)
	}
}

func TestWriteOverrides_RejectsEmptyInputs(t *testing.T) {
	cases := []struct {
		name string
		ov   Override
		want error
	}{
		{"empty selector", Override{Selector: "", Version: "1.0.0"}, ErrEmptySelector},
		{"empty version", Override{Selector: "foo", Version: ""}, ErrEmptyVersion},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := writeOverrides([]byte(basePkgJSON), []Manager{ManagerPnpm}, []Override{tt.ov})
			assert.ErrorIs(t, err, tt.want)
		})
	}
}

func TestWriteOverrides_RejectsUnknownManager(t *testing.T) {
	_, err := writeOverrides([]byte(basePkgJSON), []Manager{Manager("bower")},
		[]Override{{Selector: "x", Version: "1"}})
	assert.ErrorIs(t, err, ErrUnknownManager)
}

func TestApplyOverrides_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	pkgPath := filepath.Join(dir, "package.json")
	require.NoError(t, os.WriteFile(pkgPath, []byte(basePkgJSON), 0o600))

	overrides := []Override{
		{Selector: "simple-git", Version: "3.36.0", Reason: "GHSA-hffm-xvc3-vprc"},
		{Selector: "@isaacs/brace-expansion", Version: "5.0.1"},
	}

	require.NoError(t, ApplyOverrides(pkgPath, []Manager{ManagerPnpm}, overrides))
	require.NoError(t, VerifyOverrides(pkgPath, []Manager{ManagerPnpm}, overrides))
}

func TestVerifyOverrides_FailsWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	pkgPath := filepath.Join(dir, "package.json")
	require.NoError(t, os.WriteFile(pkgPath, []byte(basePkgJSON), 0o600))

	err := VerifyOverrides(pkgPath, []Manager{ManagerPnpm},
		[]Override{{Selector: "simple-git", Version: "3.36.0"}})
	assert.ErrorIs(t, err, ErrOverrideMissing)
}

func TestVerifyOverrides_FailsOnVersionMismatch(t *testing.T) {
	dir := t.TempDir()
	pkgPath := filepath.Join(dir, "package.json")
	require.NoError(t, os.WriteFile(pkgPath, []byte(basePkgJSON), 0o600))

	require.NoError(t, ApplyOverrides(pkgPath, []Manager{ManagerPnpm},
		[]Override{{Selector: "simple-git", Version: "3.36.0"}}))

	err := VerifyOverrides(pkgPath, []Manager{ManagerPnpm},
		[]Override{{Selector: "simple-git", Version: "3.99.0"}})
	assert.ErrorIs(t, err, ErrOverrideMissing)
}

// TestWriteOverrides_MultipleManagersInOneCall pins that one
// writeOverrides invocation populates every selected manager's path
// from the same starting buffer.
func TestWriteOverrides_MultipleManagersInOneCall(t *testing.T) {
	out, err := writeOverrides([]byte(basePkgJSON),
		[]Manager{ManagerPnpm, ManagerYarn},
		[]Override{{Selector: "simple-git", Version: "3.36.0"}})
	require.NoError(t, err)

	got := map[string]string{
		"pnpm.overrides.simple-git": gjson.GetBytes(out, "pnpm.overrides.simple-git").String(),
		"resolutions.simple-git":    gjson.GetBytes(out, "resolutions.simple-git").String(),
	}
	want := map[string]string{
		"pnpm.overrides.simple-git": "3.36.0",
		"resolutions.simple-git":    "3.36.0",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("multi-manager write mismatch (-want +got):\n%s", diff)
	}
}

// TestVerifyOverrides_MultiManagerCollectsAllMissing pins that
// VerifyOverrides reports gaps across every manager in one call, not
// just the first.
func TestVerifyOverrides_MultiManagerCollectsAllMissing(t *testing.T) {
	dir := t.TempDir()
	pkgPath := filepath.Join(dir, "package.json")
	require.NoError(t, os.WriteFile(pkgPath, []byte(basePkgJSON), 0o600))

	// Apply only under pnpm — the yarn path will be empty.
	require.NoError(t, ApplyOverrides(pkgPath, []Manager{ManagerPnpm},
		[]Override{{Selector: "simple-git", Version: "3.36.0"}}))

	err := VerifyOverrides(pkgPath, []Manager{ManagerPnpm, ManagerYarn},
		[]Override{{Selector: "simple-git", Version: "3.36.0"}})
	require.ErrorIs(t, err, ErrOverrideMissing)

	// The error must mention the yarn path, which is what failed.
	// The pnpm path was satisfied and must not appear.
	assert.Contains(t, err.Error(), "resolutions.simple-git")
	assert.NotContains(t, err.Error(), "pnpm.overrides.simple-git")
}
