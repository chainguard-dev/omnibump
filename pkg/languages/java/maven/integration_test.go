/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package maven

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/chainguard-dev/omnibump/pkg/analyzer"
	"github.com/chainguard-dev/omnibump/pkg/languages"
)

func TestMavenUpdate(t *testing.T) {
	testCases := []struct {
		name            string
		initialPom      string
		dependencies    []languages.Dependency
		properties      map[string]string
		dryRun          bool
		wantDeps        map[string]string // groupId:artifactId -> version
		wantProps       map[string]string
		wantUpdateErr   bool
		wantValidateErr bool
	}{
		{
			name: "update single dependency",
			initialPom: `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>test-project</artifactId>
  <version>1.0.0</version>
  <dependencies>
    <dependency>
      <groupId>io.netty</groupId>
      <artifactId>netty-codec-http</artifactId>
      <version>4.1.90.Final</version>
    </dependency>
  </dependencies>
</project>`,
			dependencies: []languages.Dependency{
				{
					Name:    "io.netty:netty-codec-http",
					Version: "4.1.94.Final",
					Metadata: map[string]any{
						"groupId":    "io.netty",
						"artifactId": "netty-codec-http",
					},
				},
			},
			wantDeps: map[string]string{
				"io.netty:netty-codec-http": "4.1.94.Final",
			},
		},
		{
			name: "update property",
			initialPom: `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>test-project</artifactId>
  <version>1.0.0</version>
  <properties>
    <netty.version>4.1.90.Final</netty.version>
  </properties>
  <dependencies>
    <dependency>
      <groupId>io.netty</groupId>
      <artifactId>netty-codec-http</artifactId>
      <version>${netty.version}</version>
    </dependency>
  </dependencies>
</project>`,
			properties: map[string]string{
				"netty.version": "4.1.94.Final",
			},
			wantProps: map[string]string{
				"netty.version": "4.1.94.Final",
			},
		},
		{
			name: "add new dependency to dependency management",
			initialPom: `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>test-project</artifactId>
  <version>1.0.0</version>
  <dependencyManagement>
    <dependencies>
      <dependency>
        <groupId>io.netty</groupId>
        <artifactId>netty-codec-http</artifactId>
        <version>4.1.90.Final</version>
      </dependency>
    </dependencies>
  </dependencyManagement>
</project>`,
			dependencies: []languages.Dependency{
				{
					Name:    "com.google.guava:guava",
					Version: "32.0.0-jre",
					Scope:   "import",
					Type:    "jar",
					Metadata: map[string]any{
						"groupId":    "com.google.guava",
						"artifactId": "guava",
					},
				},
			},
			wantDeps: map[string]string{
				"com.google.guava:guava": "32.0.0-jre",
			},
		},
		{
			name: "dry run mode",
			initialPom: `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>test-project</artifactId>
  <version>1.0.0</version>
  <dependencies>
    <dependency>
      <groupId>io.netty</groupId>
      <artifactId>netty-codec-http</artifactId>
      <version>4.1.90.Final</version>
    </dependency>
  </dependencies>
</project>`,
			dependencies: []languages.Dependency{
				{
					Name:    "io.netty:netty-codec-http",
					Version: "4.1.94.Final",
					Metadata: map[string]any{
						"groupId":    "io.netty",
						"artifactId": "netty-codec-http",
					},
				},
			},
			dryRun: true,
			// In dry run, file shouldn't be updated, so version should remain old
			wantDeps: map[string]string{
				"io.netty:netty-codec-http": "4.1.90.Final",
			},
			// Note: Validate only warns for missing deps, doesn't error
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create temp directory
			tmpDir := t.TempDir()
			pomPath := filepath.Join(tmpDir, "pom.xml")

			// Write initial POM
			if err := os.WriteFile(pomPath, []byte(tc.initialPom), 0o600); err != nil {
				t.Fatalf("Failed to write test pom.xml: %v", err)
			}

			// Create Maven instance
			maven := &Maven{}

			// Prepare update config
			cfg := &languages.UpdateConfig{
				RootDir:      tmpDir,
				Dependencies: tc.dependencies,
				Properties:   tc.properties,
				DryRun:       tc.dryRun,
			}

			// Run update
			err := maven.Update(context.Background(), cfg)
			if (err != nil) != tc.wantUpdateErr {
				t.Errorf("Update() error = %v, wantErr %v", err, tc.wantUpdateErr)
				return
			}

			if tc.wantUpdateErr {
				return
			}

			// Parse updated POM to verify
			project, err := ParsePom(pomPath)
			if err != nil {
				t.Fatalf("Failed to parse updated POM: %v", err)
			}

			// Verify dependencies
			for key, wantVersion := range tc.wantDeps {
				found := false
				// Check in dependencies
				if project.Dependencies != nil {
					for _, dep := range *project.Dependencies {
						depKey := dep.GroupID + ":" + dep.ArtifactID
						if depKey == key {
							if dep.Version != wantVersion {
								t.Errorf("Dependency %s version = %s, want %s", key, dep.Version, wantVersion)
							}
							found = true
							break
						}
					}
				}
				// Check in dependency management
				if !found && project.DependencyManagement != nil && project.DependencyManagement.Dependencies != nil {
					for _, dep := range *project.DependencyManagement.Dependencies {
						depKey := dep.GroupID + ":" + dep.ArtifactID
						if depKey == key {
							if dep.Version != wantVersion {
								t.Errorf("DependencyManagement %s version = %s, want %s", key, dep.Version, wantVersion)
							}
							found = true
							break
						}
					}
				}
				if !found && !tc.dryRun {
					t.Errorf("Dependency %s not found in POM", key)
				}
			}

			// Verify properties
			for key, wantValue := range tc.wantProps {
				if project.Properties == nil {
					t.Errorf("Properties is nil, expected property %s", key)
					continue
				}
				if actualValue, exists := project.Properties.Entries[key]; !exists {
					t.Errorf("Property %s not found", key)
				} else if actualValue != wantValue {
					t.Errorf("Property %s = %s, want %s", key, actualValue, wantValue)
				}
			}

			// Run validation
			err = maven.Validate(context.Background(), cfg)
			if (err != nil) != tc.wantValidateErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tc.wantValidateErr)
			}
		})
	}
}

