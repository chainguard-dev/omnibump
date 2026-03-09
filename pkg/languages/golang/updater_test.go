/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package golang

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// maybeParseFile will parse the file if the filename is not empty
// On failure, fatals to simplify the error handling in tests.
func maybeParseFile(t *testing.T, fileName string, packages map[string]*Package) map[string]*Package {
	if fileName != "" {
		ret, err := ParseFile(fileName)
		if err != nil {
			t.Fatalf("Failed to parse file %q: %v", fileName, err)
		}
		return ret
	}
	return packages
}

func TestUpdate(t *testing.T) {
	testCases := []struct {
		name        string
		pkgVersions map[string]*Package
		fileName    string
		want        map[string]string
	}{
		{
			name: "standard update",
			pkgVersions: map[string]*Package{
				"github.com/google/uuid": {
					Name:    "github.com/google/uuid",
					Version: "v1.4.0",
				},
			},
			want: map[string]string{
				"github.com/google/uuid": "v1.4.0",
			},
		},
		{
			name:     "standard update - from file",
			fileName: "testdata/standardUpdate.yaml",
			want: map[string]string{
				"github.com/google/uuid": "v1.4.0",
			},
		},
		{
			name: "replace",
			pkgVersions: map[string]*Package{
				"k8s.io/client-go": {
					OldName: "k8s.io/client-go",
					Name:    "k8s.io/client-go",
					Version: "v0.28.0",
				},
			},
			want: map[string]string{
				"k8s.io/client-go": "v0.28.0",
			},
		},
		{
			name:     "replace - from file",
			fileName: "testdata/standardReplace.yaml",
			want: map[string]string{
				"k8s.io/client-go": "v0.28.0",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpdir := t.TempDir()
			copyFile(t, "testdata/aws-efs-csi-driver/go.mod", tmpdir)
			pkgVersions := maybeParseFile(t, tc.fileName, tc.pkgVersions)
			modFile, err := DoUpdate(context.Background(), pkgVersions, &UpdateConfig{Modroot: tmpdir, Tidy: false, GoVersion: ""})
			if err != nil {
				t.Fatal(err)
			}
			for pkg, want := range tc.want {
				if got := getVersion(modFile, pkg); got != want {
					t.Errorf("expected %s, got %s", want, got)
				}
			}
		})
	}
}

func TestUpdateInOrder(t *testing.T) {
	testCases := []struct {
		name        string
		pkgVersions map[string]*Package
		fileName    string
		want        []string
	}{
		{
			name: "standard update",
			pkgVersions: map[string]*Package{
				"github.com/google/uuid": {
					Name:    "github.com/google/uuid",
					Version: "v1.4.0",
					Index:   0,
				},
				"k8s.io/api": {
					OldName: "k8s.io/api",
					Name:    "k8s.io/api",
					Version: "v0.28.0",
					Index:   2,
				},
				"k8s.io/client-go": {
					OldName: "k8s.io/client-go",
					Name:    "k8s.io/client-go",
					Version: "v0.28.0",
					Index:   1,
				},
			},
			want: []string{
				"github.com/google/uuid",
				"k8s.io/api",
				"k8s.io/client-go",
			},
		},
		{
			name:     "standard update - file",
			fileName: "testdata/inorder.yaml",
			want: []string{
				"github.com/google/uuid",
				"k8s.io/api",
				"k8s.io/client-go",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpdir := t.TempDir()
			copyFile(t, "testdata/aws-efs-csi-driver/go.mod", tmpdir)

			pkgVersions := maybeParseFile(t, tc.fileName, tc.pkgVersions)
			got := orderPkgVersionsMap(pkgVersions)
			if len(got) != len(tc.want) || reflect.DeepEqual(got, tc.want) {
				t.Errorf("expected %s, got %s", tc.want, got)
			}
		})
	}
}

