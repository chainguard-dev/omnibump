/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package ruby

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoUpdate_SingleGem(t *testing.T) {
	tmpDir := t.TempDir()
	lockfilePath := filepath.Join(tmpDir, "Gemfile.lock")

	content := `GEM
  remote: https://rubygems.org/
  specs:
    rack (3.1.8)
    sinatra (4.1.1)
      rack (~> 3.0)
      tilt (~> 2.0)
    tilt (2.4.0)

PLATFORMS
  ruby

BUNDLED WITH
   2.5.22
`
	err := os.WriteFile(lockfilePath, []byte(content), 0o600)
	require.NoError(t, err)

	packages := map[string]*Package{
		"rack": {Name: "rack", Version: "3.1.9", Index: 0},
	}

	err = DoUpdate(context.Background(), packages, lockfilePath)
	require.NoError(t, err)

	// Verify the update
	updated, err := os.ReadFile(lockfilePath)
	require.NoError(t, err)

	assert.Contains(t, string(updated), "    rack (3.1.9)")
	assert.NotContains(t, string(updated), "    rack (3.1.8)")
	// Transitive dep constraints should be untouched
	assert.Contains(t, string(updated), "      rack (~> 3.0)")
}

func TestDoUpdate_MultipleGems(t *testing.T) {
	tmpDir := t.TempDir()
	lockfilePath := filepath.Join(tmpDir, "Gemfile.lock")

	content := `GEM
  remote: https://rubygems.org/
  specs:
    rack (3.1.8)
    sinatra (4.1.1)
      rack (~> 3.0)
      tilt (~> 2.0)
    tilt (2.4.0)

PLATFORMS
  ruby

BUNDLED WITH
   2.5.22
`
	err := os.WriteFile(lockfilePath, []byte(content), 0o600)
	require.NoError(t, err)

	packages := map[string]*Package{
		"rack": {Name: "rack", Version: "3.1.9", Index: 0},
		"tilt": {Name: "tilt", Version: "2.5.0", Index: 1},
	}

	err = DoUpdate(context.Background(), packages, lockfilePath)
	require.NoError(t, err)

	updated, err := os.ReadFile(lockfilePath)
	require.NoError(t, err)

	assert.Contains(t, string(updated), "    rack (3.1.9)")
	assert.Contains(t, string(updated), "    tilt (2.5.0)")
}

func TestDoUpdate_GemNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	lockfilePath := filepath.Join(tmpDir, "Gemfile.lock")

	content := `GEM
  remote: https://rubygems.org/
  specs:
    rack (3.1.8)

PLATFORMS
  ruby

BUNDLED WITH
   2.5.22
`
	err := os.WriteFile(lockfilePath, []byte(content), 0o600)
	require.NoError(t, err)

	packages := map[string]*Package{
		"nonexistent": {Name: "nonexistent", Version: "1.0.0", Index: 0},
	}

	// Should not error, just warn
	err = DoUpdate(context.Background(), packages, lockfilePath)
	require.NoError(t, err)
}

func TestDoUpdate_AlreadyAtVersion(t *testing.T) {
	tmpDir := t.TempDir()
	lockfilePath := filepath.Join(tmpDir, "Gemfile.lock")

	content := `GEM
  remote: https://rubygems.org/
  specs:
    rack (3.1.8)

PLATFORMS
  ruby

BUNDLED WITH
   2.5.22
`
	err := os.WriteFile(lockfilePath, []byte(content), 0o600)
	require.NoError(t, err)

	packages := map[string]*Package{
		"rack": {Name: "rack", Version: "3.1.8", Index: 0},
	}

	err = DoUpdate(context.Background(), packages, lockfilePath)
	require.NoError(t, err)

	// Content should be unchanged
	updated, err := os.ReadFile(lockfilePath)
	require.NoError(t, err)
	assert.Contains(t, string(updated), "    rack (3.1.8)")
}

func TestDoUpdate_SkipsDowngrade(t *testing.T) {
	tmpDir := t.TempDir()
	lockfilePath := filepath.Join(tmpDir, "Gemfile.lock")

	content := `GEM
  remote: https://rubygems.org/
  specs:
    rack (3.2.0)

PLATFORMS
  ruby

BUNDLED WITH
   2.5.22
`
	err := os.WriteFile(lockfilePath, []byte(content), 0o600)
	require.NoError(t, err)

	packages := map[string]*Package{
		"rack": {Name: "rack", Version: "3.1.8", Index: 0},
	}

	err = DoUpdate(context.Background(), packages, lockfilePath)
	require.NoError(t, err)

	// Version should remain 3.2.0 (no downgrade)
	updated, err := os.ReadFile(lockfilePath)
	require.NoError(t, err)
	assert.Contains(t, string(updated), "    rack (3.2.0)")
}

func TestDoUpdate_PreservesTransitiveDeps(t *testing.T) {
	tmpDir := t.TempDir()
	lockfilePath := filepath.Join(tmpDir, "Gemfile.lock")

	content := `GEM
  remote: https://rubygems.org/
  specs:
    rack (3.1.8)
    rack-protection (4.1.1)
      base64 (>= 0.1.0)
      rack (>= 3.0.0, < 4)
    sinatra (4.1.1)
      rack (>= 3.0.0, < 4)

PLATFORMS
  ruby

BUNDLED WITH
   2.5.22
`
	err := os.WriteFile(lockfilePath, []byte(content), 0o600)
	require.NoError(t, err)

	packages := map[string]*Package{
		"rack": {Name: "rack", Version: "3.1.9", Index: 0},
	}

	err = DoUpdate(context.Background(), packages, lockfilePath)
	require.NoError(t, err)

	updated, err := os.ReadFile(lockfilePath)
	require.NoError(t, err)

	// Direct version should be updated
	assert.Contains(t, string(updated), "    rack (3.1.9)")
	// Transitive constraints should remain untouched
	assert.Contains(t, string(updated), "      rack (>= 3.0.0, < 4)")
}

func TestDoUpdate_MissingFile(t *testing.T) {
	err := DoUpdate(context.Background(), map[string]*Package{
		"rack": {Name: "rack", Version: "1.0.0", Index: 0},
	}, "/nonexistent/Gemfile.lock")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read Gemfile.lock")
}

func TestOrderPackages(t *testing.T) {
	packages := map[string]*Package{
		"c": {Name: "c", Version: "1.0.0", Index: 2},
		"a": {Name: "a", Version: "1.0.0", Index: 0},
		"b": {Name: "b", Version: "1.0.0", Index: 1},
	}

	ordered := orderPackages(packages)
	assert.Equal(t, []string{"a", "b", "c"}, ordered)
}
