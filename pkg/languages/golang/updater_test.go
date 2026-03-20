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

	"github.com/chainguard-dev/omnibump/pkg/languages"
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
			// With +incompatible normalization, v3.3.15+incompatible is a valid version so
			// go mod tidy succeeds. Tidy then removes etcd from go.mod (it is not directly
			// imported by confd), so verifyAndFinalize reports package not found.
			errMsgContains: "package not found in go.mod",
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

func TestDowngradeSkipped(t *testing.T) {
	testCases := []struct {
		name        string
		pkgVersions map[string]*Package
		fileName    string
		// currentVersion is the version already in go.mod (higher than what we request)
		currentVersion string
	}{
		{
			name: "downgrade skipped with warning",
			pkgVersions: map[string]*Package{
				"github.com/google/uuid": {
					Name:    "github.com/google/uuid",
					Version: "v1.0.0",
				},
			},
			currentVersion: "v1.3.1",
		},
		{
			name:           "downgrade skipped with warning - from file",
			fileName:       "testdata/nodowngrade.yaml",
			currentVersion: "v1.3.1",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpdir := t.TempDir()
			copyFile(t, "testdata/aws-efs-csi-driver/go.mod", tmpdir)

			pkgVersions := maybeParseFile(t, tc.fileName, tc.pkgVersions)
			modFile, err := DoUpdate(t.Context(), pkgVersions, &UpdateConfig{Modroot: tmpdir, Tidy: false, GoVersion: ""})
			if err != nil {
				t.Fatalf("expected no error, got: %v", err)
			}
			// Verify the package was not downgraded
			if got := getVersion(modFile, "github.com/google/uuid"); got != tc.currentVersion {
				t.Errorf("github.com/google/uuid version: got = %s, want = %s", got, tc.currentVersion)
			}
		})
	}
}

