/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package maven

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chainguard-dev/gopom"
	"github.com/chainguard-dev/omnibump/pkg/languages"
	"github.com/google/go-cmp/cmp"
)

// tomeeFixture is the real apache/tomee 10.3.0 root POM. Its
// <dependencyManagement> declares activemq-broker and activemq-client as
// ${version.activemq} (6.3.3), tomcat-catalina as ${tomcat.version}
// (10.1.61), jackson-databind as ${version.jackson} (2.22.4) and neethi with
// a literal 3.2.4.
const tomeeFixture = "tomee-10.3.0.pom.xml"

// copyTomeePom copies the TomEE fixture to pom.xml in a fresh directory and
// returns that directory.
func copyTomeePom(t *testing.T) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", tomeeFixture))
	if err != nil {
		t.Fatalf("failed to read fixture %s: %v", tomeeFixture, err)
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pom.xml"), string(content))
	return dir
}

// parsePomIn parses dir/pom.xml.
func parsePomIn(t *testing.T, dir string) *gopom.Project {
	t.Helper()
	project, err := ParsePom(filepath.Join(dir, "pom.xml"))
	if err != nil {
		t.Fatalf("ParsePom() error = %v", err)
	}
	return project
}

// readPomProperty returns a property value from dir/pom.xml.
func readPomProperty(t *testing.T, dir, name string) string {
	t.Helper()
	return parsePomIn(t, dir).Properties.Entries[name]
}

// updateAndValidate runs Update followed by Validate and fails the test on
// any error.
func updateAndValidate(t *testing.T, cfg *languages.UpdateConfig) {
	t.Helper()
	if err := (&Maven{}).Update(t.Context(), cfg); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if err := (&Maven{}).Validate(t.Context(), cfg); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

// mavenDep builds the Dependency the config loader produces for a
// groupId@artifactId@version[@scope] entry.
func mavenDep(groupID, artifactID, version, scope string) languages.Dependency {
	return languages.Dependency{
		Name:    groupID + ":" + artifactID,
		Version: version,
		Scope:   scope,
		Metadata: map[string]any{
			"groupId":    groupID,
			"artifactId": artifactID,
		},
	}
}

// managedVersion returns the raw <version> of coordinate ("groupId:artifactId")
// in the project's <dependencyManagement>, and whether the entry exists.
func managedVersion(project *gopom.Project, coordinate string) (string, bool) {
	if project.DependencyManagement == nil || project.DependencyManagement.Dependencies == nil {
		return "", false
	}
	groupID, artifactID, _ := strings.Cut(coordinate, ":")
	for _, dep := range *project.DependencyManagement.Dependencies {
		if dep.GroupID == groupID && dep.ArtifactID == artifactID {
			return dep.Version, true
		}
	}
	return "", false
}

// TestMaven_Update_DowngradeSkipped runs CVE pins that were set for an older
// TomEE release against the real 10.3.0 root POM. Each request is older than
// the version upstream already declares, so the POM must keep upstream's
// version rather than lower it. Upgrades requested alongside a downgrade must
// still be applied.
func TestMaven_Update_DowngradeSkipped(t *testing.T) {
	tests := []struct {
		name           string
		deps           []languages.Dependency
		properties     map[string]string
		wantProperties map[string]string
		wantManaged    map[string]string
	}{{
		// An activemq pin that outlived upstream's move to 6.3.3.
		name: "property shared by two artifacts",
		deps: []languages.Dependency{
			mavenDep("org.apache.activemq", "activemq-broker", "6.2.7", ""),
			mavenDep("org.apache.activemq", "activemq-client", "6.2.7", ""),
		},
		wantProperties: map[string]string{"version.activemq": "6.3.3"},
	}, {
		name:           "property referenced by one artifact",
		deps:           []languages.Dependency{mavenDep("org.apache.tomcat", "tomcat-catalina", "10.1.59", "")},
		wantProperties: map[string]string{"tomcat.version": "10.1.61"},
	}, {
		name:           "property for a version family",
		deps:           []languages.Dependency{mavenDep("com.fasterxml.jackson.core", "jackson-databind", "2.21.7", "")},
		wantProperties: map[string]string{"version.jackson": "2.22.4"},
	}, {
		name:        "literal managed version",
		deps:        []languages.Dependency{mavenDep("org.apache.neethi", "neethi", "3.2.2", "")},
		wantManaged: map[string]string{"org.apache.neethi:neethi": "3.2.4"},
	}, {
		name:           "explicit property",
		properties:     map[string]string{"version.activemq": "6.2.7"},
		wantProperties: map[string]string{"version.activemq": "6.3.3"},
	}, {
		name: "upgrade applied next to a skipped downgrade",
		deps: []languages.Dependency{
			mavenDep("org.apache.tomcat", "tomcat-catalina", "10.1.62", ""),
			mavenDep("org.apache.neethi", "neethi", "3.2.2", ""),
		},
		wantProperties: map[string]string{"tomcat.version": "10.1.62"},
		wantManaged:    map[string]string{"org.apache.neethi:neethi": "3.2.4"},
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := copyTomeePom(t)
			cfg := &languages.UpdateConfig{RootDir: dir, Dependencies: tt.deps, Properties: tt.properties}
			if err := (&Maven{}).Update(t.Context(), cfg); err != nil {
				t.Fatalf("Update() error = %v", err)
			}

			project := parsePomIn(t, dir)
			checkProps(t, project, tt.wantProperties)
			for coordinate, want := range tt.wantManaged {
				if got, _ := managedVersion(project, coordinate); got != want {
					t.Errorf("managed %s version: got = %q, want = %q", coordinate, got, want)
				}
			}
		})
	}
}

