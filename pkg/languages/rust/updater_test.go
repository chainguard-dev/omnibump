/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package rust

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func Test_findMatchingPackages(t *testing.T) {
	tests := []struct {
		name        string
		packageName string
		expected    []CargoPackage
	}{
		{
			name:        "upgrade specific version",
			packageName: "rand@0.8.4",
			expected:    []CargoPackage{{Name: "rand", Version: "0.8.4"}},
		},
		{
			name:        "upgrade all rand crates",
			packageName: "rand",
			expected: []CargoPackage{
				{Name: "rand", Version: "0.8.4"},
				{Name: "rand", Version: "0.10.1"},
			},
		},
		{
			name:        "no matching version",
			packageName: "rand@0.9.0",
			expected:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			packages := []CargoPackage{
				{Name: "anyhow", Version: "1.0.102"},
				{Name: "rand", Version: "0.8.4"},
				{Name: "rand", Version: "0.10.1"},
				{Name: "serde", Version: "1.0.228"},
			}

			expected := findMatchingPackages(tt.packageName, packages)
			if diff := cmp.Diff(tt.expected, expected); diff != "" {
				t.Errorf("unexpected result: %s", diff)
			}
		})
	}
}

func TestUpdate(t *testing.T) {
	tests := []struct {
		name   string
		update bool
		verify func(t *testing.T, original, updated []CargoPackage)
	}{
		{
			name:   "with cargo update refreshes the whole lock file",
			update: true,
			verify: func(t *testing.T, original, updated []CargoPackage) {
				if diff := cmp.Diff(original, updated); diff == "" {
					t.Error("no diff found between original and updated Cargo.lock files")
				}
			},
		},
		{
			name:   "without cargo update bumps only the target crate",
			update: false,
			verify: func(t *testing.T, _, updated []CargoPackage) {
				const wantName, wantVersion = "mio", "0.8.11"
				for _, pkg := range updated {
					if pkg.Name == wantName && pkg.Version == wantVersion {
						return
					}
				}
				t.Errorf("failed to find %q updated package with version %q", wantName, wantVersion)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original, updated := runUpdate(t, tt.update)
			tt.verify(t, original, updated)
		})
	}
}

// runUpdate performs the shared setup (parse the bump file, stage the
// Cargo.toml/Cargo.lock fixtures, run DoUpdate) and returns the packages
// parsed from the original lock file and from the lock file after the run.
// update controls cfg.Update, i.e. whether DoUpdate runs 'cargo update' to
// refresh the whole lock file before bumping individual crates.
func runUpdate(t *testing.T, update bool) (original, updated []CargoPackage) {
	t.Helper()

	tmpDir := t.TempDir()
	cargoRoot := tmpDir

	bumpFile, err := os.Open("testdata/cargobump-deps.yaml")
	if err != nil {
		t.Fatalf("failed reading file: %v", err)
	}
	defer func() { _ = bumpFile.Close() }()

	patches, err := ParseBumpFile(bumpFile)
	if err != nil {
		t.Fatalf("failed to parse the bump file: %v", err)
	}

	cargoLockFile, err := os.Open("testdata/Cargo.lock.orig")
	if err != nil {
		t.Fatalf("failed reading file: %v", err)
	}
	defer func() { _ = cargoLockFile.Close() }()

	// copy the source files to their destinations
	copyFile(t, "testdata/Cargo.toml.orig", filepath.Join(cargoRoot, "Cargo.toml"))

	copyFile(t, cargoLockFile.Name(), filepath.Join(cargoRoot, "Cargo.lock"))

	original, err = ParseCargoLock(cargoLockFile)
	if err != nil {
		t.Fatalf("failed to parse Cargo.lock file: %v", err)
	}

	cfg := &UpdateConfig{
		CargoRoot: cargoRoot,
		Update:    update,
	}

	if err = DoUpdate(context.Background(), patches, original, cfg); err != nil {
		t.Errorf("failed to update packages: %v", err)
	}

	updatedLockFile, err := os.Open(filepath.Join(cargoRoot, "Cargo.lock"))
	if err != nil {
		t.Fatalf("failed reading file: %v", err)
	}
	defer func() { _ = updatedLockFile.Close() }()

	updated, err = ParseCargoLock(updatedLockFile)
	if err != nil {
		t.Fatalf("failed to parse Cargo.lock file: %v", err)
	}

	return original, updated
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	_, err := exec.CommandContext(context.Background(), "cp", "-r", src, dst).Output()
	if err != nil {
		t.Fatal(err)
	}
}
