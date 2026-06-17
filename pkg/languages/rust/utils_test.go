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