func TestMavenDetect(t *testing.T) {
	testCases := []struct {
		name      string
		setupFunc func(string) error
		want      bool
	}{
		{
			name: "pom.xml exists",
			setupFunc: func(dir string) error {
				pomContent := `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>test</artifactId>
  <version>1.0.0</version>
</project>`
				return os.WriteFile(filepath.Join(dir, "pom.xml"), []byte(pomContent), 0o600)
			},
			want: true,
		},
		{
			name: "no pom.xml",
			setupFunc: func(_ string) error { // dir not needed for empty test case
				return nil
			},
			want: false,
		},
		{
			name: "only go.mod exists",
			setupFunc: func(dir string) error {
				return os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0o600)
			},
			want: false,
		},
		{
			name: "no root pom.xml but POM in subdirectory",
			setupFunc: func(dir string) error {
				sub := filepath.Join(dir, "submodule")
				if err := os.MkdirAll(sub, 0o755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(sub, "pom.xml"), []byte(minimalPOM), 0o600)
			},
			want: true,
		},
		{
			name: "no root pom.xml but POM in dot-prefixed source dir (.build)",
			setupFunc: func(dir string) error {
				build := filepath.Join(dir, ".build")
				if err := os.MkdirAll(build, 0o755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(build, "parent-pom-template.xml"), []byte(minimalPOM), 0o600)
			},
			want: true,
		},
		{
			name: "POM only inside skipped VCS dir (.git) is not detected",
			setupFunc: func(dir string) error {
				git := filepath.Join(dir, ".git")
				if err := os.MkdirAll(git, 0o755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(git, "pom.xml"), []byte(minimalPOM), 0o600)
			},
			want: false,
		},
		{
			name: "only non-Maven XML files present",
			setupFunc: func(dir string) error {
				return os.WriteFile(filepath.Join(dir, "config.xml"), []byte(`<?xml version="1.0"?><configuration/>`), 0o600)
			},
			want: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			if err := tc.setupFunc(tmpDir); err != nil {
				t.Fatalf("Setup failed: %v", err)
			}

			maven := &Maven{}
			got, err := maven.Detect(context.Background(), tmpDir)
			if err != nil {
				t.Fatalf("Detect() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("Detect() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestMavenDetect_SkippedDirectories verifies that a valid Maven POM buried inside any
// skippable directory does not cause the project to be detected as Maven.
// This is a regression test for the Beats false-positive: the repo contains a test-fixture
// POM at metricbeat/module/dropwizard/_meta/test/pom.xml which previously caused incorrect
// Java detection when hasMavenPom() walked the full tree without skipping "test" directories.
func TestMavenDetect_SkippedDirectories(t *testing.T) {
	skippable := []string{
		".git", ".svn", ".hg", ".bzr",
		"target", "node_modules",
		"build", "dist", "out",
		"testdata", "vendor", "test",
	}

	for _, dirName := range skippable {
		t.Run(dirName, func(t *testing.T) {
			tmpDir := t.TempDir()
			// Place a fully valid pom.xml (Maven namespace + all required fields) inside
			// the skipped directory. This mirrors the real Beats scenario where
			// metricbeat/module/dropwizard/_meta/test/pom.xml is a legitimate Maven POM
			// that should be invisible to the project-level detector.
			// The file would be detected as Maven if the directory were not skipped —
			// confirmed by TestMavenDetect/"no root pom.xml but POM in subdirectory".
			writeFile(t, filepath.Join(tmpDir, dirName, "pom.xml"), minimalPOM)

			maven := &Maven{}
			got, err := maven.Detect(context.Background(), tmpDir)
			if err != nil {
				t.Fatalf("Detect() error = %v", err)
			}
			if got {
				t.Errorf("Detect() = true, want false — valid pom.xml inside skipped dir %q must not trigger detection", dirName)
			}
		})
	}
}

// TestMavenDetect_RootDirNameInSkipList is a regression test for the bug where Detect()
// returned false for a valid Maven project whose root directory had a name that appears in
// the skip list (e.g. a Flink checkout at /home/build/).
func TestMavenDetect_RootDirNameInSkipList(t *testing.T) {
	for _, rootName := range []string{"build", "target", "dist", "out"} {
		t.Run(rootName, func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, rootName)
			writeFile(t, filepath.Join(root, "pom.xml"), minimalPOM)

			maven := &Maven{}
			got, err := maven.Detect(context.Background(), root)
			if err != nil {
				t.Fatalf("Detect() error = %v", err)
			}
			if !got {
				t.Errorf("Detect() = false, want true — project root named %q must be detected as Maven", rootName)
			}
		})
	}
}

// TestMavenAnalyzer_Analyze_RootDirNameInSkipList is a regression test for the bug where
// Analyze() returned "no Maven POM files found" when the project root directory's name
// matched an entry in the skip list (e.g. a Flink checkout at /home/build/).
func TestMavenAnalyzer_Analyze_RootDirNameInSkipList(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "build")
	writeFile(t, filepath.Join(root, "pom.xml"), pomWithDep("io.netty", "netty-all", "4.1.130.Final"))

	ma := &MavenAnalyzer{}
	result, err := ma.Analyze(context.Background(), root)
	if err != nil {
		t.Fatalf("Analyze() error = %v — root dir named 'build' must not be skipped", err)
	}
	if _, ok := result.Dependencies["io.netty:netty-all"]; !ok {
		t.Error("expected dependency io.netty:netty-all not found")
	}
}

// TestMavenAnalyzer_AnalyzeAllPoms covers analysis of projects that have no root pom.xml.
func TestMavenAnalyzer_AnalyzeAllPoms(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(t *testing.T, dir string)
		wantErr   bool
		checkFunc func(t *testing.T, result *analyzer.AnalysisResult)
	}{
		{
			name: "no POMs anywhere returns error",
			setup: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "build.xml"), `<?xml version="1.0"?><project/>`)
			},
			wantErr: true,
		},
		{
			name: "single POM in subdirectory is found and analyzed",
			setup: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, ".build", "parent.xml"),
					pomWithDep("io.netty", "netty-all", "4.1.130.Final"))
			},
			checkFunc: func(t *testing.T, result *analyzer.AnalysisResult) {
				t.Helper()
				dep, ok := result.Dependencies["io.netty:netty-all"]
				if !ok {
					t.Fatal("expected dependency io.netty:netty-all not found")
				}
				if dep.Version != "4.1.130.Final" {
					t.Errorf("netty-all version = %q, want 4.1.130.Final", dep.Version)
				}
			},
		},
		{
			name: "dependencies from multiple POMs are aggregated",
			setup: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, ".build", "parent.xml"),
					pomWithDep("io.netty", "netty-all", "4.1.130.Final"))
				writeFile(t, filepath.Join(dir, "module-a", "pom.xml"),
					pomWithDep("com.google.guava", "guava", "32.0.0-jre"))
			},
			checkFunc: func(t *testing.T, result *analyzer.AnalysisResult) {
				t.Helper()
				if _, ok := result.Dependencies["io.netty:netty-all"]; !ok {
					t.Error("missing io.netty:netty-all")
				}
				if _, ok := result.Dependencies["com.google.guava:guava"]; !ok {
					t.Error("missing com.google.guava:guava")
				}
			},
		},
		{
			name: "properties from multiple POMs are aggregated, first definition wins",
			setup: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "module-a", "pom.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>a</artifactId>
  <version>1.0.0</version>
  <properties>
    <netty.version>4.1.100.Final</netty.version>
    <shared.prop>from-a</shared.prop>
  </properties>
</project>`)
				writeFile(t, filepath.Join(dir, "module-b", "pom.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>b</artifactId>
  <version>1.0.0</version>
  <properties>
    <guava.version>32.0.0-jre</guava.version>
    <shared.prop>from-b</shared.prop>
  </properties>
</project>`)
			},
			checkFunc: func(t *testing.T, result *analyzer.AnalysisResult) {
				t.Helper()
				if result.Properties["netty.version"] != "4.1.100.Final" {
					t.Errorf("netty.version = %q, want 4.1.100.Final", result.Properties["netty.version"])
				}
				if result.Properties["guava.version"] != "32.0.0-jre" {
					t.Errorf("guava.version = %q, want 32.0.0-jre", result.Properties["guava.version"])
				}
				// First definition wins — whichever module is walked first sets shared.prop
				if v := result.Properties["shared.prop"]; v != "from-a" && v != "from-b" {
					t.Errorf("shared.prop = %q, want one of [from-a, from-b]", v)
				}
			},
		},
		{
			name: "POMs inside skipped dirs (target, .git) are excluded",
			setup: func(t *testing.T, dir string) {
				// The only real POM lives in a proper subdir
				writeFile(t, filepath.Join(dir, ".build", "parent.xml"),
					pomWithDep("io.netty", "netty-all", "4.1.130.Final"))
				// These must not contribute
				writeFile(t, filepath.Join(dir, "target", "pom.xml"),
					pomWithDep("should", "not-appear", "9.9.9"))
				writeFile(t, filepath.Join(dir, ".git", "pom.xml"),
					pomWithDep("also", "not-appear", "9.9.9"))
			},
			checkFunc: func(t *testing.T, result *analyzer.AnalysisResult) {
				t.Helper()
				if _, ok := result.Dependencies["should:not-appear"]; ok {
					t.Error("dependency from target/ dir must not be included")
				}
				if _, ok := result.Dependencies["also:not-appear"]; ok {
					t.Error("dependency from .git/ dir must not be included")
				}
				if _, ok := result.Dependencies["io.netty:netty-all"]; !ok {
					t.Error("expected io.netty:netty-all from .build/")
				}
			},
		},
		{
			name: "result language is maven",
			setup: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, ".build", "parent.xml"), minimalPOM)
			},
			checkFunc: func(t *testing.T, result *analyzer.AnalysisResult) {
				t.Helper()
				if result.Language != "maven" {
					t.Errorf("Language = %q, want maven", result.Language)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.setup(t, dir)

			ma := &MavenAnalyzer{}
			result, err := ma.Analyze(context.Background(), dir)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Analyze() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if tt.checkFunc != nil {
				tt.checkFunc(t, result)
			}
		})
	}
}

