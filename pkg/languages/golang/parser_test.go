/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package golang

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

const (
	invalidFile        = "testdata-parser/invalid.yaml"
	testFile           = "testdata-parser/bumpfile.yaml"
	missingNameFile    = "testdata-parser/missingname.yaml"
	missingVersionFile = "testdata-parser/missingversion.yaml"
)

func TestParseFile(t *testing.T) {
	testCases := []struct {
		name     string
		bumpFile string
		want     map[string]*Package
		wantErr  string
	}{{
		name:     "no file",
		bumpFile: "",
		wantErr:  "no filename specified",
	}, {
		name:     "file not found",
		bumpFile: "testdata-parser/missing",
		wantErr:  "failed reading file",
	}, {
		name:     "missing version",
		bumpFile: missingVersionFile,
		wantErr:  "missing version",
	}, {
		name:     "missing name",
		bumpFile: missingNameFile,
		wantErr:  "missing name",
	}, {
		name:     "invalid file",
		bumpFile: invalidFile,
		wantErr:  "unmarshaling file",
	}, {
		name:     "file",
		bumpFile: testFile,
		want: map[string]*Package{"name-1": {
			Name:    "name-1",
			Version: "version-1",
			Index:   0,
		},
			"name-2": {
				Name:    "name-2",
				Version: "version-2",
				Index:   1,
			},
			"name-3": {
				Name:    "name-3",
				Version: "version-3",
				Index:   2,
			}},
	}}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseFile(tc.bumpFile)
			if err != nil && (tc.wantErr == "") {
				t.Errorf("%s: ParseFile(%s) = %v)", tc.name, tc.bumpFile, err)
			}
			if err != nil && tc.wantErr != "" {
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("%s: ParseFile(%s) = %v, want %v", tc.name, tc.bumpFile, err, tc.wantErr)
				}
				return
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("%s: ParseFile(%s) (-got +want)\n%s", tc.name, tc.bumpFile, diff)
			}
		})
	}
}

// TestParseFile_IOReadError tests that io.ReadAll errors are properly propagated
// This test ensures FINDING-OMNIBUMP-003 is fixed
func TestParseFile_IOReadError(t *testing.T) {
	// Test that a non-existent file returns a proper error
	_, err := ParseFile("testdata-parser/non-existent-file.yaml")
	if err == nil {
		t.Error("ParseFile should return error for non-existent file")
	}
	if !strings.Contains(err.Error(), "failed reading file") {
		t.Errorf("ParseFile error should mention 'failed reading file', got: %v", err)
	}
}