func TestGoModTidy(t *testing.T) {
	testCases := []struct {
		name        string
		pkgVersions map[string]*Package
		fileName    string
		want        map[string]string
		wantErr     bool
		errMsg      string
	}{
		{
			name: "standard update",
			pkgVersions: map[string]*Package{
				"github.com/sirupsen/logrus": {
					Name:    "github.com/sirupsen/logrus",
					Version: "v1.9.0",
				},
			},
			want: map[string]string{
				"github.com/sirupsen/logrus": "v1.9.0",
			},
		},
		{
			name:     "standard update - file",
			fileName: "testdata/logrus.yaml",
			want: map[string]string{
				"github.com/sirupsen/logrus": "v1.9.0",
			},
		},
		{
			name: "error when bumping main module",
			pkgVersions: map[string]*Package{
				"github.com/puerco/hello": {
					Name:    "github.com/puerco/hello",
					Version: "v1.9.0",
				},
			},
			wantErr: true,
			errMsg:  "bumping the main module is not allowed: 'github.com/puerco/hello'",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpdir := t.TempDir()
			copyFile(t, "testdata/hello/go.mod", tmpdir)
			copyFile(t, "testdata/hello/go.sum", tmpdir)
			copyFile(t, "testdata/hello/main.go", tmpdir)

			pkgVersions := maybeParseFile(t, tc.fileName, tc.pkgVersions)
			modFile, err := DoUpdate(context.Background(), pkgVersions, &UpdateConfig{Modroot: tmpdir, Tidy: false, GoVersion: ""})
			if (err != nil) != tc.wantErr {
				t.Errorf("DoUpdate() error = %v, wantErr %v", err, tc.wantErr)
				return
			}
			if tc.wantErr && err.Error() != tc.errMsg {
				t.Errorf("expected err message %s, got %s", tc.errMsg, err.Error())
			}
			for pkg, want := range tc.want {
				if got := getVersion(modFile, pkg); got != want {
					t.Errorf("expected %s, got %s", want, got)
				}
			}
		})
	}
}

func TestWorkFlagInUpdate(t *testing.T) {
	testCases := []struct {
		name        string
		pkgVersions map[string]*Package
		workFlag    bool
		setupFunc   func(string) error
		want        map[string]string
	}{
		{
			name: "update with work flag false",
			pkgVersions: map[string]*Package{
				"github.com/google/uuid": {
					Name:    "github.com/google/uuid",
					Version: "v1.4.0",
				},
			},
			workFlag: false,
			want: map[string]string{
				"github.com/google/uuid": "v1.4.0",
			},
		},
		{
			name: "update with work flag true",
			pkgVersions: map[string]*Package{
				"github.com/google/uuid": {
					Name:    "github.com/google/uuid",
					Version: "v1.4.0",
				},
			},
			workFlag: true,
			want: map[string]string{
				"github.com/google/uuid": "v1.4.0",
			},
		},
		{
			name: "update with work flag true and go.work file",
			pkgVersions: map[string]*Package{
				"github.com/google/uuid": {
					Name:    "github.com/google/uuid",
					Version: "v1.4.0",
				},
			},
			workFlag: true,
			setupFunc: func(dir string) error {
				// Create a go.work file
				workContent := `go 1.21

use .
`
				return os.WriteFile(filepath.Join(dir, "go.work"), []byte(workContent), 0o600)
			},
			want: map[string]string{
				"github.com/google/uuid": "v1.4.0",
			},
		},
		{
			name: "update with vendor directory and work flag false",
			pkgVersions: map[string]*Package{
				"github.com/google/uuid": {
					Name:    "github.com/google/uuid",
					Version: "v1.4.0",
				},
			},
			workFlag: false,
			setupFunc: func(dir string) error {
				// Create vendor directory to trigger vendor command
				vendorDir := filepath.Join(dir, "vendor")
				return os.Mkdir(vendorDir, 0o750)
			},
			want: map[string]string{
				"github.com/google/uuid": "v1.4.0",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a temporary directory for testing
			tmpDir := t.TempDir()

			// Setup test environment if needed
			if tc.setupFunc != nil {
				if err := tc.setupFunc(tmpDir); err != nil {
					t.Fatalf("Setup failed: %v", err)
				}
			}

			// Create a simple go.mod file
			goModContent := `module test

go 1.21

require github.com/google/uuid v1.3.0
`
			if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goModContent), 0o600); err != nil {
				t.Fatalf("Failed to create go.mod: %v", err)
			}

			// Test DoUpdate with work flag
			modFile, err := DoUpdate(context.Background(), tc.pkgVersions, &UpdateConfig{
				Modroot:   tmpDir,
				ForceWork: tc.workFlag,
			})
			if err != nil {
				t.Errorf("DoUpdate() error = %v", err)
				return
			}

			// Verify the package was updated
			for pkg, want := range tc.want {
				if got := getVersion(modFile, pkg); got != want {
					t.Errorf("expected %s, got %s", want, got)
				}
			}
		})
	}
}

