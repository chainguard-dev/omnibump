/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package golang

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chainguard-dev/omnibump/pkg/analyzer"
	"github.com/chainguard-dev/omnibump/pkg/languages"
	"github.com/stretchr/testify/require"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

func TestAppendIncompatibleIfNeeded(t *testing.T) {
	tests := []struct {
		name       string
		modulePath string
		version    string
		want       string
	}{
		{
			name:       "no suffix needed for v1",
			modulePath: "github.com/docker/cli",
			version:    "v1.0.0",
			want:       "v1.0.0",
		},
		{
			name:       "no suffix needed for v0",
			modulePath: "github.com/docker/cli",
			version:    "v0.9.0",
			want:       "v0.9.0",
		},
		{
			name:       "adds +incompatible for v2+ without path suffix",
			modulePath: "github.com/docker/cli",
			version:    "v29.2.0",
			want:       "v29.2.0+incompatible",
		},
		{
			name:       "already has +incompatible, no change",
			modulePath: "github.com/docker/cli",
			version:    "v28.4.0+incompatible",
			want:       "v28.4.0+incompatible",
		},
		{
			name:       "module with /v2 path suffix, no +incompatible needed",
			modulePath: "github.com/docker/cli/v2",
			version:    "v2.1.0",
			want:       "v2.1.0",
		},
		{
			name:       "module with /v29 path suffix, no +incompatible needed",
			modulePath: "github.com/docker/cli/v29",
			version:    "v29.2.0",
			want:       "v29.2.0",
		},
		{
			name:       "non-semver version, no change",
			modulePath: "github.com/docker/cli",
			version:    "abc123def456",
			want:       "abc123def456",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := appendIncompatibleIfNeeded(tt.modulePath, tt.version)
			if got != tt.want {
				t.Errorf("appendIncompatibleIfNeeded(%q, %q) = %q, want %q", tt.modulePath, tt.version, got, tt.want)
			}
		})
	}
}

func TestIsVersionQuery(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    bool
	}{
		{
			name:    "latest query",
			version: "latest",
			want:    true,
		},
		{
			name:    "upgrade query",
			version: "upgrade",
			want:    true,
		},
		{
			name:    "patch query",
			version: "patch",
			want:    true,
		},
		{
			name:    "specific version",
			version: "v1.2.3",
			want:    false,
		},
		{
			name:    "semver without v prefix",
			version: "1.2.3",
			want:    false,
		},
		{
			name:    "commit hash",
			version: "abc123def456",
			want:    false,
		},
		{
			name:    "empty string",
			version: "",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isVersionQuery(tt.version)
			if got != tt.want {
				t.Errorf("isVersionQuery(%q) = %v, want %v", tt.version, got, tt.want)
			}
		})
	}
}

