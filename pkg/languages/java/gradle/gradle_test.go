/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradle

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/analyzer"
	"github.com/chainguard-dev/omnibump/pkg/languages"
)

func TestGradle_Name(t *testing.T) {
	g := &Gradle{}
	if got := g.Name(); got != "gradle" {
		t.Errorf("Name() = %q, want %q", got, "gradle")
	}
}

func TestGradle_Detect(t *testing.T) {
	tests := []struct {
		name      string
		files     []string
		wantFound bool
	}{
		{
			name:      "kotlin dsl",
			files:     []string{"build.gradle.kts"},
			wantFound: true,
		},
		{
			name:      "groovy dsl",
			files:     []string{"build.gradle"},
			wantFound: true,
		},
		{
			name:      "settings kotlin",
			files:     []string{"settings.gradle.kts"},
			wantFound: true,
		},
		{
			name:      "settings groovy",
			files:     []string{"settings.gradle"},
			wantFound: true,
		},
		{
			name:      "no gradle files",
			files:     []string{"pom.xml"},
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
				if err := os.WriteFile(path, []byte("# test"), 0o600); err != nil {
					t.Fatalf("failed to create test file: %v", err)
				}
			}

			g := &Gradle{}
			found, err := g.Detect(context.Background(), tmpDir)
			if err != nil {
				t.Fatalf("Detect() error = %v", err)
			}
			if found != tt.wantFound {
				t.Errorf("Detect() = %v, want %v", found, tt.wantFound)
			}
		})
	}
}

func TestGradle_GetManifestFiles(t *testing.T) {
	g := &Gradle{}
	files := g.GetManifestFiles()

	expected := []string{
		"build.gradle",
		"build.gradle.kts",
		"settings.gradle",
		"settings.gradle.kts",
		"gradle/libs.versions.toml",
	}

	if len(files) != len(expected) {
		t.Fatalf("GetManifestFiles() returned %d files, want %d", len(files), len(expected))
	}

	for i, want := range expected {
		if files[i] != want {
			t.Errorf("GetManifestFiles()[%d] = %q, want %q", i, files[i], want)
		}
	}
}

// Helper function to reduce test duplication.
func runGradleUpdateTest(t *testing.T, testdataDir, buildFile string, deps []languages.Dependency, expectedVersions map[string]string) {
	t.Helper()

	tmpDir := t.TempDir()
	srcFile := filepath.Join(testdataDir, buildFile)
	dstFile := filepath.Join(tmpDir, buildFile)

	content, err := os.ReadFile(srcFile)
	if err != nil {
		t.Fatalf("failed to read testdata: %v", err)
	}
	if err := os.WriteFile(dstFile, content, 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	g := &Gradle{}
	cfg := &languages.UpdateConfig{
		RootDir:      tmpDir,
		Dependencies: deps,
	}

	if err := g.Update(context.Background(), cfg); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	updated, err := os.ReadFile(dstFile)
	if err != nil {
		t.Fatalf("failed to read updated file: %v", err)
	}

	updatedStr := string(updated)

	for dep, version := range expectedVersions {
		expected := dep + ":" + version
		if !strings.Contains(updatedStr, expected) {
			t.Errorf("Updated file should contain %q, but doesn't.\nContent:\n%s", expected, updatedStr)
		}
	}
}

func TestGradle_Update_StringNotation(t *testing.T) {
	testdataDir := filepath.Join("testdata", "simple-kotlin")
	deps := []languages.Dependency{
		{Name: "org.apache.commons:commons-lang3", Version: "3.14.0"},
		{Name: "io.netty:netty-all", Version: "4.1.101.Final"},
		{Name: "junit:junit", Version: "4.13.3"},
	}
	expected := map[string]string{
		"org.apache.commons:commons-lang3": "3.14.0",
		"io.netty:netty-all":               "4.1.101.Final",
		"junit:junit":                      "4.13.3",
	}
	runGradleUpdateTest(t, testdataDir, "build.gradle.kts", deps, expected)
}

func TestGradle_Update_LibraryFunction(t *testing.T) {
	// Use the Spring Boot style testdata
	testdataDir := filepath.Join("testdata", "spring-boot-style")

	// Create a temporary copy
	tmpDir := t.TempDir()
	srcFile := filepath.Join(testdataDir, "build.gradle.kts")
	dstFile := filepath.Join(tmpDir, "build.gradle.kts")

	content, err := os.ReadFile(srcFile)
	if err != nil {
		t.Fatalf("failed to read testdata: %v", err)
	}
	if err := os.WriteFile(dstFile, content, 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	g := &Gradle{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{
				Name:    "org.apache.commons:commons-lang3",
				Version: "3.18.0",
			},
			{
				Name:    "io.netty:netty-handler",
				Version: "4.1.101.Final",
			},
		},
	}

	if err := g.Update(context.Background(), cfg); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	// Read updated file
	updated, err := os.ReadFile(dstFile)
	if err != nil {
		t.Fatalf("failed to read updated file: %v", err)
	}

	updatedStr := string(updated)

	// Verify updates - check library() function calls have new versions
	tests := []struct {
		name    string
		version string
	}{
		{"commons-lang3", "3.18.0"},
		{"netty-handler", "4.1.101.Final"},
	}

	for _, tt := range tests {
		expected := `library("` + tt.name + `", "` + tt.version + `")`
		if !strings.Contains(updatedStr, expected) {
			t.Errorf("Updated file should contain %q, but doesn't.\nContent:\n%s", expected, updatedStr)
		}
	}
}

func TestGradle_Update_DryRun(t *testing.T) {
	testdataDir := filepath.Join("testdata", "simple-kotlin")

	tmpDir := t.TempDir()
	srcFile := filepath.Join(testdataDir, "build.gradle.kts")
	dstFile := filepath.Join(tmpDir, "build.gradle.kts")

	originalContent, err := os.ReadFile(srcFile)
	if err != nil {
		t.Fatalf("failed to read testdata: %v", err)
	}
	if err := os.WriteFile(dstFile, originalContent, 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	g := &Gradle{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		DryRun:  true,
		Dependencies: []languages.Dependency{
			{
				Name:    "org.apache.commons:commons-lang3",
				Version: "3.99.0",
			},
		},
	}

	if err := g.Update(context.Background(), cfg); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	// File should be unchanged in dry run
	afterContent, err := os.ReadFile(dstFile)
	if err != nil {
		t.Fatalf("failed to read file after dry run: %v", err)
	}

	if string(afterContent) != string(originalContent) {
		t.Errorf("Dry run should not modify file, but content changed")
	}
}

func TestGradle_Update_NoBuildFile(t *testing.T) {
	tmpDir := t.TempDir()

	g := &Gradle{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "test:test", Version: "1.0.0"},
		},
	}

	err := g.Update(context.Background(), cfg)
	if err == nil {
		t.Fatal("Update() should error when no build files exist")
	}
	if !strings.Contains(err.Error(), "no build.gradle") {
		t.Errorf("Update() error = %v, want error about missing build files", err)
	}
}