func TestMavenGetManifestFiles(t *testing.T) {
	tmpDir := t.TempDir()
	pomPath := filepath.Join(tmpDir, "pom.xml")
	pomContent := `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>test</artifactId>
  <version>1.0.0</version>
</project>`

	if err := os.WriteFile(pomPath, []byte(pomContent), 0o600); err != nil {
		t.Fatalf("Failed to create pom.xml: %v", err)
	}

	maven := &Maven{}
	files := maven.GetManifestFiles()

	if len(files) != 1 {
		t.Fatalf("Expected 1 manifest file, got %d", len(files))
	}

	if files[0] != "pom.xml" {
		t.Errorf("Expected pom.xml, got %s", files[0])
	}
}

func TestMavenSupportsAnalysis(t *testing.T) {
	maven := &Maven{}
	analyzer := maven.GetAnalyzer()
	if analyzer == nil {
		t.Error("Maven should support analysis (GetAnalyzer should not return nil)")
	}
}

func TestMavenName(t *testing.T) {
	maven := &Maven{}
	if maven.Name() != "maven" {
		t.Errorf("Name() = %s, want maven", maven.Name())
	}
}

func TestConvertDependenciesToPatches(t *testing.T) {
	testCases := []struct {
		name string
		deps []languages.Dependency
		want []Patch
	}{
		{
			name: "single dependency with metadata",
			deps: []languages.Dependency{
				{
					Name:    "io.netty:netty-codec-http",
					Version: "4.1.94.Final",
					Scope:   "compile",
					Type:    "jar",
					Metadata: map[string]any{
						"groupId":    "io.netty",
						"artifactId": "netty-codec-http",
					},
				},
			},
			want: []Patch{
				{
					GroupID:    "io.netty",
					ArtifactID: "netty-codec-http",
					Version:    "4.1.94.Final",
					Scope:      "compile",
					Type:       "jar",
				},
			},
		},
		{
			name: "dependency with Name format",
			deps: []languages.Dependency{
				{
					Name:    "io.netty:netty-codec-http",
					Version: "4.1.94.Final",
				},
			},
			want: []Patch{
				{
					GroupID:    "io.netty",
					ArtifactID: "netty-codec-http",
					Version:    "4.1.94.Final",
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := convertDependenciesToPatches(tc.deps)
			if err != nil {
				t.Fatalf("convertDependenciesToPatches() error = %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d patches, want %d", len(got), len(tc.want))
			}
			for i, patch := range got {
				want := tc.want[i]
				if patch.GroupID != want.GroupID {
					t.Errorf("patch[%d].GroupID = %s, want %s", i, patch.GroupID, want.GroupID)
				}
				if patch.ArtifactID != want.ArtifactID {
					t.Errorf("patch[%d].ArtifactID = %s, want %s", i, patch.ArtifactID, want.ArtifactID)
				}
				if patch.Version != want.Version {
					t.Errorf("patch[%d].Version = %s, want %s", i, patch.Version, want.Version)
				}
			}
		})
	}
}

// pomWithParentAndProp returns a parent POM that declares netty.version.
func pomWithParentAndProp() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>parent</artifactId>
  <version>1.0.0</version>
  <packaging>pom</packaging>
  <properties>
    <netty.version>4.1.100.Final</netty.version>
  </properties>
</project>`
}

// pomWithRelativeParent returns a POM that points at a parent via <relativePath>.
func pomWithRelativeParent(relativePath string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>com.example</groupId>
    <artifactId>parent</artifactId>
    <version>1.0.0</version>
    <relativePath>` + relativePath + `</relativePath>
  </parent>
  <groupId>com.example</groupId>
  <artifactId>child</artifactId>
  <version>1.0.0</version>
</project>`
}