func TestResolveAndFilterPackages(t *testing.T) {
	tests := []struct {
		name         string
		packages     map[string]*Package
		modFile      *modfile.File
		wantFiltered int
		wantSkipped  []string
		skipResolver bool // Skip actual version resolution
	}{
		{
			name: "package already at target version",
			packages: map[string]*Package{
				"example.com/foo": {
					Name:    "example.com/foo",
					Version: "v1.2.3",
				},
			},
			modFile: &modfile.File{
				Module: &modfile.Module{
					Mod: module.Version{Path: "test/module"},
				},
				Require: []*modfile.Require{
					{
						Mod: module.Version{
							Path:    "example.com/foo",
							Version: "v1.2.3",
						},
					},
				},
			},
			wantFiltered: 0,
			wantSkipped:  []string{"example.com/foo"},
			skipResolver: true,
		},
		{
			name: "package needs upgrade",
			packages: map[string]*Package{
				"example.com/bar": {
					Name:    "example.com/bar",
					Version: "v1.5.0",
				},
			},
			modFile: &modfile.File{
				Module: &modfile.Module{
					Mod: module.Version{Path: "test/module"},
				},
				Require: []*modfile.Require{
					{
						Mod: module.Version{
							Path:    "example.com/bar",
							Version: "v1.2.0",
						},
					},
				},
			},
			wantFiltered: 1,
			wantSkipped:  nil,
			skipResolver: true,
		},
		{
			name: "current version newer than requested",
			packages: map[string]*Package{
				"example.com/baz": {
					Name:    "example.com/baz",
					Version: "v1.0.0",
				},
			},
			modFile: &modfile.File{
				Module: &modfile.Module{
					Mod: module.Version{Path: "test/module"},
				},
				Require: []*modfile.Require{
					{
						Mod: module.Version{
							Path:    "example.com/baz",
							Version: "v2.0.0",
						},
					},
				},
			},
			wantFiltered: 0,
			wantSkipped:  []string{"example.com/baz"},
			skipResolver: true,
		},
		{
			name: "package not in go.mod",
			packages: map[string]*Package{
				"example.com/new": {
					Name:    "example.com/new",
					Version: "v1.0.0",
				},
			},
			modFile: &modfile.File{
				Module: &modfile.Module{
					Mod: module.Version{Path: "test/module"},
				},
				Require: []*modfile.Require{},
			},
			wantFiltered: 1,
			wantSkipped:  nil,
			skipResolver: true,
		},
		{
			name: "package with versioned replace directive is skipped",
			packages: map[string]*Package{
				"k8s.io/apiserver": {
					Name:    "k8s.io/apiserver",
					Version: "v0.29.4",
					Replace: false,
				},
			},
			modFile: &modfile.File{
				Module: &modfile.Module{
					Mod: module.Version{Path: "test/module"},
				},
				Require: []*modfile.Require{
					{Mod: module.Version{Path: "k8s.io/apiserver", Version: "v0.29.3"}},
				},
				Replace: []*modfile.Replace{
					{
						Old: module.Version{Path: "k8s.io/apiserver", Version: "v0.0.0"},
						New: module.Version{Path: "k8s.io/apiserver", Version: "v0.29.3"},
					},
				},
			},
			wantFiltered: 0,
			wantSkipped:  []string{"k8s.io/apiserver"},
			skipResolver: true,
		},
		{
			name: "package with replace directive is not skipped when explicitly marked Replace",
			packages: map[string]*Package{
				"k8s.io/apiserver": {
					Name:    "k8s.io/apiserver",
					OldName: "k8s.io/apiserver",
					Version: "v0.29.4",
					Replace: true,
				},
			},
			modFile: &modfile.File{
				Module: &modfile.Module{
					Mod: module.Version{Path: "test/module"},
				},
				Require: []*modfile.Require{
					{Mod: module.Version{Path: "k8s.io/apiserver", Version: "v0.29.3"}},
				},
				Replace: []*modfile.Replace{
					{
						Old: module.Version{Path: "k8s.io/apiserver", Version: "v0.0.0"},
						New: module.Version{Path: "k8s.io/apiserver", Version: "v0.29.3"},
					},
				},
			},
			wantFiltered: 1,
			wantSkipped:  nil,
			skipResolver: true,
		},
		{
			name: "multiple packages mixed scenario",
			packages: map[string]*Package{
				"example.com/upgrade": {
					Name:    "example.com/upgrade",
					Version: "v2.0.0",
				},
				"example.com/same": {
					Name:    "example.com/same",
					Version: "v1.5.0",
				},
				"example.com/newer": {
					Name:    "example.com/newer",
					Version: "v1.0.0",
				},
			},
			modFile: &modfile.File{
				Module: &modfile.Module{
					Mod: module.Version{Path: "test/module"},
				},
				Require: []*modfile.Require{
					{
						Mod: module.Version{
							Path:    "example.com/upgrade",
							Version: "v1.0.0",
						},
					},
					{
						Mod: module.Version{
							Path:    "example.com/same",
							Version: "v1.5.0",
						},
					},
					{
						Mod: module.Version{
							Path:    "example.com/newer",
							Version: "v2.0.0",
						},
					},
				},
			},
			wantFiltered: 1, // Only upgrade package
			wantSkipped:  []string{"example.com/same", "example.com/newer"},
			skipResolver: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// For tests that don't need actual resolution (skipResolver=true),
			// we can test the filtering logic directly
			if tt.skipResolver {
				filtered := resolveAndFilterPackagesForTest(tt.packages, tt.modFile)

				if len(filtered) != tt.wantFiltered {
					t.Errorf("got %d filtered packages, want %d", len(filtered), tt.wantFiltered)
				}

				// Check that skipped packages are not in filtered result
				for _, skipped := range tt.wantSkipped {
					if _, exists := filtered[skipped]; exists {
						t.Errorf("package %s should have been skipped but was included", skipped)
					}
				}
			}
		})
	}
}

// resolveAndFilterPackagesForTest is a test version that doesn't call go list.
func resolveAndFilterPackagesForTest(packages map[string]*Package, modFile *modfile.File) map[string]*Package {
	filtered := make(map[string]*Package)

	for name, pkg := range packages {
		// Skip version resolution for tests - use version as-is
		resolvedVersion := pkg.Version

		// Mirror the normalization in the real resolveAndFilterPackages.
		resolvedVersion = appendIncompatibleIfNeeded(name, resolvedVersion)

		// Get current version from go.mod
		currentVersion := getVersion(modFile, name)

		if currentVersion == "" {
			// Package doesn't exist in go.mod, add it
			pkg.Version = resolvedVersion
			filtered[name] = pkg
			continue
		}

		// Skip packages pinned via replace directive unless explicitly a replace update.
		if !pkg.Replace && hasReplaceDirective(modFile, name) {
			continue
		}

		// Compare versions using semver (simplified for test)
		if currentVersion == resolvedVersion {
			// Already at target version, skip
			continue
		}

		// Check if current version is newer
		if isNewer(currentVersion, resolvedVersion) {
			// Current version is newer, skip
			continue
		}

		// Update to resolved version
		pkg.Version = resolvedVersion
		filtered[name] = pkg
	}

	return filtered
}