func TestGradle_Update_NonExistentDependency(t *testing.T) {
	testdataDir := filepath.Join("testdata", "simple-kotlin")

	tmpDir := t.TempDir()
	srcFile := filepath.Join(testdataDir, "build.gradle.kts")
	dstFile := filepath.Join(tmpDir, "build.gradle.kts")

	content, err := os.ReadFile(srcFile)
	if err != nil {
		t.Fatalf("failed to read testdata: %v", err)
	}
	if err := os.WriteFile(dstFile, content, 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	g := &Gradle{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{
				Name:    "com.example:nonexistent",
				Version: "1.0.0",
			},
		},
	}

	// Should not error, but should log warning (no changes made)
	if err := g.Update(context.Background(), cfg); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	// File should be unchanged
	afterContent, err := os.ReadFile(dstFile)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}

	if string(afterContent) != string(content) {
		t.Errorf("File should be unchanged when dependency not found, but content changed")
	}
}

func TestParseDependencyName(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		wantGroupID    string
		wantArtifactID string
	}{
		{
			name:           "full notation",
			input:          "org.apache.commons:commons-lang3",
			wantGroupID:    "org.apache.commons",
			wantArtifactID: "commons-lang3",
		},
		{
			name:           "artifact only",
			input:          "junit",
			wantGroupID:    "",
			wantArtifactID: "junit",
		},
		{
			name:           "empty string",
			input:          "",
			wantGroupID:    "",
			wantArtifactID: "",
		},
		{
			name:           "too many colons",
			input:          "group:artifact:version",
			wantGroupID:    "",
			wantArtifactID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotGroup, gotArtifact := parseDependencyName(tt.input)
			if gotGroup != tt.wantGroupID {
				t.Errorf("parseDependencyName() groupID = %q, want %q", gotGroup, tt.wantGroupID)
			}
			if gotArtifact != tt.wantArtifactID {
				t.Errorf("parseDependencyName() artifactID = %q, want %q", gotArtifact, tt.wantArtifactID)
			}
		})
	}
}

func TestBuildDependencyPatterns(t *testing.T) {
	patterns := buildDependencyPatterns("org.example", "my-lib")

	if len(patterns) != 3 {
		t.Fatalf("buildDependencyPatterns() returned %d patterns, want 3", len(patterns))
	}

	// Verify pattern names
	expectedNames := []string{"string-notation", "library-function", "map-notation"}
	for i, name := range expectedNames {
		if patterns[i].name != name {
			t.Errorf("pattern[%d].name = %q, want %q", i, patterns[i].name, name)
		}
	}

	// Verify all patterns have valid regex
	for i, pattern := range patterns {
		if pattern.regex == "" {
			t.Errorf("pattern[%d].regex is empty", i)
		}
		if pattern.versionGroup < 1 {
			t.Errorf("pattern[%d].versionGroup = %d, should be >= 1", i, pattern.versionGroup)
		}
	}
}

// Integration tests based on real enterprise packages

func TestGradle_Update_KafkaStyle(t *testing.T) {
	testdataDir := filepath.Join("testdata", "kafka-style")
	deps := []languages.Dependency{
		{Name: "org.apache.commons:commons-lang3", Version: "3.18.0"},
		{Name: "commons-beanutils:commons-beanutils", Version: "1.11.0"},
		{Name: "io.netty:netty-all", Version: "4.1.101.Final"},
	}
	expected := map[string]string{
		"org.apache.commons:commons-lang3":    "3.18.0",
		"commons-beanutils:commons-beanutils": "1.11.0",
		"io.netty:netty-all":                  "4.1.101.Final",
	}
	runGradleUpdateTest(t, testdataDir, "build.gradle", deps, expected)
}

func TestGradle_Update_MultiModule(t *testing.T) {
	// Test multi-module project with subdirectories
	tmpDir := t.TempDir()

	// Create root build.gradle
	rootBuild := filepath.Join(tmpDir, "build.gradle")
	rootContent := `dependencies {
    implementation("org.apache.commons:commons-lang3:3.12.0")
}`
	if err := os.WriteFile(rootBuild, []byte(rootContent), 0o600); err != nil {
		t.Fatalf("failed to create root build.gradle: %v", err)
	}

	// Create subproject directory and build.gradle
	subprojectDir := filepath.Join(tmpDir, "subproject")
	if err := os.MkdirAll(subprojectDir, 0o750); err != nil {
		t.Fatalf("failed to create subproject dir: %v", err)
	}

	subBuild := filepath.Join(subprojectDir, "build.gradle.kts")
	subContent := `dependencies {
    implementation("io.netty:netty-all:4.1.100.Final")
}`
	if err := os.WriteFile(subBuild, []byte(subContent), 0o600); err != nil {
		t.Fatalf("failed to create subproject build.gradle.kts: %v", err)
	}

	// Create nested subproject
	nestedDir := filepath.Join(tmpDir, "subproject", "nested")
	if err := os.MkdirAll(nestedDir, 0o750); err != nil {
		t.Fatalf("failed to create nested dir: %v", err)
	}

	nestedBuild := filepath.Join(nestedDir, "build.gradle")
	nestedContent := `dependencies {
    testImplementation("junit:junit:4.13.2")
}`
	if err := os.WriteFile(nestedBuild, []byte(nestedContent), 0o600); err != nil {
		t.Fatalf("failed to create nested build.gradle: %v", err)
	}

	g := &Gradle{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{
				Name:    "org.apache.commons:commons-lang3",
				Version: "3.18.0",
			},
			{
				Name:    "io.netty:netty-all",
				Version: "4.1.101.Final",
			},
			{
				Name:    "junit:junit",
				Version: "4.13.3",
			},
		},
	}

	if err := g.Update(context.Background(), cfg); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	// Verify root build.gradle was updated
	rootUpdated, err := os.ReadFile(rootBuild)
	if err != nil {
		t.Fatalf("failed to read root build.gradle: %v", err)
	}
	if !strings.Contains(string(rootUpdated), "commons-lang3:3.18.0") {
		t.Errorf("Root build.gradle not updated correctly:\n%s", string(rootUpdated))
	}

	// Verify subproject build.gradle.kts was updated
	subUpdated, err := os.ReadFile(subBuild)
	if err != nil {
		t.Fatalf("failed to read subproject build.gradle.kts: %v", err)
	}
	if !strings.Contains(string(subUpdated), "netty-all:4.1.101.Final") {
		t.Errorf("Subproject build.gradle.kts not updated correctly:\n%s", string(subUpdated))
	}

	// Verify nested build.gradle was updated
	nestedUpdated, err := os.ReadFile(nestedBuild)
	if err != nil {
		t.Fatalf("failed to read nested build.gradle: %v", err)
	}
	if !strings.Contains(string(nestedUpdated), "junit:4.13.3") {
		t.Errorf("Nested build.gradle not updated correctly:\n%s", string(nestedUpdated))
	}
}