// TestAnalyze_PropertySources_SinglePom checks that properties defined in the
// analysed pom.xml itself are attributed to that file.
func TestAnalyze_PropertySources_SinglePom(t *testing.T) {
	dir := t.TempDir()
	pomContent := `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>test</artifactId>
  <version>1.0.0</version>
  <properties>
    <netty.version>4.1.100.Final</netty.version>
  </properties>
</project>`
	pomPath := filepath.Join(dir, "pom.xml")
	writeFile(t, pomPath, pomContent)

	ma := &MavenAnalyzer{}
	result, err := ma.Analyze(t.Context(), pomPath)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	if src := result.PropertySources["netty.version"]; src != "pom.xml" {
		t.Errorf("PropertySources[netty.version] = %q, want %q", src, "pom.xml")
	}
}

// TestAnalyze_PropertySources_DirectoryAnalysis checks that analyzeAllPoms
// attributes each property to the POM file that declares it.
func TestAnalyze_PropertySources_DirectoryAnalysis(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pom.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId><artifactId>root</artifactId><version>1.0.0</version>
  <properties><root.prop>1.0</root.prop></properties>
</project>`)
	writeFile(t, filepath.Join(dir, "module-a", "pom.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId><artifactId>module-a</artifactId><version>1.0.0</version>
  <properties><module.prop>2.0</module.prop></properties>
</project>`)

	ma := &MavenAnalyzer{}
	result, err := ma.Analyze(t.Context(), dir)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	if src := result.PropertySources["root.prop"]; src != "pom.xml" {
		t.Errorf("PropertySources[root.prop] = %q, want %q", src, "pom.xml")
	}
	if src := result.PropertySources["module.prop"]; src != filepath.Join("module-a", "pom.xml") {
		t.Errorf("PropertySources[module.prop] = %q, want %q", src, filepath.Join("module-a", "pom.xml"))
	}
}

