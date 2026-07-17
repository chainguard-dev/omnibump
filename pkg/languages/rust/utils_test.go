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

// Test_cargoCompatible covers Cargo's default caret-compatibility rules: a
// matching major for major>=1, a matching major.minor for 0.minor releases,
// an exact match for 0.0.patch releases, and exact-only for non-semver input.
func Test_cargoCompatible(t *testing.T) {
	tests := []struct {
		name string
		req  string
		have string
		want bool
	}{
		{name: "major>=1 same major", req: "1.2.3", have: "1.9.0", want: true},
		{name: "major>=1 different major", req: "1.0.0", have: "2.0.0", want: false},
		{name: "0.minor same minor", req: "0.9.0", have: "0.9.3", want: true},
		{name: "0.minor different minor", req: "0.9.0", have: "0.10.1", want: false},
		{name: "0.0.patch exact", req: "0.0.3", have: "0.0.3", want: true},
		{name: "0.0.patch different patch", req: "0.0.3", have: "0.0.4", want: false},
		{name: "non-semver exact match", req: "abc", have: "abc", want: true},
		{name: "non-semver mismatch", req: "abc", have: "def", want: false},
		{name: "one side non-semver", req: "1.0.0", have: "notsemver", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, cargoCompatible(tt.req, tt.have))
		})
	}
}

// Test_isDowngrade covers the SemVer ordering used to refuse precise pins that
// would move a crate backwards, plus the non-semver escape hatch (no ordering
// to compare, so never flagged as a downgrade).
func Test_isDowngrade(t *testing.T) {
	tests := []struct {
		name    string
		current string
		target  string
		want    bool
	}{
		{name: "lower patch is a downgrade", current: "1.2.3", target: "1.2.2", want: true},
		{name: "lower minor is a downgrade", current: "1.2.0", target: "1.1.9", want: true},
		{name: "higher version is not a downgrade", current: "1.2.3", target: "1.3.0", want: false},
		{name: "equal version is not a downgrade", current: "1.2.3", target: "1.2.3", want: false},
		{name: "non-semver current", current: "notsemver", target: "1.0.0", want: false},
		{name: "non-semver target", current: "1.0.0", target: "notsemver", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isDowngrade(tt.current, tt.target))
		})
	}
}

// Test_parseTarget covers the three CLI target forms: bare name, name@version,
// and the precise-pin name@version=precise.
func Test_parseTarget(t *testing.T) {
	tests := []struct {
		name string
		arg  string
		want target
	}{
		{name: "bare name", arg: "rand", want: target{name: "rand"}},
		{name: "name@version", arg: "rand@0.9.0", want: target{name: "rand", version: "0.9.0", hasVersion: true}},
		{
			name: "precise pin",
			arg:  "rand@0.9.0=0.9.3",
			want: target{name: "rand", version: "0.9.0", precise: "0.9.3", hasVersion: true, isPrecise: true},
		},
		{
			name: "precise pin with empty from",
			arg:  "rand@=0.9.3",
			want: target{name: "rand", precise: "0.9.3", hasVersion: true, isPrecise: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, parseTarget(tt.arg))
		})
	}
}

// Test_inLineVersion checks that the highest present version in the request's
// caret line is selected, and "" when nothing is compatible.
func Test_inLineVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
		present []string
		want    string
	}{
		{name: "none present", version: "0.9.0", present: nil, want: ""},
		{name: "single compatible", version: "0.9.0", present: []string{"0.9.3"}, want: "0.9.3"},
		{name: "picks highest in line", version: "0.9.0", present: []string{"0.9.1", "0.9.5", "0.9.3"}, want: "0.9.5"},
		{name: "excludes other lines", version: "0.9.0", present: []string{"0.10.1", "0.8.4"}, want: ""},
		{name: "mixed lines picks compatible", version: "0.9.0", present: []string{"0.10.1", "0.9.2"}, want: "0.9.2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, inLineVersion(tt.version, tt.present))
		})
	}
}

