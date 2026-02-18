/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package maven

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/chainguard-dev/gopom"
	"github.com/chainguard-dev/omnibump/pkg/languages"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func makeDep(groupID, artifactID, version string, opts ...string) gopom.Dependency {
	dep := gopom.Dependency{GroupID: groupID, ArtifactID: artifactID, Version: version, Scope: defaultScope, Type: defaultType}
	if len(opts) > 0 {
		dep.Scope = opts[0]
	}
	if len(opts) > 1 {
		dep.Type = opts[1]
	}
	return dep
}

func TestSimplePoms(t *testing.T) {
	testCases := []struct {
		name    string
		in      *gopom.Project
		patches []Patch
		props   map[string]string
		want    *gopom.Project
	}{{
		name:    "simple dependency, bumped inline, type and scope unmodified",
		in:      &gopom.Project{Dependencies: &[]gopom.Dependency{makeDep("a1", "b1", "1.0.0", "import", "jar")}},
		patches: []Patch{{"a1", "b1", "1.0.1", "INVALID_SCOPE", "INVALID_TYPE"}},
		want:    &gopom.Project{Dependencies: &[]gopom.Dependency{makeDep("a1", "b1", "1.0.1", "import", "jar")}},
	}, {
		name:    "simple dependencymanagement, bumped inline, type and scope unmodified",
		in:      &gopom.Project{DependencyManagement: &gopom.DependencyManagement{Dependencies: &[]gopom.Dependency{makeDep("a2", "b2", "2.0.0", "compile", "pom")}}},
		patches: []Patch{{"a2", "b2", "2.0.1", "INVALID_SCOPE", "INVALID_TYPE"}},
		want:    &gopom.Project{DependencyManagement: &gopom.DependencyManagement{Dependencies: &[]gopom.Dependency{makeDep("a2", "b2", "2.0.1", "compile", "pom")}}},
	}, {
		name:    "dependencymanagement, added to dependency management",
		in:      &gopom.Project{DependencyManagement: &gopom.DependencyManagement{Dependencies: &[]gopom.Dependency{makeDep("other", "b3", "2.0.0")}}},
		patches: []Patch{{"added", "b", "2.0.1", "import", "somethingelse"}},
		want:    &gopom.Project{DependencyManagement: &gopom.DependencyManagement{Dependencies: &[]gopom.Dependency{makeDep("other", "b3", "2.0.0"), makeDep("added", "b", "2.0.1", "import", "somethingelse")}}},
	}}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.in
			got, err := PatchProject(context.Background(), in, tc.patches, tc.props)
			if err != nil {
				t.Errorf("%s: Failed to patch %+v: %v", tc.name, tc.in, err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("%s: DIFFS: %s", tc.name, diff)
			}
		})
	}
}