// TestAnalyze_PropertySources_ParentPom checks that a property declared in a
// parent POM referenced via <parent><relativePath> is found and attributed correctly.
// The parent must be within the analyzed directory (the project boundary).
func TestAnalyze_PropertySources_ParentPom(t *testing.T) {
	root := t.TempDir()
	// Project layout: root/pom.xml has a <parent> pointing at root/config/pom.xml.
	// Both are within root, so the boundary check passes.
	parentPom := filepath.Join(root, "config", "pom.xml")
	childPom := filepath.Join(root, "pom.xml")

	writeFile(t, parentPom, pomWithParentAndProp())
	writeFile(t, childPom, pomWithRelativeParent("config/pom.xml"))

	ma := &MavenAnalyzer{}
	result, err := ma.Analyze(t.Context(), childPom)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	if v := result.Properties["netty.version"]; v != "4.1.100.Final" {
		t.Errorf("Properties[netty.version] = %q, want 4.1.100.Final", v)
	}
	if src := result.PropertySources["netty.version"]; src != filepath.Join("config", "pom.xml") {
		t.Errorf("PropertySources[netty.version] = %q, want %q", src, filepath.Join("config", "pom.xml"))
	}
}

// TestAnalyze_PropertySources_ParentPom_Directory checks the same for
// directory-mode analysis (analyzeAllPoms path). Analyzing the project root
// allows finding properties in any POM within that root.
func TestAnalyze_PropertySources_ParentPom_Directory(t *testing.T) {
	root := t.TempDir()
	parentPom := filepath.Join(root, "pom.xml")
	childPom := filepath.Join(root, "lib", "pom.xml")

	writeFile(t, parentPom, pomWithParentAndProp())
	// Child POM references a dep via the property so PropertyUsage is populated.
	writeFile(t, childPom, `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>com.example</groupId>
    <artifactId>parent</artifactId>
    <version>1.0.0</version>
    <relativePath>../pom.xml</relativePath>
  </parent>
  <groupId>com.example</groupId>
  <artifactId>child</artifactId>
  <version>1.0.0</version>
  <dependencyManagement>
    <dependencies>
      <dependency>
        <groupId>io.netty</groupId>
        <artifactId>netty-all</artifactId>
        <version>${netty.version}</version>
      </dependency>
    </dependencies>
  </dependencyManagement>
</project>`)

	// Analyze the whole project root so root/pom.xml is within the boundary.
	ma := &MavenAnalyzer{}
	result, err := ma.Analyze(t.Context(), root)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	if v := result.Properties["netty.version"]; v != "4.1.100.Final" {
		t.Errorf("Properties[netty.version] = %q, want 4.1.100.Final", v)
	}
	if src := result.PropertySources["netty.version"]; src != "pom.xml" {
		t.Errorf("PropertySources[netty.version] = %q, want %q", src, "pom.xml")
	}
}