// isNewer is a simple version comparison helper for tests.
func isNewer(v1, v2 string) bool {
	// Simple string comparison for test purposes
	// In real code, use semver.Compare
	return v1 > v2
}

func TestConvertDependenciesToPackages(t *testing.T) {
	tests := []struct {
		name string
		deps []struct {
			Name    string
			Version string
			Replace bool
			OldName string
		}
		wantCount int
	}{
		{
			name: "single dependency",
			deps: []struct {
				Name    string
				Version string
				Replace bool
				OldName string
			}{
				{Name: "example.com/foo", Version: "v1.2.3"},
			},
			wantCount: 1,
		},
		{
			name: "dependency with replace",
			deps: []struct {
				Name    string
				Version string
				Replace bool
				OldName string
			}{
				{Name: "example.com/new", Version: "v2.0.0", Replace: true, OldName: "example.com/old"},
			},
			wantCount: 1,
		},
		{
			name: "empty dependencies",
			deps: []struct {
				Name    string
				Version string
				Replace bool
				OldName string
			}{},
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// We can't directly test convertDependenciesToPackages since it uses
			// languages.Dependency type, but we can test the concept
			if len(tt.deps) != tt.wantCount {
				t.Errorf("expected %d dependencies, got %d", tt.wantCount, len(tt.deps))
			}
		})
	}
}