func TestPatchesFromPomFiles(t *testing.T) {
	testCases := []struct {
		name       string
		in         string
		patches    []Patch
		props      map[string]string
		wantDeps   []Patch
		wantDMDeps []Patch
		wantProps  map[string]string
	}{{
		// This adds new dependencies to the project. They end up in
		// DependencyManagement.dependencies.
		name:    "trino - dependency patch - add new ones and replace existing",
		in:      "trino.pom.xml",
		patches: []Patch{{GroupID: "io.projectreactor.netty", ArtifactID: "reactor-netty-http", Version: "1.0.39", Scope: "import"}, {GroupID: "org.json", ArtifactID: "json", Version: "20231013"}, {ArtifactID: "ch.qos.logback", GroupID: "logback-core", Version: "[1.4.12,2.0.0)"}, {GroupID: "com.azure", ArtifactID: "azure-sdk-bom", Version: "1.2.19", Type: "pom", Scope: "INVALID"}},

		wantDMDeps: []Patch{{GroupID: "io.projectreactor.netty", ArtifactID: "reactor-netty-http", Version: "1.0.39", Scope: "import"}, {GroupID: "org.json", ArtifactID: "json", Version: "20231013"}, {ArtifactID: "ch.qos.logback", GroupID: "logback-core", Version: "[1.4.12,2.0.0)"}, {GroupID: "com.azure", ArtifactID: "azure-sdk-bom", Version: "1.2.19", Type: "pom", Scope: "import"}},
	}, {
		// This patches existing dependencies in a project, but they are
		// specified in the 'properties' section.
		name:      "zookeeper - properties patch",
		in:        "zookeeper.pom.xml",
		props:     map[string]string{"logback-version": "1.2.13", "jetty.version": "9.4.53.v20231009"},
		wantProps: map[string]string{"logback-version": "1.2.13", "jetty.version": "9.4.53.v20231009"},
	}, {
		// This patches existing dependency in a project
		name:     "cloudwatch-exporter - dependency patch - existing",
		in:       "cloudwatch-exporter.pom.xml",
		patches:  []Patch{{GroupID: "org.eclipse.jetty", ArtifactID: "jetty-servlet", Version: "11.0.16"}},
		wantDeps: []Patch{{GroupID: "org.eclipse.jetty", ArtifactID: "jetty-servlet", Version: "11.0.16"}},
	}, {
		// This patches existing dependency in a project
		name:       "common-docker - nil DependencyManagement",
		in:         "common-docker.pom.xml",
		patches:    []Patch{{GroupID: "org.bitbucket.b_c", ArtifactID: "jose4j", Version: "0.9.6"}},
		wantDMDeps: []Patch{{GroupID: "org.bitbucket.b_c", ArtifactID: "jose4j", Version: "0.9.6"}},
	}}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			parsedPom, err := gopom.Parse(fmt.Sprintf("testdata/%s", tc.in))
			if err != nil {
				t.Fatal(err)
			}
			got, err := PatchProject(context.Background(), parsedPom, tc.patches, tc.props)
			if err != nil {
				t.Errorf("%s: Failed to patch %s: %v", tc.name, tc.in, err)
			}

			checkDependencies(t, got, tc.wantDeps)
			checkDMDependencies(t, got, tc.wantDMDeps)
			checkProps(t, got, tc.wantProps)
			t.Logf("Doing the second checks!!!")
			checkDependencies(t, parsedPom, tc.wantDeps)
			checkDMDependencies(t, parsedPom, tc.wantDMDeps)
			checkProps(t, parsedPom, tc.wantProps)
		})
	}
}

// This is a helper function to check dependencies in a list of dependencies.
// Because the deps can live in 'explicit depencies' or
// 'dependencyManagement.dependencies', this just shares the main loop.
func checkDeps(t *testing.T, indeps *[]gopom.Dependency, wantdeps []Patch) {
	if indeps == nil {
		if len(wantdeps) > 0 {
			t.Errorf("dependencies is nil but there are (%d) expected dependencies", len(wantdeps))
		}
		return
	}

	// In addition to version mismatches, make sure we are not missing any
	// dependencies that should be there. Knock them off of this when we find
	// them, regardless of whether the version is matched or not.
	missing := make(map[Patch]Patch, len(wantdeps))
	for _, p := range wantdeps {
		missing[p] = p
	}
	for _, dep := range *indeps {
		for _, patch := range wantdeps {
			if dep.ArtifactID == patch.ArtifactID &&
				dep.GroupID == patch.GroupID {
				if dep.Version != patch.Version {
					t.Errorf("dep %s.%s version %s != %s", patch.GroupID, patch.ArtifactID, dep.Version, patch.Version)
				}
				if dep.Scope != patch.Scope {
					t.Errorf("dep %s.%s scope %s != %s", patch.GroupID, patch.ArtifactID, dep.Scope, patch.Scope)
				}
				if dep.Type != patch.Type {
					t.Errorf("dep %s.%s type %s != %s", patch.GroupID, patch.ArtifactID, dep.Type, patch.Type)
				}
				delete(missing, patch)
			}
		}
	}
	if len(missing) > 0 {
		t.Errorf("missing dependencies: %+v", missing)
	}
}

func checkDependencies(t *testing.T, project *gopom.Project, deps []Patch) {
	checkDeps(t, project.Dependencies, deps)
}

