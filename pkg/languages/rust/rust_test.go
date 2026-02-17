/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package rust

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

func TestRust_Name(t *testing.T) {
	r := &Rust{}
	assert.Equal(t, "rust", r.Name(), "Rust language name should be 'rust'")
}

func TestRust_Detect(t *testing.T) {
	tests := []struct {
		name      string
		files     []string
		wantFound bool
	}{
		{
			name:      "cargo lock found",
			files:     []string{"Cargo.lock"},
			wantFound: true,
		},
		{
			name:      "cargo toml only - not detected",
			files:     []string{"Cargo.toml"},
			wantFound: false, // Detect only looks for Cargo.lock
		},
		{
			name:      "both found",
			files:     []string{"Cargo.lock", "Cargo.toml"},
			wantFound: true,
		},
		{
			name:      "no rust files",
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

			// Create test files
			for _, file := range tt.files {
				path := filepath.Join(tmpDir, file)
				err := os.WriteFile(path, []byte("# test content"), 0600)
				require.NoError(t, err)
			}

			r := &Rust{}
			found, err := r.Detect(context.Background(), tmpDir)
			require.NoError(t, err)
			assert.Equal(t, tt.wantFound, found)
		})
	}
}

func TestRust_Update_MissingCargoLock(t *testing.T) {
	tmpDir := t.TempDir()

	r := &Rust{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "serde", Version: "1.0.0"},
		},
	}

	err := r.Update(context.Background(), cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cargo.lock not found")
}

func TestRust_Update_IOReadError(t *testing.T) {
	tmpDir := t.TempDir()

	// Create an empty Cargo.lock (will fail to parse)
	cargoLockPath := filepath.Join(tmpDir, "Cargo.lock")
	err := os.WriteFile(cargoLockPath, []byte("invalid content"), 0600)
	require.NoError(t, err)

	r := &Rust{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "serde", Version: "1.0.0"},
		},
	}

	err = r.Update(context.Background(), cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse Cargo.lock")
}

func TestRust_Validate_MissingCargoLock(t *testing.T) {
	tmpDir := t.TempDir()

	r := &Rust{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "serde", Version: "1.0.0"},
		},
	}

	err := r.Validate(context.Background(), cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to open updated Cargo.lock")
}

func TestRust_Validate_IOReadError(t *testing.T) {
	tmpDir := t.TempDir()

	// Create an invalid Cargo.lock
	cargoLockPath := filepath.Join(tmpDir, "Cargo.lock")
	err := os.WriteFile(cargoLockPath, []byte("invalid toml content"), 0600)
	require.NoError(t, err)

	r := &Rust{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "serde", Version: "1.0.0"},
		},
	}

	err = r.Validate(context.Background(), cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse updated Cargo.lock")
}

func TestRust_SupportsAnalysis(t *testing.T) {
	r := &Rust{}
	assert.True(t, r.SupportsAnalysis(), "Rust should support analysis")
}

func TestRust_GetManifestFiles(t *testing.T) {
	r := &Rust{}
	files := r.GetManifestFiles()
	expected := []string{"Cargo.toml", "Cargo.lock"}

	if len(files) != len(expected) {
		t.Errorf("Expected %d manifest files, got %d", len(expected), len(files))
	}

	for i, file := range expected {
		if files[i] != file {
			t.Errorf("Expected manifest file %s, got %s", file, files[i])
		}
	}
}

func TestConvertDependenciesToPackages(t *testing.T) {
	deps := []languages.Dependency{
		{Name: "serde", Version: "1.0.0"},
		{Name: "tokio", Version: "1.28.0"},
	}

	packages := convertDependenciesToPackages(deps)

	if len(packages) != 2 {
		t.Errorf("Expected 2 packages, got %d", len(packages))
	}

	if pkg, ok := packages["serde"]; !ok {
		t.Error("Expected serde package")
	} else if pkg.Version != "1.0.0" {
		t.Errorf("Expected version 1.0.0, got %s", pkg.Version)
	}

	if pkg, ok := packages["tokio"]; !ok {
		t.Error("Expected tokio package")
	} else if pkg.Version != "1.28.0" {
		t.Errorf("Expected version 1.28.0, got %s", pkg.Version)
	}
}

func TestGetOptionBool(t *testing.T) {
	tests := []struct {
		name     string
		options  map[string]any
		key      string
		defVal   bool
		expected bool
	}{
		{
			name:     "option exists and is true",
			options:  map[string]any{"update": true},
			key:      "update",
			defVal:   false,
			expected: true,
		},
		{
			name:     "option exists and is false",
			options:  map[string]any{"update": false},
			key:      "update",
			defVal:   true,
			expected: false,
		},
		{
			name:     "option does not exist",
			options:  map[string]any{},
			key:      "update",
			defVal:   true,
			expected: true,
		},
		{
			name:     "nil options map",
			options:  nil,
			key:      "update",
			defVal:   false,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getOptionBool(tt.options, tt.key, tt.defVal)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestRust_Update_DryRun(t *testing.T) {
	tmpDir := t.TempDir()

	// Create minimal Cargo.lock
	cargoLockContent := `
version = 3

[[package]]
name = "serde"
version = "1.0.0"
source = "registry+https://github.com/rust-lang/crates.io-index"
`
	cargoLockPath := filepath.Join(tmpDir, "Cargo.lock")
	err := os.WriteFile(cargoLockPath, []byte(cargoLockContent), 0600)
	require.NoError(t, err)

	r := &Rust{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "serde", Version: "1.0.1"},
		},
		DryRun: true,
	}

	err = r.Update(context.Background(), cfg)
	require.NoError(t, err)
}

func TestRust_Validate_Success(t *testing.T) {
	tmpDir := t.TempDir()

	// Create Cargo.lock with updated version
	cargoLockContent := `
version = 3

[[package]]
name = "serde"
version = "1.0.1"
source = "registry+https://github.com/rust-lang/crates.io-index"
`
	cargoLockPath := filepath.Join(tmpDir, "Cargo.lock")
	err := os.WriteFile(cargoLockPath, []byte(cargoLockContent), 0600)
	require.NoError(t, err)

	r := &Rust{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "serde", Version: "1.0.1"},
		},
	}

	err = r.Validate(context.Background(), cfg)
	require.NoError(t, err)
}

