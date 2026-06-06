/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package maven

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	}, {
		name: "auto-resolve property: dep uses property ref, no --properties passed",
		in: &gopom.Project{
			Properties:   &gopom.Properties{Entries: map[string]string{"log4j2.version": "2.19.0"}},
			Dependencies: &[]gopom.Dependency{makeDep("org.apache.logging.log4j", "log4j-core", "${log4j2.version}")},
		},
		patches: []Patch{{GroupID: "org.apache.logging.log4j", ArtifactID: "log4j-core", Version: "2.20.0", Scope: defaultScope, Type: defaultType}},
		want: &gopom.Project{
			Properties:   &gopom.Properties{Entries: map[string]string{"log4j2.version": "2.20.0"}},
			Dependencies: &[]gopom.Dependency{makeDep("org.apache.logging.log4j", "log4j-core", "${log4j2.version}")},
		},
	}, {
		name: "auto-resolve property: dep uses property ref, no existing Properties in project",
		in: &gopom.Project{
			Dependencies: &[]gopom.Dependency{makeDep("org.apache.logging.log4j", "log4j-core", "${log4j2.version}")},
		},
		patches: []Patch{{GroupID: "org.apache.logging.log4j", ArtifactID: "log4j-core", Version: "2.20.0", Scope: defaultScope, Type: defaultType}},
		want: &gopom.Project{
			Properties:   &gopom.Properties{Entries: map[string]string{"log4j2.version": "2.20.0"}},
			Dependencies: &[]gopom.Dependency{makeDep("org.apache.logging.log4j", "log4j-core", "${log4j2.version}")},
		},
	}, {
		name: "explicit --properties wins over auto-resolved value",
		in: &gopom.Project{
			Properties:   &gopom.Properties{Entries: map[string]string{"log4j2.version": "2.19.0"}},
			Dependencies: &[]gopom.Dependency{makeDep("org.apache.logging.log4j", "log4j-core", "${log4j2.version}")},
		},
		patches: []Patch{{GroupID: "org.apache.logging.log4j", ArtifactID: "log4j-core", Version: "2.20.0", Scope: defaultScope, Type: defaultType}},
		props:   map[string]string{"log4j2.version": "2.20.1"},
		want: &gopom.Project{
			Properties:   &gopom.Properties{Entries: map[string]string{"log4j2.version": "2.20.1"}},
			Dependencies: &[]gopom.Dependency{makeDep("org.apache.logging.log4j", "log4j-core", "${log4j2.version}")},
		},
	}, {
		name: "auto-resolve property: two deps share same property, property set once",
		in: &gopom.Project{
			Properties: &gopom.Properties{Entries: map[string]string{"jackson.version": "2.14.0"}},
			Dependencies: &[]gopom.Dependency{
				makeDep("com.fasterxml.jackson.core", "jackson-core", "${jackson.version}"),
				makeDep("com.fasterxml.jackson.core", "jackson-databind", "${jackson.version}"),
			},
		},
		patches: []Patch{
			{GroupID: "com.fasterxml.jackson.core", ArtifactID: "jackson-core", Version: "2.15.0", Scope: defaultScope, Type: defaultType},
			{GroupID: "com.fasterxml.jackson.core", ArtifactID: "jackson-databind", Version: "2.15.0", Scope: defaultScope, Type: defaultType},
		},
		want: &gopom.Project{
			Properties: &gopom.Properties{Entries: map[string]string{"jackson.version": "2.15.0"}},
			Dependencies: &[]gopom.Dependency{
				makeDep("com.fasterxml.jackson.core", "jackson-core", "${jackson.version}"),
				makeDep("com.fasterxml.jackson.core", "jackson-databind", "${jackson.version}"),
			},
		},
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
		wantDeps: []Patch{{GroupID: "org.eclipse.jetty", ArtifactID: "jetty-servlet", Version: "11.0.18"}},
	}, {
		// This patches existing dependency in a project
		name:       "common-docker - nil DependencyManagement",
		in:         "common-docker.pom.xml",
		patches:    []Patch{{GroupID: "org.bitbucket.b_c", ArtifactID: "jose4j", Version: "0.9.6"}},
		wantDMDeps: []Patch{{GroupID: "org.bitbucket.b_c", ArtifactID: "jose4j", Version: "0.9.6"}},
	}, {
		// logback-core uses ${logback-version}; no --properties passed — property
		// should be auto-resolved from the dep patch.
		name:      "zookeeper - auto-resolve property from dep patch",
		in:        "zookeeper.pom.xml",
		patches:   []Patch{{GroupID: "ch.qos.logback", ArtifactID: "logback-core", Version: "1.2.13"}},
		wantProps: map[string]string{"logback-version": "1.2.13"},
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
		// Maven version ranges
		"[1.4.12,2.0.0)",
		"[1.0,2.0]",
		"(1.0,2.0)",
		"(,1.0]",
		"[1.0,)",
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

// TestDepDisplayName verifies that depDisplayName returns the expected identifier.
func TestDepDisplayName(t *testing.T) {
	tests := []struct {
		dep  languages.Dependency
		want string
	}{
		{
			dep:  languages.Dependency{Metadata: map[string]any{"groupId": "org.example", "artifactId": "mylib"}},
			want: "org.example:mylib",
		},
		{
			dep:  languages.Dependency{Name: "some-module", Metadata: map[string]any{}},
			want: "some-module",
		},
		{
			dep:  languages.Dependency{Metadata: map[string]any{}},
			want: "<unknown>",
		},
	}
	for _, tt := range tests {
		got := depDisplayName(tt.dep)
		if got != tt.want {
			t.Errorf("depDisplayName(%+v) = %q, want %q", tt.dep, got, tt.want)
		}
	}
}

// TestMaven_Update_EmptyVersionPreservesAndAdds verifies that Update() handles
// dependencies with empty versions correctly:
//   - An empty-version dep that ALREADY EXISTS in the POM has its version preserved
//     (the existing version is not overwritten with "").
//   - An empty-version dep that is ABSENT from the POM is added to DependencyManagement
//     without a <version> element (Maven exclusion-by-provided-scope trick).
func TestMaven_Update_EmptyVersionPreservesAndAdds(t *testing.T) {
	tmpDir := t.TempDir()

	initialPOM := `<?xml version="1.0" encoding="UTF-8"?>
	<project>
	  <groupId>com.example</groupId>
	  <artifactId>test-project</artifactId>
	  <version>1.0.0</version>
	  <dependencies>
	    <dependency>
	      <groupId>org.example</groupId>
	      <artifactId>real-dep</artifactId>
	      <version>1.0.0</version>
	    </dependency>
	    <dependency>
	      <groupId>javax.servlet</groupId>
	      <artifactId>javax.servlet-api</artifactId>
	      <version>4.0.1</version>
	      <scope>provided</scope>
	    </dependency>
	  </dependencies>
	</project>`

	pomPath := tmpDir + "/pom.xml"
	if err := os.WriteFile(pomPath, []byte(initialPOM), 0o600); err != nil {
		t.Fatalf("Failed to write initial POM: %v", err)
	}

	m := &Maven{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{
				// Normal bump.
				Version: "1.0.1",
				Metadata: map[string]any{
					"groupId":    "org.example",
					"artifactId": "real-dep",
				},
			},
			{
				// Existing dep with no version — existing version must be preserved.
				Version: "",
				Scope:   "provided",
				Metadata: map[string]any{
					"groupId":    "javax.servlet",
					"artifactId": "javax.servlet-api",
				},
			},
			{
				// Absent dep with no version — added to DependencyManagement without <version>
				// (Maven exclusion-by-provided-scope trick for relocated artifacts).
				Version: "",
				Scope:   "provided",
				Metadata: map[string]any{
					"groupId":    "old.groupid",
					"artifactId": "relocated-artifact",
				},
			},
		},
	}

	if err := m.Update(t.Context(), cfg); err != nil {
		t.Fatalf("Maven.Update() should not error for empty-version dep, got: %v", err)
	}

	// Verify the POM was written and existing version was not clobbered.
	project, err := ParsePom(pomPath)
	if err != nil {
		t.Fatalf("Failed to parse updated POM: %v", err)
	}

	// real-dep must be bumped to 1.0.1.
	for _, dep := range *project.Dependencies {
		if dep.GroupID == "org.example" && dep.ArtifactID == "real-dep" {
			if dep.Version != "1.0.1" {
				t.Errorf("real-dep version = %q, want 1.0.1", dep.Version)
			}
		}
		// javax.servlet-api version must be preserved (not blanked).
		if dep.GroupID == "javax.servlet" && dep.ArtifactID == "javax.servlet-api" {
			if dep.Version != "4.0.1" {
				t.Errorf("javax.servlet-api version = %q, want 4.0.1 (must not be overwritten)", dep.Version)
			}
		}
	}

	// relocated-artifact must be present in DependencyManagement with scope provided and no version.
	if project.DependencyManagement == nil || project.DependencyManagement.Dependencies == nil {
		t.Fatal("DependencyManagement should not be nil after adding relocated-artifact")
	}
	found := false
	for _, dep := range *project.DependencyManagement.Dependencies {
		if dep.GroupID == "old.groupid" && dep.ArtifactID == "relocated-artifact" {
			found = true
			if dep.Version != "" {
				t.Errorf("relocated-artifact version = %q, want empty (omitted)", dep.Version)
			}
			if dep.Scope != "provided" {
				t.Errorf("relocated-artifact scope = %q, want provided", dep.Scope)
			}
		}
	}
	if !found {
		t.Error("relocated-artifact was not added to DependencyManagement")
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

// TestMaven_Update_CustomManifestPath verifies that Update() uses the path supplied via
// cfg.Options["manifestFile"] rather than the default <RootDir>/pom.xml.
func TestMaven_Update_CustomManifestPath(t *testing.T) {
	tmpDir := t.TempDir()

	// Place the POM at a non-default path; leave RootDir empty of any pom.xml.
	customPath := tmpDir + "/subdir/custom.xml"
	if err := os.MkdirAll(tmpDir+"/subdir", 0o755); err != nil {
		t.Fatalf("Failed to create subdir: %v", err)
	}

	initialPOM := `<?xml version="1.0" encoding="UTF-8"?>
	<project>
	  <groupId>com.example</groupId>
	  <artifactId>test-project</artifactId>
	  <version>1.0.0</version>
	  <dependencies>
	    <dependency>
	      <groupId>com.example</groupId>
	      <artifactId>artifact</artifactId>
	      <version>1.0.0</version>
	    </dependency>
	  </dependencies>
	</project>`

	if err := os.WriteFile(customPath, []byte(initialPOM), 0o600); err != nil {
		t.Fatalf("Failed to write POM: %v", err)
	}

	m := &Maven{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir, // no pom.xml here — would fail without the manifest override
		Dependencies: []languages.Dependency{
			{
				Version: "2.0.0",
				Metadata: map[string]any{
					"groupId":    "com.example",
					"artifactId": "artifact",
				},
			},
		},
		ManifestFile: customPath,
	}

	if err := m.Update(context.Background(), cfg); err != nil {
		t.Fatalf("Maven.Update() with custom manifest path failed: %v", err)
	}

	project, err := ParsePom(customPath)
	if err != nil {
		t.Fatalf("Failed to parse updated POM: %v", err)
	}
	for _, dep := range *project.Dependencies {
		if dep.GroupID == "com.example" && dep.ArtifactID == "artifact" {
			if dep.Version != "2.0.0" {
				t.Errorf("artifact version = %q, want 2.0.0", dep.Version)
			}
			return
		}
	}
	t.Error("dependency com.example:artifact not found in updated POM (CustomManifestPath)")
}

// TestMaven_Update_DefaultFallback verifies that Update() still uses <RootDir>/pom.xml
// when no "manifestFile" option is set (regression guard).
func TestMaven_Update_DefaultFallback(t *testing.T) {
	tmpDir := t.TempDir()

	initialPOM := `<?xml version="1.0" encoding="UTF-8"?>
	<project>
	  <groupId>com.example</groupId>
	  <artifactId>test-project</artifactId>
	  <version>1.0.0</version>
	  <dependencies>
	    <dependency>
	      <groupId>com.example</groupId>
	      <artifactId>artifact</artifactId>
	      <version>1.0.0</version>
	    </dependency>
	  </dependencies>
	</project>`

	if err := os.WriteFile(tmpDir+"/pom.xml", []byte(initialPOM), 0o600); err != nil {
		t.Fatalf("Failed to write POM: %v", err)
	}

	m := &Maven{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{
				Version: "3.0.0",
				Metadata: map[string]any{
					"groupId":    "com.example",
					"artifactId": "artifact",
				},
			},
		},
		// No Options set — must fall back to <RootDir>/pom.xml
	}

	if err := m.Update(context.Background(), cfg); err != nil {
		t.Fatalf("Maven.Update() with default path failed: %v", err)
	}

	project, err := ParsePom(tmpDir + "/pom.xml")
	if err != nil {
		t.Fatalf("Failed to parse updated POM: %v", err)
	}
	for _, dep := range *project.Dependencies {
		if dep.GroupID == "com.example" && dep.ArtifactID == "artifact" {
			if dep.Version != "3.0.0" {
				t.Errorf("artifact version = %q, want 3.0.0", dep.Version)
			}
			return
		}
	}
	t.Error("dependency com.example:artifact not found in updated POM (DefaultFallback)")
}

func TestMaven_Validate_ReturnsErrorWhenPropertyMissing(t *testing.T) {
	tmpDir := t.TempDir()
	writeFile(t, filepath.Join(tmpDir, "pom.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>test-project</artifactId>
  <version>1.0.0</version>
</project>`)

	m := &Maven{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Properties: map[string]string{
			"missing.version": "1.2.3",
		},
	}

	err := m.Validate(context.Background(), cfg)
	if !errors.Is(err, ErrPropertyNotFound) {
		t.Fatalf("Maven.Validate() error = %v, want ErrPropertyNotFound", err)
	}
	if !strings.Contains(err.Error(), "missing.version") {
		t.Fatalf("Maven.Validate() error = %q, want to contain missing property name", err)
	}
}

func TestMaven_Update_ExplicitPropertyDefinedInParent(t *testing.T) {
	tmpDir := t.TempDir()
	parentPom := filepath.Join(tmpDir, "pom.xml")
	moduleDir := filepath.Join(tmpDir, "module")
	modulePom := filepath.Join(moduleDir, "pom.xml")

	writeFile(t, parentPom, `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>parent</artifactId>
  <version>1.0.0</version>
  <packaging>pom</packaging>
  <properties>
    <netty.version>4.1.90.Final</netty.version>
  </properties>
</project>`)
	writeFile(t, modulePom, `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>com.example</groupId>
    <artifactId>parent</artifactId>
    <version>1.0.0</version>
  </parent>
  <artifactId>module</artifactId>
  <dependencies>
    <dependency>
      <groupId>io.netty</groupId>
      <artifactId>netty-codec-http</artifactId>
      <version>${netty.version}</version>
    </dependency>
  </dependencies>
</project>`)

	m := &Maven{}
	cfg := &languages.UpdateConfig{
		RootDir:      tmpDir,
		ManifestFile: modulePom,
		Properties: map[string]string{
			"netty.version": "4.1.94.Final",
		},
	}

	if err := m.Update(context.Background(), cfg); err != nil {
		t.Fatalf("Maven.Update() failed: %v", err)
	}
	if err := m.Validate(context.Background(), cfg); err != nil {
		t.Fatalf("Maven.Validate() failed: %v", err)
	}

	parent, err := ParsePom(parentPom)
	if err != nil {
		t.Fatalf("ParsePom(parent) failed: %v", err)
	}
	if got := parent.Properties.Entries["netty.version"]; got != "4.1.94.Final" {
		t.Errorf("parent netty.version = %q, want 4.1.94.Final", got)
	}

	module, err := ParsePom(modulePom)
	if err != nil {
		t.Fatalf("ParsePom(module) failed: %v", err)
	}
	if module.Properties != nil {
		if _, exists := module.Properties.Entries["netty.version"]; exists {
			t.Error("module POM should not get a shadowing netty.version property")
		}
	}
}

func TestMaven_Update_DependencyPropertyDefinedInParent(t *testing.T) {
	tmpDir := t.TempDir()
	parentPom := filepath.Join(tmpDir, "pom.xml")
	moduleDir := filepath.Join(tmpDir, "module")
	modulePom := filepath.Join(moduleDir, "pom.xml")

	writeFile(t, parentPom, `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>parent</artifactId>
  <version>1.0.0</version>
  <packaging>pom</packaging>
  <properties>
    <netty.version>4.1.90.Final</netty.version>
    <jackson.version>2.14.0</jackson.version>
  </properties>
</project>`)
	writeFile(t, modulePom, `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>com.example</groupId>
    <artifactId>parent</artifactId>
    <version>1.0.0</version>
  </parent>
  <artifactId>module</artifactId>
  <dependencyManagement>
    <dependencies>
      <dependency>
        <groupId>io.netty</groupId>
        <artifactId>netty-codec</artifactId>
        <version>${netty.version}</version>
      </dependency>
      <dependency>
        <groupId>io.netty</groupId>
        <artifactId>netty-codec-http</artifactId>
        <version>${netty.version}</version>
      </dependency>
      <dependency>
        <groupId>com.fasterxml.jackson.core</groupId>
        <artifactId>jackson-core</artifactId>
        <version>${jackson.version}</version>
      </dependency>
    </dependencies>
  </dependencyManagement>
</project>`)

	cfg := &languages.UpdateConfig{
		RootDir:      tmpDir,
		ManifestFile: modulePom,
		Dependencies: []languages.Dependency{
			{Name: "io.netty:netty-codec", Version: "4.1.94.Final"},
			{Name: "io.netty:netty-codec-http", Version: "4.1.94.Final"},
			{Name: "com.fasterxml.jackson.core:jackson-core", Version: "2.15.0"},
		},
	}

	m := &Maven{}
	if err := m.Update(context.Background(), cfg); err != nil {
		t.Fatalf("Maven.Update() failed: %v", err)
	}
	if err := m.Validate(context.Background(), cfg); err != nil {
		t.Fatalf("Maven.Validate() failed: %v", err)
	}

	parent, err := ParsePom(parentPom)
	if err != nil {
		t.Fatalf("ParsePom(parent) failed: %v", err)
	}
	if got := parent.Properties.Entries["netty.version"]; got != "4.1.94.Final" {
		t.Errorf("parent netty.version = %q, want 4.1.94.Final", got)
	}
	if got := parent.Properties.Entries["jackson.version"]; got != "2.15.0" {
		t.Errorf("parent jackson.version = %q, want 2.15.0", got)
	}

	module, err := ParsePom(modulePom)
	if err != nil {
		t.Fatalf("ParsePom(module) failed: %v", err)
	}
	if module.Properties != nil {
		if _, exists := module.Properties.Entries["netty.version"]; exists {
			t.Error("module POM should not get a shadowing netty.version property")
		}
		if _, exists := module.Properties.Entries["jackson.version"]; exists {
			t.Error("module POM should not get a shadowing jackson.version property")
		}
	}
	for _, dep := range *module.DependencyManagement.Dependencies {
		switch dep.ArtifactID {
		case "netty-codec", "netty-codec-http":
			if dep.Version != "${netty.version}" {
				t.Errorf("%s version = %q, want ${netty.version}", dep.ArtifactID, dep.Version)
			}
		case "jackson-core":
			if dep.Version != "${jackson.version}" {
				t.Errorf("jackson-core version = %q, want ${jackson.version}", dep.Version)
			}
		}
	}
}

func TestMaven_Update_DependencyPropertyDefinedInGrandparent(t *testing.T) {
	tmpDir := t.TempDir()
	rootPom := filepath.Join(tmpDir, "pom.xml")
	parentPom := filepath.Join(tmpDir, "module-parent", "pom.xml")
	modulePom := filepath.Join(tmpDir, "module", "pom.xml")

	writeFile(t, rootPom, `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>root-parent</artifactId>
  <version>1.0.0</version>
  <packaging>pom</packaging>
  <properties>
    <netty.version>4.1.90.Final</netty.version>
  </properties>
</project>`)
	writeFile(t, parentPom, `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>com.example</groupId>
    <artifactId>root-parent</artifactId>
    <version>1.0.0</version>
    <relativePath>../pom.xml</relativePath>
  </parent>
  <artifactId>module-parent</artifactId>
  <packaging>pom</packaging>
</project>`)
	writeFile(t, modulePom, `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>com.example</groupId>
    <artifactId>module-parent</artifactId>
    <version>1.0.0</version>
    <relativePath>../module-parent/pom.xml</relativePath>
  </parent>
  <artifactId>module</artifactId>
  <dependencyManagement>
    <dependencies>
      <dependency>
        <groupId>io.netty</groupId>
        <artifactId>netty-codec</artifactId>
        <version>${netty.version}</version>
      </dependency>
    </dependencies>
  </dependencyManagement>
</project>`)

	cfg := &languages.UpdateConfig{
		RootDir:      tmpDir,
		ManifestFile: modulePom,
		Dependencies: []languages.Dependency{
			{Name: "io.netty:netty-codec", Version: "4.1.94.Final"},
		},
	}

	m := &Maven{}
	if err := m.Update(context.Background(), cfg); err != nil {
		t.Fatalf("Maven.Update() failed: %v", err)
	}
	if err := m.Validate(context.Background(), cfg); err != nil {
		t.Fatalf("Maven.Validate() failed: %v", err)
	}

	root, err := ParsePom(rootPom)
	if err != nil {
		t.Fatalf("ParsePom(root) failed: %v", err)
	}
	if got := root.Properties.Entries["netty.version"]; got != "4.1.94.Final" {
		t.Errorf("root netty.version = %q, want 4.1.94.Final", got)
	}

	parent, err := ParsePom(parentPom)
	if err != nil {
		t.Fatalf("ParsePom(parent) failed: %v", err)
	}
	if parent.Properties != nil {
		if _, exists := parent.Properties.Entries["netty.version"]; exists {
			t.Error("parent POM should not get a shadowing netty.version property")
		}
	}

	module, err := ParsePom(modulePom)
	if err != nil {
		t.Fatalf("ParsePom(module) failed: %v", err)
	}
	if module.Properties != nil {
		if _, exists := module.Properties.Entries["netty.version"]; exists {
			t.Error("module POM should not get a shadowing netty.version property")
		}
	}
	if got := (*module.DependencyManagement.Dependencies)[0].Version; got != "${netty.version}" {
		t.Errorf("module dependency version = %q, want ${netty.version}", got)
	}
}

func TestMaven_Update_DependencySharedPropertyConflictErrors(t *testing.T) {
	tmpDir := t.TempDir()
	moduleDir := filepath.Join(tmpDir, "module")

	writeFile(t, filepath.Join(tmpDir, "pom.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>parent</artifactId>
  <version>1.0.0</version>
  <packaging>pom</packaging>
  <properties>
    <netty.version>4.1.90.Final</netty.version>
  </properties>
</project>`)
	writeFile(t, filepath.Join(moduleDir, "pom.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>com.example</groupId>
    <artifactId>parent</artifactId>
    <version>1.0.0</version>
  </parent>
  <artifactId>module</artifactId>
  <dependencyManagement>
    <dependencies>
      <dependency>
        <groupId>io.netty</groupId>
        <artifactId>netty-codec</artifactId>
        <version>${netty.version}</version>
      </dependency>
      <dependency>
        <groupId>io.netty</groupId>
        <artifactId>netty-codec-http</artifactId>
        <version>${netty.version}</version>
      </dependency>
    </dependencies>
  </dependencyManagement>
</project>`)

	m := &Maven{}
	cfg := &languages.UpdateConfig{
		RootDir:      tmpDir,
		ManifestFile: filepath.Join(moduleDir, "pom.xml"),
		Dependencies: []languages.Dependency{
			{Name: "io.netty:netty-codec", Version: "4.1.130.Final"},
			{Name: "io.netty:netty-codec-http", Version: "4.1.133.Final"},
		},
	}

	err := m.Update(context.Background(), cfg)
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("Maven.Update() error = %v, want ErrVersionConflict", err)
	}
	if !strings.Contains(err.Error(), "netty.version") {
		t.Fatalf("Maven.Update() error = %q, want to mention netty.version", err)
	}
}

func TestMaven_Update_DirectDependencyVersionConflictErrors(t *testing.T) {
	tmpDir := t.TempDir()

	writeFile(t, filepath.Join(tmpDir, "pom.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>module</artifactId>
  <version>1.0.0</version>
  <dependencies>
    <dependency>
      <groupId>com.example</groupId>
      <artifactId>library</artifactId>
      <version>1.0.0</version>
    </dependency>
  </dependencies>
</project>`)

	m := &Maven{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "com.example:library", Version: "1.1.0"},
			{Name: "com.example:library", Version: "1.2.0"},
		},
	}

	err := m.Update(context.Background(), cfg)
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("Maven.Update() error = %v, want ErrVersionConflict", err)
	}
	if !strings.Contains(err.Error(), "com.example:library") {
		t.Fatalf("Maven.Update() error = %q, want to mention com.example:library", err)
	}
}

