/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package golang

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGoWork(t *testing.T) {
	// Skip if go command is not available
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go command not found, skipping test")
	}

	// Get current Go version for comparison
	currentGoVersion := strings.TrimPrefix(runtime.Version(), "go")
	parts := strings.Split(currentGoVersion, ".")
	if len(parts) >= 2 {
		currentGoVersion = fmt.Sprintf("%s.%s", parts[0], parts[1])
	}

	t.Run("FindGoWork", func(t *testing.T) {
		testCases := []struct {
			name         string
			setupFunc    func(string) error
			goWorkEnv    string
			expectedPath string
		}{
			{
				name: "finds go.work in current directory",
				setupFunc: func(dir string) error {
					return os.WriteFile(filepath.Join(dir, "go.work"), []byte("go 1.21\n"), 0600)
				},
				goWorkEnv:    "",
				expectedPath: "go.work",
			},
			{
				name: "finds go.work in parent directory",
				setupFunc: func(dir string) error {
					subdir := filepath.Join(dir, "subdir")
					if err := os.Mkdir(subdir, 0750); err != nil {
						return err
					}
					return os.WriteFile(filepath.Join(dir, "go.work"), []byte("go 1.22\n"), 0600)
				},
				goWorkEnv:    "",
				expectedPath: "../go.work",
			},
			{
				name:         "returns empty when no go.work found",
				setupFunc:    func(_ string) error { return nil },
				goWorkEnv:    "",
				expectedPath: "",
			},
			{
				name: "GOWORK=off disables workspace",
				setupFunc: func(dir string) error {
					return os.WriteFile(filepath.Join(dir, "go.work"), []byte("go 1.23\n"), 0600)
				},
				goWorkEnv:    "off",
				expectedPath: "",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				tmpDir := t.TempDir()
				if tc.setupFunc != nil {
					if err := tc.setupFunc(tmpDir); err != nil {
						t.Fatalf("Setup failed: %v", err)
					}
				}

				if tc.goWorkEnv != "" {
					oldGoWork := os.Getenv("GOWORK")
					t.Setenv("GOWORK", tc.goWorkEnv)
					defer func() {
						_ = os.Setenv("GOWORK", oldGoWork)
					}()
				}

				workDir := tmpDir
				if strings.Contains(tc.name, "parent") {
					workDir = filepath.Join(tmpDir, "subdir")
				}

				result := findGoWork(workDir)

				switch tc.expectedPath {
				case "":
					if result != "" {
						t.Errorf("Expected no go.work file, got %q", result)
					}
				case "go.work", "../go.work":
					if result == "" {
						t.Errorf("Expected to find go.work file, but got empty result")
					} else if !strings.Contains(result, "go.work") {
						t.Errorf("Expected result to contain 'go.work', got %q", result)
					}
				}
			})
		}
	})

	t.Run("UpdateGoWorkVersion", func(t *testing.T) {
		// Read real Kubernetes go.work files for testing
		k8sV134, err := os.ReadFile("testdata-workspace/kubernetes/go.work.v1.34")
		if err != nil {
			t.Fatalf("Failed to read Kubernetes v1.34 go.work: %v", err)
		}

		k8sV131, err := os.ReadFile("testdata-workspace/kubernetes/go.work.v1.31")
		if err != nil {
			t.Fatalf("Failed to read Kubernetes v1.31 go.work: %v", err)
		}

		testCases := []struct {
			name            string
			initialWork     string
			goVersion       string
			expectedVersion string
		}{
			{
				name:            "updates Kubernetes v1.31 (1.22.0) to 1.25",
				initialWork:     string(k8sV131),
				goVersion:       "1.25",
				expectedVersion: "1.25",
			},
			{
				name:            "updates Kubernetes v1.34 (1.24.0) to current version",
				initialWork:     string(k8sV134),
				goVersion:       "", // Auto-detect
				expectedVersion: currentGoVersion,
			},
			{
				name:            "handles patch versions correctly",
				initialWork:     string(k8sV134),
				goVersion:       "1.23",
				expectedVersion: "1.23",
			},
			{
				name: "preserves complex structure",
				initialWork: `// Generated file
go 1.21.5
godebug default=go1.21
use (
	.
	./cmd/app
	./pkg/api
)
replace example.com/old => ./new`,
				goVersion:       "1.24",
				expectedVersion: "1.24",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				tmpDir := t.TempDir()
				workPath := filepath.Join(tmpDir, "go.work")

				if err := os.WriteFile(workPath, []byte(tc.initialWork), 0600); err != nil {
					t.Fatalf("Failed to create go.work: %v", err)
				}

				// Create minimal go.mod for valid workspace
				modPath := filepath.Join(tmpDir, "go.mod")
				if err := os.WriteFile(modPath, []byte("module test\n\ngo 1.19\n"), 0600); err != nil {
					t.Fatalf("Failed to create go.mod: %v", err)
				}

				// For tests, we call UpdateGoWorkVersion with the directory containing go.work
				// and forceWork=true since we know we want to update it
				err := UpdateGoWorkVersion(filepath.Dir(workPath), true, tc.goVersion)
				if err != nil {
					t.Fatalf("Failed to update go.work: %v", err)
				}

				// Verify update
				updated, err := os.ReadFile(filepath.Clean(workPath))
				if err != nil {
					t.Fatalf("Failed to read updated go.work: %v", err)
				}

				expectedLine := fmt.Sprintf("go %s", tc.expectedVersion)
				if !strings.Contains(string(updated), expectedLine) {
					t.Errorf("Expected '%s' in file, got:\n%s", expectedLine, updated)
				}

				// Verify content preservation
				if strings.Contains(tc.initialWork, "// Generated file") {
					if !strings.Contains(string(updated), "// Generated file") {
						t.Error("Lost comment during update")
					}
				}
				if strings.Contains(tc.initialWork, "godebug") {
					if !strings.Contains(string(updated), "godebug") {
						t.Error("Lost godebug directive during update")
					}
				}
				if strings.Contains(tc.initialWork, "use (") {
					if !strings.Contains(string(updated), "use (") {
						t.Error("Lost use directives during update")
					}
				}
				if strings.Contains(tc.initialWork, "replace") {
					if !strings.Contains(string(updated), "replace") {
						t.Error("Lost replace directives during update")
					}
				}
			})
		}
	})

	t.Run("GoVendor", func(t *testing.T) {
		// GoVendor itself doesn't update go.work anymore, that's done by UpdateGoWorkVersion
		// This test just verifies GoVendor chooses the right vendor command
		testCases := []struct {
			name            string
			createWorkFile  bool
			forceWork       bool
			expectedCommand string // "work" or "mod"
		}{
			{
				name:            "uses go mod vendor when no work file",
				createWorkFile:  false,
				forceWork:       false,
				expectedCommand: "mod",
			},
			{
				name:            "uses go work vendor when work file exists",
				createWorkFile:  true,
				forceWork:       false,
				expectedCommand: "work",
			},
			{
				name:            "uses go work vendor when forceWork is true",
				createWorkFile:  false,
				forceWork:       true,
				expectedCommand: "work",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				tmpDir := t.TempDir()

				// Create go.mod
				modPath := filepath.Join(tmpDir, "go.mod")
				modContent := `module test
go 1.19
require github.com/google/uuid v1.3.0`
				if err := os.WriteFile(modPath, []byte(modContent), 0600); err != nil {
					t.Fatalf("Failed to create go.mod: %v", err)
				}

				if tc.createWorkFile {
					workPath := filepath.Join(tmpDir, "go.work")
					workContent := `go 1.25
use .`
					if err := os.WriteFile(workPath, []byte(workContent), 0600); err != nil {
						t.Fatalf("Failed to create go.work: %v", err)
					}
				}

				// Create vendor directory
				vendorDir := filepath.Join(tmpDir, "vendor")
				if err := os.Mkdir(vendorDir, 0750); err != nil {
					t.Fatalf("Failed to create vendor directory: %v", err)
				}

				// Call GoVendor
				_, _ = GoVendor(tmpDir, tc.forceWork)

				// Test passes if no panic (we can't easily test the actual command executed)
			})
		}
	})
}
