/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradlefile

import (
	"strings"
	"testing"
)

func mustParseBuild(t *testing.T, path, content string) *BuildFile {
	t.Helper()
	f, err := ParseBuild(path, []byte(content))
	if err != nil {
		t.Fatalf("ParseBuild() error = %v", err)
	}
	return f
}

func findDecl(t *testing.T, f *BuildFile, group, artifact string, kind DeclKind) DependencyDecl {
	t.Helper()
	for _, d := range f.Dependencies() {
		if d.Group == group && d.Artifact == artifact && d.Kind == kind {
			return d
		}
	}
	t.Fatalf("declaration %s:%s (kind %d) not found in %v", group, artifact, kind, f.Dependencies())
	return DependencyDecl{}
}

func findVar(t *testing.T, f *BuildFile, path string) VarDef {
	t.Helper()
	for _, v := range f.Variables() {
		if v.Path() == path {
			return v
		}
	}
	t.Fatalf("variable %s not found in %v", path, f.Variables())
	return VarDef{}
}

// lookupVar returns the variable with the given path, if recorded.
func lookupVar(f *BuildFile, path string) (VarDef, bool) {
	for _, v := range f.Variables() {
		if v.Path() == path {
			return v, true
		}
	}
	return VarDef{}, false
}

func TestParseBuild_StringNotation(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		content string
		wantDSL DSL
	}{
		{
			name:    "kotlin with parens",
			path:    "build.gradle.kts",
			content: `dependencies { implementation("io.netty:netty-codec:4.1.100.Final") }`,
			wantDSL: Kotlin,
		},
		{
			name:    "groovy with parens single quotes",
			path:    "build.gradle",
			content: `dependencies { implementation('io.netty:netty-codec:4.1.100.Final') }`,
			wantDSL: Groovy,
		},
		{
			name: "groovy without parens",
			path: "build.gradle",
			content: `dependencies {
    implementation "io.netty:netty-codec:4.1.100.Final"
}`,
			wantDSL: Groovy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := mustParseBuild(t, tt.path, tt.content)
			if f.DSL() != tt.wantDSL {
				t.Errorf("DSL() = %v, want %v", f.DSL(), tt.wantDSL)
			}
			d := findDecl(t, f, "io.netty", "netty-codec", StringNotation)
			if d.Version != "4.1.100.Final" {
				t.Errorf("Version = %q, want 4.1.100.Final", d.Version)
			}

			if err := f.SetDependencyVersion(d, "4.1.133.Final"); err != nil {
				t.Fatalf("SetDependencyVersion() error = %v", err)
			}
			updated := string(f.Content())
			if !strings.Contains(updated, "io.netty:netty-codec:4.1.133.Final") {
				t.Errorf("updated content missing new version:\n%s", updated)
			}
			if !f.Changed() {
				t.Error("Changed() = false after edit")
			}
		})
	}
}

func TestParseBuild_InterpolatedVersions(t *testing.T) {
	content := `dependencies {
    implementation "io.netty:netty-handler:${nettyVersion}"
    implementation "org.apache.logging.log4j:log4j-api:$versions.log4j2"
    api "com.example:lib:$libVersion"
}`
	f := mustParseBuild(t, "build.gradle", content)

	tests := []struct {
		group, artifact, wantRef string
	}{
		{"io.netty", "netty-handler", "nettyVersion"},
		{"org.apache.logging.log4j", "log4j-api", "versions.log4j2"},
		{"com.example", "lib", "libVersion"},
	}
	for _, tt := range tests {
		d := findDecl(t, f, tt.group, tt.artifact, StringNotation)
		if d.VarRef != tt.wantRef {
			t.Errorf("%s:%s VarRef = %q, want %q", tt.group, tt.artifact, d.VarRef, tt.wantRef)
		}
		if d.Version != "" {
			t.Errorf("%s:%s Version = %q, want empty for interpolated", tt.group, tt.artifact, d.Version)
		}
		if err := f.SetDependencyVersion(d, "1.0.0"); err == nil {
			t.Errorf("SetDependencyVersion() on interpolated decl should error")
		}
	}
}

