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

	"github.com/chainguard-dev/omnibump/pkg/analyzer"
	"github.com/chainguard-dev/omnibump/pkg/languages"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuby_Name(t *testing.T) {
	r := &Ruby{}
	assert.Equal(t, "ruby", r.Name())
}

func TestRuby_Detect(t *testing.T) {
	tests := []struct {
		name      string
		files     []string
		wantFound bool
	}{
		{
			name:      "gemfile lock found",
			files:     []string{"Gemfile.lock"},
			wantFound: true,
		},
		{
			name:      "gemfile only - not detected",
			files:     []string{"Gemfile"},
			wantFound: false,
		},
		{
			name:      "both found",
			files:     []string{"Gemfile", "Gemfile.lock"},
			wantFound: true,
		},
		{
			name:      "no ruby files",
			files:     []string{"go.mod"},
			wantFound: false,
		},
		{
			name:      "empty directory",
			files:     []string{},
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()

			for _, file := range tt.files {
				path := filepath.Join(tmpDir, file)
				err := os.WriteFile(path, []byte("# test content"), 0o600)
				require.NoError(t, err)
			}

			r := &Ruby{}
			found, err := r.Detect(context.Background(), tmpDir)
			require.NoError(t, err)
			assert.Equal(t, tt.wantFound, found)
		})
	}
}

func TestRuby_Update_MissingGemfileLock(t *testing.T) {
	tmpDir := t.TempDir()

	r := &Ruby{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "rack", Version: "3.1.9"},
		},
	}

	err := r.Update(context.Background(), cfg)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrGemfileLockNotFound)
}

func TestRuby_Update_InvalidGemfileLock(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a directory named Gemfile.lock to trigger an error during parsing
	gemfileLockPath := filepath.Join(tmpDir, "Gemfile.lock")
	err := os.Mkdir(gemfileLockPath, 0o700)
	require.NoError(t, err)

	r := &Ruby{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "rack", Version: "3.1.9"},
		},
	}

	err = r.Update(context.Background(), cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse Gemfile.lock")
}

func TestRuby_Validate_MissingGemfileLock(t *testing.T) {
	tmpDir := t.TempDir()

	r := &Ruby{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "rack", Version: "3.1.9"},
		},
	}

	err := r.Validate(context.Background(), cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to open updated Gemfile.lock")
}

func TestRuby_SupportsAnalysis(t *testing.T) {
	r := &Ruby{}
	assert.True(t, r.SupportsAnalysis())
}

func TestRuby_GetManifestFiles(t *testing.T) {
	r := &Ruby{}
	files := r.GetManifestFiles()
	assert.Equal(t, []string{"Gemfile", "Gemfile.lock"}, files)
}

func TestConvertDependenciesToPackages(t *testing.T) {
	deps := []languages.Dependency{
		{Name: "rack", Version: "3.1.9"},
		{Name: "sinatra", Version: "4.1.2"},
	}

	packages := convertDependenciesToPackages(deps)

	assert.Len(t, packages, 2)

	if pkg, ok := packages["rack"]; assert.True(t, ok) {
		assert.Equal(t, "3.1.9", pkg.Version)
	}

	if pkg, ok := packages["sinatra"]; assert.True(t, ok) {
		assert.Equal(t, "4.1.2", pkg.Version)
	}
}

func TestRuby_Update_DryRun(t *testing.T) {
	tmpDir := t.TempDir()

	content := `GEM
  remote: https://rubygems.org/
  specs:
    rack (3.1.8)

PLATFORMS
  ruby

BUNDLED WITH
   2.5.22
`
	gemfileLockPath := filepath.Join(tmpDir, "Gemfile.lock")
	err := os.WriteFile(gemfileLockPath, []byte(content), 0o600)
	require.NoError(t, err)

	r := &Ruby{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "rack", Version: "3.1.9"},
		},
		DryRun: true,
	}

	err = r.Update(context.Background(), cfg)
	require.NoError(t, err)

	// Verify file was NOT modified (dry run)
	result, err := os.ReadFile(gemfileLockPath)
	require.NoError(t, err)
	assert.Contains(t, string(result), "    rack (3.1.8)")
}