func TestMavenVersionIsNewer(t *testing.T) {
	tests := []struct {
		current   string
		requested string
		want      bool
	}{
		{"1.84", "1.78", true},
		{"1.78", "1.84", false},
		{"2.21.2", "2.21.0", true},
		{"2.21.0", "2.21.2", false},
		{"4.1.133.Final", "4.1.110.Final", true},
		{"4.1.110.Final", "4.1.133.Final", false},
		{"4.1.133.Final", "4.1.133.Final", false},
		{"1.84", "1.84", false},
		{"13.2.1.jre11", "13.2.0.jre11", true},
		{"13.2.0.jre11", "13.2.1.jre11", false},
		{"4.1.133.Final", "4.1.133", false},
		{"2.0.16.RELEASE", "2.0.13.RELEASE", true},
		{"2.0.16.GA", "2.0.13.GA", true},
		{"4.1.134-SNAPSHOT", "4.1.133.Final", true},
		{"", "1.0.0", false},
		{"1.0.0", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.current+"_vs_"+tt.requested, func(t *testing.T) {
			got := mavenVersionIsNewer(tt.current, tt.requested)
			if got != tt.want {
				t.Errorf("mavenVersionIsNewer(%q, %q) = %v, want %v",
					tt.current, tt.requested, got, tt.want)
			}
		})
	}
}