// TestCrossPathReplacePreservedWithRequireUpdate verifies that a cross-path replace
// directive (OldName != NewName) is preserved when a regular require update is also
// being processed. The bug was that GoModEditReplaceModule writes to disk, but the
// subsequent AddRequire + WriteFile path overwrites the disk file using a stale
// in-memory modFile that was parsed before the replace was written.
func TestCrossPathReplacePreservedWithRequireUpdate(t *testing.T) {
	pkgVersions := map[string]*Package{
		// Cross-path replace: github.com/google/gofuzz -> github.com/fakefuzz
		"github.com/fakefuzz": {
			OldName: "github.com/google/gofuzz",
			Name:    "github.com/fakefuzz",
			Version: "v1.2.3",
			Replace: true,
		},
		// Regular require update: causes hasDirectEdits=true, triggering the WriteFile path
		"github.com/google/uuid": {
			Name:    "github.com/google/uuid",
			Version: "v1.4.0",
		},
	}

	tmpdir := t.TempDir()
	copyFile(t, "testdata/aws-efs-csi-driver/go.mod", tmpdir)

	modFile, err := DoUpdate(context.Background(), pkgVersions, &UpdateConfig{
		Modroot: tmpdir,
		Tidy:    false,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify the require was updated
	if got := getVersion(modFile, "github.com/google/uuid"); got != "v1.4.0" {
		t.Errorf("github.com/google/uuid: expected v1.4.0, got %s", got)
	}

	// Verify the cross-path replace directive is still present.
	// Without the fix, DoUpdate returns "package not found in go.mod: package github.com/fakefuzz"
	// because the in-memory modFile write overwrites the replace that was written to disk.
	if got := getVersion(modFile, "github.com/fakefuzz"); got != "v1.2.3" {
		t.Errorf("github.com/fakefuzz: expected v1.2.3 in replace directive, got %q", got)
	}

	foundReplace := false
	for _, r := range modFile.Replace {
		if r.Old.Path == "github.com/google/gofuzz" && r.New.Path == "github.com/fakefuzz" {
			foundReplace = true
			if r.New.Version != "v1.2.3" {
				t.Errorf("replace version: expected v1.2.3, got %s", r.New.Version)
			}
		}
	}
	if !foundReplace {
		t.Error("cross-path replace directive was not preserved after require update")
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

func TestReplaceIncompatibleVersion(t *testing.T) {
	// Verifies that +incompatible is correctly applied when updating a package
	// via a replace directive (pkg.Replace == true).
	tmpdir := t.TempDir()
	initialMod := `module test

go 1.21

require github.com/example/legacy v2.0.0+incompatible

replace github.com/example/legacy => github.com/example/legacy v2.0.0+incompatible
`
	if err := os.WriteFile(filepath.Join(tmpdir, "go.mod"), []byte(initialMod), 0o600); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}

	pkgVersions := map[string]*Package{
		"github.com/example/legacy": {
			OldName: "github.com/example/legacy",
			Name:    "github.com/example/legacy",
			Version: "v3.0.0", // deliberately omit +incompatible
			Replace: true,
		},
	}

	modFile, err := DoUpdate(context.Background(), pkgVersions, &UpdateConfig{
		Modroot: tmpdir,
		Tidy:    false,
	})
	if err != nil {
		t.Fatalf("DoUpdate() error = %v", err)
	}

	// Verify the replace directive was updated with the correct +incompatible version.
	found := false
	for _, r := range modFile.Replace {
		if r.Old.Path == "github.com/example/legacy" {
			found = true
			if r.New.Version != "v3.0.0+incompatible" {
				t.Errorf("replace version: got %q, want %q", r.New.Version, "v3.0.0+incompatible")
			}
		}
	}
	if !found {
		t.Error("replace directive for github.com/example/legacy not found after update")
	}

	// Verify the written go.mod parses cleanly.
	if _, _, err := ParseGoModfile(filepath.Join(tmpdir, "go.mod")); err != nil {
		t.Errorf("go.mod is not parseable after update: %v", err)
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

func TestDowngradeHandling(t *testing.T) {
	testCases := []struct {
		name            string
		pkgVersions     map[string]*Package
		setupFunc       func(string)
		wantErr         bool
		// wantVersions maps package name to the version expected in go.mod after the update
		wantVersions map[string]string
	}{
		{
			name: "downgrade skipped for required module",
			pkgVersions: map[string]*Package{
				"github.com/google/uuid": {
					Name:    "github.com/google/uuid",
					Version: "v1.0.0", // lower than v1.3.1 in go.mod
				},
			},
			setupFunc: func(dir string) {
				copyFile(t, "testdata/aws-efs-csi-driver/go.mod", dir)
			},
			wantVersions: map[string]string{
				"github.com/google/uuid": "v1.3.1", // unchanged
			},
		},
		{
			name: "replace takes precedence over require - allow upgrade",
			pkgVersions: map[string]*Package{
				"github.com/example/dependency": {
					Name:    "github.com/example/dependency",
					Version: "v1.2.0", // higher than replace (v1.1.0), lower than require (v1.4.0)
				},
			},
			setupFunc: func(dir string) {
				// replace takes precedence in Go, so v1.2.0 > v1.1.0 is an upgrade, not a downgrade
				goModContent := `module test
go 1.23
replace github.com/example/dependency => github.com/example/dependency v1.1.0
require github.com/example/dependency v1.4.0
`
				if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goModContent), 0o600); err != nil {
					t.Fatalf("Failed to create go.mod: %v", err)
				}
			},
		},
		{
			name: "downgrade skipped for replaced module",
			pkgVersions: map[string]*Package{
				"k8s.io/client-go": {
					Name:    "k8s.io/client-go",
					OldName: "k8s.io/client-go",
					Version: "v0.20.0", // lower than what's in the replace directive
					Replace: true,
				},
			},
			setupFunc: func(dir string) {
				copyFile(t, "testdata/aws-efs-csi-driver/go.mod", dir)
			},
		},
		{
			name: "multiple downgrades all skipped",
			pkgVersions: map[string]*Package{
				"github.com/google/uuid": {
					Name:    "github.com/google/uuid",
					Version: "v1.0.0", // go.mod has v1.3.1
				},
				"github.com/aws/aws-sdk-go": {
					Name:    "github.com/aws/aws-sdk-go",
					Version: "v1.40.0", // go.mod has v1.44.116
				},
				"k8s.io/api": {
					Name:    "k8s.io/api",
					Version: "v0.20.0", // go.mod has v0.26.10
				},
			},
			setupFunc: func(dir string) {
				copyFile(t, "testdata/aws-efs-csi-driver/go.mod", dir)
			},
			wantVersions: map[string]string{
				"github.com/google/uuid":    "v1.3.1",   // unchanged
				"github.com/aws/aws-sdk-go": "v1.44.116", // unchanged
				"k8s.io/api":                "v0.26.10",  // unchanged
			},
		},
		{
			name: "force flag allows downgrade for required module",
			pkgVersions: map[string]*Package{
				"github.com/google/uuid": {
					Name:    "github.com/google/uuid",
					Version: "v1.0.0", // lower than v1.3.1 in go.mod
					Force:   true,
				},
			},
			setupFunc: func(dir string) {
				copyFile(t, "testdata/aws-efs-csi-driver/go.mod", dir)
			},
			wantVersions: map[string]string{
				"github.com/google/uuid": "v1.0.0", // forced downgrade
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpdir := t.TempDir()

			tc.setupFunc(tmpdir)

			modFile, err := DoUpdate(t.Context(), tc.pkgVersions, &UpdateConfig{Modroot: tmpdir, Tidy: false, GoVersion: ""})
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for pkg, want := range tc.wantVersions {
				if got := getVersion(modFile, pkg); got != want {
					t.Errorf("package %s version: got = %s, want = %s", pkg, got, want)
				}
			}
		})
	}
}

// incompatibleVersionTestCases is shared between TestUpdateIncompatibleVersion and
// TestDoUpdateIncompatibleVersion to verify +incompatible normalization through both
// the Golang.Update() and DoUpdate() call paths.
var incompatibleVersionTestCases = []struct {
	name        string
	depName     string
	depVersion  string
	initialMod  string
	wantVersion string
}{
	{
		name:       "bare version without +incompatible suffix",
		depName:    "github.com/example/legacy",
		depVersion: "v3.0.0",
		initialMod: `module test

go 1.21

require github.com/example/legacy v2.0.0+incompatible
`,
		wantVersion: "v3.0.0+incompatible",
	},
	{
		name:       "version already has +incompatible suffix",
		depName:    "github.com/example/legacy",
		depVersion: "v3.0.0+incompatible",
		initialMod: `module test

go 1.21

require github.com/example/legacy v2.0.0+incompatible
`,
		wantVersion: "v3.0.0+incompatible",
	},
	{
		name:       "module with /vN path suffix unaffected",
		depName:    "github.com/example/modern/v3",
		depVersion: "v3.1.0",
		initialMod: `module test

go 1.21

require github.com/example/modern/v3 v3.0.0
`,
		wantVersion: "v3.1.0",
	},
}

// TestUpdateIncompatibleVersion reproduces the bug where updating a module that uses
// +incompatible versioning (e.g., github.com/docker/cli v28.4.0+incompatible -> v29.2.0)
// would write "v29.2.0" to go.mod without the required +incompatible suffix, causing
// modfile.Parse to reject it. Exercises the full path through Golang.Update() ->
// resolveAndFilterPackages -> DoUpdate.
func TestUpdateIncompatibleVersion(t *testing.T) {
	for _, tc := range incompatibleVersionTestCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpdir := t.TempDir()
			goModPath := filepath.Join(tmpdir, "go.mod")
			if err := os.WriteFile(goModPath, []byte(tc.initialMod), 0o600); err != nil {
				t.Fatalf("failed to write go.mod: %v", err)
			}

			g := &Golang{}
			err := g.Update(context.Background(), &languages.UpdateConfig{
				RootDir: tmpdir,
				Dependencies: []languages.Dependency{
					{Name: tc.depName, Version: tc.depVersion},
				},
				Tidy: false,
			})
			if err != nil {
				t.Fatalf("Update() error = %v", err)
			}

			// Verify the written go.mod can be parsed — the primary regression check.
			parsedMod, _, err := ParseGoModfile(goModPath)
			if err != nil {
				t.Fatalf("go.mod is not parseable after update: %v", err)
			}

			if got := getVersion(parsedMod, tc.depName); got != tc.wantVersion {
				t.Errorf("package %s: got version %q, want %q", tc.depName, got, tc.wantVersion)
			}
		})
	}
}