func TestGoModTidySkipInitial(t *testing.T) {
	testCases := []struct {
		name            string
		pkgVersions     map[string]*Package
		tidySkipInitial bool
		wantError       bool
		want            map[string]string
		errMsgContains  string
	}{
		{
			name: "do not skip initial tidy",
			pkgVersions: map[string]*Package{
				"github.com/coreos/etcd": {
					Name:    "github.com/coreos/etcd",
					Version: "v3.3.15",
				},
				"google.golang.org/grpc": {
					Name:    "google.golang.org/grpc",
					OldName: "google.golang.org/grpc",
					Version: "v1.29.0",
					Replace: true,
				},
			},
			tidySkipInitial: false,
			wantError:       true,
			errMsgContains:  "ambiguous import",
		},
		{
			name: "skip initial tidy",
			pkgVersions: map[string]*Package{
				"github.com/coreos/etcd": {
					Name:    "github.com/coreos/etcd",
					Version: "v3.3.15",
				},
				"google.golang.org/grpc": {
					Name:    "google.golang.org/grpc",
					OldName: "google.golang.org/grpc",
					Version: "v1.29.0",
					Replace: true,
				},
			},
			tidySkipInitial: true,
			wantError:       true,
			errMsgContains:  "go mod tidy",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpdir := t.TempDir()
			copyFile(t, "testdata/confd/go.mod", tmpdir)
			copyFile(t, "testdata/confd/go.sum", tmpdir)

			modFile, err := DoUpdate(context.Background(), tc.pkgVersions, &UpdateConfig{Modroot: tmpdir, Tidy: true, GoVersion: "1.19", SkipInitialTidy: tc.tidySkipInitial})
			if (err != nil) != tc.wantError {
				t.Errorf("DoUpdate() error = %v, wantErr %v", err, tc.wantError)
				return
			}
			if tc.wantError && !strings.Contains(err.Error(), tc.errMsgContains) {
				t.Errorf("expected err message not contains %s, got %s", tc.errMsgContains, err.Error())
			}
			for pkg, want := range tc.want {
				if got := getVersion(modFile, pkg); got != want {
					t.Errorf("expected %s, got %s", want, got)
				}
			}
		})
	}
}

func TestReplaceAndRequire(t *testing.T) {
	testCases := []struct {
		name        string
		pkgVersions map[string]*Package
		fileName    string
		want        map[string]string
	}{
		{
			name: "standard update",
			pkgVersions: map[string]*Package{
				"github.com/sirupsen/logrus": {
					Name:    "github.com/sirupsen/logrus",
					Version: "v1.9.0",
				},
			},
			want: map[string]string{
				"github.com/sirupsen/logrus": "v1.9.0",
			},
		},
		{
			name:     "standard update - file",
			fileName: "testdata/logrus.yaml",
			want: map[string]string{
				"github.com/sirupsen/logrus": "v1.9.0",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpdir := t.TempDir()
			copyFile(t, "testdata/bye/go.mod", tmpdir)
			copyFile(t, "testdata/bye/go.sum", tmpdir)
			copyFile(t, "testdata/bye/main.go", tmpdir)

			pkgVersions := maybeParseFile(t, tc.fileName, tc.pkgVersions)
			modFile, err := DoUpdate(context.Background(), pkgVersions, &UpdateConfig{Modroot: tmpdir, Tidy: false, GoVersion: ""})
			if err != nil {
				t.Fatal(err)
			}
			for pkg, want := range tc.want {
				if got := getVersion(modFile, pkg); got != want {
					t.Errorf("expected %s, got %s", want, got)
				}
			}
		})
	}
}