func TestRust_Validate_VersionMismatch(t *testing.T) {
	tmpDir := t.TempDir()

	// Create Cargo.lock with old version
	cargoLockContent := `
version = 3

[[package]]
name = "serde"
version = "1.0.0"
source = "registry+https://github.com/rust-lang/crates.io-index"
`
	cargoLockPath := filepath.Join(tmpDir, "Cargo.lock")
	err := os.WriteFile(cargoLockPath, []byte(cargoLockContent), 0600)
	require.NoError(t, err)

	r := &Rust{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "serde", Version: "1.0.1"},
		},
	}

	// Validate logs warnings but doesn't return error for version mismatches
	err = r.Validate(context.Background(), cfg)
	require.NoError(t, err)
}

func TestRust_Validate_PackageNotFound(t *testing.T) {
	tmpDir := t.TempDir()

	// Create Cargo.lock without the requested package
	cargoLockContent := `
version = 3

[[package]]
name = "tokio"
version = "1.0.0"
source = "registry+https://github.com/rust-lang/crates.io-index"
`
	cargoLockPath := filepath.Join(tmpDir, "Cargo.lock")
	err := os.WriteFile(cargoLockPath, []byte(cargoLockContent), 0600)
	require.NoError(t, err)

	r := &Rust{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "serde", Version: "1.0.1"},
		},
	}

	// Validate logs warnings but doesn't return error for missing packages
	err = r.Validate(context.Background(), cfg)
	require.NoError(t, err)
}

func TestRustAnalyzer_Analyze(t *testing.T) {
	tmpDir := t.TempDir()

	// Create minimal Cargo.lock
	cargoLockContent := `
version = 3

[[package]]
name = "serde"
version = "1.0.0"
source = "registry+https://github.com/rust-lang/crates.io-index"
`
	cargoLockPath := filepath.Join(tmpDir, "Cargo.lock")
	err := os.WriteFile(cargoLockPath, []byte(cargoLockContent), 0600)
	require.NoError(t, err)

	analyzer := &RustAnalyzer{}
	result, err := analyzer.Analyze(context.Background(), tmpDir)
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotNil(t, result.Dependencies)
}

func TestRustAnalyzer_AnalyzeRemote(t *testing.T) {
	analyzer := &RustAnalyzer{}
	_, err := analyzer.AnalyzeRemote(context.Background(), map[string][]byte{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not yet implemented")
}

func TestRustAnalyzer_RecommendStrategy(t *testing.T) {
	// Create sample analysis result
	analysis := &analyzer.AnalysisResult{
		Language: "rust",
		Dependencies: map[string]*analyzer.DependencyInfo{
			"serde": {
				Name:           "serde",
				Version:        "1.0.0",
				Transitive:     false,
				UpdateStrategy: "direct",
			},
			"tokio": {
				Name:           "tokio",
				Version:        "1.28.0",
				Transitive:     false,
				UpdateStrategy: "direct",
			},
		},
	}

	deps := []analyzer.Dependency{
		{Name: "serde", Version: "1.0.1"},
		{Name: "tokio", Version: "1.29.0"},
		{Name: "missing-dep", Version: "1.0.0"},
	}

	ra := &RustAnalyzer{}
	strategy, err := ra.RecommendStrategy(context.Background(), analysis, deps)
	require.NoError(t, err)
	assert.NotNil(t, strategy)

	// Should have 2 direct updates (serde and tokio)
	assert.Equal(t, 2, len(strategy.DirectUpdates))

	// Should have 1 warning for missing dependency
	assert.Equal(t, 1, len(strategy.Warnings))
	assert.Contains(t, strategy.Warnings[0], "missing-dep")
}

func TestRustAnalyzer_RecommendStrategy_MultipleVersions(t *testing.T) {
	// Create analysis result with multiple versions of same dependency
	analysis := &analyzer.AnalysisResult{
		Language: "rust",
		Dependencies: map[string]*analyzer.DependencyInfo{
			"serde-0": {
				Name:           "serde",
				Version:        "1.0.0",
				Transitive:     false,
				UpdateStrategy: "direct",
			},
			"serde-1": {
				Name:           "serde",
				Version:        "1.1.0",
				Transitive:     false,
				UpdateStrategy: "direct",
			},
		},
	}

	deps := []analyzer.Dependency{
		{Name: "serde", Version: "1.2.0"},
	}

	ra := &RustAnalyzer{}
	strategy, err := ra.RecommendStrategy(context.Background(), analysis, deps)
	require.NoError(t, err)

	// Should have warning about multiple versions
	foundMultipleWarning := false
	for _, warning := range strategy.Warnings {
		if strings.Contains(warning, "Multiple versions") && strings.Contains(warning, "serde") {
			foundMultipleWarning = true
			break
		}
	}
	assert.True(t, foundMultipleWarning, "Expected warning about multiple versions")
}