func TestGradle_Update_SpringBootReal(t *testing.T) {
	// Real-world test based on enterprise-packages/spring-boot.yaml
	// CVE-2025-48924 remediation: library("Commons Lang3", "3.17.0") -> "3.18.0"
	testdataDir := filepath.Join("testdata", "spring-boot-real")

	tmpDir := t.TempDir()
	srcFile := filepath.Join(testdataDir, "build.gradle")
	dstFile := filepath.Join(tmpDir, "build.gradle")

	content, err := os.ReadFile(srcFile)
	if err != nil {
		t.Fatalf("failed to read testdata: %v", err)
	}
	if err := os.WriteFile(dstFile, content, 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	g := &Gradle{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{
				// Note: library() uses display name, not artifact ID
				// In real usage, melange would specify: "org.apache.commons:Commons Lang3"
				Name:    "org.apache.commons:Commons Lang3",
				Version: "3.18.0", // Real CVE-2025-48924 remediation
			},
			{
				Name:    "io.netty:Netty",
				Version: "4.1.101.Final",
			},
		},
	}

	if err := g.Update(context.Background(), cfg); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	// Read updated file
	updated, err := os.ReadFile(dstFile)
	if err != nil {
		t.Fatalf("failed to read updated file: %v", err)
	}

	updatedStr := string(updated)

	// Verify the exact CVE remediation from enterprise-packages
	if !strings.Contains(updatedStr, `library("Commons Lang3", "3.18.0")`) {
		t.Errorf("CVE-2025-48924 remediation failed. Should update to 3.18.0.\nContent:\n%s", updatedStr)
	}

	// Verify original pattern was replaced
	if strings.Contains(updatedStr, `library("Commons Lang3", "3.17.0")`) {
		t.Errorf("Old version 3.17.0 should be replaced, but still exists.\nContent:\n%s", updatedStr)
	}

	// Verify Netty update
	if !strings.Contains(updatedStr, `library("Netty", "4.1.101.Final")`) {
		t.Errorf("Netty update failed. Should update to 4.1.101.Final.\nContent:\n%s", updatedStr)
	}
}

func TestGradle_Update_SettingsGradle(t *testing.T) {
	tmpDir := t.TempDir()

	// Create settings.gradle with inline version catalog
	settingsFile := filepath.Join(tmpDir, "settings.gradle")
	settingsContent := `rootProject.name = 'test-project'

dependencyResolutionManagement {
    versionCatalogs {
        libs {
            version("netty-all", "4.1.100.Final")
            version("commons-lang3", "3.12.0")
            library("netty", "io.netty", "netty-all").versionRef("netty-all")
            library("commons-lang3", "org.apache.commons", "commons-lang3").versionRef("commons-lang3")
        }
    }
}
`
	if err := os.WriteFile(settingsFile, []byte(settingsContent), 0o600); err != nil {
		t.Fatalf("failed to write settings.gradle: %v", err)
	}

	// Update dependencies
	g := &Gradle{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{
				Name:    "io.netty:netty-all",
				Version: "4.1.101.Final",
			},
			{
				Name:    "org.apache.commons:commons-lang3",
				Version: "3.18.0",
			},
		},
	}

	if err := g.Update(context.Background(), cfg); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	// Read updated file
	updated, err := os.ReadFile(settingsFile)
	if err != nil {
		t.Fatalf("failed to read updated file: %v", err)
	}

	updatedStr := string(updated)

	// Verify version updates
	if !strings.Contains(updatedStr, `version("netty-all", "4.1.101.Final")`) {
		t.Errorf("netty-all version not updated.\nContent:\n%s", updatedStr)
	}

	if !strings.Contains(updatedStr, `version("commons-lang3", "3.18.0")`) {
		t.Errorf("commons-lang3 version not updated.\nContent:\n%s", updatedStr)
	}

	// Verify old versions are gone
	if strings.Contains(updatedStr, `version("netty-all", "4.1.100.Final")`) {
		t.Errorf("Old netty-all version should be replaced.\nContent:\n%s", updatedStr)
	}

	if strings.Contains(updatedStr, `version("commons-lang3", "3.12.0")`) {
		t.Errorf("Old commons-lang3 version should be replaced.\nContent:\n%s", updatedStr)
	}
}

func TestGradle_Update_VersionCatalogToml(t *testing.T) {
	tmpDir := t.TempDir()

	// Create gradle directory for libs.versions.toml
	gradleDir := filepath.Join(tmpDir, "gradle")
	if err := os.MkdirAll(gradleDir, 0o750); err != nil {
		t.Fatalf("failed to create gradle directory: %v", err)
	}

	// Create libs.versions.toml
	tomlFile := filepath.Join(gradleDir, "libs.versions.toml")
	tomlContent := `[versions]
netty-all = "4.1.100.Final"
commons-lang3 = "3.12.0"
junit = "4.13.2"

[libraries]
netty-all = { module = "io.netty:netty-all", version.ref = "netty-all" }
commons-lang3 = { module = "org.apache.commons:commons-lang3", version.ref = "commons-lang3" }
junit-junit = { module = "junit:junit", version.ref = "junit" }
`
	if err := os.WriteFile(tomlFile, []byte(tomlContent), 0o600); err != nil {
		t.Fatalf("failed to write libs.versions.toml: %v", err)
	}

	// Update dependencies
	g := &Gradle{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{
				Name:    "io.netty:netty-all",
				Version: "4.1.101.Final",
			},
			{
				Name:    "org.apache.commons:commons-lang3",
				Version: "3.18.0",
			},
			{
				Name:    "junit:junit",
				Version: "4.13.3",
			},
		},
	}

	if err := g.Update(context.Background(), cfg); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	// Read updated file
	updated, err := os.ReadFile(tomlFile)
	if err != nil {
		t.Fatalf("failed to read updated file: %v", err)
	}

	updatedStr := string(updated)

	// Verify version updates
	if !strings.Contains(updatedStr, `netty-all = "4.1.101.Final"`) {
		t.Errorf("netty-all version not updated.\nContent:\n%s", updatedStr)
	}

	if !strings.Contains(updatedStr, `commons-lang3 = "3.18.0"`) {
		t.Errorf("commons-lang3 version not updated.\nContent:\n%s", updatedStr)
	}

	if !strings.Contains(updatedStr, `junit = "4.13.3"`) {
		t.Errorf("junit version not updated.\nContent:\n%s", updatedStr)
	}

	// Verify old versions are gone
	if strings.Contains(updatedStr, `netty-all = "4.1.100.Final"`) {
		t.Errorf("Old netty-all version should be replaced.\nContent:\n%s", updatedStr)
	}

	if strings.Contains(updatedStr, `commons-lang3 = "3.12.0"`) {
		t.Errorf("Old commons-lang3 version should be replaced.\nContent:\n%s", updatedStr)
	}

	if strings.Contains(updatedStr, `junit = "4.13.2"`) {
		t.Errorf("Old junit version should be replaced.\nContent:\n%s", updatedStr)
	}
}