func TestParseBuild_MapNotation(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		content string
		kind    DeclKind
	}{
		{
			name:    "kotlin named arguments",
			path:    "build.gradle.kts",
			content: `implementation(group = "ch.qos.logback", name = "logback-classic", version = "1.5.20")`,
			kind:    MapNotationKotlin,
		},
		{
			name:    "groovy map notation",
			path:    "build.gradle",
			content: `implementation group: 'ch.qos.logback', name: 'logback-classic', version: '1.5.20'`,
			kind:    MapNotationGroovy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := mustParseBuild(t, tt.path, tt.content)
			d := findDecl(t, f, "ch.qos.logback", "logback-classic", tt.kind)
			if d.Version != "1.5.20" {
				t.Fatalf("Version = %q, want 1.5.20", d.Version)
			}
			if err := f.SetDependencyVersion(d, "1.5.25"); err != nil {
				t.Fatalf("SetDependencyVersion() error = %v", err)
			}
			if !strings.Contains(string(f.Content()), "1.5.25") {
				t.Errorf("updated content missing new version:\n%s", f.Content())
			}
		})
	}
}

func TestParseBuild_GroovyMapNotation_BareVariable(t *testing.T) {
	content := `implementation group: 'org.lz4', name: 'lz4-java', version: versions.lz4`
	f := mustParseBuild(t, "build.gradle", content)
	d := findDecl(t, f, "org.lz4", "lz4-java", MapNotationGroovy)
	if d.VarRef != "versions.lz4" {
		t.Errorf("VarRef = %q, want versions.lz4", d.VarRef)
	}
}

func TestParseBuild_LibraryFn(t *testing.T) {
	content := `bom {
    library("Commons Lang3", "3.17.0") {
        group("org.apache.commons") {
            modules = ["commons-lang3"]
        }
    }
}`
	f := mustParseBuild(t, "build.gradle", content)
	var found DependencyDecl
	for _, d := range f.Dependencies() {
		if d.Kind == LibraryFn && d.Artifact == "Commons Lang3" {
			found = d
		}
	}
	if found.Version != "3.17.0" {
		t.Fatalf("library() version = %q, want 3.17.0", found.Version)
	}
	if err := f.SetDependencyVersion(found, "3.18.0"); err != nil {
		t.Fatalf("SetDependencyVersion() error = %v", err)
	}
	if !strings.Contains(string(f.Content()), `library("Commons Lang3", "3.18.0")`) {
		t.Errorf("updated content missing new library version:\n%s", f.Content())
	}
}

func TestParseBuild_StrictlyBlocks(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		content   string
		wantAlias string
		wantGA    [2]string
	}{
		{
			name:      "kotlin catalog alias",
			path:      "build.gradle.kts",
			content:   `api(libs.commonsLang3)  { version { strictly("3.14.0") }}`,
			wantAlias: "commonsLang3",
		},
		{
			name:    "groovy quoted coordinate",
			path:    "build.gradle",
			content: `api('org.apache.commons:commons-lang3') { version { strictly '3.14.0' } }`,
			wantGA:  [2]string{"org.apache.commons", "commons-lang3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := mustParseBuild(t, tt.path, tt.content)
			var found *DependencyDecl
			for _, d := range f.Dependencies() {
				if d.Kind == StrictlyBlock {
					found = &d
					break
				}
			}
			if found == nil {
				t.Fatalf("no StrictlyBlock declaration found in %v", f.Dependencies())
			}
			if tt.wantAlias != "" && found.CatalogAlias != tt.wantAlias {
				t.Errorf("CatalogAlias = %q, want %q", found.CatalogAlias, tt.wantAlias)
			}
			if tt.wantGA[0] != "" && (found.Group != tt.wantGA[0] || found.Artifact != tt.wantGA[1]) {
				t.Errorf("coordinates = %s:%s, want %s:%s", found.Group, found.Artifact, tt.wantGA[0], tt.wantGA[1])
			}
			if found.Version != "3.14.0" {
				t.Errorf("Version = %q, want 3.14.0", found.Version)
			}
			if err := f.SetDependencyVersion(*found, "3.18.0"); err != nil {
				t.Fatalf("SetDependencyVersion() error = %v", err)
			}
			if !strings.Contains(string(f.Content()), "3.18.0") {
				t.Errorf("updated content missing new strictly version:\n%s", f.Content())
			}
		})
	}
}