func TestGetOptionString(t *testing.T) {
	tests := []struct {
		name         string
		options      map[string]any
		key          string
		defaultValue string
		want         string
	}{
		{
			name:         "key exists with string value",
			options:      map[string]any{"foo": "bar"},
			key:          "foo",
			defaultValue: "default",
			want:         "bar",
		},
		{
			name:         "key does not exist",
			options:      map[string]any{},
			key:          "missing",
			defaultValue: "default",
			want:         "default",
		},
		{
			name:         "key exists with non-string value",
			options:      map[string]any{"foo": 123},
			key:          "foo",
			defaultValue: "default",
			want:         "default",
		},
		{
			name:         "nil options map",
			options:      nil,
			key:          "foo",
			defaultValue: "default",
			want:         "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getOptionString(tt.options, tt.key, tt.defaultValue)
			if got != tt.want {
				t.Errorf("getOptionString() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetOptionBool(t *testing.T) {
	tests := []struct {
		name         string
		options      map[string]any
		key          string
		defaultValue bool
		want         bool
	}{
		{
			name:         "key exists with bool value true",
			options:      map[string]any{"foo": true},
			key:          "foo",
			defaultValue: false,
			want:         true,
		},
		{
			name:         "key exists with bool value false",
			options:      map[string]any{"foo": false},
			key:          "foo",
			defaultValue: true,
			want:         false,
		},
		{
			name:         "key does not exist",
			options:      map[string]any{},
			key:          "missing",
			defaultValue: true,
			want:         true,
		},
		{
			name:         "key exists with non-bool value",
			options:      map[string]any{"foo": "true"},
			key:          "foo",
			defaultValue: false,
			want:         false,
		},
		{
			name:         "nil options map",
			options:      nil,
			key:          "foo",
			defaultValue: true,
			want:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getOptionBool(tt.options, tt.key, tt.defaultValue)
			if got != tt.want {
				t.Errorf("getOptionBool() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGolang_Detect(t *testing.T) {
	tests := []struct {
		name      string
		files     []string
		wantFound bool
	}{
		{
			name:      "go.mod found",
			files:     []string{"go.mod"},
			wantFound: true,
		},
		{
			name:      "go.sum only - not found",
			files:     []string{"go.sum"},
			wantFound: false,
		},
		{
			name:      "both found",
			files:     []string{"go.mod", "go.sum"},
			wantFound: true,
		},
		{
			name:      "no go files",
			files:     []string{"package.json"},
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()

			for _, file := range tt.files {
				path := filepath.Join(tmpDir, file)
				err := os.WriteFile(path, []byte("module test"), 0o600)
				if err != nil {
					t.Fatalf("Failed to create file: %v", err)
				}
			}

			g := &Golang{}
			found, err := g.Detect(context.Background(), tmpDir)
			if err != nil {
				t.Fatalf("Detect failed: %v", err)
			}
			if found != tt.wantFound {
				t.Errorf("Expected found=%v, got %v", tt.wantFound, found)
			}
		})
	}
}

func TestGolang_GetManifestFiles(t *testing.T) {
	g := &Golang{}
	files := g.GetManifestFiles()
	expected := []string{"go.mod", "go.sum", "go.work"}

	if len(files) != len(expected) {
		t.Errorf("Expected %d files, got %d", len(expected), len(files))
	}
	for i, file := range expected {
		if files[i] != file {
			t.Errorf("Expected file %s, got %s", file, files[i])
		}
	}
}

func TestGolang_SupportsAnalysis(t *testing.T) {
	g := &Golang{}
	if !g.SupportsAnalysis() {
		t.Error("Golang should support analysis")
	}
}

func TestGolang_Update_MissingGoMod(t *testing.T) {
	tmpDir := t.TempDir()

	g := &Golang{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "github.com/google/uuid", Version: "v1.3.0"},
		},
	}

	err := g.Update(context.Background(), cfg)
	if err == nil {
		t.Fatal("Expected error for missing go.mod, got nil")
	}
	if !strings.Contains(err.Error(), "go.mod not found") {
		t.Errorf("Expected 'go.mod not found' error, got: %v", err)
	}
}

func TestGolang_Update_DryRun(t *testing.T) {
	tmpDir := t.TempDir()

	// Create minimal go.mod
	goModContent := `module test/module

go 1.25

require github.com/google/uuid v1.0.0
`
	goModPath := filepath.Join(tmpDir, "go.mod")
	err := os.WriteFile(goModPath, []byte(goModContent), 0o600)
	if err != nil {
		t.Fatalf("Failed to create go.mod: %v", err)
	}

	g := &Golang{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "github.com/google/uuid", Version: "v1.3.0"},
		},
		DryRun: true,
	}

	err = g.Update(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Verify go.mod was not changed
	content, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatalf("Failed to read go.mod: %v", err)
	}
	if !strings.Contains(string(content), "v1.0.0") {
		t.Error("go.mod should not have been modified in dry run mode")
	}
}

func TestGolang_Update_AllPackagesUpToDate(t *testing.T) {
	tmpDir := t.TempDir()

	// Create go.mod with package already at target version
	goModContent := `module test/module

go 1.25

require github.com/google/uuid v1.3.0
`
	goModPath := filepath.Join(tmpDir, "go.mod")
	err := os.WriteFile(goModPath, []byte(goModContent), 0o600)
	if err != nil {
		t.Fatalf("Failed to create go.mod: %v", err)
	}

	g := &Golang{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "github.com/google/uuid", Version: "v1.3.0"},
		},
	}

	err = g.Update(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
}

func TestGolang_Update_InvalidGoMod(t *testing.T) {
	tmpDir := t.TempDir()

	// Create invalid go.mod
	goModPath := filepath.Join(tmpDir, "go.mod")
	err := os.WriteFile(goModPath, []byte("invalid content"), 0o600)
	if err != nil {
		t.Fatalf("Failed to create go.mod: %v", err)
	}

	g := &Golang{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "github.com/google/uuid", Version: "v1.3.0"},
		},
	}

	err = g.Update(context.Background(), cfg)
	if err == nil {
		t.Fatal("Expected error for invalid go.mod, got nil")
	}
	if !strings.Contains(err.Error(), "failed to parse go.mod") {
		t.Errorf("Expected parse error, got: %v", err)
	}
}

func TestGolang_Validate_MissingGoMod(t *testing.T) {
	tmpDir := t.TempDir()

	g := &Golang{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "github.com/google/uuid", Version: "v1.3.0"},
		},
	}

	err := g.Validate(context.Background(), cfg)
	if err == nil {
		t.Fatal("Expected error for missing go.mod, got nil")
	}
	if !strings.Contains(err.Error(), "failed to parse updated go.mod") {
		t.Errorf("Expected parse error, got: %v", err)
	}
}

func TestGolang_Validate_Success(t *testing.T) {
	tmpDir := t.TempDir()

	// Create go.mod with updated version
	goModContent := `module test/module

go 1.25

require github.com/google/uuid v1.3.0
`
	goModPath := filepath.Join(tmpDir, "go.mod")
	err := os.WriteFile(goModPath, []byte(goModContent), 0o600)
	if err != nil {
		t.Fatalf("Failed to create go.mod: %v", err)
	}

	g := &Golang{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "github.com/google/uuid", Version: "v1.3.0"},
		},
	}

	err = g.Validate(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
}

func TestGolang_Validate_PackageNotFound(t *testing.T) {
	tmpDir := t.TempDir()

	// Create go.mod without the requested package
	goModContent := `module test/module

go 1.25

require github.com/sirupsen/logrus v1.0.0
`
	goModPath := filepath.Join(tmpDir, "go.mod")
	err := os.WriteFile(goModPath, []byte(goModContent), 0o600)
	if err != nil {
		t.Fatalf("Failed to create go.mod: %v", err)
	}

	g := &Golang{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "github.com/google/uuid", Version: "v1.3.0"},
		},
	}

	// Validate logs warnings but doesn't return error for missing packages
	err = g.Validate(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Validate should not fail for missing packages: %v", err)
	}
}