func checkDMDependencies(t *testing.T, project *gopom.Project, deps []Patch) {
	if project.DependencyManagement == nil || project.DependencyManagement.Dependencies == nil && len(deps) > 0 {
		return
	}
	checkDeps(t, project.DependencyManagement.Dependencies, deps)
}

func checkProps(t *testing.T, project *gopom.Project, props map[string]string) {
	for k, v := range props {
		if project.Properties.Entries[k] != v {
			t.Errorf("property %s value %s != %s", k, project.Properties.Entries[k], v)
		}
	}
}

func TestNilPointerDereferenceDependenciesRegression(t *testing.T) {
	// Test the specific case that caused the nil pointer panic:
	// zipkin.pom.xml has a dependencyManagement section but no dependencies element
	project, err := gopom.Parse("testdata/zipkin.pom.xml")
	if err != nil {
		t.Fatalf("Failed to parse zipkin.pom.xml: %v", err)
	}

	patches, err := parsePatches(context.Background(), "testdata/zipkin-pombump-deps.yaml", "")
	if err != nil {
		t.Fatalf("Failed to parse zipkin-pombump-deps.yaml: %v", err)
	}

	// This should not panic
	_, err = PatchProject(context.Background(), project, patches, nil)
	if err != nil {
		t.Errorf("PatchProject failed: %v", err)
	}
}

func lessPatch(a, b Patch) bool {
	return a.ArtifactID < b.ArtifactID && a.GroupID < b.GroupID && a.Version < b.Version && a.Scope < b.Scope
}

func TestParsePatches(t *testing.T) {
	testCases := []struct {
		name    string
		inFile  string
		inDeps  string
		want    []Patch
		wantErr bool
	}{{
		name:   "no file",
		inFile: "",
		inDeps: "",
		want:   []Patch{},
	}, {
		name:    "file not found",
		inFile:  "testdata/missing",
		wantErr: true,
	}, {
		name:   "file",
		inFile: "testdata/patches.yaml",
		inDeps: "",
		want: []Patch{{
			GroupID:    "groupid-2",
			ArtifactID: "artifactid-2",
			Version:    "2.0.0",
			Scope:      "scope-2",
			Type:       "jar", // defaulted
		}, {
			GroupID:    "groupid-1",
			ArtifactID: "artifactid-1",
			Version:    "1.0.0",
			Scope:      "import", // defaulted
			Type:       "pom",
		}, {
			GroupID:    "groupid-3",
			ArtifactID: "artifactid-3",
			Version:    "3.0.0",
			Scope:      "import", // Defaulted
			Type:       "somethingelse",
		}},
	}, {
		name:   "file - trino",
		inFile: "testdata/trino-patches.yaml",
		inDeps: "",
		want: []Patch{{
			GroupID:    "io.projectreactor.netty",
			ArtifactID: "reactor-netty-http",
			Version:    "1.0.39",
			Scope:      "import", // defaulted
			Type:       "jar",    // defaulted
		}, {
			GroupID:    "org.json",
			ArtifactID: "json",
			Version:    "20231013",
			Scope:      "import",
			Type:       "jar",
		}, {
			GroupID:    "ch.qos.logback",
			ArtifactID: "logback-core",
			Version:    "[1.4.12,2.0.0)",
			Scope:      "import", // defaulted
			Type:       "jar",    // defaulted
		}},
	}, {
		name:    "invalid flag",
		inDeps:  "g1@a1 g2",
		wantErr: true,
	}, {
		name:   "flag",
		inFile: "",
		inDeps: "g1@a1@v1 g2@a2@v2@scope-2 g3@a3@v3@scope-3@type-3",
		want: []Patch{{
			GroupID:    "g2",
			ArtifactID: "a2",
			Version:    "v2",
			Scope:      "scope-2",
			Type:       "jar", // default
		}, {
			GroupID:    "g3",
			ArtifactID: "a3",
			Version:    "v3",
			Scope:      "scope-3", // default
			Type:       "type-3",
		}, {
			GroupID:    "g1",
			ArtifactID: "a1",
			Version:    "v1",
			Scope:      "import", // default
			Type:       "jar",    // default
		}},
	}}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePatches(context.Background(), tc.inFile, tc.inDeps)
			if (err != nil) != tc.wantErr {
				t.Errorf("%s: parsePatches(%s, %s) = %v)", tc.name, tc.inFile, tc.inDeps, err)
			}
			// We don't care about the order of the patches
			if diff := cmp.Diff(tc.want, got, cmpopts.SortSlices(lessPatch)); diff != "" {
				t.Errorf("%s: parsePatches(%s, %s) (-got +want)\n%s", tc.name, tc.inFile, tc.inDeps, diff)
			}
		})
	}
}