// TestMergeProperty verifies first-definition-wins and no double-assignment.
func TestMergeProperty(t *testing.T) {
	result := &analyzer.AnalysisResult{
		Properties:      make(map[string]string),
		PropertySources: make(map[string]string),
	}

	if !mergeProperty(t.Context(), result, "k", "v1", "a.xml") {
		t.Error("first merge should return true (newly added)")
	}
	if result.Properties["k"] != "v1" || result.PropertySources["k"] != "a.xml" {
		t.Errorf("property not set correctly after first merge")
	}

	// Same value — no warning, returns false.
	if mergeProperty(t.Context(), result, "k", "v1", "b.xml") {
		t.Error("merge of same value should return false (already present)")
	}
	if result.PropertySources["k"] != "a.xml" {
		t.Error("source should not change when property already present")
	}

	// Different value — conflict warning, still returns false, source unchanged.
	if mergeProperty(t.Context(), result, "k", "v2", "c.xml") {
		t.Error("conflicting merge should return false (already present)")
	}
	if result.Properties["k"] != "v1" {
		t.Error("conflicting value should not overwrite existing")
	}
}

// TestResolveUnknownProperties_ParentChain verifies that properties missing
// from the scanned files are found by following <parent><relativePath>.
func TestResolveUnknownProperties_ParentChain(t *testing.T) {
	root := t.TempDir()
	parentPom := filepath.Join(root, "pom.xml")
	childPom := filepath.Join(root, "lib", "pom.xml")

	writeFile(t, parentPom, pomWithParentAndProp())
	writeFile(t, childPom, pomWithRelativeParent("../pom.xml"))

	usage := map[string]int{"netty.version": 1, "already.found": 1}
	known := map[string]string{"already.found": "1.0"} // pre-filled, should be skipped

	results := resolveUnknownProperties(t.Context(), usage, known, childPom, root)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	pf := results[0]
	if v := pf.Properties["netty.version"]; v != "4.1.100.Final" {
		t.Errorf("Properties[netty.version] = %q, want 4.1.100.Final", v)
	}
	if pf.PomFile != "pom.xml" {
		t.Errorf("PomFile = %q, want %q", pf.PomFile, "../pom.xml")
	}
}