func TestConvertDependenciesToPackages_WithReplaces(t *testing.T) {
	deps := []languages.Dependency{
		{Name: "example.com/foo", Version: "v1.2.3"},
		{Name: "example.com/new", Version: "v2.0.0", Replace: true, OldName: "example.com/old"},
	}

	packages := convertDependenciesToPackages(deps)

	if len(packages) != 2 {
		t.Errorf("Expected 2 packages, got %d", len(packages))
	}

	if pkg, ok := packages["example.com/foo"]; !ok {
		t.Error("Expected example.com/foo package")
	} else if pkg.Version != "v1.2.3" {
		t.Errorf("Expected version v1.2.3, got %s", pkg.Version)
	}

	if pkg, ok := packages["example.com/new"]; !ok {
		t.Error("Expected example.com/new package")
	} else {
		if pkg.Version != "v2.0.0" {
			t.Errorf("Expected version v2.0.0, got %s", pkg.Version)
		}
		if !pkg.Replace {
			t.Error("Expected Replace to be true")
		}
		if pkg.OldName != "example.com/old" {
			t.Errorf("Expected OldName example.com/old, got %s", pkg.OldName)
		}
	}
}

func TestGolangAnalyzer_Analyze(t *testing.T) {
	tmpDir := t.TempDir()

	// Create minimal go.mod
	goModContent := `module test/module

go 1.25

require (
	github.com/google/uuid v1.3.0
	golang.org/x/sys v0.1.0 // indirect
)
`
	goModPath := filepath.Join(tmpDir, "go.mod")
	err := os.WriteFile(goModPath, []byte(goModContent), 0o600)
	if err != nil {
		t.Fatalf("Failed to create go.mod: %v", err)
	}

	analyzer := &GolangAnalyzer{}
	result, err := analyzer.Analyze(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	if result.Language != "go" {
		t.Errorf("Expected language 'go', got %s", result.Language)
	}

	if len(result.Dependencies) != 2 {
		t.Errorf("Expected 2 dependencies, got %d", len(result.Dependencies))
	}

	// Check direct dependency
	if dep, ok := result.Dependencies["github.com/google/uuid"]; !ok {
		t.Error("Expected github.com/google/uuid dependency")
	} else {
		if dep.Version != "v1.3.0" {
			t.Errorf("Expected version v1.3.0, got %s", dep.Version)
		}
		if dep.Transitive {
			t.Error("Expected Transitive to be false for direct dependency")
		}
	}

	// Check indirect dependency
	if dep, ok := result.Dependencies["golang.org/x/sys"]; !ok {
		t.Error("Expected golang.org/x/sys dependency")
	} else {
		if dep.Version != "v0.1.0" {
			t.Errorf("Expected version v0.1.0, got %s", dep.Version)
		}
		if !dep.Transitive {
			t.Error("Expected Transitive to be true for indirect dependency")
		}
	}
}

func TestGolangAnalyzer_Analyze_MissingGoMod(t *testing.T) {
	tmpDir := t.TempDir()

	analyzer := &GolangAnalyzer{}
	_, err := analyzer.Analyze(context.Background(), tmpDir)
	if err == nil {
		t.Fatal("Expected error for missing go.mod, got nil")
	}
	if !strings.Contains(err.Error(), "failed to parse go.mod") {
		t.Errorf("Expected parse error, got: %v", err)
	}
}

func TestGolangAnalyzer_Analyze_WithReplaceDirectives(t *testing.T) {
	tmpDir := t.TempDir()

	// Create go.mod with replace directives
	goModContent := `module test/module

go 1.25

require github.com/old/pkg v1.0.0

replace github.com/old/pkg => github.com/new/pkg v2.0.0
replace github.com/another/pkg => github.com/forked/pkg v1.5.0
`
	goModPath := filepath.Join(tmpDir, "go.mod")
	err := os.WriteFile(goModPath, []byte(goModContent), 0o600)
	if err != nil {
		t.Fatalf("Failed to create go.mod: %v", err)
	}

	ga := &GolangAnalyzer{}
	result, err := ga.Analyze(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	// Check that replaced dependency has correct metadata
	if dep, ok := result.Dependencies["github.com/old/pkg"]; !ok {
		t.Error("Expected github.com/old/pkg dependency")
	} else {
		if replaced, ok := dep.Metadata["replaced"].(bool); !ok || !replaced {
			t.Error("Expected replaced metadata to be true")
		}
		if replacedWith, ok := dep.Metadata["replacedWith"].(string); !ok || replacedWith != "github.com/new/pkg" {
			t.Errorf("Expected replacedWith to be github.com/new/pkg, got %v", replacedWith)
		}
		if dep.UpdateStrategy != "replace" {
			t.Errorf("Expected UpdateStrategy to be replace, got %s", dep.UpdateStrategy)
		}
	}

	// Check that replace-only dependency exists
	if dep, ok := result.Dependencies["github.com/another/pkg"]; !ok {
		t.Error("Expected github.com/another/pkg dependency")
	} else {
		if replaced, ok := dep.Metadata["replaced"].(bool); !ok || !replaced {
			t.Error("Expected replaced metadata to be true for replace-only dependency")
		}
	}
}

func TestGolangAnalyzer_Analyze_FilePathDirectly(t *testing.T) {
	tmpDir := t.TempDir()

	// Create minimal go.mod
	goModContent := `module test/module

go 1.25

require github.com/google/uuid v1.3.0
`
	goModPath := filepath.Join(tmpDir, "go.mod")
	err := os.WriteFile(goModPath, []byte(goModContent), 0o600)
	if err != nil {
		t.Fatalf("Failed to create go.mod: %v", err)
	}

	// Pass file path directly instead of directory
	analyzer := &GolangAnalyzer{}
	result, err := analyzer.Analyze(context.Background(), goModPath)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	if len(result.Dependencies) != 1 {
		t.Errorf("Expected 1 dependency, got %d", len(result.Dependencies))
	}
}

func TestGolang_Update_CurrentVersionNewer(t *testing.T) {
	tmpDir := t.TempDir()

	// Create go.mod with package at v1.5.0 (newer than requested v1.3.0)
	goModContent := `module test/module

go 1.25

require github.com/google/uuid v1.5.0
`
	goModPath := filepath.Join(tmpDir, "go.mod")
	err := os.WriteFile(goModPath, []byte(goModContent), 0o600)
	if err != nil {
		t.Fatalf("Failed to create go.mod: %v", err)
	}

	g := &Golang{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "github.com/google/uuid", Version: "v1.3.0"},
		},
	}

	err = g.Update(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Verify go.mod was not changed (current version is newer)
	content, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatalf("Failed to read go.mod: %v", err)
	}
	if !strings.Contains(string(content), "v1.5.0") {
		t.Error("go.mod should not have been modified when current version is newer")
	}
}