// TestMaven_Update_FamilyKeptTogether covers pins that move jackson-databind
// and jackson-core in lockstep. TomEE declares jackson-databind (kept at its
// newer 2.22.4) but not jackson-core. Adding jackson-core at the older pinned
// version would split the family, so an undeclared pin is skipped when a pin
// in the same group at the same version was skipped as a downgrade. Other
// undeclared pins are still added.
func TestMaven_Update_FamilyKeptTogether(t *testing.T) {
	const jacksonCore = "com.fasterxml.jackson.core:jackson-core"
	tests := []struct {
		name    string
		deps    []languages.Dependency
		wantPin string // "" when jackson-core must not be added
	}{{
		name: "undeclared member at the skipped version",
		deps: []languages.Dependency{
			mavenDep("com.fasterxml.jackson.core", "jackson-databind", "2.21.7", ""),
			mavenDep("com.fasterxml.jackson.core", "jackson-core", "2.21.7", ""),
		},
	}, {
		name: "undeclared member at a different version",
		deps: []languages.Dependency{
			mavenDep("com.fasterxml.jackson.core", "jackson-databind", "2.21.7", ""),
			mavenDep("com.fasterxml.jackson.core", "jackson-core", "2.23.0", ""),
		},
		wantPin: "2.23.0",
	}, {
		name:    "undeclared pin with no skipped member",
		deps:    []languages.Dependency{mavenDep("com.fasterxml.jackson.core", "jackson-core", "2.21.7", "")},
		wantPin: "2.21.7",
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := copyTomeePom(t)
			updateAndValidate(t, &languages.UpdateConfig{RootDir: dir, Dependencies: tt.deps})

			project := parsePomIn(t, dir)
			checkProps(t, project, map[string]string{"version.jackson": "2.22.4"})
			if got, _ := managedVersion(project, jacksonCore); got != tt.wantPin {
				t.Errorf("managed %s version: got = %q, want = %q", jacksonCore, got, tt.wantPin)
			}
		})
	}
}