func TestGradle_Update_VersionCatalogToml_NoVersionsSection(t *testing.T) {
	tmpDir := t.TempDir()

	// Create gradle directory for libs.versions.toml
	gradleDir := filepath.Join(tmpDir, "gradle")
	if err := os.MkdirAll(gradleDir, 0o750); err != nil {
		t.Fatalf("failed to create gradle directory: %v", err)
	}

	// Create libs.versions.toml without [versions] section
	tomlFile := filepath.Join(gradleDir, "libs.versions.toml")
	tomlContent := `[libraries]
netty-all = { module = "io.netty:netty-all", version = "4.1.100.Final" }
`
	if err := os.WriteFile(tomlFile, []byte(tomlContent), 0o600); err != nil {
		t.Fatalf("failed to write libs.versions.toml: %v", err)
	}

	// Try to update dependencies
	g := &Gradle{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{
				Name:    "io.netty:netty-all",
				Version: "4.1.101.Final",
			},
		},
	}

	if err := g.Update(context.Background(), cfg); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	// Verify file was not modified (no [versions] section to update)
	updated, err := os.ReadFile(tomlFile)
	if err != nil {
		t.Fatalf("failed to read updated file: %v", err)
	}

	if string(updated) != tomlContent {
		t.Errorf("File should not be modified when no [versions] section exists")
	}
}

func TestGradle_Update_VersionCatalogToml_InvalidToml(t *testing.T) {
	tmpDir := t.TempDir()

	// Create gradle directory for libs.versions.toml
	gradleDir := filepath.Join(tmpDir, "gradle")
	if err := os.MkdirAll(gradleDir, 0o750); err != nil {
		t.Fatalf("failed to create gradle directory: %v", err)
	}

	// Create invalid TOML file
	tomlFile := filepath.Join(gradleDir, "libs.versions.toml")
	tomlContent := `[versions
this is not valid TOML
`
	if err := os.WriteFile(tomlFile, []byte(tomlContent), 0o600); err != nil {
		t.Fatalf("failed to write libs.versions.toml: %v", err)
	}

	// Try to update dependencies - should fail with TOML parse error
	g := &Gradle{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{
				Name:    "io.netty:netty-all",
				Version: "4.1.101.Final",
			},
		},
	}

	err := g.Update(context.Background(), cfg)
	if err == nil {
		t.Fatal("Update() should fail with invalid TOML")
	}

	if !strings.Contains(err.Error(), "parse TOML") {
		t.Errorf("Expected TOML parse error, got: %v", err)
	}
}

func TestGradle_Update_MapNotation(t *testing.T) {
	tmpDir := t.TempDir()

	// Create build.gradle with map notation
	buildFile := filepath.Join(tmpDir, "build.gradle")
	buildContent := `dependencies {
    implementation group = "org.apache.commons", name = "commons-lang3", version = "3.12.0"
    testImplementation group = "junit", name = "junit", version = "4.13.2"
}
`
	if err := os.WriteFile(buildFile, []byte(buildContent), 0o600); err != nil {
		t.Fatalf("failed to write build.gradle: %v", err)
	}

	// Update dependencies
	g := &Gradle{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{
				Name:    "org.apache.commons:commons-lang3",
				Version: "3.18.0",
			},
			{
				Name:    "junit:junit",
				Version: "4.13.3",
			},
		},
	}

	if err := g.Update(context.Background(), cfg); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	// Read updated file
	updated, err := os.ReadFile(buildFile)
	if err != nil {
		t.Fatalf("failed to read updated file: %v", err)
	}

	updatedStr := string(updated)

	// Verify version updates
	if !strings.Contains(updatedStr, `version = "3.18.0"`) {
		t.Errorf("commons-lang3 version not updated.\nContent:\n%s", updatedStr)
	}

	if !strings.Contains(updatedStr, `version = "4.13.3"`) {
		t.Errorf("junit version not updated.\nContent:\n%s", updatedStr)
	}
}

func TestGradle_FindBuildFiles_SkipsHiddenDirs(t *testing.T) {
	tmpDir := t.TempDir()

	// Create build.gradle in root
	rootBuild := filepath.Join(tmpDir, "build.gradle")
	if err := os.WriteFile(rootBuild, []byte(""), 0o600); err != nil {
		t.Fatalf("failed to write root build.gradle: %v", err)
	}

	// Create .git/build.gradle (should be skipped)
	gitDir := filepath.Join(tmpDir, ".git")
	if err := os.MkdirAll(gitDir, 0o750); err != nil {
		t.Fatalf("failed to create .git directory: %v", err)
	}
	gitBuild := filepath.Join(gitDir, "build.gradle")
	if err := os.WriteFile(gitBuild, []byte(""), 0o600); err != nil {
		t.Fatalf("failed to write .git/build.gradle: %v", err)
	}

	// Create vendor/build.gradle (should be skipped)
	vendorDir := filepath.Join(tmpDir, "vendor")
	if err := os.MkdirAll(vendorDir, 0o750); err != nil {
		t.Fatalf("failed to create vendor directory: %v", err)
	}
	vendorBuild := filepath.Join(vendorDir, "build.gradle")
	if err := os.WriteFile(vendorBuild, []byte(""), 0o600); err != nil {
		t.Fatalf("failed to write vendor/build.gradle: %v", err)
	}

	// Find build files
	files, err := findBuildFiles(tmpDir)
	if err != nil {
		t.Fatalf("findBuildFiles() error = %v", err)
	}

	// Should only find root build.gradle
	if len(files) != 1 {
		t.Errorf("Expected 1 file, got %d: %v", len(files), files)
	}

	if len(files) > 0 && files[0] != rootBuild {
		t.Errorf("Expected %s, got %s", rootBuild, files[0])
	}
}