// TestResolveUnknownProperties_NotFound verifies that a property absent from
// the entire parent chain returns no results (not an error).
func TestResolveUnknownProperties_NotFound(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pom.xml"), minimalPOM)

	usage := map[string]int{"does.not.exist": 1}
	results := resolveUnknownProperties(t.Context(), usage, nil, filepath.Join(dir, "pom.xml"), dir)

	if len(results) != 0 {
		t.Errorf("expected 0 results for unknown property, got %d", len(results))
	}
}

// pomWithPropertyDep returns a POM whose dependencyManagement uses a property reference,
// ensuring PropertyUsage is populated during analysis.
func pomWithPropertyDep(groupID, artifactID, propName string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>child</artifactId>
  <version>1.0.0</version>
  <dependencyManagement>
    <dependencies>
      <dependency>
        <groupId>` + groupID + `</groupId>
        <artifactId>` + artifactID + `</artifactId>
        <version>${` + propName + `}</version>
      </dependency>
    </dependencies>
  </dependencyManagement>
</project>`
}

// TestAnalyze_DirFlag_FindsPropertiesInRoot tests the behaviour of the --dir flag:
// when the user passes --dir <projectRoot>, the analyzer uses that directory as both
// the project path and the traversal boundary, so properties in any POM within the
// root (including parent POMs referenced via <relativePath>) are resolved.
func TestAnalyze_DirFlag_FindsPropertiesInRoot(t *testing.T) {
	root := t.TempDir()
	// root/pom.xml declares the property.
	writeFile(t, filepath.Join(root, "pom.xml"), pomWithParentAndProp())
	// root/lib/pom.xml references the property via a dep and points at the parent.
	writeFile(t, filepath.Join(root, "lib", "pom.xml"), pomWithPropertyDep("io.netty", "netty-all", "netty.version"))

	// Simulate: omnibump analyze --dir <root>
	// --dir sets the project path, which is both what gets analyzed and the boundary.
	ma := &MavenAnalyzer{}
	result, err := ma.Analyze(t.Context(), root)
	if err != nil {
		t.Fatalf("Analyze(root): %v", err)
	}

	if v := result.Properties["netty.version"]; v != "4.1.100.Final" {
		t.Errorf("Properties[netty.version] = %q, want 4.1.100.Final", v)
	}
	if src := result.PropertySources["netty.version"]; src != "pom.xml" {
		t.Errorf("PropertySources[netty.version] = %q, want pom.xml", src)
	}
}

// TestAnalyze_NoDirFlag_DoesNotReadOutsideBoundary tests the default behaviour:
// when --dir is not set (i.e. the user analyzes a subdirectory directly), properties
// declared in parent POMs above that directory are blocked by the boundary check.
// The user must widen the boundary with --dir to reach them.
func TestAnalyze_NoDirFlag_DoesNotReadOutsideBoundary(t *testing.T) {
	root := t.TempDir()
	// root/pom.xml declares the property — above the analyzed subdirectory.
	writeFile(t, filepath.Join(root, "pom.xml"), pomWithParentAndProp())
	// root/lib/pom.xml is the only POM inside the analyzed boundary.
	writeFile(t, filepath.Join(root, "lib", "pom.xml"), pomWithPropertyDep("io.netty", "netty-all", "netty.version"))

	// Simulate: omnibump analyze root/lib  (no --dir flag → boundary = root/lib)
	// root/pom.xml is outside root/lib, so it must not be read.
	ma := &MavenAnalyzer{}
	result, err := ma.Analyze(t.Context(), filepath.Join(root, "lib"))
	if err != nil {
		t.Fatalf("Analyze(lib): %v", err)
	}

	if _, found := result.Properties["netty.version"]; found {
		t.Error("property from parent POM above the boundary should not be visible; use --dir to widen scope")
	}
}