// TestMaven_Update_DowngradeSkippedInParentPOM covers a module whose
// dependency reads a property its parent POM defines: the parent's value is
// the current version, so an older request must leave it alone.
func TestMaven_Update_DowngradeSkippedInParentPOM(t *testing.T) {
	dir := copyTomeePom(t)
	module := filepath.Join(dir, "tomee", "tomee-webapp")
	writeFile(t, filepath.Join(module, "pom.xml"), `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>org.apache.tomee</groupId>
    <artifactId>tomee-project</artifactId>
    <version>10.3.0</version>
    <relativePath>../../pom.xml</relativePath>
  </parent>
  <artifactId>tomee-webapp</artifactId>
  <dependencies>
    <dependency>
      <groupId>org.apache.activemq</groupId>
      <artifactId>activemq-broker</artifactId>
      <version>${version.activemq}</version>
    </dependency>
  </dependencies>
</project>
`)

	cfg := &languages.UpdateConfig{
		RootDir:      dir,
		ManifestFile: filepath.Join(module, "pom.xml"),
		Dependencies: []languages.Dependency{mavenDep("org.apache.activemq", "activemq-broker", "6.2.7", "")},
	}
	if err := (&Maven{}).Update(t.Context(), cfg); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if got := readPomProperty(t, dir, "version.activemq"); got != "6.3.3" {
		t.Errorf("parent property version.activemq: got = %q, want = %q", got, "6.3.3")
	}
}

// TestMaven_Update_ScopeOnlyKeepsPropertyValue covers a versionless entry
// (groupId@artifactId@@provided) for a dependency whose version is a property.
// The entry carries no version, so the property must keep its value.
func TestMaven_Update_ScopeOnlyKeepsPropertyValue(t *testing.T) {
	dir := copyTomeePom(t)
	cfg := &languages.UpdateConfig{
		RootDir:      dir,
		Dependencies: []languages.Dependency{mavenDep("org.apache.activemq", "activemq-broker", "", "provided")},
	}
	if err := (&Maven{}).Update(t.Context(), cfg); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if got := readPomProperty(t, dir, "version.activemq"); got != "6.3.3" {
		t.Errorf("property version.activemq: got = %q, want = %q", got, "6.3.3")
	}
}

// TestPatchProject_MissingDependenciesKeepRequestOrder checks that
// dependencies added to <dependencyManagement> appear in the order they were
// requested, so the same request always writes the same POM. The patch is run
// repeatedly because a wrong order may only show up on some runs.
func TestPatchProject_MissingDependenciesKeepRequestOrder(t *testing.T) {
	// Pins TomEE's root POM does not declare.
	patches := []Patch{
		{GroupID: "com.fasterxml.jackson.core", ArtifactID: "jackson-core", Version: "2.21.7"},
		{GroupID: "io.netty", ArtifactID: "netty-codec-http", Version: "4.1.128.Final"},
		{GroupID: "org.yaml", ArtifactID: "snakeyaml", Version: "2.4"},
		{GroupID: "commons-io", ArtifactID: "commons-io", Version: "2.20.0"},
		{GroupID: "org.apache.commons", ArtifactID: "commons-lang3", Version: "3.18.0"},
		{GroupID: "com.google.guava", ArtifactID: "guava", Version: "33.4.8-jre"},
	}
	want := make([]string, 0, len(patches))
	for _, p := range patches {
		want = append(want, p.GroupID+":"+p.ArtifactID)
	}

	for range 20 {
		project, err := PatchProject(t.Context(), &gopom.Project{}, patches, nil)
		if err != nil {
			t.Fatalf("PatchProject() error = %v", err)
		}
		managed := *project.DependencyManagement.Dependencies
		got := make([]string, 0, len(managed))
		for _, dep := range managed {
			got = append(got, dep.GroupID+":"+dep.ArtifactID)
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("dependencyManagement order (-want, +got):\n%s", diff)
		}
	}
}