func TestGradleAnalyzer_Analyze_WithTomlCatalog(t *testing.T) {
	tmpDir := t.TempDir()

	// Create gradle directory
	gradleDir := filepath.Join(tmpDir, "gradle")
	if err := os.MkdirAll(gradleDir, 0o750); err != nil {
		t.Fatalf("failed to create gradle directory: %v", err)
	}

	// Create libs.versions.toml with version catalog
	tomlFile := filepath.Join(gradleDir, "libs.versions.toml")
	tomlContent := `[versions]
netty-all = "4.1.100.Final"
commons-lang3 = "3.12.0"

[libraries]
netty-all = { module = "io.netty:netty-all", version.ref = "netty-all" }
commons-lang3 = { module = "org.apache.commons:commons-lang3", version.ref = "commons-lang3" }
`
	if err := os.WriteFile(tomlFile, []byte(tomlContent), 0o600); err != nil {
		t.Fatalf("failed to write libs.versions.toml: %v", err)
	}

	// Create build.gradle with dependencies
	buildFile := filepath.Join(tmpDir, "build.gradle")
	buildContent := `dependencies {
    implementation("io.netty:netty-all:4.1.100.Final")
    implementation("org.apache.commons:commons-lang3:3.12.0")
}
`
	if err := os.WriteFile(buildFile, []byte(buildContent), 0o600); err != nil {
		t.Fatalf("failed to write build.gradle: %v", err)
	}

	// Analyze the project
	analyzer := &GradleAnalyzer{}
	result, err := analyzer.Analyze(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	// Verify we found version catalog keys
	if len(result.Properties) != 2 {
		t.Errorf("Expected 2 version catalog keys, got %d", len(result.Properties))
	}

	if result.Properties["netty-all"] != "4.1.100.Final" {
		t.Errorf("Expected netty-all version 4.1.100.Final, got %s", result.Properties["netty-all"])
	}

	if result.Properties["commons-lang3"] != "3.12.0" {
		t.Errorf("Expected commons-lang3 version 3.12.0, got %s", result.Properties["commons-lang3"])
	}

	// Verify we found dependencies
	if len(result.Dependencies) != 2 {
		t.Errorf("Expected 2 dependencies, got %d", len(result.Dependencies))
	}
}

func TestGradleAnalyzer_Analyze_WithInlineCatalog(t *testing.T) {
	tmpDir := t.TempDir()

	// Create settings.gradle with inline version catalog
	settingsFile := filepath.Join(tmpDir, "settings.gradle")
	settingsContent := `rootProject.name = 'test-project'

dependencyResolutionManagement {
    versionCatalogs {
        libs {
            version("netty-all", "4.1.100.Final")
            version("commons-lang3", "3.12.0")
        }
    }
}
`
	if err := os.WriteFile(settingsFile, []byte(settingsContent), 0o600); err != nil {
		t.Fatalf("failed to write settings.gradle: %v", err)
	}

	// Create build.gradle with dependencies
	buildFile := filepath.Join(tmpDir, "build.gradle")
	buildContent := `dependencies {
    implementation("io.netty:netty-all:4.1.100.Final")
}
`
	if err := os.WriteFile(buildFile, []byte(buildContent), 0o600); err != nil {
		t.Fatalf("failed to write build.gradle: %v", err)
	}

	// Analyze the project
	analyzer := &GradleAnalyzer{}
	result, err := analyzer.Analyze(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	// Verify we found inline version catalog keys
	if len(result.Properties) != 2 {
		t.Errorf("Expected 2 version catalog keys, got %d", len(result.Properties))
	}

	if result.Properties["netty-all"] != "4.1.100.Final" {
		t.Errorf("Expected netty-all version 4.1.100.Final, got %s", result.Properties["netty-all"])
	}
}

func TestGradleAnalyzer_RecommendStrategy_DirectUpdates(t *testing.T) {
	tmpDir := t.TempDir()

	// Create build.gradle with direct version dependencies
	buildFile := filepath.Join(tmpDir, "build.gradle")
	buildContent := `dependencies {
    implementation("io.netty:netty-all:4.1.100.Final")
    implementation("org.apache.commons:commons-lang3:3.12.0")
}
`
	if err := os.WriteFile(buildFile, []byte(buildContent), 0o600); err != nil {
		t.Fatalf("failed to write build.gradle: %v", err)
	}

	// Analyze the project
	gradleAnalyzer := &GradleAnalyzer{}
	analysis, err := gradleAnalyzer.Analyze(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	// Request updates
	deps := []analyzer.Dependency{
		{
			Name:    "io.netty:netty-all",
			Version: "4.1.101.Final",
		},
		{
			Name:    "org.apache.commons:commons-lang3",
			Version: "3.18.0",
		},
	}

	strategy, err := gradleAnalyzer.RecommendStrategy(context.Background(), analysis, deps)
	if err != nil {
		t.Fatalf("RecommendStrategy() error = %v", err)
	}

	// Both should be direct updates (no version catalog)
	if len(strategy.DirectUpdates) != 2 {
		t.Errorf("Expected 2 direct updates, got %d", len(strategy.DirectUpdates))
	}

	if len(strategy.PropertyUpdates) != 0 {
		t.Errorf("Expected 0 version catalog updates, got %d", len(strategy.PropertyUpdates))
	}
}

func TestGradleAnalyzer_RecommendStrategy_CatalogUpdates(t *testing.T) {
	tmpDir := t.TempDir()

	// Create gradle directory
	gradleDir := filepath.Join(tmpDir, "gradle")
	if err := os.MkdirAll(gradleDir, 0o750); err != nil {
		t.Fatalf("failed to create gradle directory: %v", err)
	}

	// Create libs.versions.toml with version catalog
	tomlFile := filepath.Join(gradleDir, "libs.versions.toml")
	tomlContent := `[versions]
netty = "4.1.100.Final"

[libraries]
netty-all = { module = "io.netty:netty-all", version.ref = "netty" }
`
	if err := os.WriteFile(tomlFile, []byte(tomlContent), 0o600); err != nil {
		t.Fatalf("failed to write libs.versions.toml: %v", err)
	}

	// Create build.gradle - simulate using catalog (we detect from catalog definition)
	buildFile := filepath.Join(tmpDir, "build.gradle")
	buildContent := `dependencies {
    // Would actually be implementation(libs.netty.all) in real project
}
`
	if err := os.WriteFile(buildFile, []byte(buildContent), 0o600); err != nil {
		t.Fatalf("failed to write build.gradle: %v", err)
	}

	// Analyze the project
	gradleAnalyzer := &GradleAnalyzer{}
	analysis, err := gradleAnalyzer.Analyze(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	// Manually mark the dependency as using catalog (simulating catalog reference detection)
	if dep, exists := analysis.Dependencies["io.netty:netty-all"]; exists {
		dep.UsesProperty = true
		dep.PropertyName = "netty"
	}

	// Request update
	deps := []analyzer.Dependency{
		{
			Name:    "io.netty:netty-all",
			Version: "4.1.101.Final",
		},
	}

	strategy, err := gradleAnalyzer.RecommendStrategy(context.Background(), analysis, deps)
	if err != nil {
		t.Fatalf("RecommendStrategy() error = %v", err)
	}

	// Should be catalog update
	if len(strategy.PropertyUpdates) != 1 {
		t.Errorf("Expected 1 version catalog update, got %d", len(strategy.PropertyUpdates))
	}

	if strategy.PropertyUpdates["netty"] != "4.1.101.Final" {
		t.Errorf("Expected netty catalog key to be updated to 4.1.101.Final, got %s", strategy.PropertyUpdates["netty"])
	}

	if len(strategy.DirectUpdates) != 0 {
		t.Errorf("Expected 0 direct updates, got %d", len(strategy.DirectUpdates))
	}
}

func TestGradleAnalyzer_Analyze_EmptyProject(t *testing.T) {
	tmpDir := t.TempDir()

	// Create empty build.gradle
	buildFile := filepath.Join(tmpDir, "build.gradle")
	if err := os.WriteFile(buildFile, []byte(""), 0o600); err != nil {
		t.Fatalf("failed to write build.gradle: %v", err)
	}

	// Analyze the project
	analyzer := &GradleAnalyzer{}
	result, err := analyzer.Analyze(context.Background(), tmpDir)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	// Should find no dependencies or catalogs
	if len(result.Dependencies) != 0 {
		t.Errorf("Expected 0 dependencies, got %d", len(result.Dependencies))
	}

	if len(result.Properties) != 0 {
		t.Errorf("Expected 0 version catalog keys, got %d", len(result.Properties))
	}
}

func TestGradle_Validate_Success(t *testing.T) {
	tmpDir := t.TempDir()

	// Create build.gradle with dependencies
	buildFile := filepath.Join(tmpDir, "build.gradle.kts")
	buildContent := `
dependencies {
    implementation("io.netty:netty-all:4.1.101.Final")
}
`
	if err := os.WriteFile(buildFile, []byte(buildContent), 0o600); err != nil {
		t.Fatalf("failed to write build.gradle.kts: %v", err)
	}

	// Update configuration matching the actual versions in the file
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "io.netty:netty-all", Version: "4.1.101.Final"},
		},
	}

	gradle := &Gradle{}
	err := gradle.Validate(context.Background(), cfg)
	if err != nil {
		t.Errorf("Validate() error = %v, expected nil", err)
	}
}

func TestGradle_Validate_VersionMismatch(t *testing.T) {
	tmpDir := t.TempDir()

	// Create build.gradle with old versions
	buildFile := filepath.Join(tmpDir, "build.gradle.kts")
	buildContent := `
dependencies {
    implementation("org.apache.commons:commons-lang3:3.12.0")
}
`
	if err := os.WriteFile(buildFile, []byte(buildContent), 0o600); err != nil {
		t.Fatalf("failed to write build.gradle.kts: %v", err)
	}

	// Request validation for a different version
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "org.apache.commons:commons-lang3", Version: "3.18.0"},
		},
	}

	gradle := &Gradle{}
	err := gradle.Validate(context.Background(), cfg)
	if err == nil {
		t.Error("Validate() expected error for version mismatch, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "has version 3.12.0, expected 3.18.0") {
		t.Errorf("Validate() error = %v, expected version mismatch message", err)
	}
}