// TestDoUpdateIncompatibleVersion verifies that DoUpdate itself normalizes
// +incompatible versions, so callers that bypass resolveAndFilterPackages are
// also protected.
func TestDoUpdateIncompatibleVersion(t *testing.T) {
	for _, tc := range incompatibleVersionTestCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpdir := t.TempDir()
			goModPath := filepath.Join(tmpdir, "go.mod")
			if err := os.WriteFile(goModPath, []byte(tc.initialMod), 0o600); err != nil {
				t.Fatalf("failed to write go.mod: %v", err)
			}

			pkgVersions := map[string]*Package{
				tc.depName: {Name: tc.depName, Version: tc.depVersion},
			}

			modFile, err := DoUpdate(context.Background(), pkgVersions, &UpdateConfig{
				Modroot: tmpdir,
				Tidy:    false,
			})
			if err != nil {
				t.Fatalf("DoUpdate() error = %v", err)
			}

			if got := getVersion(modFile, tc.depName); got != tc.wantVersion {
				t.Errorf("got version %q, want %q", got, tc.wantVersion)
			}

			// Verify the written go.mod parses cleanly — primary regression check.
			if _, _, err := ParseGoModfile(goModPath); err != nil {
				t.Errorf("go.mod is not parseable after update: %v", err)
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

go 1.26

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