func TestParseBuild_DependencySets(t *testing.T) {
	content := `dependencyManagement {
  dependencies {
      dependencySet(group: 'ch.qos.logback', version: '1.5.32') {
        entry 'logback-classic'
        entry 'logback-core'
      }
      dependencySet("io.netty:4.1.100.Final") {
        entry("netty-codec")
      }
  }
}`
	f := mustParseBuild(t, "build.gradle", content)

	classic := findDecl(t, f, "ch.qos.logback", "logback-classic", DependencySet)
	core := findDecl(t, f, "ch.qos.logback", "logback-core", DependencySet)
	if classic.Version != "1.5.32" || core.Version != "1.5.32" {
		t.Errorf("dependencySet versions = %q/%q, want 1.5.32", classic.Version, core.Version)
	}
	codec := findDecl(t, f, "io.netty", "netty-codec", DependencySet)
	if codec.Version != "4.1.100.Final" {
		t.Errorf("coordinate-form dependencySet version = %q", codec.Version)
	}

	// Entries share one version literal; editing either rewrites the set.
	if err := f.SetDependencyVersion(classic, "1.5.35"); err != nil {
		t.Fatalf("SetDependencyVersion() error = %v", err)
	}
	if err := f.SetDependencyVersion(core, "1.5.35"); err != nil {
		t.Fatalf("identical set edit should be a no-op, got %v", err)
	}
	if err := f.SetDependencyVersion(core, "1.5.36"); err == nil {
		t.Error("conflicting versions for one dependencySet should error")
	}
	if !strings.Contains(string(f.Content()), `version: '1.5.35'`) {
		t.Errorf("set version not updated:\n%s", f.Content())
	}
}

func TestParseBuild_CatalogRefs(t *testing.T) {
	content := `dependencies {
    implementation(libs.netty.codec)
    api libs.commons.lang3
}`
	f := mustParseBuild(t, "build.gradle", content)
	aliases := make(map[string]struct{})
	for _, d := range f.Dependencies() {
		if d.Kind == CatalogRef {
			aliases[d.CatalogAlias] = struct{}{}
		}
	}
	for _, want := range []string{"netty.codec", "commons.lang3"} {
		if _, found := aliases[want]; !found {
			t.Errorf("catalog ref %q not found, got %v", want, aliases)
		}
	}
}

func TestParseBuild_ExtVersionMaps(t *testing.T) {
	// Kafka-style gradle/dependencies.gradle.
	content := `ext {
  versions = [:]
  libs = [:]
}

versions += [
  log4j2: "2.25.1",
  jetty: "12.0.25",
  'quoted-key': "1.0.0",
]

libs += [
  log4j2Api: "org.apache.logging.log4j:log4j-api:$versions.log4j2",
]`
	f := mustParseBuild(t, "dependencies.gradle", content)

	v := findVar(t, f, "versions.log4j2")
	if v.Value != "2.25.1" {
		t.Errorf("versions.log4j2 = %q, want 2.25.1", v.Value)
	}
	if v.MapName != "versions" || v.Name != "log4j2" {
		t.Errorf("MapName/Name = %q/%q, want versions/log4j2", v.MapName, v.Name)
	}
	findVar(t, f, "versions.jetty")
	findVar(t, f, "versions.quoted-key")

	// Coordinate entries in the libs map are declarations, not variables.
	d := findDecl(t, f, "org.apache.logging.log4j", "log4j-api", StringNotation)
	if d.VarRef != "versions.log4j2" {
		t.Errorf("libs map coordinate VarRef = %q, want versions.log4j2", d.VarRef)
	}
	for _, v := range f.Variables() {
		if v.Name == "log4j2Api" {
			t.Errorf("coordinate entry log4j2Api should not be recorded as a variable")
		}
	}

	// Edit a map entry value in place.
	if err := f.SetVariable(v, "2.25.4"); err != nil {
		t.Fatalf("SetVariable() error = %v", err)
	}
	if !strings.Contains(string(f.Content()), `log4j2: "2.25.4",`) {
		t.Errorf("updated content missing new map entry value:\n%s", f.Content())
	}
}