func TestUpdateError(t *testing.T) {
	testCases := []struct {
		name        string
		pkgVersions map[string]*Package
		fileName    string
	}{
		{
			name: "no downgrade",
			pkgVersions: map[string]*Package{
				"github.com/google/uuid": {
					Name:    "github.com/google/uuid",
					Version: "v1.0.0",
				},
			},
		},
		{
			name:     "no downgrade - from file",
			fileName: "testdata/nodowngrade.yaml",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpdir := t.TempDir()
			copyFile(t, "testdata/aws-efs-csi-driver/go.mod", tmpdir)

			pkgVersions := maybeParseFile(t, tc.fileName, tc.pkgVersions)
			_, err := DoUpdate(context.Background(), pkgVersions, &UpdateConfig{Modroot: tmpdir, Tidy: false, GoVersion: ""})
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestReplaces(t *testing.T) {
	testCases := []struct {
		name     string
		replaces map[string]*Package
		fileName string
	}{
		{
			name: "replace",
			replaces: map[string]*Package{
				"github.com/google/gofuzz": {
					OldName: "github.com/google/gofuzz",
					Name:    "github.com/fakefuzz",
					Version: "v1.2.3",
					Replace: true,
				},
			},
		},
		{
			name:     "replace - from file",
			fileName: "testdata/replaces.yaml",
		},
	}

	for _, tc := range testCases {
		tmpdir := t.TempDir()
		copyFile(t, "testdata/aws-efs-csi-driver/go.mod", tmpdir)

		replaces := maybeParseFile(t, tc.fileName, tc.replaces)
		modFile, err := DoUpdate(context.Background(), replaces, &UpdateConfig{Modroot: tmpdir, Tidy: false, GoVersion: ""})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range modFile.Replace {
			if r.Old.Path == "github.com/google/gofuzz" {
				if r.New.Path != "github.com/fakefuzz" {
					t.Errorf("expected replace of github.com/google/gofuzz with github.com/fakefuzz, got %s", r.New.Path)
				}
				if r.Old.Path != "github.com/google/gofuzz" {
					t.Errorf("expected replace of github.com/google/gofuzz, got %s", r.Old.Path)
				}
				if r.New.Version != "v1.2.3" {
					t.Errorf("expected replace of github.com/google/gofuzz with v1.2.3, got %s", r.New.Version)
				}
				break
			}
		}
	}
}

func TestCommit(t *testing.T) {
	// We use github.com/NVIDIA/go-nvml v0.11.7-0 in our go.mod
	// That corresponds to 53c34bc04d66e9209eff8654bc70563cf380e214
	pkg := "github.com/NVIDIA/go-nvml"

	// An older commit is c3a16a2b07cf2251cbedb76fa68c9292b22bfa06
	olderCommit := "c3a16a2b07cf2251cbedb76fa68c9292b22bfa06"
	olderVersion := "v0.11.6-0"
	// A newer commit is 95ef6acc3271a9894fd02c1071edef1d88527e20
	newerCommit := "95ef6acc3271a9894fd02c1071edef1d"
	newerVersion := "v0.12.0-1"

	testCases := []struct {
		name        string
		pkgVersions map[string]*Package
		fileName    string
		want        map[string]string
	}{
		{
			name: "pin to older",
			pkgVersions: map[string]*Package{
				pkg: {Name: pkg, Version: olderCommit},
			},
			want: map[string]string{
				pkg: olderVersion,
			},
		},
		{
			name:     "pin to older - file",
			fileName: "testdata/older.yaml",
			want: map[string]string{
				pkg: olderVersion,
			},
		},
		{
			name: "pin to newer",
			pkgVersions: map[string]*Package{
				pkg: {Name: pkg, Version: newerCommit},
			},
			want: map[string]string{
				pkg: newerVersion,
			},
		},
		{
			name:     "pin to newer - file",
			fileName: "testdata/newer.yaml",
			want: map[string]string{
				pkg: newerVersion,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpdir := t.TempDir()
			copyFile(t, "testdata/aws-efs-csi-driver/go.mod", tmpdir)

			pkgVersions := maybeParseFile(t, tc.fileName, tc.pkgVersions)
			modFile, err := DoUpdate(context.Background(), pkgVersions, &UpdateConfig{Modroot: tmpdir, Tidy: false, GoVersion: "1.21"})
			if err != nil {
				t.Fatal(err)
			}
			for pkg, want := range tc.want {
				if got := getVersion(modFile, pkg); got != want {
					t.Errorf("expected %s, got %s", want, got)
				}
			}
		})
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	_, err := exec.CommandContext(context.Background(), "cp", "-r", src, dst).Output()
	if err != nil {
		t.Fatal(err)
	}
}

func TestParseGoVersionString(t *testing.T) {
	tests := []struct {
		name          string
		versionOutput string
		want          string
		wantErr       bool
	}{
		{"valid version 1.15.2", "go version go1.15.2 linux/amd64", "1.15.2", false},
		{"valid version 1.21.6", "go version go1.21.6 darwin/arm64", "1.21.6", false},
		{"go not found", "sh: go: not found", "", true},
		{"unexpected format", "unexpected format string", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseGoVersionString(tt.versionOutput)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseGoVersionString() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("parseGoVersionString() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPreserveIndirectComments(t *testing.T) {
	testCases := []struct {
		name          string
		pkgVersions   map[string]*Package
		initialGoMod  string
		want          map[string]string
		checkIndirect map[string]bool // map of package -> should have indirect comment
		checkLocation bool            // verify package stays in same location
	}{
		{
			name: "preserve indirect comment",
			pkgVersions: map[string]*Package{
				"golang.org/x/sys": {
					Name:    "golang.org/x/sys",
					Version: "v0.28.0",
				},
			},
			initialGoMod: `module test

go 1.21

require (
	github.com/google/uuid v1.3.0
	golang.org/x/sys v0.27.0 // indirect
)
`,
			want: map[string]string{
				"golang.org/x/sys": "v0.28.0",
			},
			checkIndirect: map[string]bool{
				"golang.org/x/sys": true,
			},
		},
		{
			name: "preserve direct require (no indirect comment)",
			pkgVersions: map[string]*Package{
				"github.com/google/uuid": {
					Name:    "github.com/google/uuid",
					Version: "v1.4.0",
				},
			},
			initialGoMod: `module test

go 1.21

require (
	github.com/google/uuid v1.3.0
	golang.org/x/sys v0.27.0 // indirect
)
`,
			want: map[string]string{
				"github.com/google/uuid": "v1.4.0",
			},
			checkIndirect: map[string]bool{
				"github.com/google/uuid": false,
			},
		},
		{
			name: "preserve location in require block",
			pkgVersions: map[string]*Package{
				"golang.org/x/crypto": {
					Name:    "golang.org/x/crypto",
					Version: "v0.31.0",
				},
			},
			initialGoMod: `module test

go 1.21

require (
	github.com/google/uuid v1.3.0
	golang.org/x/crypto v0.30.0
	golang.org/x/sys v0.27.0
)
`,
			want: map[string]string{
				"golang.org/x/crypto": "v0.31.0",
			},
			checkLocation: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpdir := t.TempDir()
			goModPath := filepath.Join(tmpdir, "go.mod")

			// Write initial go.mod
			if err := os.WriteFile(goModPath, []byte(tc.initialGoMod), 0o600); err != nil {
				t.Fatalf("Failed to write go.mod: %v", err)
			}

			// Perform update
			modFile, err := DoUpdate(context.Background(), tc.pkgVersions, &UpdateConfig{
				Modroot: tmpdir,
				Tidy:    false,
			})
			if err != nil {
				t.Fatalf("DoUpdate failed: %v", err)
			}

			// Check versions were updated correctly
			for pkg, want := range tc.want {
				if got := getVersion(modFile, pkg); got != want {
					t.Errorf("expected %s version to be %s, got %s", pkg, want, got)
				}
			}

			// Check indirect comments are preserved
			for pkg, shouldBeIndirect := range tc.checkIndirect {
				found := false
				for _, req := range modFile.Require {
					if req.Mod.Path == pkg {
						found = true
						if req.Indirect != shouldBeIndirect {
							t.Errorf("expected %s indirect=%v, got indirect=%v", pkg, shouldBeIndirect, req.Indirect)
						}
						break
					}
				}
				if !found {
					t.Errorf("package %s not found in require list", pkg)
				}
			}

			// Check that the package stayed in the same location (not moved to a separate block)
			if tc.checkLocation {
				content, err := os.ReadFile(goModPath)
				if err != nil {
					t.Fatalf("Failed to read go.mod: %v", err)
				}
				contentStr := string(content)

				// Verify there's only one require block
				requireCount := strings.Count(contentStr, "require (")
				if requireCount > 1 {
					t.Errorf("expected 1 require block, found %d. Package may have been moved to a separate block", requireCount)
				}
			}
		})
	}
}

func TestHigherVersionRejection(t *testing.T) {
	testCases := []struct {
		name                 string
		pkgVersions          map[string]*Package
		setupFunc            func(string)
		expectErrorToContain string
		expectNoError        bool
	}{
		{
			name: "reject downgrade for required module",
			pkgVersions: map[string]*Package{
				"github.com/google/uuid": {
					Name:    "github.com/google/uuid",
					Version: "v1.0.0", // Lower version than what's in the go.mod
				},
			},
			setupFunc: func(dir string) {
				// Copy a go.mod with a higher version of github.com/google/uuid
				copyFile(t, "testdata/aws-efs-csi-driver/go.mod", dir)
			},
			expectErrorToContain: "package github.com/google/uuid: requested version 'v1.0.0', is already at version",
		},
		{
			name: "replace takes precedence over require - allow upgrade",
			pkgVersions: map[string]*Package{
				"github.com/example/dependency": {
					Name:    "github.com/example/dependency",
					Version: "v1.2.0", // Higher than replace (v1.1.0), lower than require (v1.4.0)
				},
			},
			setupFunc: func(dir string) {
				// Create a go.mod where replace has v1.1.0 but require has v1.4.0
				// In Go, replace takes precedence, so v1.2.0 should be allowed
				goModContent := `module test
go 1.23
replace github.com/example/dependency => github.com/example/dependency v1.1.0
require github.com/example/dependency v1.4.0
`
				if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goModContent), 0o600); err != nil {
					t.Fatalf("Failed to create go.mod: %v", err)
				}
			},
			expectNoError: true, // Should succeed because replace (v1.1.0) < v1.2.0
		},
		{
			name: "reject downgrade for replaced module",
			pkgVersions: map[string]*Package{
				"k8s.io/client-go": {
					Name:    "k8s.io/client-go",
					OldName: "k8s.io/client-go",
					Version: "v0.20.0", // Lower version than what's in the replace directive
					Replace: true,
				},
			},
			setupFunc: func(dir string) {
				copyFile(t, "testdata/aws-efs-csi-driver/go.mod", dir)
			},
			expectErrorToContain: "package k8s.io/client-go: requested version 'v0.20.0', is already at version",
		},
		{
			name: "reject multiple downgrades",
			pkgVersions: map[string]*Package{
				"github.com/google/uuid": {
					Name:    "github.com/google/uuid",
					Version: "v1.0.0", // In the go.mod (v1.3.1)
				},
				"github.com/aws/aws-sdk-go": {
					Name:    "github.com/aws/aws-sdk-go",
					Version: "v1.40.0", // In the go.mod (v1.44.116)
				},
				"k8s.io/api": {
					Name:    "k8s.io/api",
					Version: "v0.20.0", // In the go.mod (v0.26.10)
				},
			},
			setupFunc: func(dir string) {
				copyFile(t, "testdata/aws-efs-csi-driver/go.mod", dir)
			},
			expectErrorToContain: "The following errors were found:",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpdir := t.TempDir()

			tc.setupFunc(tmpdir)

			_, err := DoUpdate(context.Background(), tc.pkgVersions, &UpdateConfig{Modroot: tmpdir, Tidy: false, GoVersion: ""})

			if tc.expectNoError {
				if err != nil {
					t.Fatalf("Expected no error but got: %v", err)
				}
				return
			}

			if err == nil {
				t.Fatalf("Expected error but got nil") // Should get an error
			}

			if !strings.Contains(err.Error(), tc.expectErrorToContain) {
				t.Errorf("Expected error to contain %q, but got: %v", tc.expectErrorToContain, err)
			}

			// For multiple downgrade test, verify all modules are mentioned in the error
			if tc.name == "reject multiple downgrades" {
				errStr := err.Error()
				for pkg := range tc.pkgVersions {
					if !strings.Contains(errStr, pkg) {
						t.Errorf("Expected error to contain package name %q, but got: %v", pkg, err)
					}
				}

				errorCount := strings.Count(errStr, "- package ")
				if errorCount != len(tc.pkgVersions) {
					t.Errorf("Expected %d error messages, but found %d in: %v",
						len(tc.pkgVersions), errorCount, err)
				}
			}
		})
	}
}

func TestShouldDowngradeGoVersion(t *testing.T) {
	tests := []struct {
		name           string
		currentVersion string
		envGoVersion   string
		want           bool
	}{{
		name:           "same version",
		currentVersion: "1.25.7",
		envGoVersion:   "1.25.7",
		want:           false,
	}, {
		name:           "downgrade needed",
		currentVersion: "1.26.0",
		envGoVersion:   "1.25.7",
		want:           true,
	}, {
		name:           "upgrade not needed",
		currentVersion: "1.24.0",
		envGoVersion:   "1.25.7",
		want:           false,
	}, {
		name:           "invalid current version",
		currentVersion: "invalid",
		envGoVersion:   "1.25.7",
		want:           false,
	}, {
		name:           "invalid env version",
		currentVersion: "1.26.0",
		envGoVersion:   "invalid",
		want:           false,
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldDowngradeGoVersion(tt.currentVersion, tt.envGoVersion)
			if got != tt.want {
				t.Errorf("shouldDowngradeGoVersion() got = %v, wanted = %v", got, tt.want)
			}
		})
	}
}

func TestNormalizeGoModVersion(t *testing.T) {
	tests := []struct {
		name          string
		initialGoMod  string
		envGoVersion  string
		wantGoVersion string
		wantToolchain bool
	}{{
		name: "downgrade from 1.26 to 1.25.7",
		initialGoMod: `module test

go 1.25

require (
	example.com/foo v1.0.0
)
`,
		envGoVersion:  "1.25.7",
		wantGoVersion: "1.25.7",
		wantToolchain: false,
	}, {
		name: "remove toolchain directive",
		initialGoMod: `module test

go 1.25

toolchain go1.26.0

require (
	example.com/foo v1.0.0
)
`,
		envGoVersion:  "1.25.7",
		wantGoVersion: "1.25",
		wantToolchain: false,
	}, {
		name: "no change needed",
		initialGoMod: `module test

go 1.25.7

require (
	example.com/foo v1.0.0
)
`,
		envGoVersion:  "1.25.7",
		wantGoVersion: "1.25.7",
		wantToolchain: false,
	}, {
		name: "add missing go directive",
		initialGoMod: `module test

require (
	example.com/foo v1.0.0
)
`,
		envGoVersion:  "1.25.7",
		wantGoVersion: "1.25.7",
		wantToolchain: false,
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			goModPath := filepath.Join(tmpDir, "go.mod")

			if err := os.WriteFile(goModPath, []byte(tt.initialGoMod), 0o600); err != nil {
				t.Fatalf("Failed to create go.mod: %v", err)
			}

			ctx := context.Background()
			if err := normalizeGoModVersion(ctx, goModPath, tt.envGoVersion); err != nil {
				t.Fatalf("normalizeGoModVersion() error: got = %v", err)
			}

			modFile, _, err := ParseGoModfile(goModPath)
			if err != nil {
				t.Fatalf("Failed to parse updated go.mod: %v", err)
			}

			if modFile.Go == nil {
				t.Fatal("go directive is missing after normalization")
			}

			if modFile.Go.Version != tt.wantGoVersion {
				t.Errorf("go version: got = %v, wanted = %v", modFile.Go.Version, tt.wantGoVersion)
			}

			if tt.wantToolchain && modFile.Toolchain == nil {
				t.Error("expected toolchain directive to be present")
			} else if !tt.wantToolchain && modFile.Toolchain != nil {
				t.Errorf("expected toolchain directive to be removed, but found: %v", modFile.Toolchain.Name)
			}
		})
	}
}