func TestGradle_Validate_DependencyNotFound(t *testing.T) {
	tmpDir := t.TempDir()

	// Create empty build.gradle
	buildFile := filepath.Join(tmpDir, "build.gradle.kts")
	buildContent := `
dependencies {
}
`
	if err := os.WriteFile(buildFile, []byte(buildContent), 0o600); err != nil {
		t.Fatalf("failed to write build.gradle.kts: %v", err)
	}

	// Request validation for a dependency that doesn't exist
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "org.apache.commons:commons-lang3", Version: "3.18.0"},
		},
	}

	gradle := &Gradle{}
	err := gradle.Validate(context.Background(), cfg)
	if err == nil {
		t.Error("Validate() expected error for missing dependency, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "not found in project after update") {
		t.Errorf("Validate() error = %v, expected not found message", err)
	}
}

func TestGradle_Validate_VersionCatalog(t *testing.T) {
	tmpDir := t.TempDir()

	// Create gradle directory for TOML catalog
	gradleDir := filepath.Join(tmpDir, "gradle")
	if err := os.MkdirAll(gradleDir, 0o750); err != nil {
		t.Fatalf("failed to create gradle directory: %v", err)
	}

	// Create libs.versions.toml with correct version
	tomlFile := filepath.Join(gradleDir, "libs.versions.toml")
	tomlContent := `[versions]
netty-all = "4.1.101.Final"

[libraries]
netty-all = { module = "io.netty:netty-all", version.ref = "netty-all" }
`
	if err := os.WriteFile(tomlFile, []byte(tomlContent), 0o600); err != nil {
		t.Fatalf("failed to write libs.versions.toml: %v", err)
	}

	// Create build.gradle using catalog
	buildFile := filepath.Join(tmpDir, "build.gradle.kts")
	buildContent := `
dependencies {
    implementation(libs.netty.all)
}
`
	if err := os.WriteFile(buildFile, []byte(buildContent), 0o600); err != nil {
		t.Fatalf("failed to write build.gradle.kts: %v", err)
	}

	// Validate the version catalog was updated correctly
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "io.netty:netty-all", Version: "4.1.101.Final"},
		},
	}

	gradle := &Gradle{}
	err := gradle.Validate(context.Background(), cfg)
	if err != nil {
		t.Errorf("Validate() error = %v, expected nil for correct catalog version", err)
	}
}