func TestParseBuild_ExtBlockAndFlatVariables(t *testing.T) {
	content := `ext {
    nettyVersion = '4.1.100.Final'
    version = '9.9.9'
}
ext.jacksonVersion = "2.17.2"
def slf4jVersion = '2.0.9'`
	f := mustParseBuild(t, "build.gradle", content)

	if findVar(t, f, "nettyVersion").Value != "4.1.100.Final" {
		t.Error("nettyVersion not parsed from ext block")
	}
	if findVar(t, f, "jacksonVersion").Value != "2.17.2" {
		t.Error("jacksonVersion not parsed from ext.x assignment")
	}
	if findVar(t, f, "slf4jVersion").Value != "2.0.9" {
		t.Error("slf4jVersion not parsed from def assignment")
	}
	// Reserved project attribute names are never recorded as variables.
	for _, v := range f.Variables() {
		if v.Name == "version" && v.MapName == "" {
			t.Error("reserved name 'version' must not be recorded as a variable")
		}
	}
}

func TestParseBuild_KotlinExtraVariables(t *testing.T) {
	content := `extra["nettyVersion"] = "4.1.100.Final"
val jacksonVersion by extra("2.17.2")`
	f := mustParseBuild(t, "build.gradle.kts", content)

	if findVar(t, f, "nettyVersion").Value != "4.1.100.Final" {
		t.Error("nettyVersion not parsed from extra[...] assignment")
	}
	if findVar(t, f, "jacksonVersion").Value != "2.17.2" {
		t.Error("jacksonVersion not parsed from by extra(...) delegate")
	}
}

func TestParseBuild_TypedVariableDeclarations(t *testing.T) {
	// Elasticsearch libs/x-content/impl/build.gradle style: a typed String
	// declaration interpolated into coordinates.
	content := `String jacksonVersion = "2.17.2"
final String slf4jVersion = '2.0.9'
var log4jVersion = "2.25.1"

dependencies {
    implementation "com.fasterxml.jackson.core:jackson-core:${jacksonVersion}"
}`
	f := mustParseBuild(t, "build.gradle", content)

	if findVar(t, f, "jacksonVersion").Value != "2.17.2" {
		t.Error("jacksonVersion not parsed from String declaration")
	}
	if findVar(t, f, "slf4jVersion").Value != "2.0.9" {
		t.Error("slf4jVersion not parsed from final String declaration")
	}
	if findVar(t, f, "log4jVersion").Value != "2.25.1" {
		t.Error("log4jVersion not parsed from var declaration")
	}

	// The coordinate resolves its version through the typed variable.
	d := findDecl(t, f, "com.fasterxml.jackson.core", "jackson-core", StringNotation)
	if d.VarRef != "jacksonVersion" {
		t.Errorf("coordinate VarRef = %q, want jacksonVersion", d.VarRef)
	}

	// The typed declaration is editable in place.
	if err := f.SetVariable(findVar(t, f, "jacksonVersion"), "2.18.0"); err != nil {
		t.Fatalf("SetVariable() error = %v", err)
	}
	if !strings.Contains(string(f.Content()), `String jacksonVersion = "2.18.0"`) {
		t.Errorf("updated content missing new String value:\n%s", f.Content())
	}
}