func TestResolveBOMVersion(t *testing.T) {
	ctx := context.Background()
	const (
		targetGroupID    = "io.opentelemetry"
		targetArtifactID = "opentelemetry-api"
	)

	bomPath := func(m2repo, groupID, artifactID, version string) string {
		groupPath := strings.ReplaceAll(groupID, ".", string(filepath.Separator))
		return filepath.Join(m2repo, groupPath, artifactID, version, artifactID+"-"+version+".pom")
	}

	projectWithBOMImport := func(groupID, artifactID, version string) *gopom.Project {
		deps := []gopom.Dependency{{
			GroupID:    groupID,
			ArtifactID: artifactID,
			Version:    version,
			Type:       "pom",
			Scope:      "import",
		}}
		return &gopom.Project{
			DependencyManagement: &gopom.DependencyManagement{Dependencies: &deps},
		}
	}

	t.Run("BOM not in cache", func(t *testing.T) {
		t.Setenv("M2_REPO", t.TempDir())
		project := projectWithBOMImport("com.example", "test-bom", "1.0.0")

		got, err := resolveBOMVersion(ctx, project, targetGroupID, targetArtifactID)
		if err != nil {
			t.Fatalf("resolveBOMVersion() error = %v", err)
		}
		if got != "" {
			t.Fatalf("resolveBOMVersion() = %q, want empty", got)
		}
	})

	t.Run("BOM in cache dependency found", func(t *testing.T) {
		m2repo := t.TempDir()
		t.Setenv("M2_REPO", m2repo)
		project := projectWithBOMImport("com.example", "test-bom", "1.0.0")

		writeFile(t, bomPath(m2repo, "com.example", "test-bom", "1.0.0"), `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>test-bom</artifactId>
  <version>1.0.0</version>
  <packaging>pom</packaging>
  <dependencyManagement>
    <dependencies>
      <dependency>
        <groupId>io.opentelemetry</groupId>
        <artifactId>opentelemetry-api</artifactId>
        <version>1.57.0</version>
      </dependency>
    </dependencies>
  </dependencyManagement>
</project>`)

		got, err := resolveBOMVersion(ctx, project, targetGroupID, targetArtifactID)
		if err != nil {
			t.Fatalf("resolveBOMVersion() error = %v", err)
		}
		if got != "1.57.0" {
			t.Fatalf("resolveBOMVersion() = %q, want 1.57.0", got)
		}
	})

	t.Run("BOM in cache dependency property resolved", func(t *testing.T) {
		m2repo := t.TempDir()
		t.Setenv("M2_REPO", m2repo)
		project := projectWithBOMImport("com.example", "test-bom", "1.0.1")

		writeFile(t, bomPath(m2repo, "com.example", "test-bom", "1.0.1"), `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>test-bom</artifactId>
  <version>1.0.1</version>
  <packaging>pom</packaging>
  <properties>
    <otel.version>1.57.1</otel.version>
  </properties>
  <dependencyManagement>
    <dependencies>
      <dependency>
        <groupId>io.opentelemetry</groupId>
        <artifactId>opentelemetry-api</artifactId>
        <version>${otel.version}</version>
      </dependency>
    </dependencies>
  </dependencyManagement>
</project>`)

		got, err := resolveBOMVersion(ctx, project, targetGroupID, targetArtifactID)
		if err != nil {
			t.Fatalf("resolveBOMVersion() error = %v", err)
		}
		if got != "1.57.1" {
			t.Fatalf("resolveBOMVersion() = %q, want 1.57.1", got)
		}
	})

	t.Run("BOM in cache dependency not found", func(t *testing.T) {
		m2repo := t.TempDir()
		t.Setenv("M2_REPO", m2repo)
		project := projectWithBOMImport("com.example", "test-bom", "1.0.2")

		writeFile(t, bomPath(m2repo, "com.example", "test-bom", "1.0.2"), `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>test-bom</artifactId>
  <version>1.0.2</version>
  <packaging>pom</packaging>
  <dependencyManagement>
    <dependencies>
      <dependency>
        <groupId>io.opentelemetry</groupId>
        <artifactId>opentelemetry-context</artifactId>
        <version>1.57.0</version>
      </dependency>
    </dependencies>
  </dependencyManagement>
</project>`)

		got, err := resolveBOMVersion(ctx, project, targetGroupID, targetArtifactID)
		if err != nil {
			t.Fatalf("resolveBOMVersion() error = %v", err)
		}
		if got != "" {
			t.Fatalf("resolveBOMVersion() = %q, want empty", got)
		}
	})

	t.Run("project has no BOM imports", func(t *testing.T) {
		project := &gopom.Project{}
		got, err := resolveBOMVersion(ctx, project, targetGroupID, targetArtifactID)
		if err != nil {
			t.Fatalf("resolveBOMVersion() error = %v", err)
		}
		if got != "" {
			t.Fatalf("resolveBOMVersion() = %q, want empty", got)
		}
	})
}

