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

// Test_resolveVersion covers mapping a user target onto a concrete present
// spec, including the "upgrade to at least X" skip logic and the ambiguous
// bare-name error.
func Test_resolveVersion(t *testing.T) {
	tests := []struct {
		name       string
		crate      string
		version    string
		hasVersion bool
		present    []string
		wantSpec   string
		wantSkip   bool
		wantMsg    string // substring; "" means no message expected
		wantErr    bool
	}{
		{
			name:  "not present at all",
			crate: "rand", version: "0.9.0", hasVersion: true,
			present:  nil,
			wantSkip: true, wantMsg: "not present in the dependency graph",
		},
		{
			name:  "bare name single version",
			crate: "rand", hasVersion: false,
			present:  []string{"0.9.0"},
			wantSpec: "rand@0.9.0",
		},
		{
			name:  "bare name ambiguous",
			crate: "rand", hasVersion: false,
			present: []string{"0.9.0", "0.10.1"},
			wantErr: true,
		},
		{
			name:  "exact version present",
			crate: "rand", version: "0.9.0", hasVersion: true,
			present:  []string{"0.9.0", "0.10.1"},
			wantSpec: "rand@0.9.0",
		},
		{
			name:  "no compatible version present",
			crate: "rand", version: "0.9.0", hasVersion: true,
			present:  []string{"0.10.1"},
			wantSkip: true, wantMsg: "no version of rand compatible with 0.9.0",
		},
		{
			name:  "already satisfies the floor",
			crate: "rand", version: "0.9.0", hasVersion: true,
			present:  []string{"0.9.3"},
			wantSkip: true, wantMsg: "already at 0.9.3, which satisfies >= 0.9.0",
		},
		{
			name:  "compatible below the floor",
			crate: "rand", version: "0.9.5", hasVersion: true,
			present:  []string{"0.9.3"},
			wantSpec: "rand@0.9.3", wantMsg: "using compatible rand@0.9.3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, skip, msg, err := resolveVersion(tt.crate, tt.version, tt.hasVersion, tt.present)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantSpec, spec)
			require.Equal(t, tt.wantSkip, skip)
			if tt.wantMsg == "" {
				require.Empty(t, msg)
			} else {
				require.Contains(t, msg, tt.wantMsg)
			}
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

// Test_parseCargoTree checks parsing of flattened `cargo tree --prefix none`
// output into name@version specs, skipping blank and malformed lines.
func Test_parseCargoTree(t *testing.T) {
	output := "rand v0.9.0\n" +
		"foo v1.2.3 (/path/to/foo)\n" +
		"\n" +
		"   bar v0.1.0 (*)\n" +
		"malformed\n" +
		"x v\n"
	want := []string{"rand@0.9.0", "foo@1.2.3", "bar@0.1.0"}
	require.Equal(t, want, parseCargoTree(output))
}

// Test_reverseDepsFromTree checks that the target crate is dropped (by base
// name, bare or pinned) and the remainder sorted and de-duplicated.
func Test_reverseDepsFromTree(t *testing.T) {
	tests := []struct {
		name    string
		deps    []string
		pkgName string
		want    []string
	}{
		{
			name:    "drops bare target and dedups",
			deps:    []string{"rand@0.9.0", "foo@1.0.0", "bar@2.0.0", "foo@1.0.0"},
			pkgName: "rand",
			want:    []string{"bar@2.0.0", "foo@1.0.0"},
		},
		{
			name:    "drops pinned target by base name",
			deps:    []string{"rand@0.9.0", "foo@1.0.0"},
			pkgName: "rand@0.9.0",
			want:    []string{"foo@1.0.0"},
		},
		{
			name:    "no reverse deps",
			deps:    []string{"rand@0.9.0"},
			pkgName: "rand",
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, reverseDepsFromTree(tt.deps, tt.pkgName))
		})
	}
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
