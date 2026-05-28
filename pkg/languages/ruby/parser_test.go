/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package ruby

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	simpleGems = []GemPackage{
		{Name: "json", Version: "2.9.1", Source: "rubygems"},
		{Name: "rack", Version: "3.1.8", Source: "rubygems"},
		{Name: "sinatra", Version: "4.1.1", Source: "rubygems", Dependencies: []string{
			"rack (~> 3.0)",
			"tilt (~> 2.0)",
		}},
		{Name: "tilt", Version: "2.4.0", Source: "rubygems"},
		{Name: "webrick", Version: "1.9.1", Source: "rubygems"},
	}

	complexGems = []GemPackage{
		{Name: "base64", Version: "0.2.0", Source: "rubygems"},
		{Name: "bigdecimal", Version: "3.1.9", Source: "rubygems"},
		{Name: "custom-gem", Version: "0.1.0", Source: "git"},
		{Name: "logger", Version: "1.6.6", Source: "rubygems"},
		{Name: "mustermann", Version: "3.0.3", Source: "rubygems", Dependencies: []string{
			"ruby2_keywords (~> 0.0.1)",
		}},
		{Name: "my-app", Version: "1.0.0", Source: "path", Dependencies: []string{
			"rack (~> 3.0)",
			"sinatra (~> 4.0)",
		}},
		{Name: "nio4r", Version: "2.7.4", Source: "rubygems"},
		{Name: "puma", Version: "6.5.0", Source: "rubygems", Dependencies: []string{
			"nio4r (~> 2.0)",
		}},
		{Name: "rack", Version: "3.1.8", Source: "rubygems"},
		{Name: "rack-protection", Version: "4.1.1", Source: "rubygems", Dependencies: []string{
			"base64 (>= 0.1.0)",
			"logger (~> 1.6)",
			"rack (>= 3.0.0, < 4)",
		}},
		{Name: "rack-session", Version: "2.1.0", Source: "rubygems", Dependencies: []string{
			"base64 (>= 0.1.0)",
			"rack (>= 3.0.0)",
		}},
		{Name: "rack-test", Version: "2.2.0", Source: "rubygems", Dependencies: []string{
			"rack (>= 1.3)",
		}},
		{Name: "ruby2_keywords", Version: "0.0.5", Source: "rubygems"},
		{Name: "sinatra", Version: "4.1.1", Source: "rubygems", Dependencies: []string{
			"mustermann (~> 3.0)",
			"rack (>= 3.0.0, < 4)",
			"rack-protection (= 4.1.1)",
			"rack-session (>= 2.0.0, < 3)",
			"tilt (~> 2.0)",
		}},
		{Name: "tilt", Version: "2.6.0", Source: "rubygems"},
	}
)

func TestParseGemfileLock(t *testing.T) {
	tests := []struct {
		file     string
		wantPkgs []GemPackage
		wantErr  assert.ErrorAssertionFunc
	}{
		{
			file:     "testdata/Gemfile.lock.simple",
			wantPkgs: simpleGems,
			wantErr:  assert.NoError,
		},
		{
			file:     "testdata/Gemfile.lock.complex",
			wantPkgs: complexGems,
			wantErr:  assert.NoError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			f, err := os.Open(tt.file)
			require.NoError(t, err)
			defer func() { _ = f.Close() }()

			gotPkgs, err := ParseGemfileLock(f)
			if !tt.wantErr(t, err, fmt.Sprintf("ParseGemfileLock(%v)", tt.file)) {
				return
			}

			if err != nil {
				return
			}
			sortGemPackages(tt.wantPkgs)

			assert.Equalf(t, tt.wantPkgs, gotPkgs, "ParseGemfileLock(%v)", tt.file)
		})
	}
}

func TestParseGemfileLock_Empty(t *testing.T) {
	r := strings.NewReader("")
	pkgs, err := ParseGemfileLock(r)
	require.NoError(t, err)
	assert.Empty(t, pkgs)
}

func TestParseGemfileLock_IOReadError(t *testing.T) {
	failReader := &failingReader{err: fmt.Errorf("simulated I/O error")} //nolint:err113 // Test error
	_, err := ParseGemfileLock(failReader)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading Gemfile.lock")
}

// failingReader simulates an I/O error during reading.
type failingReader struct {
	err error
}

func (r *failingReader) Read(_ []byte) (n int, err error) {
	return 0, r.err
}