func TestRuby_Validate_Success(t *testing.T) {
	tmpDir := t.TempDir()

	content := `GEM
  remote: https://rubygems.org/
  specs:
    rack (3.1.9)

PLATFORMS
  ruby

BUNDLED WITH
   2.5.22
`
	gemfileLockPath := filepath.Join(tmpDir, "Gemfile.lock")
	err := os.WriteFile(gemfileLockPath, []byte(content), 0o600)
	require.NoError(t, err)

	r := &Ruby{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "rack", Version: "3.1.9"},
		},
	}

	err = r.Validate(context.Background(), cfg)
	require.NoError(t, err)
}

func TestRuby_Validate_VersionMismatch(t *testing.T) {
	tmpDir := t.TempDir()

	content := `GEM
  remote: https://rubygems.org/
  specs:
    rack (3.1.8)

PLATFORMS
  ruby

BUNDLED WITH
   2.5.22
`
	gemfileLockPath := filepath.Join(tmpDir, "Gemfile.lock")
	err := os.WriteFile(gemfileLockPath, []byte(content), 0o600)
	require.NoError(t, err)

	r := &Ruby{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "rack", Version: "3.1.9"},
		},
	}

	err = r.Validate(context.Background(), cfg)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrValidationFailed)
}

func TestRuby_Validate_PackageNotFound(t *testing.T) {
	tmpDir := t.TempDir()

	content := `GEM
  remote: https://rubygems.org/
  specs:
    sinatra (4.1.1)

PLATFORMS
  ruby

BUNDLED WITH
   2.5.22
`
	gemfileLockPath := filepath.Join(tmpDir, "Gemfile.lock")
	err := os.WriteFile(gemfileLockPath, []byte(content), 0o600)
	require.NoError(t, err)

	r := &Ruby{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "rack", Version: "3.1.9"},
		},
	}

	err = r.Validate(context.Background(), cfg)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPackageNotFound)
}

func TestAnalyzer_Analyze(t *testing.T) {
	tmpDir := t.TempDir()

	content := `GEM
  remote: https://rubygems.org/
  specs:
    rack (3.1.8)
    sinatra (4.1.1)
      rack (~> 3.0)

PLATFORMS
  ruby

BUNDLED WITH
   2.5.22
`
	gemfileLockPath := filepath.Join(tmpDir, "Gemfile.lock")
	err := os.WriteFile(gemfileLockPath, []byte(content), 0o600)
	require.NoError(t, err)

	a := &Analyzer{}
	result, err := a.Analyze(context.Background(), tmpDir)
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "ruby", result.Language)
	assert.Len(t, result.Dependencies, 2)
}

func TestAnalyzer_AnalyzeRemote(t *testing.T) {
	a := &Analyzer{}
	_, err := a.AnalyzeRemote(context.Background(), map[string][]byte{})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRemoteAnalysisNotImplemented)
}

func TestAnalyzer_RecommendStrategy(t *testing.T) {
	analysis := &analyzer.AnalysisResult{
		Language: "ruby",
		Dependencies: map[string]*analyzer.DependencyInfo{
			"rack": {
				Name:           "rack",
				Version:        "3.1.8",
				UpdateStrategy: "direct",
			},
		},
	}

	deps := []analyzer.Dependency{
		{Name: "rack", Version: "3.1.9"},
		{Name: "sinatra", Version: "4.1.2"},
	}

	a := &Analyzer{}
	strategy, err := a.RecommendStrategy(context.Background(), analysis, deps)
	require.NoError(t, err)

	// All deps are passed through as direct updates
	assert.Len(t, strategy.DirectUpdates, 2)
	assert.Empty(t, strategy.Warnings)
}