func TestParseBuild_KotlinValDeclarations(t *testing.T) {
	content := `val jacksonVersion = "2.17.2"
val nettyVersion: String = "4.1.100.Final"
const val log4jVersion = "2.25.1"`
	f := mustParseBuild(t, "build.gradle.kts", content)

	if findVar(t, f, "jacksonVersion").Value != "2.17.2" {
		t.Error("jacksonVersion not parsed from bare val declaration")
	}
	if findVar(t, f, "nettyVersion").Value != "4.1.100.Final" {
		t.Error("nettyVersion not parsed from typed val declaration")
	}
	if findVar(t, f, "log4jVersion").Value != "2.25.1" {
		t.Error("log4jVersion not parsed from const val declaration")
	}

	if err := f.SetVariable(findVar(t, f, "nettyVersion"), "4.1.133.Final"); err != nil {
		t.Fatalf("SetVariable() error = %v", err)
	}
	if !strings.Contains(string(f.Content()), `val nettyVersion: String = "4.1.133.Final"`) {
		t.Errorf("updated content missing new val value:\n%s", f.Content())
	}
}

func TestParseBuild_TypedDeclarationsSkipReservedNames(t *testing.T) {
	content := `String version = "9.9.9"
val group = "com.example"`
	groovy := mustParseBuild(t, "build.gradle", content)
	for _, v := range groovy.Variables() {
		if v.Name == "version" {
			t.Error("reserved name 'version' must not be recorded from String declaration")
		}
	}
	kotlin := mustParseBuild(t, "build.gradle.kts", content)
	for _, v := range kotlin.Variables() {
		if v.Name == "group" {
			t.Error("reserved name 'group' must not be recorded from val declaration")
		}
	}
}

func TestParseBuild_ExtVersionMapLeftShiftAppend(t *testing.T) {
	// Elasticsearch modules/repository-azure/build.gradle style: a `<<` append
	// to an existing versions map, interpolated into coordinates.
	content := `versions << [
  'azureReactorNetty': '1.0.45',
]

dependencies {
    api "io.projectreactor.netty:reactor-netty-core:${versions.azureReactorNetty}"
}`
	f := mustParseBuild(t, "build.gradle", content)

	v := findVar(t, f, "versions.azureReactorNetty")
	if v.Value != "1.0.45" {
		t.Errorf("versions.azureReactorNetty = %q, want 1.0.45", v.Value)
	}
	if v.MapName != "versions" || v.Name != "azureReactorNetty" {
		t.Errorf("MapName/Name = %q/%q, want versions/azureReactorNetty", v.MapName, v.Name)
	}

	d := findDecl(t, f, "io.projectreactor.netty", "reactor-netty-core", StringNotation)
	if d.VarRef != "versions.azureReactorNetty" {
		t.Errorf("coordinate VarRef = %q, want versions.azureReactorNetty", d.VarRef)
	}

	if err := f.SetVariable(v, "1.0.48"); err != nil {
		t.Fatalf("SetVariable() error = %v", err)
	}
	if !strings.Contains(string(f.Content()), `'azureReactorNetty': '1.0.48',`) {
		t.Errorf("updated content missing new map-append entry value:\n%s", f.Content())
	}
}

// --- Over-broad / edge-case characterization tests (code-review findings) ---
//
// These tests document the boundaries of the widened typed-variable and `<<`
// map-append matching. Each asserts the behavior a careful reviewer would
// expect; a failure means the reviewed diff over- or under-matches there.