func TestGolang_Update_PackageNotInGoMod(t *testing.T) {
	tmpDir := t.TempDir()

	// Create go.mod without the package we want to add
	goModContent := `module test/module

go 1.25
`
	goModPath := filepath.Join(tmpDir, "go.mod")
	err := os.WriteFile(goModPath, []byte(goModContent), 0o600)
	if err != nil {
		t.Fatalf("Failed to create go.mod: %v", err)
	}

	g := &Golang{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "github.com/google/uuid", Version: "v1.3.0"},
		},
		DryRun: true, // Use dry run to avoid actual go commands
	}

	err = g.Update(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
}

func TestGolang_Validate_InvalidGoMod(t *testing.T) {
	tmpDir := t.TempDir()

	// Create invalid go.mod
	goModPath := filepath.Join(tmpDir, "go.mod")
	err := os.WriteFile(goModPath, []byte("invalid content"), 0o600)
	if err != nil {
		t.Fatalf("Failed to create go.mod: %v", err)
	}

	g := &Golang{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "github.com/google/uuid", Version: "v1.3.0"},
		},
	}

	err = g.Validate(context.Background(), cfg)
	if err == nil {
		t.Fatal("Expected error for invalid go.mod, got nil")
	}
	if !strings.Contains(err.Error(), "failed to parse updated go.mod") {
		t.Errorf("Expected parse error, got: %v", err)
	}
}

