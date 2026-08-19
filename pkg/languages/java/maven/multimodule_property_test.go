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
)

// writeModule writes a POM at dir/relPath, creating parent directories.
func writeModule(t *testing.T, root, relPath, content string) string {
	t.Helper()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", full, err)
	}
	return full
}

func mavenDep(groupID, artifactID, version string) languages.Dependency {
	return languages.Dependency{
		Name:    groupID + ":" + artifactID,
		Version: version,
		Metadata: map[string]any{
			"groupId":    groupID,
			"artifactId": artifactID,
		},
	}
}

// aggregatorPom declares the property; the dependency that references it lives
// in the submodule. This is the shape AUTO-875 was filed against.
const aggregatorPom = `<?xml version="1.0" encoding="UTF-8"?>
<!--
    Copyright Example, Inc. Licensed under Apache-2.0.
-->
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>aggregator</artifactId>
  <version>1.0.0</version>
  <packaging>pom</packaging>
  <properties>
    <logback.version>1.5.32</logback.version>
  </properties>
  <modules>
    <module>server</module>
  </modules>
</project>
`

const serverModulePom = `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>com.example</groupId>
    <artifactId>aggregator</artifactId>
    <version>1.0.0</version>
  </parent>
  <artifactId>server</artifactId>
  <dependencies>
    <dependency>
      <groupId>ch.qos.logback</groupId>
      <artifactId>logback-core</artifactId>
      <version>${logback.version}</version>
    </dependency>
  </dependencies>
</project>
`

// A dependency declared only in a submodule but versioned by an aggregator
// property must be bumped through that property. Before this was handled, the
// pin was appended to the aggregator's <dependencyManagement> as a scope=import
// entry, which does not change what the submodule resolves — the CVE stayed
// unfixed while the run reported success.
func TestUpdate_SubmoduleDepUsesAggregatorProperty(t *testing.T) {
	root := t.TempDir()
	rootPom := writeModule(t, root, "pom.xml", aggregatorPom)
	writeModule(t, root, "server/pom.xml", serverModulePom)

	cfg := &languages.UpdateConfig{
		RootDir:      root,
		Dependencies: []languages.Dependency{mavenDep("ch.qos.logback", "logback-core", "1.5.35")},
	}
	if err := (&Maven{}).Update(t.Context(), cfg); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	updated, err := os.ReadFile(rootPom)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := string(updated)

	if !strings.Contains(got, "<logback.version>1.5.35</logback.version>") {
		t.Errorf("aggregator property was not bumped:\n%s", got)
	}
	if strings.Contains(got, "dependencyManagement") {
		t.Errorf("a redundant dependencyManagement entry was added instead of using the property:\n%s", got)
	}
	if strings.Contains(got, "scope>import") {
		t.Errorf("an invalid scope=import pin was written:\n%s", got)
	}
}

// The property rewrite must not disturb the rest of the file. Repositories that
// enforce a license header with license-maven-plugin fail their own build when
// the header is dropped, which is what pushed several packages off omnibump and
// onto hand-written sed steps.
func TestUpdate_PropertyBumpPreservesLicenseHeaderAndFormatting(t *testing.T) {
	root := t.TempDir()
	rootPom := writeModule(t, root, "pom.xml", aggregatorPom)
	writeModule(t, root, "server/pom.xml", serverModulePom)

	cfg := &languages.UpdateConfig{
		RootDir:      root,
		Dependencies: []languages.Dependency{mavenDep("ch.qos.logback", "logback-core", "1.5.35")},
	}
	if err := (&Maven{}).Update(t.Context(), cfg); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	updated, err := os.ReadFile(rootPom)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	want := strings.Replace(aggregatorPom,
		"<logback.version>1.5.32</logback.version>",
		"<logback.version>1.5.35</logback.version>", 1)
	if string(updated) != want {
		t.Errorf("property bump rewrote more than the value.\ngot:\n%s\nwant:\n%s", updated, want)
	}
}

// A property declared in the submodule itself, with no aggregator declaration,
// must be updated where it actually lives.
func TestUpdate_PropertyDeclaredInSubmodule(t *testing.T) {
	root := t.TempDir()
	writeModule(t, root, "pom.xml", `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>aggregator</artifactId>
  <version>1.0.0</version>
  <packaging>pom</packaging>
</project>
`)
	subPom := writeModule(t, root, "server/pom.xml", `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>server</artifactId>
  <version>1.0.0</version>
  <properties>
    <logback.version>1.5.32</logback.version>
  </properties>
  <dependencies>
    <dependency>
      <groupId>ch.qos.logback</groupId>
      <artifactId>logback-core</artifactId>
      <version>${logback.version}</version>
    </dependency>
  </dependencies>
</project>
`)

	cfg := &languages.UpdateConfig{
		RootDir:      root,
		Dependencies: []languages.Dependency{mavenDep("ch.qos.logback", "logback-core", "1.5.35")},
	}
	if err := (&Maven{}).Update(t.Context(), cfg); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	updated, err := os.ReadFile(subPom)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(updated), "<logback.version>1.5.35</logback.version>") {
		t.Errorf("submodule property was not bumped:\n%s", updated)
	}
}

