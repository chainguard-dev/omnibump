/*
Copyright 2025 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package rust

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
