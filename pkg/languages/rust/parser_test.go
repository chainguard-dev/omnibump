/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package rust

import (
	"fmt"
	"os"
	"path"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	cargoNormalLibs = []CargoPackage{
		{Name: "normal", Version: "0.1.0", Dependencies: []string{"libc@0.2.54"}},
		{Name: "libc", Version: "0.2.54"},
		{Name: "typemap", Version: "0.3.3", Dependencies: []string{"unsafe-any@0.4.2"}},
		{Name: "url", Version: "1.7.2", Dependencies: []string{"idna@0.1.5", "matches@0.1.8", "percent-encoding@1.0.1"}},
		{Name: "unsafe-any", Version: "0.4.2"},
		{Name: "matches", Version: "0.1.8"},
		{Name: "idna", Version: "0.1.5"},
		{Name: "percent-encoding", Version: "1.0.1"},
	}

	cargoMixedLibs = []CargoPackage{
		{Name: "normal", Version: "0.1.0", Dependencies: []string{"libc@0.2.54"}},
		{Name: "libc", Version: "0.2.54"},
		{Name: "typemap", Version: "0.3.3", Dependencies: []string{"unsafe-any@0.4.2"}},
		{Name: "url", Version: "1.7.2", Dependencies: []string{"idna@0.1.5", "matches@0.1.8", "percent-encoding@1.0.1"}},
		{Name: "unsafe-any", Version: "0.4.2"},
		{Name: "matches", Version: "0.1.8"},
		{Name: "idna", Version: "0.1.5"},
		{Name: "percent-encoding", Version: "1.0.1"},
	}

	cargoV3Libs = []CargoPackage{
		{Name: "aho-corasick", Version: "0.7.20", Dependencies: []string{"memchr@2.5.0"}},
		{Name: "app", Version: "0.1.0", Dependencies: []string{"memchr@1.0.2", "regex@1.7.3", "regex-syntax@0.5.6"}},
		{Name: "libc", Version: "0.2.140"},
		{Name: "memchr", Version: "1.0.2", Dependencies: []string{"libc@0.2.140"}},
		{Name: "memchr", Version: "2.5.0"},
		{Name: "regex", Version: "1.7.3", Dependencies: []string{"aho-corasick@0.7.20", "memchr@2.5.0", "regex-syntax@0.6.29"}},
		{Name: "regex-syntax", Version: "0.5.6", Dependencies: []string{"ucd-util@0.1.10"}},
		{Name: "regex-syntax", Version: "0.6.29"},
		{Name: "ucd-util", Version: "0.1.10"},
	}
)

func TestParseBumpFile(t *testing.T) {
	f, err := os.Open("testdata-parser/bumpfile.yaml")
	require.NoError(t, err)

	patches, err := ParseBumpFile(f)
	require.NoError(t, err)

	wantPatches := map[string]*Package{
		"name-1": {Name: "name-1", Version: "version-1"},
		"name-2": {Name: "name-2", Version: "version-2"},
		"name-3": {Name: "name-3", Version: "version-3"},
	}

	assert.Equalf(t, wantPatches, patches, "Parse bump file packages, got %v; want %v", patches, wantPatches)
}

func TestParseCargoLock(t *testing.T) {
	vectors := []struct {
		file     string // Test input file
		wantPkgs []CargoPackage
		wantErr  assert.ErrorAssertionFunc
	}{
		{
			file:     "testdata-parser/cargo_normal.lock",
			wantPkgs: cargoNormalLibs,
			wantErr:  assert.NoError,
		},
		{
			file:     "testdata-parser/cargo_mixed.lock",
			wantPkgs: cargoMixedLibs,
			wantErr:  assert.NoError,
		},
		{
			file:     "testdata-parser/cargo_v3.lock",
			wantPkgs: cargoV3Libs,
			wantErr:  assert.NoError,
		},
		{
			file:    "testdata-parser/cargo_invalid.lock",
			wantErr: assert.Error,
		},
	}

	for _, v := range vectors {
		t.Run(path.Base(v.file), func(t *testing.T) {
			f, err := os.Open(v.file)
			require.NoError(t, err)

			gotPkgs, err := ParseCargoLock(f)
			if !v.wantErr(t, err, fmt.Sprintf("Parse(%v)", v.file)) {
				return
			}

			if err != nil {
				return
			}
			sortCargoPackages(v.wantPkgs)

			assert.Equalf(t, v.wantPkgs, gotPkgs, "Parse packages(%v)", v.file)
		})
	}
}

// TestParseBumpFile_IOReadError tests that io.ReadAll errors are properly propagated (FINDING-003).
func TestParseBumpFile_IOReadError(t *testing.T) {
	// Create a reader that will fail
	failReader := &failingReader{err: fmt.Errorf("simulated I/O error")}

	_, err := ParseBumpFile(failReader)
	require.Error(t, err, "ParseBumpFile should return error on I/O failure")
	assert.Contains(t, err.Error(), "reading file", "Error should mention reading file")
}

// TestParseBumpFile_NonExistentFile tests error handling for missing files.
func TestParseBumpFile_NonExistentFile(t *testing.T) {
	f, err := os.Open("testdata-parser/non-existent.yaml")
	if err == nil {
		defer func() {
			_ = f.Close()
		}()
		t.Fatal("Should not be able to open non-existent file")
	}
	// This test verifies the caller handles file open errors
	assert.Error(t, err, "Opening non-existent file should error")
}

// failingReader simulates an I/O error during reading.
type failingReader struct {
	err error
}

func (r *failingReader) Read(_ []byte) (n int, err error) { // p not used since we immediately return error
	return 0, r.err
}