// Test_inLineSpec checks the shared cargo `--package` spec builder: name@version
// when a locked version shares the line, and the bare name (empty cur) otherwise.
func Test_inLineSpec(t *testing.T) {
	present := []string{"0.9.5", "0.10.1"}

	spec, cur := inLineSpec("rand", "0.9.0", present)
	require.Equal(t, "rand@0.9.5", spec)
	require.Equal(t, "0.9.5", cur)

	spec, cur = inLineSpec("rand", "0.8.0", present) // no locked 0.8.x
	require.Equal(t, "rand", spec)
	require.Empty(t, cur)
}

// Test_lockedVersionsOf checks extraction of a crate's locked versions from parsed
// Cargo.lock packages.
func Test_lockedVersionsOf(t *testing.T) {
	pkgs := []CargoPackage{
		{Name: "rand", Version: "0.9.5"},
		{Name: "serde", Version: "1.0.0"},
		{Name: "rand", Version: "0.8.5"},
	}
	require.Equal(t, []string{"0.9.5", "0.8.5"}, lockedVersionsOf(pkgs, "rand"))
	require.Nil(t, lockedVersionsOf(pkgs, "absent"))
}

// Test_maxVersion checks selection of the highest semver across compatibility
// lines, skipping unparseable entries, and the empty result when none parse.
func Test_maxVersion(t *testing.T) {
	tests := []struct {
		name     string
		versions []string
		want     string
	}{
		{name: "none present", versions: nil, want: ""},
		{name: "single version", versions: []string{"0.9.0"}, want: "0.9.0"},
		{name: "picks highest across lines", versions: []string{"0.9.3", "0.10.1", "0.8.4"}, want: "0.10.1"},
		{name: "spans major versions", versions: []string{"1.2.0", "2.0.0", "0.9.0"}, want: "2.0.0"},
		{name: "skips unparseable", versions: []string{"notsemver", "0.9.0", ""}, want: "0.9.0"},
		{name: "all unparseable", versions: []string{"notsemver", "abc"}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, maxVersion(tt.versions))
		})
	}
}

// Test_joinVersions checks the log-friendly rendering of a version list.
func Test_joinVersions(t *testing.T) {
	require.Equal(t, "none", joinVersions(nil))
	require.Equal(t, "none", joinVersions([]string{}))
	require.Equal(t, "0.9.0", joinVersions([]string{"0.9.0"}))
	require.Equal(t, "0.9.0, 0.10.1", joinVersions([]string{"0.9.0", "0.10.1"}))
}

// TestGetCurrentPackages tests the GetCurrentPackages function to make sure it returns the expected packages.
func TestGetCurrentPackages(t *testing.T) {
	tmpDir := t.TempDir()
	cargoRoot := tmpDir

	cargoLockFile, err := os.Open("testdata/Cargo.lock.orig")
	if err != nil {
		t.Fatalf("failed reading file: %v", err)
	}
	defer func() { _ = cargoLockFile.Close() }()

	// copy the source files to their destinations
	copyFile(t, cargoLockFile.Name(), filepath.Join(cargoRoot, "Cargo.lock"))

	packages, err := GetCurrentPackages(context.Background(), cargoRoot)
	require.NoError(t, err)
	require.Equal(t, 348, len(packages))
}

// TestGetCargoLockPath tests the GetCargoLockPath function.
func TestGetCargoLockPath(t *testing.T) {
	tests := []struct {
		name        string
		shouldExist bool
		err         error
	}{
		{
			name:        "No Cargo.lock",
			shouldExist: false,
			err:         ErrCargoLockNotFound,
		},
		{
			shouldExist: true,
			name:        "Yes Cargo.lock",
			err:         nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			cargoRoot := tmpDir

			canonicalCargoLockPath := filepath.Join(tmpDir, "Cargo.lock")
			if test.shouldExist {
				err := os.WriteFile(canonicalCargoLockPath, []byte("doesn't matter"), 0o600)
				require.NoError(t, err)
			}

			// We should get a valid path back or an error if the file doesn't exist
			cargoLockPath, err := GetCargoLockPath(cargoRoot)
			if err != nil {
				if test.err != nil {
					require.ErrorIs(t, err, ErrCargoLockNotFound)
				} else {
					t.Errorf("GetCargoLockPath(%s) = %v, want nil", cargoRoot, err)
				}
			} else {
				require.Equal(t, canonicalCargoLockPath, cargoLockPath)
			}
		})
	}
}