// Finding #2 (accepted limitation): `<<` is Groovy's generic collection-append
// operator, so a plain list append whose elements are map literals is mined as
// if it were a versions map. This is the same syntactic ambiguity the existing
// `+=` / `=` map forms carry and is treated identically: harmless because such
// entries are only ever rewritten if a dependency coordinate interpolates them
// by path (e.g. "${releaseNotes.build}"), which real collection appends never
// do. This test documents the boundary rather than asserting a fix.
func TestParseBuild_LeftShiftListAppendIsAcceptedOverCapture(t *testing.T) {
	content := `releaseNotes << [
    title: 'Initial',
    build: '1.2.3',
]`
	f := mustParseBuild(t, "build.gradle", content)
	if _, ok := lookupVar(f, "releaseNotes.build"); !ok {
		t.Errorf("expected `<<` map entries to be captured (consistent with `+=`): %v", f.Variables())
	}
}

// Finding #3: the Kotlin `val` pattern and the Groovy `def/var/String` pattern
// must be gated by DSL, so a Kotlin `val` is not captured in a Groovy build
// file where it has no meaning as a version declaration.
func TestParseBuild_CrossDSLValInGroovyFile(t *testing.T) {
	content := `val someConstant = "not-a-version"`
	f := mustParseBuild(t, "build.gradle", content) // Groovy DSL
	if _, ok := lookupVar(f, "someConstant"); ok {
		t.Errorf("Kotlin `val` captured in a Groovy build file: %v", f.Variables())
	}
}

// Finding #4: a typed String whose value is a full Maven coordinate is a
// coordinate, not a version, so it must not be recorded as a redundant version
// variable (scanFlatAssignments now guards `:`-bearing values, mirroring
// scanExtMaps). The coordinate-literal scan still records the dependency
// itself, so the version remains editable in place.
func TestParseBuild_TypedStringWithCoordinateValue(t *testing.T) {
	content := `String lz4 = "org.lz4:lz4-java:1.8.0"`
	f := mustParseBuild(t, "build.gradle", content)
	if v, ok := lookupVar(f, "lz4"); ok {
		t.Errorf("coordinate-valued String captured as a version variable: value=%q", v.Value)
	}
	// The dependency is still recognized with an editable literal version.
	d := findDecl(t, f, "org.lz4", "lz4-java", StringNotation)
	if d.Version != "1.8.0" {
		t.Errorf("coordinate version = %q, want 1.8.0", d.Version)
	}
	if err := f.SetDependencyVersion(d, "1.8.1"); err != nil {
		t.Fatalf("SetDependencyVersion() error = %v", err)
	}
	if !strings.Contains(string(f.Content()), `"org.lz4:lz4-java:1.8.1"`) {
		t.Errorf("coordinate version not editable in place:\n%s", f.Content())
	}
}

// Finding #5: annotation-prefixed declarations are missed. A Groovy script
// field annotated with @Field is a real version source; failing to capture it
// means a bump leaves the source literal stale.
func TestParseBuild_AnnotationPrefixedDeclarationCaptured(t *testing.T) {
	content := `@Field String nettyVersion = '4.1.100.Final'

dependencies {
    implementation "io.netty:netty-codec:${nettyVersion}"
}`
	f := mustParseBuild(t, "build.gradle", content)
	if _, ok := lookupVar(f, "nettyVersion"); !ok {
		t.Errorf("@Field-annotated version declaration not captured; bump would leave it stale: %v", f.Variables())
	}
}

func TestParseBuild_SkipsComments(t *testing.T) {
	content := `dependencies {
    // implementation("io.netty:netty-codec:4.1.0.Final")
    implementation("io.netty:netty-handler:4.1.100.Final")
}`
	f := mustParseBuild(t, "build.gradle.kts", content)
	for _, d := range f.Dependencies() {
		if d.Artifact == "netty-codec" {
			t.Error("commented-out declaration should be skipped")
		}
	}
}

func TestParseBuild_FormatPreservedOutsideEdit(t *testing.T) {
	content := "dependencies {\n\timplementation(\"a.b:c:1.0\")   // trailing comment\n}\n"
	f := mustParseBuild(t, "build.gradle.kts", content)
	d := findDecl(t, f, "a.b", "c", StringNotation)
	if err := f.SetDependencyVersion(d, "2.0"); err != nil {
		t.Fatalf("SetDependencyVersion() error = %v", err)
	}
	want := "dependencies {\n\timplementation(\"a.b:c:2.0\")   // trailing comment\n}\n"
	if string(f.Content()) != want {
		t.Errorf("content not byte-identical outside edit:\ngot:  %q\nwant: %q", f.Content(), want)
	}
}