// When two sibling modules declare the same property name, resolution starts at
// the module that declares the dependency and walks its own parent chain, so an
// unrelated sibling's copy is never picked.
func TestUpdate_SamePropertyNameInSiblingModuleIsNotChosen(t *testing.T) {
	root := t.TempDir()
	rootPom := writeModule(t, root, "pom.xml", aggregatorPom)
	writeModule(t, root, "server/pom.xml", serverModulePom)
	// A sibling that declares the same property but no matching dependency.
	siblingPom := writeModule(t, root, "agent/pom.xml", `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>agent</artifactId>
  <version>1.0.0</version>
  <properties>
    <logback.version>1.4.0</logback.version>
  </properties>
</project>
`)

	cfg := &languages.UpdateConfig{
		RootDir:      root,
		Dependencies: []languages.Dependency{mavenDep("ch.qos.logback", "logback-core", "1.5.35")},
	}
	if err := (&Maven{}).Update(t.Context(), cfg); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	rootContent, err := os.ReadFile(rootPom)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(rootContent), "<logback.version>1.5.35</logback.version>") {
		t.Errorf("the declaring module's parent chain should own the update:\n%s", rootContent)
	}

	siblingContent, err := os.ReadFile(siblingPom)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(siblingContent), "<logback.version>1.4.0</logback.version>") {
		t.Errorf("unrelated sibling module was modified:\n%s", siblingContent)
	}
}

// A dependency that is not property-backed anywhere keeps the existing direct
// patch behaviour, so widening detection did not change the common case.
func TestUpdate_NonPropertyDepStillPatchedDirectly(t *testing.T) {
	root := t.TempDir()
	rootPom := writeModule(t, root, "pom.xml", `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>app</artifactId>
  <version>1.0.0</version>
  <dependencies>
    <dependency>
      <groupId>io.netty</groupId>
      <artifactId>netty-codec-http</artifactId>
      <version>4.1.90.Final</version>
    </dependency>
  </dependencies>
</project>
`)

	cfg := &languages.UpdateConfig{
		RootDir:      root,
		Dependencies: []languages.Dependency{mavenDep("io.netty", "netty-codec-http", "4.1.94.Final")},
	}
	if err := (&Maven{}).Update(t.Context(), cfg); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	project, err := ParsePom(rootPom)
	if err != nil {
		t.Fatalf("ParsePom: %v", err)
	}
	if project.Dependencies == nil {
		t.Fatal("Dependencies is nil")
	}
	for _, dep := range *project.Dependencies {
		if dep.ArtifactID == "netty-codec-http" {
			if dep.Version != "4.1.94.Final" {
				t.Errorf("netty-codec-http = %s, want 4.1.94.Final", dep.Version)
			}
			return
		}
	}
	t.Error("netty-codec-http not found after update")
}

// Detection descends from the POM under update and never climbs above it, so a
// module outside the project root cannot be read or written.
func TestPropertyScanPoms_DoesNotEscapeRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "project")
	writeModule(t, root, "pom.xml", aggregatorPom)
	writeModule(t, root, "server/pom.xml", serverModulePom)
	// A POM above the project root must never be picked up.
	writeModule(t, base, "pom.xml", aggregatorPom)

	poms := propertyScanPoms(t.Context(), filepath.Join(root, "pom.xml"), root)

	for _, p := range poms {
		rel, err := filepath.Rel(root, p)
		if err != nil || strings.HasPrefix(rel, "..") {
			t.Errorf("scan returned %s, which is outside the project root %s", p, root)
		}
	}
	if len(poms) != 2 {
		t.Errorf("scan returned %d POMs (%v), want 2", len(poms), poms)
	}
}

// A dependency versioned by a property that no in-tree POM declares (typically
// an external parent such as spring-boot-starter-parent) fails the run rather
// than silently writing a pin that will not take effect. The package is then
// flagged for a human instead of shipping an unfixed CVE.
func TestUpdate_PropertyOutsideRootFailsLoudly(t *testing.T) {
	root := t.TempDir()
	writeModule(t, root, "pom.xml", `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>app</artifactId>
  <version>1.0.0</version>
  <dependencies>
    <dependency>
      <groupId>ch.qos.logback</groupId>
      <artifactId>logback-core</artifactId>
      <version>${logback.version}</version>
    </dependency>
  </dependencies>
</project>
`)

	cfg := &languages.UpdateConfig{
		RootDir:      root,
		Dependencies: []languages.Dependency{mavenDep("ch.qos.logback", "logback-core", "1.5.35")},
	}
	err := (&Maven{}).Update(t.Context(), cfg)
	if err == nil {
		t.Fatal("Update() succeeded, want an error for an unresolvable property")
	}
	if !strings.Contains(err.Error(), "logback.version") {
		t.Errorf("Update() error = %v, want it to name the unresolvable property", err)
	}
}

// Validation resolves property-backed dependencies through the module that
// declares them, so a successful property bump is not reported as missing.
func TestValidate_FindsSubmoduleDepBumpedViaProperty(t *testing.T) {
	root := t.TempDir()
	writeModule(t, root, "pom.xml", strings.Replace(aggregatorPom,
		"<logback.version>1.5.32</logback.version>",
		"<logback.version>1.5.35</logback.version>", 1))
	writeModule(t, root, "server/pom.xml", serverModulePom)

	rootPom := filepath.Join(root, "pom.xml")
	project, err := ParsePom(rootPom)
	if err != nil {
		t.Fatalf("ParsePom: %v", err)
	}
	ctx := t.Context()
	modules := validationModules(ctx, rootPom, root, project, pomProperties(ctx, rootPom, project))

	matches := func(dep gopom.Dependency) bool {
		return dep.GroupID == "ch.qos.logback" && dep.ArtifactID == "logback-core"
	}
	found := false
	for _, module := range modules {
		if moduleHasDependencyAtVersion(module, matches, "1.5.35") {
			found = true
			break
		}
	}
	if !found {
		t.Error("validation did not resolve the submodule dependency to the bumped property value")
	}
}