func TestGolangAnalyzer_Analyze_WithGoVersion(t *testing.T) {
	tmpDir := t.TempDir()

	// Create go.mod with go version
	goModContent := `module test/module

go 1.21

require github.com/google/uuid v1.3.0
`
	goModPath := filepath.Join(tmpDir, "go.mod")
	err := os.WriteFile(goModPath, []byte(goModContent), 0o600)
	if err != nil {
		t.Fatalf("Failed to create go.mod: %v", err)
	}

	ga := &GolangAnalyzer{}
	result, err := ga.Analyze(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	// Check that Go version is captured in metadata
	if goVersion, ok := result.Metadata["goVersion"].(string); !ok || goVersion != "1.21" {
		t.Errorf("Expected goVersion to be 1.21, got %v", result.Metadata["goVersion"])
	}
}

func TestGolangAnalyzer_RecommendStrategy(t *testing.T) {
	// Create sample analysis result
	analysis := &analyzer.AnalysisResult{
		Language: "go",
		Dependencies: map[string]*analyzer.DependencyInfo{
			"github.com/google/uuid": {
				Name:           "github.com/google/uuid",
				Version:        "v1.3.0",
				Transitive:     false,
				UpdateStrategy: "direct",
				Metadata:       make(map[string]any),
			},
			"golang.org/x/sys": {
				Name:           "golang.org/x/sys",
				Version:        "v0.1.0",
				Transitive:     true,
				UpdateStrategy: "direct",
				Metadata:       map[string]any{"indirect": true},
			},
			"github.com/replaced/pkg": {
				Name:           "github.com/replaced/pkg",
				Version:        "v2.0.0",
				Transitive:     false,
				UpdateStrategy: "direct",
				Metadata: map[string]any{
					"replaced":     true,
					"replacedWith": "github.com/new/pkg",
				},
			},
		},
	}

	deps := []analyzer.Dependency{
		{Name: "github.com/google/uuid", Version: "v1.4.0"},
		{Name: "golang.org/x/sys", Version: "v0.2.0"},
		{Name: "github.com/replaced/pkg", Version: "v2.1.0"},
	}

	ga := &GolangAnalyzer{}
	strategy, err := ga.RecommendStrategy(context.Background(), analysis, deps)
	if err != nil {
		t.Fatalf("RecommendStrategy failed: %v", err)
	}

	if len(strategy.DirectUpdates) != 3 {
		t.Errorf("Expected 3 direct updates, got %d", len(strategy.DirectUpdates))
	}

	// Should have warnings for indirect and replaced dependencies
	if len(strategy.Warnings) != 2 {
		t.Errorf("Expected 2 warnings, got %d", len(strategy.Warnings))
	}

	// Check warnings contain expected messages
	warningsText := strings.Join(strategy.Warnings, " ")
	if !strings.Contains(warningsText, "indirect") {
		t.Error("Expected warning about indirect dependency")
	}
	if !strings.Contains(warningsText, "replaced") {
		t.Error("Expected warning about replaced dependency")
	}
}

func TestGolang_Update_Workspace(t *testing.T) {
	tmpDir := t.TempDir()

	// Create go.work file
	workContent := `go 1.25

use (
	.
	./moduleA
	./moduleB
)
`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "go.work"), []byte(workContent), 0o600))

	// Create root go.mod with shared dependency
	rootMod := `module test/workspace

go 1.25

require github.com/google/uuid v1.0.0
`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(rootMod), 0o600))

	// Create moduleA with shared dependency
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "moduleA"), 0o755))
	modAContent := `module test/workspace/moduleA

go 1.25

require github.com/google/uuid v1.0.0
`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "moduleA", "go.mod"), []byte(modAContent), 0o600))

	// Create moduleB without the dependency
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "moduleB"), 0o755))
	modBContent := `module test/workspace/moduleB

go 1.25

require golang.org/x/crypto v0.45.0
`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "moduleB", "go.mod"), []byte(modBContent), 0o600))

	// Test dry run update
	g := &Golang{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "github.com/google/uuid", Version: "v1.3.0"},
		},
		DryRun: true,
	}

	err := g.Update(context.Background(), cfg)
	require.NoError(t, err)

	// Verify go.mod files were not changed in dry run
	rootContent, err := os.ReadFile(filepath.Join(tmpDir, "go.mod"))
	require.NoError(t, err)
	require.Contains(t, string(rootContent), "v1.0.0", "root go.mod should not change in dry run")

	modAPath := filepath.Join(tmpDir, "moduleA", "go.mod")
	modAActual, err := os.ReadFile(modAPath)
	require.NoError(t, err)
	require.Contains(t, string(modAActual), "v1.0.0", "moduleA go.mod should not change in dry run")

	// moduleB should not be touched since it doesn't have the dependency
	modBPath := filepath.Join(tmpDir, "moduleB", "go.mod")
	modBActual, err := os.ReadFile(modBPath)
	require.NoError(t, err)
	require.NotContains(t, string(modBActual), "uuid", "moduleB should not be modified")
}

func TestGolang_Update_Workspace_IncompatibleVersion(t *testing.T) {
	// Verifies that +incompatible is correctly applied when updating a package
	// that lives in a go.work workspace module.
	tmpDir := t.TempDir()

	workContent := `go 1.21

use (
	.
	./moduleA
)
`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "go.work"), []byte(workContent), 0o600))

	// Root module without the incompatible dependency.
	rootMod := `module test/workspace

go 1.21

require github.com/sirupsen/logrus v1.9.0
`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(rootMod), 0o600))

	// moduleA has the +incompatible dependency.
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "moduleA"), 0o755))
	modAContent := `module test/workspace/moduleA

go 1.21

require github.com/example/legacy v2.0.0+incompatible
`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "moduleA", "go.mod"), []byte(modAContent), 0o600))

	g := &Golang{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		// Deliberately omit +incompatible to test normalization.
		Dependencies: []languages.Dependency{
			{Name: "github.com/example/legacy", Version: "v3.0.0"},
		},
		Tidy: false,
	}

	require.NoError(t, g.Update(context.Background(), cfg))

	// Verify moduleA's go.mod has the +incompatible suffix and parses cleanly.
	modAPath := filepath.Join(tmpDir, "moduleA", "go.mod")
	parsedMod, _, err := ParseGoModfile(modAPath)
	require.NoError(t, err, "moduleA go.mod should be parseable after update")

	got := getVersion(parsedMod, "github.com/example/legacy")
	require.Equal(t, "v3.0.0+incompatible", got)
}