func TestGradle_Validate_VersionCatalogMismatch(t *testing.T) {
	tmpDir := t.TempDir()

	// Create gradle directory for TOML catalog
	gradleDir := filepath.Join(tmpDir, "gradle")
	if err := os.MkdirAll(gradleDir, 0o750); err != nil {
		t.Fatalf("failed to create gradle directory: %v", err)
	}

	// Create libs.versions.toml with old version
	tomlFile := filepath.Join(gradleDir, "libs.versions.toml")
	tomlContent := `[versions]
netty-all = "4.1.100.Final"

[libraries]
netty-all = { module = "io.netty:netty-all", version.ref = "netty-all" }
`
	if err := os.WriteFile(tomlFile, []byte(tomlContent), 0o600); err != nil {
		t.Fatalf("failed to write libs.versions.toml: %v", err)
	}

	// Create build.gradle using catalog
	buildFile := filepath.Join(tmpDir, "build.gradle.kts")
	buildContent := `
dependencies {
    implementation(libs.netty.all)
}
`
	if err := os.WriteFile(buildFile, []byte(buildContent), 0o600); err != nil {
		t.Fatalf("failed to write build.gradle.kts: %v", err)
	}

	// Validate should fail because catalog wasn't updated
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "io.netty:netty-all", Version: "4.1.101.Final"},
		},
	}

	gradle := &Gradle{}
	err := gradle.Validate(context.Background(), cfg)
	if err == nil {
		t.Error("Validate() expected error for catalog version mismatch, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "catalog key netty-all has version 4.1.100.Final, expected 4.1.101.Final") {
		t.Errorf("Validate() error = %v, expected catalog version mismatch message", err)
	}
}

func TestGradle_Validate_InvalidDirectory(t *testing.T) {
	cfg := &languages.UpdateConfig{
		RootDir: "/nonexistent/directory",
		Dependencies: []languages.Dependency{
			{Name: "org.apache.commons:commons-lang3", Version: "3.18.0"},
		},
	}

	gradle := &Gradle{}
	err := gradle.Validate(context.Background(), cfg)
	if err == nil {
		t.Error("Validate() expected error for invalid directory, got nil")
	}
}

// Security Tests

func TestValidateVersion_ValidVersions(t *testing.T) {
	validVersions := []string{
		"1.0.0",
		"3.14.0",
		"4.1.101.Final",
		"1.2.3-SNAPSHOT",
		"2.0.0+build.123",
		"1.0_beta",
		"5.0.0.RELEASE",
	}

	for _, version := range validVersions {
		t.Run(version, func(t *testing.T) {
			err := validateVersion(version)
			if err != nil {
				t.Errorf("validateVersion(%q) should be valid, got error: %v", version, err)
			}
		})
	}
}

func TestValidateVersion_InvalidVersions(t *testing.T) {
	invalidVersions := []struct {
		version string
		desc    string
	}{
		{
			version: `3.14.0")
}

task('backdoor') {
  doLast {
    println('injected')
  }
}

dependencies {
    implementation("org.apache.commons:commons-lang3:3.14.0`,
			desc: "code injection with newlines and braces",
		},
		{
			version: `1.0"; println("injected"); "1.0`,
			desc:    "code injection with quotes and semicolons",
		},
		{
			version: "1.0.0{injected}",
			desc:    "version with braces",
		},
		{
			version: "1.0.0(injected)",
			desc:    "version with parentheses",
		},
		{
			version: "1.0.0;malicious",
			desc:    "version with semicolon",
		},
		{
			version: "1.0.0'injected'",
			desc:    "version with single quotes",
		},
		{
			version: `1.0.0"injected"`,
			desc:    "version with double quotes",
		},
		{
			version: "1.0.0\ninjected",
			desc:    "version with newline",
		},
		{
			version: "1.0.0\rinjected",
			desc:    "version with carriage return",
		},
		{
			version: "1.0.0<injected>",
			desc:    "version with angle brackets",
		},
		{
			version: "1.0.0$injected",
			desc:    "version with dollar sign",
		},
		{
			version: "1.0.0\\injected",
			desc:    "version with backslash",
		},
	}

	for _, tt := range invalidVersions {
		t.Run(tt.desc, func(t *testing.T) {
			err := validateVersion(tt.version)
			if err == nil {
				t.Errorf("validateVersion(%q) should be invalid for: %s", tt.version, tt.desc)
			}
			if err != nil && !strings.Contains(err.Error(), "invalid version string") {
				t.Errorf("validateVersion(%q) error should mention invalid version, got: %v", tt.version, err)
			}
		})
	}
}

func TestGradle_Update_RejectsCodeInjection(t *testing.T) {
	tmpDir := t.TempDir()

	// Create build.gradle
	buildFile := filepath.Join(tmpDir, "build.gradle")
	buildContent := `dependencies {
    implementation("org.apache.commons:commons-lang3:3.12.0")
}
`
	if err := os.WriteFile(buildFile, []byte(buildContent), 0o600); err != nil {
		t.Fatalf("failed to write build.gradle: %v", err)
	}

	// Attempt code injection via malicious version string
	maliciousVersion := `3.14.0")
}

task('backdoor') {
  doLast {
    println('INJECTED CODE')
  }
}

dependencies {
    implementation("org.apache.commons:commons-lang3:3.14.0`

	g := &Gradle{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{
				Name:    "org.apache.commons:commons-lang3",
				Version: maliciousVersion,
			},
		},
	}

	// Update should fail with validation error
	err := g.Update(context.Background(), cfg)
	if err == nil {
		t.Fatal("Update() should reject malicious version string, but succeeded")
	}

	if !strings.Contains(err.Error(), "invalid version string") {
		t.Errorf("Update() error should mention invalid version, got: %v", err)
	}

	// Verify file was not modified
	afterContent, err := os.ReadFile(buildFile)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}

	if string(afterContent) != buildContent {
		t.Errorf("File should not be modified after validation failure, but was changed")
	}

	// Verify no malicious code was injected
	if strings.Contains(string(afterContent), "backdoor") {
		t.Error("Malicious code was injected into build file!")
	}
}