func TestParseProperties(t *testing.T) {
	testCases := []struct {
		name    string
		inFile  string
		inProps string
		want    map[string]string
		wantErr bool
	}{{
		name:    "no file",
		inFile:  "",
		inProps: "",
		want:    map[string]string{},
	}, {
		name:    "file not found",
		inFile:  "testdata/missing",
		wantErr: true,
	}, {
		name:    "file",
		inFile:  "testdata/properties.yaml",
		inProps: "",
		want: map[string]string{
			"prop2": "value2",
			"prop1": "value1",
		},
	}, {
		name:    "flag",
		inFile:  "",
		inProps: "key-1@value-1 key-2@value-2",
		want: map[string]string{
			"key-1": "value-1",
			"key-2": "value-2",
		},
	}, {
		name:    "invalid flag",
		inFile:  "",
		inProps: "key-1",
		wantErr: true,
	}}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseProperties(context.Background(), tc.inFile, tc.inProps)
			if (err != nil) != tc.wantErr {
				t.Errorf("%s: parseProperties(%s, %s) = %v)", tc.name, tc.inFile, tc.inProps, err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("%s: parseProperties(%s, %s) (-got +want)\n%s", tc.name, tc.inFile, tc.inProps, diff)
			}
		})
	}
}

// TestParsePatches_NonExistentFile tests error handling for missing patch files (FINDING-003).
func TestParsePatches_NonExistentFile(t *testing.T) {
	_, err := parsePatches(context.Background(), "testdata/non-existent-patches.yaml", "")
	if err == nil {
		t.Fatal("parsePatches should return error for non-existent file")
	}
	if err.Error() == "" {
		t.Error("Error message should not be empty")
	}
}

// TestParseProperties_NonExistentFile tests error handling for missing property files (FINDING-003).
func TestParseProperties_NonExistentFile(t *testing.T) {
	_, err := parseProperties(context.Background(), "testdata/non-existent-properties.yaml", "")
	if err == nil {
		t.Fatal("parseProperties should return error for non-existent file")
	}
	if err.Error() == "" {
		t.Error("Error message should not be empty")
	}
}

// TestParsePatches_InvalidYAML tests error handling for invalid YAML in patch files.
func TestParsePatches_InvalidYAML(t *testing.T) {
	// Create a temporary file with invalid YAML
	tmpFile, err := os.CreateTemp("", "invalid-patches-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer func() {
		_ = os.Remove(tmpFile.Name())
	}()

	_, err = tmpFile.WriteString("invalid: yaml: content: [")
	if err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("Failed to close temp file: %v", err)
	}

	_, err = parsePatches(context.Background(), tmpFile.Name(), "")
	if err == nil {
		t.Fatal("parsePatches should return error for invalid YAML")
	}
}

// TestParseProperties_InvalidYAML tests error handling for invalid YAML in property files.
func TestParseProperties_InvalidYAML(t *testing.T) {
	// Create a temporary file with invalid YAML
	tmpFile, err := os.CreateTemp("", "invalid-properties-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer func() {
		_ = os.Remove(tmpFile.Name())
	}()

	_, err = tmpFile.WriteString("invalid: yaml: content: [")
	if err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("Failed to close temp file: %v", err)
	}

	_, err = parseProperties(context.Background(), tmpFile.Name(), "")
	if err == nil {
		t.Fatal("parseProperties should return error for invalid YAML")
	}
}

