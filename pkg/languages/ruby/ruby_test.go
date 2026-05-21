/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package ruby

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chainguard-dev/omnibump/pkg/analyzer"
	"github.com/chainguard-dev/omnibump/pkg/languages"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuby_Name(t *testing.T) {
	r := &Ruby{}
	assert.Equal(t, "ruby", r.Name(), "Ruby language name should be 'ruby'")
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
			wantFound: false, // Detect only looks for Gemfile.lock
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
	assert.Contains(t, err.Error(), "gemfile.lock not found")
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
	assert.True(t, r.SupportsAnalysis(), "Ruby should support analysis")
}

func TestRuby_GetManifestFiles(t *testing.T) {
	r := &Ruby{}
	files := r.GetManifestFiles()
	expected := []string{"Gemfile", "Gemfile.lock"}

	assert.Equal(t, expected, files)
}

func TestConvertDependenciesToPackages(t *testing.T) {
	deps := []languages.Dependency{
		{Name: "rack", Version: "3.1.9"},
		{Name: "sinatra", Version: "4.1.2"},
	}

	packages := convertDependenciesToPackages(deps)

	assert.Len(t, packages, 2)

	if pkg, ok := packages["rack"]; assert.True(t, ok, "Expected rack package") {
		assert.Equal(t, "3.1.9", pkg.Version)
	}

	if pkg, ok := packages["sinatra"]; assert.True(t, ok, "Expected sinatra package") {
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

	// Validate logs warnings but doesn't return error for version mismatches
	err = r.Validate(context.Background(), cfg)
	require.NoError(t, err)
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

	// Validate logs warnings but doesn't return error for missing packages
	err = r.Validate(context.Background(), cfg)
	require.NoError(t, err)
}

func TestRubyAnalyzer_Analyze(t *testing.T) {
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

	ra := &RubyAnalyzer{}
	result, err := ra.Analyze(context.Background(), tmpDir)
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "ruby", result.Language)
	assert.NotNil(t, result.Dependencies)
	assert.Len(t, result.Dependencies, 2)
}

func TestRubyAnalyzer_AnalyzeRemote(t *testing.T) {
	ra := &RubyAnalyzer{}
	_, err := ra.AnalyzeRemote(context.Background(), map[string][]byte{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not yet implemented")
}

func TestRubyAnalyzer_RecommendStrategy(t *testing.T) {
	analysis := &analyzer.AnalysisResult{
		Language: "ruby",
		Dependencies: map[string]*analyzer.DependencyInfo{
			"rack": {
				Name:           "rack",
				Version:        "3.1.8",
				Transitive:     false,
				UpdateStrategy: "direct",
			},
			"sinatra": {
				Name:           "sinatra",
				Version:        "4.1.1",
				Transitive:     false,
				UpdateStrategy: "direct",
			},
		},
	}

	deps := []analyzer.Dependency{
		{Name: "rack", Version: "3.1.9"},
		{Name: "sinatra", Version: "4.1.2"},
		{Name: "missing-gem", Version: "1.0.0"},
	}

	ra := &RubyAnalyzer{}
	strategy, err := ra.RecommendStrategy(context.Background(), analysis, deps)
	require.NoError(t, err)
	assert.NotNil(t, strategy)

	// Should have 2 direct updates (rack and sinatra)
	assert.Equal(t, 2, len(strategy.DirectUpdates))

	// Should have 1 warning for missing dependency
	assert.Equal(t, 1, len(strategy.Warnings))
	assert.Contains(t, strategy.Warnings[0], "missing-gem")
}

func TestRubyAnalyzer_RecommendStrategy_MultipleVersions(t *testing.T) {
	analysis := &analyzer.AnalysisResult{
		Language: "ruby",
		Dependencies: map[string]*analyzer.DependencyInfo{
			"rack@3.0.0": {
				Name:           "rack",
				Version:        "3.0.0",
				Transitive:     false,
				UpdateStrategy: "direct",
			},
			"rack@3.1.8": {
				Name:           "rack",
				Version:        "3.1.8",
				Transitive:     false,
				UpdateStrategy: "direct",
			},
		},
	}

	deps := []analyzer.Dependency{
		{Name: "rack", Version: "3.1.9"},
	}

	ra := &RubyAnalyzer{}
	strategy, err := ra.RecommendStrategy(context.Background(), analysis, deps)
	require.NoError(t, err)

	foundMultipleWarning := false
	for _, warning := range strategy.Warnings {
		if strings.Contains(warning, "Multiple versions") && strings.Contains(warning, "rack") {
			foundMultipleWarning = true
			break
		}
	}
	assert.True(t, foundMultipleWarning, "Expected warning about multiple versions")
}
