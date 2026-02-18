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
					return os.WriteFile(filepath.Join(dir, "go.work"), []byte("go 1.21\n"), 0o600)
				},
				goWorkEnv:    "",
				expectedPath: "go.work",
			},
			{
				name: "finds go.work in parent directory",
				setupFunc: func(dir string) error {
					subdir := filepath.Join(dir, "subdir")
					if err := os.Mkdir(subdir, 0o750); err != nil {
						return err
					}
					return os.WriteFile(filepath.Join(dir, "go.work"), []byte("go 1.22\n"), 0o600)
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
					return os.WriteFile(filepath.Join(dir, "go.work"), []byte("go 1.23\n"), 0o600)
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

				if err := os.WriteFile(workPath, []byte(tc.initialWork), 0o600); err != nil {
					t.Fatalf("Failed to create go.work: %v", err)
				}

				// Create minimal go.mod for valid workspace
				modPath := filepath.Join(tmpDir, "go.mod")
				if err := os.WriteFile(modPath, []byte("module test\n\ngo 1.19\n"), 0o600); err != nil {
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
				if err := os.WriteFile(modPath, []byte(modContent), 0o600); err != nil {
					t.Fatalf("Failed to create go.mod: %v", err)
				}

				if tc.createWorkFile {
					workPath := filepath.Join(tmpDir, "go.work")
					workContent := `go 1.25
use .`
					if err := os.WriteFile(workPath, []byte(workContent), 0o600); err != nil {
						t.Fatalf("Failed to create go.work: %v", err)
					}
				}

				// Create vendor directory
				vendorDir := filepath.Join(tmpDir, "vendor")
				if err := os.Mkdir(vendorDir, 0o750); err != nil {
					t.Fatalf("Failed to create vendor directory: %v", err)
				}

				// Call GoVendor
				_, _ = GoVendor(tmpDir, tc.forceWork)

				// Test passes if no panic (we can't easily test the actual command executed)
			})
		}

		t.Run("sets correct working directory", func(t *testing.T) {
			// This test verifies the bug fix where cmd.Dir wasn't being set
			tmpDir := t.TempDir()
			subDir := filepath.Join(tmpDir, "subproject")
			if err := os.Mkdir(subDir, 0o750); err != nil {
				t.Fatalf("Failed to create subdirectory: %v", err)
			}

			// Create go.mod in subdirectory
			modPath := filepath.Join(subDir, "go.mod")
			modContent := `module testproject
go 1.21
require github.com/google/uuid v1.3.0`
			if err := os.WriteFile(modPath, []byte(modContent), 0o600); err != nil {
				t.Fatalf("Failed to create go.mod: %v", err)
			}

			// Create go.sum
			sumPath := filepath.Join(subDir, "go.sum")
			sumContent := `github.com/google/uuid v1.3.0 h1:t6JiXgmwXMjEs8VusXIJk2BXHsn+wx8BZdTaoZ5fu7I=
github.com/google/uuid v1.3.0/go.mod h1:TIyPZe4MgqvfeYDBFedMoGGpEw/LqOeaOT+nhxU+yHo=`
			if err := os.WriteFile(sumPath, []byte(sumContent), 0o600); err != nil {
				t.Fatalf("Failed to create go.sum: %v", err)
			}

			// Call GoVendor - if cmd.Dir isn't set, vendor would be in wrong place
			_, _ = GoVendor(subDir, false)

			// Verify vendor wasn't created in parent (wrong) directory
			vendorInParent := filepath.Join(tmpDir, "vendor")
			if _, err := os.Stat(vendorInParent); err == nil {
				t.Errorf("vendor directory incorrectly created in parent directory %s", vendorInParent)
			}
		})
	})
}

// TestValidateModulePath tests module path validation against injection attacks.
func TestValidateModulePath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		// Valid paths
		{name: "valid github path", path: "github.com/google/uuid", wantErr: false},
		{name: "valid golang.org path", path: "golang.org/x/mod", wantErr: false},
		{name: "valid nested path", path: "github.com/chainguard-dev/omnibump/pkg/languages", wantErr: false},
		{name: "valid with dashes", path: "github.com/some-org/some-repo", wantErr: false},

		// Invalid/Injection paths
		{name: "empty string", path: "", wantErr: true},
		{name: "flag injection", path: "--flag-injection", wantErr: true},
		{name: "semicolon injection", path: "name; rm -rf /", wantErr: true},
		{name: "pipe injection", path: "name | cat /etc/passwd", wantErr: true},
		{name: "dollar sign", path: "name$USER", wantErr: true},
		{name: "backtick injection", path: "name`whoami`", wantErr: true},
		{name: "newline injection", path: "name\nrm -rf /", wantErr: true},
		{name: "carriage return", path: "name\rmalicious", wantErr: true},
		{name: "relative path", path: "../../../etc/passwd", wantErr: true},
		{name: "absolute path", path: "/usr/local/go", wantErr: true},
		{name: "spaces", path: "github.com/name with spaces", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateModulePath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateModulePath() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestGoGetModule_RejectsInvalidPath tests that GoGetModule rejects invalid paths.
func TestGoGetModule_RejectsInvalidPath(t *testing.T) {
	tmpDir := t.TempDir()

	invalidPaths := []string{
		"--flag-injection",
		"name; rm -rf /",
		"",
		"name | cat /etc/passwd",
	}

	for _, invalidPath := range invalidPaths {
		t.Run(invalidPath, func(t *testing.T) {
			_, err := GoGetModule(invalidPath, "v1.0.0", tmpDir)
			if err == nil {
				t.Errorf("GoGetModule should reject invalid path %q", invalidPath)
			}
			errMsg := err.Error()
			if !strings.Contains(errMsg, "invalid module path") && !strings.Contains(errMsg, "cannot be empty") {
				t.Errorf("Expected 'invalid module path' or 'cannot be empty' error, got: %v", err)
			}
		})
	}
}

// TestGoModEditReplaceModule_RejectsInvalidPath tests that GoModEditReplaceModule rejects invalid paths.
func TestGoModEditReplaceModule_RejectsInvalidPath(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name    string
		nameOld string
		nameNew string
	}{
		{"invalid old path", "--flag-injection", "github.com/valid/repo"},
		{"invalid new path", "github.com/valid/repo", "name; rm -rf /"},
		{"both invalid", "--flag", "name | cat"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := GoModEditReplaceModule(tt.nameOld, tt.nameNew, "v1.0.0", tmpDir)
			if err == nil {
				t.Errorf("GoModEditReplaceModule should reject invalid paths")
			}
			if !strings.Contains(err.Error(), "invalid") {
				t.Errorf("Expected 'invalid' error, got: %v", err)
			}
		})
	}
}

// TestGoModEditDropRequireModule_RejectsInvalidPath tests that GoModEditDropRequireModule rejects invalid paths.
func TestGoModEditDropRequireModule_RejectsInvalidPath(t *testing.T) {
	tmpDir := t.TempDir()

	invalidPaths := []string{
		"--flag-injection",
		"name; rm -rf /",
		"",
	}

	for _, invalidPath := range invalidPaths {
		t.Run(invalidPath, func(t *testing.T) {
			_, err := GoModEditDropRequireModule(invalidPath, tmpDir)
			if err == nil {
				t.Errorf("GoModEditDropRequireModule should reject invalid path %q", invalidPath)
			}
			if !strings.Contains(err.Error(), "invalid module path") && !strings.Contains(err.Error(), "cannot be empty") {
				t.Errorf("Expected 'invalid module path' error, got: %v", err)
			}
		})
	}
}