// TestValidateVersion_ValidVersions tests that valid version strings pass validation.
func TestValidateVersion_ValidVersions(t *testing.T) {
	validVersions := []string{
		"1.0.0",
		"2.3.4-SNAPSHOT",
		"1.2.3.4",
		"5.0.0.Final",
		"1.0-alpha",
		"2.0+build.123",
		"3.0_rc1",
		"1.0.0-rc1+build.456",
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

// TestValidateVersion_InvalidVersions tests that invalid version strings are rejected.
// This includes XML injection payloads and other malicious strings.
func TestValidateVersion_InvalidVersions(t *testing.T) {
	tests := []struct {
		version string
		desc    string
	}{
		// XML injection payloads
		{`<script>alert(1)</script>`, "XSS payload"},
		{`"><script>alert(1)</script>`, "XSS with quote escape"},
		{`1.0.0" /><!--`, "XML comment injection"},
		{`1.0.0"><dependency><groupId>evil`, "XML tag injection"},
		{`${env.SECRET}`, "Property expansion injection"},

		// Command injection attempts
		{`1.0.0; rm -rf /`, "Command injection"},
		{`1.0.0 && malicious`, "Command chaining"},
		{`1.0.0|cat /etc/passwd`, "Pipe injection"},
		{"`whoami`", "Backtick injection"},

		// Path traversal and special characters
		{`../../../etc/passwd`, "Path traversal"},
		{`C:\Windows\System32`, "Windows path"},
		{`1.0.0\n<evil/>`, "Newline injection"},
		{`1.0.0\r\n<evil/>`, "CRLF injection"},

		// Quotes and braces (XML special characters)
		{`1.0.0"`, "Double quote"},
		{`1.0.0'`, "Single quote"},
		{`1.0.0{}`, "Curly braces"},
		{`1.0.0[]`, "Square brackets"},
		{`1.0.0()`, "Parentheses"},
		{`1.0.0<>`, "Angle brackets"},

		// Whitespace and control characters
		{`1.0.0 with spaces`, "Spaces"},
		{`1.0.0	tab`, "Tab character"},
		{"1.0.0\n", "Newline"},
		{"1.0.0\r", "Carriage return"},
		{"1.0.0\x00", "Null byte"},

		// Empty and special values
		{"", "Empty string"},
		{" ", "Space only"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			err := validateVersion(tt.version)
			if err == nil {
				t.Errorf("validateVersion(%q) should be invalid for: %s", tt.version, tt.desc)
			}
			if err != nil && err.Error() == "" {
				t.Errorf("validateVersion(%q) error should have a message", tt.version)
			}
		})
	}
}

// TestMaven_Update_RejectsInvalidVersion verifies that Maven.Update() rejects invalid versions
// and leaves the POM file unchanged.
func TestMaven_Update_RejectsInvalidVersion(t *testing.T) {
	tmpDir := t.TempDir()

	// Create initial POM with known content
	initialPOM := `<?xml version="1.0" encoding="UTF-8"?>
<project>
  <groupId>com.example</groupId>
  <artifactId>test-project</artifactID>
  <version>1.0.0</version>
  <dependencies>
    <dependency>
      <groupId>org.example</groupId>
      <artifactId>test-dep</artifactId>
      <version>1.0.0</version>
    </dependency>
  </dependencies>
</project>`

	pomPath := fmt.Sprintf("%s/pom.xml", tmpDir)
	if err := os.WriteFile(pomPath, []byte(initialPOM), 0o600); err != nil {
		t.Fatalf("Failed to write initial POM: %v", err)
	}

	// Attempt to update with invalid version
	m := &Maven{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{
				Name:    "org.example:test-dep",
				Version: `1.0.0"><script>alert(1)</script>`, // XML injection attempt
				Metadata: map[string]any{
					"groupId":    "org.example",
					"artifactId": "test-dep",
				},
			},
		},
	}

	err := m.Update(context.Background(), cfg)
	if err == nil {
		t.Fatal("Maven.Update() should reject invalid version")
	}

	// Verify POM file is unchanged
	updatedContent, err := os.ReadFile(pomPath)
	if err != nil {
		t.Fatalf("Failed to read POM after update attempt: %v", err)
	}

	if string(updatedContent) != initialPOM {
		t.Error("POM file should be unchanged after rejected update")
	}
}