func TestMaven_Update_SkipsBOMDowngrade(t *testing.T) {
	m2repo := t.TempDir()
	t.Setenv("M2_REPO", m2repo)

	bomPath := filepath.Join(m2repo, "com", "example", "test-bom", "1.0.0", "test-bom-1.0.0.pom")
	writeFile(t, bomPath, `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>test-bom</artifactId>
  <version>1.0.0</version>
  <packaging>pom</packaging>
  <dependencyManagement>
    <dependencies>
      <dependency>
        <groupId>io.opentelemetry</groupId>
        <artifactId>opentelemetry-api</artifactId>
        <version>1.57.0</version>
      </dependency>
    </dependencies>
  </dependencyManagement>
</project>`)

	writeProjectPom := func(dir string) {
		writeFile(t, filepath.Join(dir, "pom.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>test-project</artifactId>
  <version>1.0.0</version>
  <dependencyManagement>
    <dependencies>
      <dependency>
        <groupId>com.example</groupId>
        <artifactId>test-bom</artifactId>
        <version>1.0.0</version>
        <type>pom</type>
        <scope>import</scope>
      </dependency>
    </dependencies>
  </dependencyManagement>
</project>`)
	}

	findManagedVersion := func(project *gopom.Project, groupID, artifactID string) (string, bool) {
		if project.DependencyManagement == nil || project.DependencyManagement.Dependencies == nil {
			return "", false
		}
		for _, dep := range *project.DependencyManagement.Dependencies {
			if dep.GroupID == groupID && dep.ArtifactID == artifactID {
				return dep.Version, true
			}
		}
		return "", false
	}

	t.Run("skips downgrade", func(t *testing.T) {
		tmpDir := t.TempDir()
		writeProjectPom(tmpDir)

		m := &Maven{}
		cfg := &languages.UpdateConfig{
			RootDir: tmpDir,
			Dependencies: []languages.Dependency{{
				Name:    "io.opentelemetry:opentelemetry-api",
				Version: "1.44.0",
				Metadata: map[string]any{
					"groupId":    "io.opentelemetry",
					"artifactId": "opentelemetry-api",
				},
			}},
		}
		if err := m.Update(context.Background(), cfg); err != nil {
			t.Fatalf("Update() unexpected error: %v", err)
		}

		project, err := ParsePom(filepath.Join(tmpDir, "pom.xml"))
		if err != nil {
			t.Fatalf("ParsePom() error: %v", err)
		}
		if _, found := findManagedVersion(project, "io.opentelemetry", "opentelemetry-api"); found {
			t.Fatal("opentelemetry-api should not be added when BOM manages a newer version")
		}
	})

	t.Run("adds upgrade", func(t *testing.T) {
		tmpDir := t.TempDir()
		writeProjectPom(tmpDir)

		m := &Maven{}
		cfg := &languages.UpdateConfig{
			RootDir: tmpDir,
			Dependencies: []languages.Dependency{{
				Name:    "io.opentelemetry:opentelemetry-api",
				Version: "1.58.0",
				Metadata: map[string]any{
					"groupId":    "io.opentelemetry",
					"artifactId": "opentelemetry-api",
				},
			}},
		}
		if err := m.Update(context.Background(), cfg); err != nil {
			t.Fatalf("Update() unexpected error: %v", err)
		}

		project, err := ParsePom(filepath.Join(tmpDir, "pom.xml"))
		if err != nil {
			t.Fatalf("ParsePom() error: %v", err)
		}
		version, found := findManagedVersion(project, "io.opentelemetry", "opentelemetry-api")
		if !found {
			t.Fatal("opentelemetry-api should be added when requested version is newer than BOM-managed version")
		}
		if version != "1.58.0" {
			t.Fatalf("opentelemetry-api version = %q, want 1.58.0", version)
		}
	})
}

func TestMaven_Update_SkipsDowngrade(t *testing.T) {
	tmpDir := t.TempDir()
	writeFile(t, filepath.Join(tmpDir, "pom.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>test-project</artifactId>
  <version>1.0.0</version>
  <dependencies>
    <dependency>
      <groupId>io.netty</groupId>
      <artifactId>netty-codec-http</artifactId>
      <version>4.1.133.Final</version>
    </dependency>
  </dependencies>
</project>`)
	m := &Maven{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{
				Name:    "io.netty:netty-codec-http",
				Version: "4.1.110.Final",
				Metadata: map[string]any{
					"groupId":    "io.netty",
					"artifactId": "netty-codec-http",
				},
			},
		},
	}
	if err := m.Update(context.Background(), cfg); err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}
	project, err := ParsePom(filepath.Join(tmpDir, "pom.xml"))
	if err != nil {
		t.Fatalf("ParsePom() error: %v", err)
	}
	for _, dep := range *project.Dependencies {
		if dep.GroupID == "io.netty" && dep.ArtifactID == "netty-codec-http" {
			if dep.Version != "4.1.133.Final" {
				t.Errorf("version = %q, want 4.1.133.Final (downgrade must be skipped)", dep.Version)
			}
			return
		}
	}
	t.Error("dependency not found in updated POM")
}