func TestSetDependencyVersion_RejectsInjection(t *testing.T) {
	f := mustParseBuild(t, "build.gradle", `implementation "a.b:c:1.0"`)
	d := findDecl(t, f, "a.b", "c", StringNotation)
	for _, payload := range []string{
		`1.0"; exec("rm -rf /") //`,
		"1.0\nmalicious()",
		`1.0')`,
		`${evil}`,
	} {
		if err := f.SetDependencyVersion(d, payload); err == nil {
			t.Errorf("SetDependencyVersion(%q) should reject injection payload", payload)
		}
	}
}

func TestEditBuffer_ConflictDetection(t *testing.T) {
	f := mustParseBuild(t, "build.gradle", `implementation "a.b:c:1.0"`)
	d := findDecl(t, f, "a.b", "c", StringNotation)
	if err := f.SetDependencyVersion(d, "2.0"); err != nil {
		t.Fatalf("first edit error = %v", err)
	}
	// Same span, same replacement: no-op.
	if err := f.SetDependencyVersion(d, "2.0"); err != nil {
		t.Errorf("identical re-edit should be a no-op, got %v", err)
	}
	// Same span, different replacement: conflict.
	if err := f.SetDependencyVersion(d, "3.0"); err == nil {
		t.Error("conflicting edit should error")
	}
	if f.ChangeCount() != 1 {
		t.Errorf("ChangeCount() = %d, want 1", f.ChangeCount())
	}
}

func TestParseBuild_ResolutionRules(t *testing.T) {
	content := `eachDependency { DependencyResolveDetails details ->
    if (details.requested.group == "io.netty" && !details.requested.name.startsWith("netty-tcnative-")) {
        details.useVersion(libs.versions.netty.get())
    }
    if (details.requested.group == 'com.signalfx.public') {
        if (details.requested.name == 'signalfx-java') {
            details.useVersion '1.0.49'
        }
    }
    if (details.requested.group == 'org.yaml' && details.requested.name == 'snakeyaml') {
        details.useVersion("$snakeyamlVersion")
    }
}`
	f := mustParseBuild(t, "build.gradle", content)

	rules := f.ResolutionRules()
	if len(rules) != 3 {
		t.Fatalf("ResolutionRules() = %d rules, want 3: %+v", len(rules), rules)
	}

	// Group-wide rule reading a catalog version accessor.
	if r := rules[0]; r.Group != "io.netty" || r.Artifact != "" || r.CatalogKey != "netty" {
		t.Errorf("catalog accessor rule = %+v", r)
	}
	// Kayenta-style nested rule with a literal: outer group, inner name.
	if r := rules[1]; r.Group != "com.signalfx.public" || r.Artifact != "signalfx-java" || r.Version != "1.0.49" {
		t.Errorf("nested literal rule = %+v", r)
	}
	// Interpolated variable rule.
	if r := rules[2]; r.Group != "org.yaml" || r.Artifact != "snakeyaml" || r.VarRef != "snakeyamlVersion" {
		t.Errorf("variable rule = %+v", r)
	}

	// Literal rules are editable in place.
	if err := f.SetResolutionRuleVersion(rules[1], "1.0.50"); err != nil {
		t.Fatalf("SetResolutionRuleVersion() error = %v", err)
	}
	if !strings.Contains(string(f.Content()), `details.useVersion '1.0.50'`) {
		t.Errorf("literal rule not updated:\n%s", f.Content())
	}
	// Catalog-backed rules are not editable in place.
	if err := f.SetResolutionRuleVersion(rules[0], "1.0.0"); err == nil {
		t.Error("SetResolutionRuleVersion() on a catalog-backed rule should error")
	}
}