func TestFindBuildFiles_SkipsSymlinks(t *testing.T) {
	tmpDir := t.TempDir()

	// Create real build.gradle
	realBuildFile := filepath.Join(tmpDir, "build.gradle")
	if err := os.WriteFile(realBuildFile, []byte("real file"), 0o600); err != nil {
		t.Fatalf("failed to write real build.gradle: %v", err)
	}

	// Create a sensitive target file outside the project
	sensitiveDir := filepath.Join(tmpDir, "sensitive")
	if err := os.MkdirAll(sensitiveDir, 0o750); err != nil {
		t.Fatalf("failed to create sensitive directory: %v", err)
	}
	sensitiveFile := filepath.Join(sensitiveDir, "secrets.txt")
	if err := os.WriteFile(sensitiveFile, []byte("SECRET DATA"), 0o600); err != nil {
		t.Fatalf("failed to write sensitive file: %v", err)
	}

	// Create a symlink named build.gradle.kts pointing to sensitive file
	symlinkPath := filepath.Join(tmpDir, "build.gradle.kts")
	if err := os.Symlink(sensitiveFile, symlinkPath); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	// Find build files - should skip the symlink
	files, err := findBuildFiles(tmpDir)
	if err != nil {
		t.Fatalf("findBuildFiles() error = %v", err)
	}

	// Should only find the real build.gradle, not the symlink
	if len(files) != 1 {
		t.Errorf("Expected 1 file (real build.gradle), got %d: %v", len(files), files)
	}

	if len(files) > 0 && files[0] != realBuildFile {
		t.Errorf("Expected to find %s, got %s", realBuildFile, files[0])
	}

	// Verify the symlink was not included
	for _, file := range files {
		if file == symlinkPath {
			t.Error("Symlink should not be included in build files list")
		}
	}

	// Verify sensitive file was not modified
	sensitiveContent, err := os.ReadFile(sensitiveFile)
	if err != nil {
		t.Fatalf("failed to read sensitive file: %v", err)
	}
	if string(sensitiveContent) != "SECRET DATA" {
		t.Error("Sensitive file was modified!")
	}
}

// TestFindVersionKeyForArtifact_EdgeCases tests edge cases for version catalog key lookup.
func TestFindVersionKeyForArtifact_EdgeCases(t *testing.T) {
	tests := []struct {
		name       string
		artifactID string
		versions   map[string]any
		expected   string
	}{
		{
			name:       "exact match found",
			artifactID: "commons-lang3",
			versions: map[string]any{
				"commons-lang3": "3.14.0",
			},
			expected: "commons-lang3",
		},
		{
			name:       "no match - empty map",
			artifactID: "nonexistent",
			versions:   map[string]any{},
			expected:   "",
		},
		{
			name:       "no match with different artifact",
			artifactID: "spring-boot",
			versions: map[string]any{
				"spring-boot-starter": "2.7.0",
			},
			expected: "",
		},
		{
			name:       "nil versions map",
			artifactID: "any",
			versions:   nil,
			expected:   "",
		},
		{
			name:       "ambiguous artifact requires exact match",
			artifactID: "netty-codec-http",
			versions: map[string]any{
				"netty":       "4.1.90.Final",
				"netty-codec": "4.1.91.Final",
			},
			expected: "", // No fuzzy matching - must be exact
		},
		{
			name:       "no fuzzy match - artifact contains version key",
			artifactID: "netty-all",
			versions: map[string]any{
				"netty": "4.1.90.Final",
			},
			expected: "", // Previously would match "netty", now requires exact match
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := findVersionKeyForArtifact(tt.artifactID, tt.versions)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

// TestGradleAnalyzer_AnalyzeRemote tests the unimplemented remote analysis.
func TestGradleAnalyzer_AnalyzeRemote(t *testing.T) {
	ga := &GradleAnalyzer{}
	files := map[string][]byte{
		"build.gradle": []byte("dependencies {}"),
	}

	_, err := ga.AnalyzeRemote(context.Background(), files)
	if err == nil {
		t.Error("AnalyzeRemote should return error for unimplemented function")
	}
	if !strings.Contains(err.Error(), "not yet implemented") {
		t.Errorf("Error should mention not implemented, got: %v", err)
	}
}

// TestAnalyzeVersionCatalogToml_EmptyFile tests empty TOML file handling.
func TestAnalyzeVersionCatalogToml_EmptyFile(t *testing.T) {
	result := &analyzer.AnalysisResult{
		Dependencies: make(map[string]*analyzer.DependencyInfo),
		Properties:   make(map[string]string),
	}

	err := analyzeVersionCatalogToml(context.Background(), "", result)
	if err == nil {
		t.Error("Should error on empty content")
	}
}

// TestAnalyzeVersionCatalogToml_InvalidToml tests invalid TOML handling.
func TestAnalyzeVersionCatalogToml_InvalidToml(t *testing.T) {
	result := &analyzer.AnalysisResult{
		Dependencies: make(map[string]*analyzer.DependencyInfo),
		Properties:   make(map[string]string),
	}

	invalidToml := "invalid toml content [[[["
	err := analyzeVersionCatalogToml(context.Background(), invalidToml, result)
	if err == nil {
		t.Error("Should error on invalid TOML")
	}
}

func TestParseLibraryModule(t *testing.T) {
	tests := []struct {
		name           string
		libMap         map[string]any
		wantGroupID    string
		wantArtifactID string
	}{
		{
			name:           "valid module",
			libMap:         map[string]any{"module": "com.example:library"},
			wantGroupID:    "com.example",
			wantArtifactID: "library",
		},
		{
			name:           "module not string",
			libMap:         map[string]any{"module": 123},
			wantGroupID:    "",
			wantArtifactID: "",
		},
		{
			name:           "module not present",
			libMap:         map[string]any{},
			wantGroupID:    "",
			wantArtifactID: "",
		},
		{
			name:           "invalid format - no colon",
			libMap:         map[string]any{"module": "com.example.library"},
			wantGroupID:    "",
			wantArtifactID: "",
		},
		{
			name:           "invalid format - too many parts",
			libMap:         map[string]any{"module": "com.example:library:extra"},
			wantGroupID:    "",
			wantArtifactID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotGroupID, gotArtifactID := parseLibraryModule(tt.libMap)
			if gotGroupID != tt.wantGroupID {
				t.Errorf("parseLibraryModule() groupID = %v, want %v", gotGroupID, tt.wantGroupID)
			}
			if gotArtifactID != tt.wantArtifactID {
				t.Errorf("parseLibraryModule() artifactID = %v, want %v", gotArtifactID, tt.wantArtifactID)
			}
		})
	}
}

func TestParseLibraryVersion(t *testing.T) {
	tests := []struct {
		name   string
		libMap map[string]any
		alias  string
		want   string
	}{
		{
			name:   "direct version string",
			libMap: map[string]any{"version": "1.0.0"},
			alias:  "test",
			want:   "1.0.0",
		},
		{
			name:   "version map with ref key",
			libMap: map[string]any{"version": map[string]any{"ref": "someVersion"}},
			alias:  "test",
			want:   "",
		},
		{
			name:   "no version",
			libMap: map[string]any{},
			alias:  "test",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			log := clog.FromContext(ctx)
			got := parseLibraryVersion(tt.libMap, log, tt.alias)
			if got != tt.want {
				t.Errorf("parseLibraryVersion() = %v, want %v", got, tt.want)
			}
		})
	}
}