func TestMaven_Update_SkipsPropertyDowngrade(t *testing.T) {
	tmpDir := t.TempDir()
	writeFile(t, filepath.Join(tmpDir, "pom.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>test-project</artifactId>
  <version>1.0.0</version>
  <properties>
    <netty.version>4.1.133.Final</netty.version>
  </properties>
  <dependencies>
    <dependency>
      <groupId>io.netty</groupId>
      <artifactId>netty-codec-http</artifactId>
      <version>${netty.version}</version>
    </dependency>
  </dependencies>
</project>`)
	m := &Maven{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Properties: map[string]string{
			"netty.version": "4.1.110.Final",
		},
	}
	if err := m.Update(context.Background(), cfg); err != nil {
		t.Fatalf("Update() unexpected error: %v", err)
	}
	project, err := ParsePom(filepath.Join(tmpDir, "pom.xml"))
	if err != nil {
		t.Fatalf("ParsePom() error: %v", err)
	}
	if project.Properties == nil || project.Properties.Entries["netty.version"] != "4.1.133.Final" {
		t.Errorf("netty.version = %q, want 4.1.133.Final (downgrade must be skipped)",
			project.Properties.Entries["netty.version"])
	}
}

func TestMaven_Update_AllowsUpgrade(t *testing.T) {
	tmpDir := t.TempDir()
	writeFile(t, filepath.Join(tmpDir, "pom.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>test-project</artifactId>
  <version>1.0.0</version>
  <dependencies>
    <dependency>
      <groupId>io.netty</groupId>
      <artifactId>netty-codec-http</artifactId>
      <version>4.1.110.Final</version>
    </dependency>
  </dependencies>
</project>`)
	m := &Maven{}
	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{
				Name:    "io.netty:netty-codec-http",
				Version: "4.1.133.Final",
				Metadata: map[string]any{
					"groupId":    "io.netty",
					"artifactId": "netty-codec-http",
				},
			},
		},
	}
	if err := m.Update(context.Background(), cfg); err != nil {
		t.Fatalf("Update() error: %v", err)
	}
	project, err := ParsePom(filepath.Join(tmpDir, "pom.xml"))
	if err != nil {
		t.Fatalf("ParsePom() error: %v", err)
	}
	for _, dep := range *project.Dependencies {
		if dep.GroupID == "io.netty" && dep.ArtifactID == "netty-codec-http" {
			if dep.Version != "4.1.133.Final" {
				t.Errorf("version = %q, want 4.1.133.Final", dep.Version)
			}
			return
		}
	}
	t.Error("dependency not found")
}

func TestMaven_Update_ExplicitPropertyConflictErrors(t *testing.T) {
	tmpDir := t.TempDir()
	moduleDir := filepath.Join(tmpDir, "module")

	writeFile(t, filepath.Join(tmpDir, "pom.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>parent</artifactId>
  <version>1.0.0</version>
  <packaging>pom</packaging>
  <properties>
    <log4j.version>2.25.3</log4j.version>
  </properties>
</project>`)
	writeFile(t, filepath.Join(moduleDir, "pom.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>com.example</groupId>
    <artifactId>parent</artifactId>
    <version>1.0.0</version>
  </parent>
  <artifactId>module</artifactId>
  <dependencies>
    <dependency>
      <groupId>org.apache.logging.log4j</groupId>
      <artifactId>log4j-core</artifactId>
      <version>${log4j.version}</version>
    </dependency>
  </dependencies>
</project>`)

	m := &Maven{}
	cfg := &languages.UpdateConfig{
		RootDir:      tmpDir,
		ManifestFile: filepath.Join(moduleDir, "pom.xml"),
		Dependencies: []languages.Dependency{
			{Name: "org.apache.logging.log4j:log4j-core", Version: "2.25.4"},
		},
		Properties: map[string]string{
			"log4j.version": "2.25.3",
		},
	}

	err := m.Update(context.Background(), cfg)
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("Maven.Update() error = %v, want ErrVersionConflict", err)
	}
	if !strings.Contains(err.Error(), "log4j.version") {
		t.Fatalf("Maven.Update() error = %q, want to mention log4j.version", err)
	}
}