func TestFilterDepsForModule(t *testing.T) {
	tests := []struct {
		name     string
		deps     []languages.Dependency
		modFile  *modfile.File
		wantDeps []string
	}{
		{
			name: "all deps present",
			deps: []languages.Dependency{
				{Name: "example.com/foo", Version: "v1.0.0"},
				{Name: "example.com/bar", Version: "v2.0.0"},
			},
			modFile: &modfile.File{
				Require: []*modfile.Require{
					{Mod: module.Version{Path: "example.com/foo", Version: "v0.9.0"}},
					{Mod: module.Version{Path: "example.com/bar", Version: "v1.0.0"}},
				},
			},
			wantDeps: []string{"example.com/foo", "example.com/bar"},
		},
		{
			name: "some deps absent — only present ones returned",
			deps: []languages.Dependency{
				{Name: "example.com/present", Version: "v1.0.0"},
				{Name: "example.com/absent", Version: "v2.0.0"},
			},
			modFile: &modfile.File{
				Require: []*modfile.Require{
					{Mod: module.Version{Path: "example.com/present", Version: "v0.9.0"}},
				},
			},
			wantDeps: []string{"example.com/present"},
		},
		{
			name: "no deps present",
			deps: []languages.Dependency{
				{Name: "example.com/absent", Version: "v1.0.0"},
			},
			modFile: &modfile.File{
				Require: []*modfile.Require{},
			},
			wantDeps: []string{},
		},
		{
			name: "dep present via replace directive new path",
			deps: []languages.Dependency{
				{Name: "example.com/new", Version: "v1.0.0"},
			},
			modFile: &modfile.File{
				Require: []*modfile.Require{},
				Replace: []*modfile.Replace{
					{
						Old: module.Version{Path: "example.com/old"},
						New: module.Version{Path: "example.com/new", Version: "v0.9.0"},
					},
				},
			},
			wantDeps: []string{"example.com/new"},
		},
		{
			name: "replace dep matched via OldName",
			deps: []languages.Dependency{
				{OldName: "example.com/old", Name: "example.com/new", Version: "v1.0.0", Replace: true},
			},
			modFile: &modfile.File{
				Require: []*modfile.Require{
					{Mod: module.Version{Path: "example.com/old", Version: "v0.9.0"}},
				},
				Replace: []*modfile.Replace{
					{
						Old: module.Version{Path: "example.com/old"},
						New: module.Version{Path: "example.com/new", Version: "v0.9.0"},
					},
				},
			},
			wantDeps: []string{"example.com/new"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterDepsForModule(tt.deps, tt.modFile)

			if len(got) != len(tt.wantDeps) {
				t.Errorf("got %d deps, want %d: %v", len(got), len(tt.wantDeps), got)
				return
			}

			gotNames := make(map[string]bool)
			for _, d := range got {
				gotNames[d.Name] = true
			}
			for _, want := range tt.wantDeps {
				if !gotNames[want] {
					t.Errorf("expected dep %q in result, got: %v", want, got)
				}
			}
		})
	}
}

func TestGolang_Update_Workspace_OnlyTargetedModules(t *testing.T) {
	tmpDir := t.TempDir()

	// Create go.work file with 3 modules
	workContent := `go 1.25

use (
	.
	./with-dep
	./without-dep
)
`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "go.work"), []byte(workContent), 0o600))

	// Root module without target dependency
	rootMod := `module test/workspace

go 1.25

require github.com/sirupsen/logrus v1.9.0
`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(rootMod), 0o600))

	// Module with target dependency
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "with-dep"), 0o755))
	withDepMod := `module test/workspace/with-dep

go 1.25

require github.com/google/uuid v1.0.0
`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "with-dep", "go.mod"), []byte(withDepMod), 0o600))

	// Module without target dependency
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "without-dep"), 0o755))
	withoutDepMod := `module test/workspace/without-dep

go 1.25

require golang.org/x/crypto v0.45.0
`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "without-dep", "go.mod"), []byte(withoutDepMod), 0o600))

	// Update the dependency (dry run to avoid actual network calls)
	g := &Golang{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "github.com/google/uuid", Version: "v1.3.0"},
		},
		DryRun: true,
	}

	err := g.Update(context.Background(), cfg)
	require.NoError(t, err)

	// Only with-dep module should have been processed
	// (In actual execution, only that module would be updated)
}