func TestMaven_Update_DependencyPropertyMissingInPomAndParentErrors(t *testing.T) {
	tmpDir := t.TempDir()
	moduleDir := filepath.Join(tmpDir, "module")

	writeFile(t, filepath.Join(tmpDir, "pom.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>parent</artifactId>
  <version>1.0.0</version>
  <packaging>pom</packaging>
</project>`)
	writeFile(t, filepath.Join(moduleDir, "pom.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>com.example</groupId>
    <artifactId>parent</artifactId>
    <version>1.0.0</version>
  </parent>
  <artifactId>module</artifactId>
  <dependencyManagement>
    <dependencies>
      <dependency>
        <groupId>io.netty</groupId>
        <artifactId>netty-codec</artifactId>
        <version>${missing.version}</version>
      </dependency>
    </dependencies>
  </dependencyManagement>
</project>`)

	m := &Maven{}
	cfg := &languages.UpdateConfig{
		RootDir:      tmpDir,
		ManifestFile: filepath.Join(moduleDir, "pom.xml"),
		Dependencies: []languages.Dependency{
			{Name: "io.netty:netty-codec", Version: "4.1.94.Final"},
		},
	}

	err := m.Update(context.Background(), cfg)
	if !errors.Is(err, ErrPropertyNotFound) {
		t.Fatalf("Maven.Update() error = %v, want ErrPropertyNotFound", err)
	}
}

func TestMaven_Update_DependencyWithProjectVersionSkipsGracefully(t *testing.T) {
	tmpDir := t.TempDir()

	writeFile(t, filepath.Join(tmpDir, "pom.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>parent</artifactId>
  <version>1.0.0</version>
  <packaging>pom</packaging>
  <dependencies>
    <dependency>
      <groupId>com.example</groupId>
      <artifactId>internal-lib</artifactId>
      <version>${project.version}</version>
    </dependency>
  </dependencies>
</project>`)

	m := &Maven{}
	cfg := &languages.UpdateConfig{
		RootDir:      tmpDir,
		ManifestFile: filepath.Join(tmpDir, "pom.xml"),
		Dependencies: []languages.Dependency{
			{Name: "com.example:internal-lib", Version: "2.0.0"},
		},
	}

	if err := m.Update(context.Background(), cfg); err != nil {
		t.Fatalf("Maven.Update() error = %v, want nil", err)
	}
}

func TestMaven_Update_PropertiesSplitBetweenCurrentAndParent(t *testing.T) {
	tmpDir := t.TempDir()
	parentPom := filepath.Join(tmpDir, "pom.xml")
	moduleDir := filepath.Join(tmpDir, "module")
	modulePom := filepath.Join(moduleDir, "pom.xml")

	writeFile(t, parentPom, `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>parent</artifactId>
  <version>1.0.0</version>
  <packaging>pom</packaging>
  <properties>
    <parent.version>1.0.0</parent.version>
    <parent.extra.version>1.0.1</parent.extra.version>
  </properties>
</project>`)
	writeFile(t, modulePom, `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>com.example</groupId>
    <artifactId>parent</artifactId>
    <version>1.0.0</version>
  </parent>
  <artifactId>module</artifactId>
  <properties>
    <module.version>2.0.0</module.version>
    <module.extra.version>2.0.1</module.extra.version>
  </properties>
</project>`)

	cfg := &languages.UpdateConfig{
		RootDir:      tmpDir,
		ManifestFile: modulePom,
		Properties: map[string]string{
			"parent.version":       "1.1.0",
			"parent.extra.version": "1.1.1",
			"module.version":       "2.1.0",
			"module.extra.version": "2.1.1",
		},
	}

	m := &Maven{}
	if err := m.Update(context.Background(), cfg); err != nil {
		t.Fatalf("Maven.Update() failed: %v", err)
	}
	if err := m.Validate(context.Background(), cfg); err != nil {
		t.Fatalf("Maven.Validate() failed: %v", err)
	}

	parent, err := ParsePom(parentPom)
	if err != nil {
		t.Fatalf("ParsePom(parent) failed: %v", err)
	}
	if got := parent.Properties.Entries["parent.version"]; got != "1.1.0" {
		t.Errorf("parent.version = %q, want 1.1.0", got)
	}
	if got := parent.Properties.Entries["parent.extra.version"]; got != "1.1.1" {
		t.Errorf("parent.extra.version = %q, want 1.1.1", got)
	}
	if _, exists := parent.Properties.Entries["module.version"]; exists {
		t.Error("parent POM should not get module.version")
	}
	if _, exists := parent.Properties.Entries["module.extra.version"]; exists {
		t.Error("parent POM should not get module.extra.version")
	}

	module, err := ParsePom(modulePom)
	if err != nil {
		t.Fatalf("ParsePom(module) failed: %v", err)
	}
	if got := module.Properties.Entries["module.version"]; got != "2.1.0" {
		t.Errorf("module.version = %q, want 2.1.0", got)
	}
	if got := module.Properties.Entries["module.extra.version"]; got != "2.1.1" {
		t.Errorf("module.extra.version = %q, want 2.1.1", got)
	}
	if _, exists := module.Properties.Entries["parent.version"]; exists {
		t.Error("module POM should not get parent.version")
	}
	if _, exists := module.Properties.Entries["parent.extra.version"]; exists {
		t.Error("module POM should not get parent.extra.version")
	}
}

func TestMaven_Update_PropertyDefinedInParentDirectoryRelativePath(t *testing.T) {
	tmpDir := t.TempDir()
	parentPom := filepath.Join(tmpDir, "parent", "pom.xml")
	moduleDir := filepath.Join(tmpDir, "module")

	writeFile(t, parentPom, `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>parent</artifactId>
  <version>1.0.0</version>
  <packaging>pom</packaging>
  <properties>
    <parent.version>1.0.0</parent.version>
  </properties>
</project>`)
	writeFile(t, filepath.Join(moduleDir, "pom.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>com.example</groupId>
    <artifactId>parent</artifactId>
    <version>1.0.0</version>
    <relativePath>../parent</relativePath>
  </parent>
  <artifactId>module</artifactId>
</project>`)

	cfg := &languages.UpdateConfig{
		RootDir:      tmpDir,
		ManifestFile: filepath.Join(moduleDir, "pom.xml"),
		Properties: map[string]string{
			"parent.version": "1.1.0",
		},
	}

	m := &Maven{}
	if err := m.Update(context.Background(), cfg); err != nil {
		t.Fatalf("Maven.Update() failed: %v", err)
	}

	parent, err := ParsePom(parentPom)
	if err != nil {
		t.Fatalf("ParsePom(parent) failed: %v", err)
	}
	if got := parent.Properties.Entries["parent.version"]; got != "1.1.0" {
		t.Errorf("parent.version = %q, want 1.1.0", got)
	}
}

func TestMaven_Update_PropertyMissingInPomAndParentErrors(t *testing.T) {
	tmpDir := t.TempDir()
	moduleDir := filepath.Join(tmpDir, "module")

	writeFile(t, filepath.Join(tmpDir, "pom.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>parent</artifactId>
  <version>1.0.0</version>
  <packaging>pom</packaging>
</project>`)
	writeFile(t, filepath.Join(moduleDir, "pom.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>com.example</groupId>
    <artifactId>parent</artifactId>
    <version>1.0.0</version>
    <relativePath>../pom.xml</relativePath>
  </parent>
  <artifactId>module</artifactId>
</project>`)

	m := &Maven{}
	cfg := &languages.UpdateConfig{
		RootDir:      tmpDir,
		ManifestFile: filepath.Join(moduleDir, "pom.xml"),
		Properties: map[string]string{
			"missing.version": "1.2.3",
		},
	}

	err := m.Update(context.Background(), cfg)
	if !errors.Is(err, ErrPropertyNotFound) {
		t.Fatalf("Maven.Update() error = %v, want ErrPropertyNotFound", err)
	}
}

func TestMaven_Update_RejectsParentRelativePathOutsideProjectRoot(t *testing.T) {
	tmpDir := t.TempDir()
	projectRoot := filepath.Join(tmpDir, "root")
	moduleDir := filepath.Join(projectRoot, "module")
	modulePom := filepath.Join(moduleDir, "pom.xml")
	outsidePom := filepath.Join(tmpDir, "outside", "pom.xml")

	writeFile(t, filepath.Join(projectRoot, "pom.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>root</artifactId>
  <version>1.0.0</version>
</project>`)
	writeFile(t, outsidePom, `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>outside-parent</artifactId>
  <version>1.0.0</version>
  <properties>
    <outside.version>1.0.0</outside.version>
  </properties>
</project>`)
	writeFile(t, modulePom, `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>com.example</groupId>
    <artifactId>outside-parent</artifactId>
    <version>1.0.0</version>
    <relativePath>../../outside/pom.xml</relativePath>
  </parent>
  <artifactId>module</artifactId>
</project>`)

	m := &Maven{}
	cfg := &languages.UpdateConfig{
		RootDir:      projectRoot,
		ManifestFile: modulePom,
		Properties: map[string]string{
			"outside.version": "2.0.0",
		},
	}

	err := m.Update(context.Background(), cfg)
	if !errors.Is(err, ErrUnsafePomPath) {
		t.Fatalf("Maven.Update() error = %v, want ErrUnsafePomPath", err)
	}

	outside, err := ParsePom(outsidePom)
	if err != nil {
		t.Fatalf("ParsePom(outside) failed: %v", err)
	}
	if got := outside.Properties.Entries["outside.version"]; got != "1.0.0" {
		t.Errorf("outside.version = %q, want unchanged value 1.0.0", got)
	}
}

func TestMaven_Update_RejectsAbsoluteParentPathOutsideProjectRoot(t *testing.T) {
	tmpDir := t.TempDir()
	projectRoot := filepath.Join(tmpDir, "root")
	modulePom := filepath.Join(projectRoot, "module", "pom.xml")
	outsidePom := filepath.Join(tmpDir, "outside", "pom.xml")

	writeFile(t, filepath.Join(projectRoot, "pom.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>root</artifactId>
  <version>1.0.0</version>
</project>`)
	writeFile(t, outsidePom, `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>outside-parent</artifactId>
  <version>1.0.0</version>
  <properties>
    <outside.version>1.0.0</outside.version>
  </properties>
</project>`)
	writeFile(t, modulePom, `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>com.example</groupId>
    <artifactId>outside-parent</artifactId>
    <version>1.0.0</version>
    <relativePath>`+outsidePom+`</relativePath>
  </parent>
  <artifactId>module</artifactId>
</project>`)

	m := &Maven{}
	cfg := &languages.UpdateConfig{
		RootDir:      projectRoot,
		ManifestFile: modulePom,
		Properties: map[string]string{
			"outside.version": "2.0.0",
		},
	}

	err := m.Update(context.Background(), cfg)
	if !errors.Is(err, ErrUnsafePomPath) {
		t.Fatalf("Maven.Update() error = %v, want ErrUnsafePomPath", err)
	}

	outside, err := ParsePom(outsidePom)
	if err != nil {
		t.Fatalf("ParsePom(outside) failed: %v", err)
	}
	if got := outside.Properties.Entries["outside.version"]; got != "1.0.0" {
		t.Errorf("outside.version = %q, want unchanged value 1.0.0", got)
	}
}

func TestMaven_Update_RejectsManifestFileOutsideProjectRoot(t *testing.T) {
	tmpDir := t.TempDir()
	projectRoot := filepath.Join(tmpDir, "root")
	outsidePom := filepath.Join(tmpDir, "outside", "pom.xml")

	writeFile(t, filepath.Join(projectRoot, "pom.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>root</artifactId>
  <version>1.0.0</version>
</project>`)
	writeFile(t, outsidePom, `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>outside</artifactId>
  <version>1.0.0</version>
  <dependencies>
    <dependency>
      <groupId>com.example</groupId>
      <artifactId>library</artifactId>
      <version>1.0.0</version>
    </dependency>
  </dependencies>
</project>`)

	m := &Maven{}
	cfg := &languages.UpdateConfig{
		RootDir:      projectRoot,
		ManifestFile: outsidePom,
		Dependencies: []languages.Dependency{
			{
				Version: "2.0.0",
				Metadata: map[string]any{
					"groupId":    "com.example",
					"artifactId": "library",
				},
			},
		},
	}

	err := m.Update(context.Background(), cfg)
	if !errors.Is(err, ErrUnsafePomPath) {
		t.Fatalf("Maven.Update() error = %v, want ErrUnsafePomPath", err)
	}

	outside, err := ParsePom(outsidePom)
	if err != nil {
		t.Fatalf("ParsePom(outside) failed: %v", err)
	}
	if got := (*outside.Dependencies)[0].Version; got != "1.0.0" {
		t.Errorf("outside dependency version = %q, want unchanged value 1.0.0", got)
	}
}

func TestResolvePropertyPomPath(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T, dir string) string
		property    string
		wantPath    func(dir string) string
		wantErr     error
		wantErrText string
	}{
		{
			name: "property in current POM",
			setup: func(t *testing.T, dir string) string {
				pomPath := filepath.Join(dir, "pom.xml")
				writeFile(t, pomPath, `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>module</artifactId>
  <version>1.0.0</version>
  <properties>
    <module.version>2.0.0</module.version>
  </properties>
</project>`)
				return pomPath
			},
			property: "module.version",
			wantPath: func(dir string) string {
				return filepath.Join(dir, "pom.xml")
			},
		},
		{
			name: "property in parent POM with default relativePath",
			setup: func(t *testing.T, dir string) string {
				writeFile(t, filepath.Join(dir, "pom.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>parent</artifactId>
  <version>1.0.0</version>
  <properties>
    <parent.version>1.0.0</parent.version>
  </properties>
</project>`)
				modulePom := filepath.Join(dir, "module", "pom.xml")
				writeFile(t, modulePom, `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>com.example</groupId>
    <artifactId>parent</artifactId>
    <version>1.0.0</version>
  </parent>
  <artifactId>module</artifactId>
</project>`)
				return modulePom
			},
			property: "parent.version",
			wantPath: func(dir string) string {
				return filepath.Join(dir, "pom.xml")
			},
		},
		{
			name: "property in parent POM with explicit relativePath",
			setup: func(t *testing.T, dir string) string {
				parentPom := filepath.Join(dir, "build", "parent.xml")
				writeFile(t, parentPom, `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>parent</artifactId>
  <version>1.0.0</version>
  <properties>
    <shared.version>3.0.0</shared.version>
  </properties>
</project>`)
				modulePom := filepath.Join(dir, "module", "pom.xml")
				writeFile(t, modulePom, `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>com.example</groupId>
    <artifactId>parent</artifactId>
    <version>1.0.0</version>
    <relativePath>../build/parent.xml</relativePath>
  </parent>
  <artifactId>module</artifactId>
</project>`)
				return modulePom
			},
			property: "shared.version",
			wantPath: func(dir string) string {
				return filepath.Join(dir, "build", "parent.xml")
			},
		},
		{
			name: "property in parent POM with directory relativePath",
			setup: func(t *testing.T, dir string) string {
				parentPom := filepath.Join(dir, "parent", "pom.xml")
				writeFile(t, parentPom, `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>parent</artifactId>
  <version>1.0.0</version>
  <properties>
    <parent.dir.version>4.0.0</parent.dir.version>
  </properties>
</project>`)
				modulePom := filepath.Join(dir, "module", "pom.xml")
				writeFile(t, modulePom, `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>com.example</groupId>
    <artifactId>parent</artifactId>
    <version>1.0.0</version>
    <relativePath>../parent</relativePath>
  </parent>
  <artifactId>module</artifactId>
</project>`)
				return modulePom
			},
			property: "parent.dir.version",
			wantPath: func(dir string) string {
				return filepath.Join(dir, "parent", "pom.xml")
			},
		},
		{
			name: "property missing with parent POM cycle",
			setup: func(t *testing.T, dir string) string {
				parentPom := filepath.Join(dir, "parent", "pom.xml")
				modulePom := filepath.Join(dir, "module", "pom.xml")
				writeFile(t, parentPom, `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>com.example</groupId>
    <artifactId>module</artifactId>
    <version>1.0.0</version>
    <relativePath>../module/pom.xml</relativePath>
  </parent>
  <artifactId>parent</artifactId>
</project>`)
				writeFile(t, modulePom, `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>com.example</groupId>
    <artifactId>parent</artifactId>
    <version>1.0.0</version>
    <relativePath>../parent/pom.xml</relativePath>
  </parent>
  <artifactId>module</artifactId>
</project>`)
				return modulePom
			},
			property:    "missing.version",
			wantErr:     ErrPropertyNotFound,
			wantErrText: "not found in parent POM chain",
		},
		{
			name: "property missing without parent",
			setup: func(t *testing.T, dir string) string {
				pomPath := filepath.Join(dir, "pom.xml")
				writeFile(t, pomPath, `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>module</artifactId>
  <version>1.0.0</version>
</project>`)
				return pomPath
			},
			property:    "missing.version",
			wantErr:     ErrPropertyNotFound,
			wantErrText: "no parent POM is configured",
		},
		{
			name: "property missing in current and parent",
			setup: func(t *testing.T, dir string) string {
				writeFile(t, filepath.Join(dir, "pom.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>parent</artifactId>
  <version>1.0.0</version>
</project>`)
				modulePom := filepath.Join(dir, "module", "pom.xml")
				writeFile(t, modulePom, `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>com.example</groupId>
    <artifactId>parent</artifactId>
    <version>1.0.0</version>
  </parent>
  <artifactId>module</artifactId>
</project>`)
				return modulePom
			},
			property:    "missing.version",
			wantErr:     ErrPropertyNotFound,
			wantErrText: "or parent POM",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			pomPath := tt.setup(t, dir)

			got, err := resolvePropertyPomPath(t.Context(), pomPath, tt.property, dir)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("resolvePropertyPomPath() error = %v, want %v", err, tt.wantErr)
				}
				if tt.wantErrText != "" && !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("resolvePropertyPomPath() error = %q, want to contain %q", err, tt.wantErrText)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolvePropertyPomPath() error = %v", err)
			}
			if want := tt.wantPath(dir); got != want {
				t.Errorf("resolvePropertyPomPath() = %q, want %q", got, want)
			}
		})
	}
}

func TestValidatePathWithinRoot(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	insidePom := filepath.Join(root, "module", "pom.xml")
	outsidePom := filepath.Join(dir, "outside", "pom.xml")
	writeFile(t, insidePom, "<project></project>")
	writeFile(t, outsidePom, "<project></project>")

	if err := validatePathWithinRoot(root, insidePom); err != nil {
		t.Fatalf("validatePathWithinRoot() inside path error = %v", err)
	}
	if err := validatePathWithinRoot(root, outsidePom); !errors.Is(err, ErrUnsafePomPath) {
		t.Fatalf("validatePathWithinRoot() outside path error = %v, want ErrUnsafePomPath", err)
	}
}

func TestValidatePathWithinRootRejectsSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	outsidePom := filepath.Join(dir, "outside", "pom.xml")
	linkPom := filepath.Join(root, "linked-pom.xml")
	writeFile(t, outsidePom, "<project></project>")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", root, err)
	}
	if err := os.Symlink(outsidePom, linkPom); err != nil {
		t.Fatalf("Symlink(%s, %s): %v", outsidePom, linkPom, err)
	}

	if err := validatePathWithinRoot(root, linkPom); !errors.Is(err, ErrUnsafePomPath) {
		t.Fatalf("validatePathWithinRoot() symlink escape error = %v, want ErrUnsafePomPath", err)
	}
}

func TestIsMavenPom_Valid(t *testing.T) {
	path := t.TempDir() + "/pom.xml"
	content := `<?xml version="1.0" encoding="UTF-8"?><project xmlns="http://maven.apache.org/POM/4.0.0" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"></project>`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	ok, err := IsMavenPom(path)
	if err != nil {
		t.Fatalf("IsMavenPom() unexpected error: %v", err)
	}
	if !ok {
		t.Error("IsMavenPom() = false, want true for valid Maven POM")
	}
}

func TestIsMavenPom_NonMaven(t *testing.T) {
	path := t.TempDir() + "/other.xml"
	if err := os.WriteFile(path, []byte(`<?xml version="1.0"?><configuration><foo>bar</foo></configuration>`), 0o600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	ok, err := IsMavenPom(path)
	if err != nil {
		t.Fatalf("IsMavenPom() unexpected error: %v", err)
	}
	if ok {
		t.Error("IsMavenPom() = true, want false for non-Maven XML")
	}
}

func TestIsMavenPom_NotFound(t *testing.T) {
	ok, err := IsMavenPom("/nonexistent/path/pom.xml")
	if err == nil {
		t.Error("IsMavenPom() expected error for missing file, got nil")
	}
	if ok {
		t.Error("IsMavenPom() = true, want false for missing file")
	}
}

func TestMaven_Detect_StandardName(t *testing.T) {
	tmpDir := t.TempDir()
	pomContent := `<?xml version="1.0" encoding="UTF-8"?><project xmlns="http://maven.apache.org/POM/4.0.0" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"></project>`
	if err := os.WriteFile(tmpDir+"/pom.xml", []byte(pomContent), 0o600); err != nil {
		t.Fatalf("failed to write pom.xml: %v", err)
	}

	m := &Maven{}
	ok, err := m.Detect(t.Context(), tmpDir)
	if err != nil {
		t.Fatalf("Detect() unexpected error: %v", err)
	}
	if !ok {
		t.Error("Detect() = false, want true for valid pom.xml")
	}
}

func TestMaven_Detect_CustomManifestFile(t *testing.T) {
	// Custom-named POM files are identified via IsMavenPom before detection;
	// verify that a non-standard filename with Maven content is recognised.
	customPath := t.TempDir() + "/parent-pom-template.xml"
	pomContent := `<?xml version="1.0" encoding="UTF-8"?><project xmlns="http://maven.apache.org/POM/4.0.0" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"></project>`
	if err := os.WriteFile(customPath, []byte(pomContent), 0o600); err != nil {
		t.Fatalf("failed to write custom POM: %v", err)
	}

	ok, err := IsMavenPom(customPath)
	if err != nil {
		t.Fatalf("IsMavenPom() unexpected error: %v", err)
	}
	if !ok {
		t.Error("IsMavenPom() = false, want true for valid Maven POM with non-standard filename")
	}
}

func TestIsMavenPom_LargeLicenseHeader(t *testing.T) {
	// Reproduces the real-world case where an Apache license comment pushes
	// the <project> element past what a naive fixed-buffer read would capture.
	content := `<?xml version="1.0" encoding="UTF-8"?>
<!--
  Licensed to the Apache Software Foundation (ASF) under one or more
  contributor license agreements. See the NOTICE file distributed with
  this work for additional information regarding copyright ownership.
  The ASF licenses this file to You under the Apache License, Version 2.0
  (the "License"); you may not use this file except in compliance with
  the License. You may obtain a copy of the License at
      http://www.apache.org/licenses/LICENSE-2.0
  Unless required by applicable law or agreed to in writing, software
  distributed under the License is distributed on an "AS IS" BASIS,
  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
  See the License for the specific language governing permissions and
  limitations under the License.
-->
<project xmlns="http://maven.apache.org/POM/4.0.0" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"></project>`

	path := t.TempDir() + "/parent-pom-template.xml"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	ok, err := IsMavenPom(path)
	if err != nil {
		t.Fatalf("IsMavenPom() unexpected error: %v", err)
	}
	if !ok {
		t.Error("IsMavenPom() = false, want true — license header must not prevent detection")
	}
}

func TestMaven_Detect_WrongContent(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(tmpDir+"/pom.xml", []byte(`<notmaven/>`), 0o600); err != nil {
		t.Fatalf("failed to write pom.xml: %v", err)
	}

	m := &Maven{}
	ok, err := m.Detect(t.Context(), tmpDir)
	if err != nil {
		t.Fatalf("Detect() unexpected error: %v", err)
	}
	if ok {
		t.Error("Detect() = true, want false for file without Maven namespace")
	}
}

// minimalPOM is a valid Maven POM with no dependencies used across multiple tests.
const minimalPOM = `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>test</artifactId>
  <version>1.0.0</version>
</project>`

// pomWithDep returns a minimal Maven POM that declares one dependency in dependencyManagement.
func pomWithDep(groupID, artifactID, version string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>test</artifactId>
  <version>1.0.0</version>
  <dependencyManagement>
    <dependencies>
      <dependency>
        <groupId>` + groupID + `</groupId>
        <artifactId>` + artifactID + `</artifactId>
        <version>` + version + `</version>
      </dependency>
    </dependencies>
  </dependencyManagement>
</project>`
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

// TestIsSkippableDirectory verifies the explicit allowlist behaviour:
// VCS and build-output dirs are skipped; dot-prefixed source dirs (e.g. .build) are not.
func TestIsSkippableDirectory(t *testing.T) {
	skippable := []string{
		".git", ".svn", ".hg", ".bzr",
		"target", "node_modules",
		"build", "dist", "out",
		"testdata", "vendor", "test",
	}
	for _, name := range skippable {
		if !isSkippableDirectory(name) {
			t.Errorf("isSkippableDirectory(%q) = false, want true", name)
		}
	}

	allowed := []string{".build", ".mvn", ".github", "src", "lib", "my-module"}
	for _, name := range allowed {
		if isSkippableDirectory(name) {
			t.Errorf("isSkippableDirectory(%q) = true, want false", name)
		}
	}
}

// TestWalkXMLFiles_RootDirNameInSkipList is a regression test for the bug where
// walkXMLFiles skipped its own root when the root directory's name matched an entry
// in isSkippableDirectory (e.g. a project checked out into a directory named "build").
func TestWalkXMLFiles_RootDirNameInSkipList(t *testing.T) {
	for _, rootName := range []string{"build", "target", "dist", "out", "node_modules"} {
		t.Run(rootName, func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, rootName)
			writeFile(t, filepath.Join(root, "file.xml"), minimalPOM)

			got := walkXMLFiles(root)
			if len(got) == 0 {
				t.Errorf("walkXMLFiles(%q) returned no files — root directory must not be skipped when its name matches the skip list", rootName)
			}
		})
	}
}

func TestFindMavenPoms(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(t *testing.T, dir string)
		wantCount int
		wantPaths func(dir string) []string // optional: paths that must be present
	}{
		{
			name:      "empty directory",
			setup:     func(_ *testing.T, _ string) {},
			wantCount: 0,
		},
		{
			name: "root pom.xml",
			setup: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "pom.xml"), minimalPOM)
			},
			wantCount: 1,
			wantPaths: func(dir string) []string { return []string{filepath.Join(dir, "pom.xml")} },
		},
		{
			name: "pom in subdirectory, no root pom.xml",
			setup: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "submodule", "pom.xml"), minimalPOM)
			},
			wantCount: 1,
		},
		{
			name: "pom in dot-prefixed source dir (.build)",
			setup: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, ".build", "parent-pom-template.xml"), minimalPOM)
			},
			wantCount: 1,
		},
		{
			name: "pom inside skipped VCS dir (.git) is not returned",
			setup: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, ".git", "pom.xml"), minimalPOM)
			},
			wantCount: 0,
		},
		{
			name: "pom inside skipped build-output dir (target) is not returned",
			setup: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "target", "pom.xml"), minimalPOM)
			},
			wantCount: 0,
		},
		{
			name: "non-Maven XML is not returned",
			setup: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "config.xml"), `<?xml version="1.0"?><configuration/>`)
			},
			wantCount: 0,
		},
		{
			name: "non-XML file is not returned",
			setup: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "pom.json"), `{}`)
			},
			wantCount: 0,
		},
		{
			name: "multiple POMs across subdirectories",
			setup: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, ".build", "parent.xml"), minimalPOM)
				writeFile(t, filepath.Join(dir, "module-a", "pom.xml"), minimalPOM)
				writeFile(t, filepath.Join(dir, "module-b", "pom.xml"), minimalPOM)
				// This one is in target/ and must be excluded
				writeFile(t, filepath.Join(dir, "target", "pom.xml"), minimalPOM)
			},
			wantCount: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.setup(t, dir)

			got := findMavenPoms(dir)
			if len(got) != tt.wantCount {
				t.Errorf("findMavenPoms() returned %d paths, want %d: %v", len(got), tt.wantCount, got)
			}
			if tt.wantPaths != nil {
				want := tt.wantPaths(dir)
				for _, p := range want {
					found := false
					for _, g := range got {
						if g == p {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("findMavenPoms() missing expected path %s; got %v", p, got)
					}
				}
			}
		})
	}
}
