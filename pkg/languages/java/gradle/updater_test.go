/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradle

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chainguard-dev/omnibump/pkg/gradlefile"
	"github.com/chainguard-dev/omnibump/pkg/languages"
)

// copyFixture copies a testdata fixture directory into a temp dir and
// returns the temp dir path.
func copyFixture(t *testing.T, fixture string) string {
	t.Helper()
	dst := t.TempDir()
	if err := os.CopyFS(dst, os.DirFS(filepath.Join("testdata", fixture))); err != nil {
		t.Fatalf("failed to copy fixture %s: %v", fixture, err)
	}
	return dst
}

// updateAndValidate runs Update followed by Validate and fails the test on
// any error.
func updateAndValidate(t *testing.T, cfg *languages.UpdateConfig) {
	t.Helper()
	g := &Gradle{}
	if err := g.Update(t.Context(), cfg); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if err := g.Validate(t.Context(), cfg); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path, err)
	}
	return string(content)
}

func TestGradle_Update_OpenSearchStyle_VersionCatalog(t *testing.T) {
	dir := copyFixture(t, "opensearch-style")

	updateAndValidate(t, &languages.UpdateConfig{
		RootDir: dir,
		Dependencies: []languages.Dependency{
			// Routed through [libraries] module -> version.ref -> [versions];
			// the key name differs from the artifact id.
			{Name: "io.netty:netty-codec", Version: "4.2.13.Final"},
			{Name: "org.apache.logging.log4j:log4j-core", Version: "2.25.4"},
			// Inline version in the [libraries] entry.
			{Name: "com.squareup.okio:okio", Version: "3.6.0"},
		},
	})

	catalog := readFile(t, filepath.Join(dir, "gradle", "libs.versions.toml"))
	for _, want := range []string{
		`netty             = "4.2.13.Final"`, // alignment preserved
		`log4j                   = "2.25.4"`,
		`version = "3.6.0"`,
	} {
		if !strings.Contains(catalog, want) {
			t.Errorf("catalog missing %q:\n%s", want, catalog)
		}
	}
	// The build script itself is untouched: catalog refs carry no version.
	build := readFile(t, filepath.Join(dir, "build.gradle"))
	if strings.Contains(build, "4.2.13.Final") {
		t.Errorf("build.gradle should be unchanged:\n%s", build)
	}
}

func TestGradle_Update_CatalogAccessorBridge(t *testing.T) {
	dir := copyFixture(t, "opensearch-style")

	// OpenSearch-style "${versions.log4j}" references bridge to the catalog
	// version key (ext.versions = libs.versions); no ext map defines them.
	updateAndValidate(t, &languages.UpdateConfig{
		RootDir: dir,
		Dependencies: []languages.Dependency{
			{Name: "org.apache.logging.log4j:log4j-jul", Version: "2.25.4"},
		},
	})

	catalog := readFile(t, filepath.Join(dir, "gradle", "libs.versions.toml"))
	if !strings.Contains(catalog, `log4j                   = "2.25.4"`) {
		t.Errorf("catalog key not updated through accessor bridge:\n%s", catalog)
	}
	qa := readFile(t, filepath.Join(dir, "qa", "build.gradle"))
	if !strings.Contains(qa, "${versions.log4j}") {
		t.Errorf("accessor reference should stay a reference:\n%s", qa)
	}
}

func TestGradle_Update_KafkaStyle_DependenciesGradle(t *testing.T) {
	dir := copyFixture(t, "kafka-deps-style")

	updateAndValidate(t, &languages.UpdateConfig{
		RootDir: dir,
		Dependencies: []languages.Dependency{
			// Declared in the libs map as "g:a:$versions.x" -> the versions
			// map entry is updated, not the declaration.
			{Name: "com.fasterxml.jackson.core:jackson-databind", Version: "2.18.6"},
			{Name: "org.apache.logging.log4j:log4j-api", Version: "2.25.3"},
		},
	})

	deps := readFile(t, filepath.Join(dir, "gradle", "dependencies.gradle"))
	for _, want := range []string{
		`jackson: "2.18.6",`,
		`log4j2: "2.25.3",`,
		`jetty: "12.0.25",`, // untouched entries preserved
		`jacksonDatabind: "com.fasterxml.jackson.core:jackson-databind:$versions.jackson",`,
	} {
		if !strings.Contains(deps, want) {
			t.Errorf("dependencies.gradle missing %q:\n%s", want, deps)
		}
	}
}

func TestGradle_Update_KafkaStyle_PropertyByMapEntryName(t *testing.T) {
	dir := copyFixture(t, "kafka-deps-style")

	// Properties address version-map entries by their bare entry name, the
	// name users know from the build file (and from today's sed commands).
	updateAndValidate(t, &languages.UpdateConfig{
		RootDir:    dir,
		Properties: map[string]string{"jetty": "12.0.32", "lz4": "1.10.1"},
	})

	deps := readFile(t, filepath.Join(dir, "gradle", "dependencies.gradle"))
	for _, want := range []string{`jetty: "12.0.32",`, `lz4: "1.10.1",`} {
		if !strings.Contains(deps, want) {
			t.Errorf("dependencies.gradle missing %q:\n%s", want, deps)
		}
	}
}

func TestGradle_Update_SonarqubeStyle_GradleProperties(t *testing.T) {
	dir := copyFixture(t, "sonarqube-style")

	updateAndValidate(t, &languages.UpdateConfig{
		RootDir: dir,
		Dependencies: []languages.Dependency{
			// "${nettyVersion}" reference -> gradle.properties entry.
			{Name: "io.netty:netty-handler", Version: "4.1.133.Final"},
			// Groovy colon map notation, literal version.
			{Name: "ch.qos.logback:logback-classic", Version: "1.5.25"},
		},
		Properties: map[string]string{"elasticSearchServerVersion": "8.19.10"},
	})

	props := readFile(t, filepath.Join(dir, "gradle.properties"))
	for _, want := range []string{
		"nettyVersion=4.1.133.Final",
		"elasticSearchServerVersion=8.19.10",
		"version=10.4.0", // project version untouched
	} {
		if !strings.Contains(props, want) {
			t.Errorf("gradle.properties missing %q:\n%s", want, props)
		}
	}
	build := readFile(t, filepath.Join(dir, "build.gradle"))
	if !strings.Contains(build, "version: '1.5.25'") {
		t.Errorf("groovy map notation not updated:\n%s", build)
	}
	if !strings.Contains(build, "${nettyVersion}") {
		t.Errorf("variable reference should stay a reference:\n%s", build)
	}
}

func TestGradle_Update_ElasticsearchStyle_VersionProperties(t *testing.T) {
	dir := copyFixture(t, "elasticsearch-style")

	updateAndValidate(t, &languages.UpdateConfig{
		RootDir: dir,
		Dependencies: []languages.Dependency{
			// Direct literal in a module build script.
			{Name: "co.elastic.apm:elastic-apm-agent", Version: "1.55.6"},
		},
		// version.properties entries are addressable as properties.
		Properties: map[string]string{"jackson": "2.18.6"},
	})

	versions := readFile(t, filepath.Join(dir, "build-tools-internal", "version.properties"))
	if !strings.Contains(versions, "jackson           = 2.18.6") {
		t.Errorf("version.properties not updated (or separator not preserved):\n%s", versions)
	}
	if !strings.Contains(versions, "bouncycastle      = 1.78") {
		t.Errorf("unrelated entry modified:\n%s", versions)
	}
	build := readFile(t, filepath.Join(dir, "build.gradle"))
	if !strings.Contains(build, "co.elastic.apm:elastic-apm-agent:1.55.6") {
		t.Errorf("direct declaration not updated:\n%s", build)
	}
}

func TestGradle_Update_ElasticsearchStyle_TypedVarsAndMapAppend(t *testing.T) {
	dir := copyFixture(t, "elasticsearch-typed-vars-style")

	// Bumps whose coordinates interpolate a typed `String x = "..."` variable
	// and a `versions << [...]` map-append entry (AUTO-761). Both source
	// literals must be rewritten in place, not left stale, in addition to the
	// managed-block force pin.
	updateAndValidate(t, &languages.UpdateConfig{
		RootDir: dir,
		Dependencies: []languages.Dependency{
			{Name: "com.fasterxml.jackson.core:jackson-core", Version: "2.18.6"},
			{Name: "io.projectreactor.netty:reactor-netty-core", Version: "1.0.48"},
		},
	})

	build := readFile(t, filepath.Join(dir, "build.gradle"))
	for _, want := range []string{
		`String jacksonVersion = "2.18.6"`, // typed String literal edited in place
		`'azureReactorNetty': '1.0.48',`,   // `<<` map-append entry edited in place
	} {
		if !strings.Contains(build, want) {
			t.Errorf("build.gradle missing in-place edit %q:\n%s", want, build)
		}
	}
	// The interpolations are preserved (only the variable definitions changed).
	for _, want := range []string{
		`jackson-core:${jacksonVersion}`,
		`reactor-netty-core:${versions.azureReactorNetty}`,
	} {
		if !strings.Contains(build, want) {
			t.Errorf("build.gradle interpolation not preserved %q:\n%s", want, build)
		}
	}

	// The bumps are also force-pinned in the managed block.
	coords := managedCoordinates(t, filepath.Join(dir, "settings.gradle"))
	if coords["com.fasterxml.jackson.core:jackson-core"] != "2.18.6" {
		t.Errorf("jackson-core not force-pinned: %v", coords)
	}
	if coords["io.projectreactor.netty:reactor-netty-core"] != "1.0.48" {
		t.Errorf("reactor-netty-core not force-pinned: %v", coords)
	}
}

func TestGradle_Update_KotlinValCrossModuleAliasing(t *testing.T) {
	// Code-review finding #1: the widened Kotlin `val` capture treats every
	// string-valued `val` as a version source, and variableSites is keyed by
	// bare name across the whole project. The `app` module's unrelated
	// `val nettyVersion` (its own artifact version) shares a name with the
	// dependency version declared in `lib`. Bumping io.netty:netty-codec routes
	// through applyVariableRef, which sets EVERY site of the shared name --
	// clobbering the app module's unrelated declaration.
	dir := copyFixture(t, "kotlin-val-collision")

	g := &Gradle{}
	if err := g.Update(t.Context(), &languages.UpdateConfig{
		RootDir: dir,
		Dependencies: []languages.Dependency{
			{Name: "io.netty:netty-codec", Version: "4.1.118.Final"},
		},
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	lib := readFile(t, filepath.Join(dir, "lib", "build.gradle.kts"))
	if !strings.Contains(lib, `val nettyVersion = "4.1.118.Final"`) {
		t.Errorf("lib nettyVersion not bumped:\n%s", lib)
	}

	// The app module's unrelated artifact version must NOT have been rewritten.
	app := readFile(t, filepath.Join(dir, "app", "build.gradle.kts"))
	if !strings.Contains(app, `val nettyVersion = "9.9.9"`) {
		t.Errorf("app module's unrelated `val nettyVersion` was clobbered by the cross-module bump:\n%s", app)
	}
}

func TestGradle_Update_Substitution(t *testing.T) {
	dir := copyFixture(t, "kayenta-style")

	// A replace directive (coordinate swap) is applied through a
	// dependencySubstitution rule in the managed block. updateAndValidate also
	// proves post-update validation finds the new module's version, which is
	// surfaced from the substitution's target.
	updateAndValidate(t, &languages.UpdateConfig{
		RootDir: dir,
		Dependencies: []languages.Dependency{
			{Replace: true, OldName: "org.lz4:lz4-java", Name: "at.yawk.lz4:lz4-java", Version: "1.10.1"},
		},
	})

	settings := filepath.Join(dir, "settings.gradle")
	if got := readFile(t, settings); !strings.Contains(got,
		"substitute module('org.lz4:lz4-java') using module('at.yawk.lz4:lz4-java:1.10.1')") {
		t.Errorf("settings missing substitution rule:\n%s", got)
	}
	if coords := managedCoordinates(t, settings); coords["at.yawk.lz4:lz4-java"] != "1.10.1" {
		t.Errorf("substitution target not surfaced for validation: %v", coords)
	}
}

func TestGradle_Update_Substitution_AndConstraintTogether(t *testing.T) {
	dir := copyFixture(t, "kayenta-style")

	updateAndValidate(t, &languages.UpdateConfig{
		RootDir: dir,
		Dependencies: []languages.Dependency{
			// Transitive-only version pin -> constraint.
			{Name: "io.netty:netty-codec", Version: "4.1.135.Final"},
			// Coordinate swap -> substitution.
			{Replace: true, OldName: "org.lz4:lz4-java", Name: "at.yawk.lz4:lz4-java", Version: "1.10.1"},
		},
	})

	settings := filepath.Join(dir, "settings.gradle")
	coords := managedCoordinates(t, settings)
	if coords["io.netty:netty-codec"] != "4.1.135.Final" || coords["at.yawk.lz4:lz4-java"] != "1.10.1" {
		t.Errorf("managed coords = %v", coords)
	}
	got := readFile(t, settings)
	if !strings.Contains(got, "configuration.resolutionStrategy.force 'io.netty:netty-codec:4.1.135.Final'") {
		t.Errorf("constraint missing:\n%s", got)
	}
	if !strings.Contains(got, "substitute module('org.lz4:lz4-java')") {
		t.Errorf("substitution missing:\n%s", got)
	}
}

func TestGradle_Update_ManagedBlock_Groovy(t *testing.T) {
	dir := copyFixture(t, "kayenta-style")

	cfg := &languages.UpdateConfig{
		RootDir: dir,
		Dependencies: []languages.Dependency{
			// Transitive-only modules: declared nowhere in the project.
			{Name: "io.netty:netty-codec", Version: "4.1.133.Final"},
			{Name: "com.signalfx.public:signalfx-java", Version: "1.0.49"},
		},
	}
	updateAndValidate(t, cfg)

	// kayenta-style has a settings.gradle, which hosts the managed block.
	settings := filepath.Join(dir, "settings.gradle")
	coords := managedCoordinates(t, settings)
	if coords["io.netty:netty-codec"] != "4.1.133.Final" || coords["com.signalfx.public:signalfx-java"] != "1.0.49" {
		t.Errorf("managed coords = %v", coords)
	}
	if build := readFile(t, filepath.Join(dir, "build.gradle")); strings.Contains(build, gradlefile.ForceBlockBegin) {
		t.Errorf("managed block should live in settings, not build.gradle:\n%s", build)
	}

	// Re-running with one bumped version merges into the existing block.
	updateAndValidate(t, &languages.UpdateConfig{
		RootDir: dir,
		Dependencies: []languages.Dependency{
			{Name: "io.netty:netty-codec", Version: "4.1.135.Final"},
		},
	})

	if got := readFile(t, settings); strings.Count(got, gradlefile.ForceBlockBegin) != 1 {
		t.Errorf("managed block duplicated on re-run:\n%s", got)
	}
	coords = managedCoordinates(t, settings)
	if coords["io.netty:netty-codec"] != "4.1.135.Final" {
		t.Errorf("constraint not merged to new version: %v", coords)
	}
	if coords["com.signalfx.public:signalfx-java"] != "1.0.49" {
		t.Errorf("existing constraint lost on merge: %v", coords)
	}
}

func TestGradle_Update_DeclaredBumpAlsoForcePinned(t *testing.T) {
	dir := copyFixture(t, "kayenta-style")

	// A declared literal dependency is edited in place AND force-pinned in the
	// managed block. The force pin makes the bump apply uniformly across every
	// subproject classpath, so a module that pulls the same coordinate
	// transitively at the old version cannot trigger a version conflict under
	// failOnVersionConflict() (the OpenSearch ml-commons commons-io case).
	updateAndValidate(t, &languages.UpdateConfig{
		RootDir: dir,
		Dependencies: []languages.Dependency{
			{Name: "com.netflix.kayenta:kayenta-core", Version: "1.2.0"},
		},
	})

	if build := readFile(t, filepath.Join(dir, "build.gradle")); !strings.Contains(build,
		"com.netflix.kayenta:kayenta-core:1.2.0") {
		t.Errorf("declared version not edited in place:\n%s", build)
	}

	settings := filepath.Join(dir, "settings.gradle")
	if got := readFile(t, settings); !strings.Contains(got,
		"configuration.resolutionStrategy.force 'com.netflix.kayenta:kayenta-core:1.2.0'") {
		t.Errorf("declared bump not also force-pinned in managed block:\n%s", got)
	}
	if coords := managedCoordinates(t, settings); coords["com.netflix.kayenta:kayenta-core"] != "1.2.0" {
		t.Errorf("managed coords = %v", coords)
	}
}

func TestGradle_Update_ManagedBlock_InExistingSettings(t *testing.T) {
	dir := copyFixture(t, "settings-only")

	updateAndValidate(t, &languages.UpdateConfig{
		RootDir: dir,
		Dependencies: []languages.Dependency{
			{Name: "com.example:transitive", Version: "1.0.0"},
		},
	})

	// The existing settings.gradle.kts hosts the managed block; no build
	// script is created.
	settings := filepath.Join(dir, "settings.gradle.kts")
	if got := readFile(t, settings); !strings.Contains(got, `resolutionStrategy.force("com.example:transitive:1.0.0")`) {
		t.Errorf("settings.gradle.kts missing kotlin constraint:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "build.gradle.kts")); err == nil {
		t.Errorf("no build script should be created when a settings script exists")
	}
	if coords := managedCoordinates(t, settings); coords["com.example:transitive"] != "1.0.0" {
		t.Errorf("managed coords = %v", coords)
	}
}

// managedCoordinates parses the settings file at path and returns the
// effective pins of its omnibump-managed block.
func managedCoordinates(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := gradlefile.ParseSettings(path, []byte(readFile(t, path)))
	if err != nil {
		t.Fatalf("ParseSettings(%q) error = %v", path, err)
	}
	return f.ManagedCoordinates()
}

func TestGradle_Update_StrictlyWithCatalog(t *testing.T) {
	dir := copyFixture(t, "strictly-style")

	updateAndValidate(t, &languages.UpdateConfig{
		RootDir: dir,
		Dependencies: []languages.Dependency{
			{Name: "org.apache.commons:commons-lang3", Version: "3.18.0"},
		},
	})

	catalog := readFile(t, filepath.Join(dir, "gradle", "libs.versions.toml"))
	if !strings.Contains(catalog, `commons-lang3 = "3.18.0"`) {
		t.Errorf("catalog key not updated:\n%s", catalog)
	}
	// The strictly("...") literal referencing the catalog alias is kept
	// consistent with the catalog bump.
	build := readFile(t, filepath.Join(dir, "build.gradle.kts"))
	if !strings.Contains(build, `strictly("3.18.0")`) {
		t.Errorf("strictly constraint not updated:\n%s", build)
	}
}

func TestGradle_Update_MetadataCoordinates(t *testing.T) {
	// The melange bump pipeline passes groupId@artifactId@version, which the
	// config layer maps to metadata with an empty Name.
	dir := copyFixture(t, "opensearch-style")

	updateAndValidate(t, &languages.UpdateConfig{
		RootDir: dir,
		Dependencies: []languages.Dependency{
			{
				Version: "4.2.13.Final",
				Metadata: map[string]any{
					"groupId":    "io.netty",
					"artifactId": "netty-codec",
				},
			},
		},
	})

	catalog := readFile(t, filepath.Join(dir, "gradle", "libs.versions.toml"))
	if !strings.Contains(catalog, `netty             = "4.2.13.Final"`) {
		t.Errorf("metadata-identified dependency not routed to catalog:\n%s", catalog)
	}
}

func TestGradle_Update_PropertyPrecedence(t *testing.T) {
	// A dependency routed to a key that is also explicitly updated: the
	// property wins when versions agree, conflicts when they differ.
	t.Run("agreeing versions", func(t *testing.T) {
		dir := copyFixture(t, "opensearch-style")
		updateAndValidate(t, &languages.UpdateConfig{
			RootDir: dir,
			Dependencies: []languages.Dependency{
				{Name: "io.netty:netty-codec", Version: "4.2.13.Final"},
			},
			Properties: map[string]string{"netty": "4.2.13.Final"},
		})
	})

	t.Run("conflicting versions", func(t *testing.T) {
		dir := copyFixture(t, "opensearch-style")
		g := &Gradle{}
		err := g.Update(t.Context(), &languages.UpdateConfig{
			RootDir: dir,
			Dependencies: []languages.Dependency{
				{Name: "io.netty:netty-codec", Version: "4.2.13.Final"},
			},
			Properties: map[string]string{"netty": "4.2.14.Final"},
		})
		if !errors.Is(err, ErrVersionConflict) {
			t.Errorf("Update() error = %v, want ErrVersionConflict", err)
		}
	})
}

func TestGradle_Update_ConflictingDependencyVersions(t *testing.T) {
	dir := copyFixture(t, "opensearch-style")

	// netty-codec and netty-handler share the "netty" version key; asking
	// for two different versions cannot be satisfied.
	g := &Gradle{}
	err := g.Update(t.Context(), &languages.UpdateConfig{
		RootDir: dir,
		Dependencies: []languages.Dependency{
			{Name: "io.netty:netty-codec", Version: "4.2.13.Final"},
			{Name: "io.netty:netty-handler", Version: "4.2.14.Final"},
		},
	})
	if !errors.Is(err, ErrVersionConflict) {
		t.Errorf("Update() error = %v, want ErrVersionConflict", err)
	}
}

func TestGradle_Update_PropertyNotFound(t *testing.T) {
	dir := copyFixture(t, "opensearch-style")

	g := &Gradle{}
	err := g.Update(t.Context(), &languages.UpdateConfig{
		RootDir:    dir,
		Properties: map[string]string{"noSuchProperty": "1.0.0"},
	})
	if !errors.Is(err, ErrPropertyNotFound) {
		t.Errorf("Update() error = %v, want ErrPropertyNotFound", err)
	}
}

func TestGradle_Update_DryRun_AllMechanisms(t *testing.T) {
	dir := copyFixture(t, "sonarqube-style")
	before := map[string]string{}
	for _, name := range []string{"build.gradle", "gradle.properties"} {
		before[name] = readFile(t, filepath.Join(dir, name))
	}

	g := &Gradle{}
	err := g.Update(t.Context(), &languages.UpdateConfig{
		RootDir: dir,
		DryRun:  true,
		Dependencies: []languages.Dependency{
			{Name: "io.netty:netty-handler", Version: "4.1.133.Final"},
			{Name: "ch.qos.logback:logback-classic", Version: "1.5.25"},
			{Name: "com.example:transitive", Version: "1.0.0"}, // would force
		},
		Properties: map[string]string{"elasticSearchServerVersion": "8.19.10"},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	for name, content := range before {
		if readFile(t, filepath.Join(dir, name)) != content {
			t.Errorf("dry run modified %s", name)
		}
	}
}

func TestGradle_Update_MissingCoordinates(t *testing.T) {
	dir := copyFixture(t, "opensearch-style")

	g := &Gradle{}
	err := g.Update(t.Context(), &languages.UpdateConfig{
		RootDir: dir,
		Dependencies: []languages.Dependency{
			{Name: "justartifact", Version: "1.0.0"},
		},
	})
	if !errors.Is(err, errMissingCoordinates) {
		t.Errorf("Update() error = %v, want errMissingCoordinates", err)
	}
}

func TestGradle_Validate_ForceBlockSatisfiesDependency(t *testing.T) {
	dir := copyFixture(t, "kayenta-style")
	cfg := &languages.UpdateConfig{
		RootDir: dir,
		Dependencies: []languages.Dependency{
			{Name: "io.netty:netty-buffer", Version: "4.1.133.Final"},
		},
	}
	g := &Gradle{}
	if err := g.Update(t.Context(), cfg); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if err := g.Validate(t.Context(), cfg); err != nil {
		t.Errorf("Validate() should accept force-block pinning, got %v", err)
	}

	// A different expected version must fail validation.
	cfg.Dependencies[0].Version = "4.1.999.Final"
	if err := g.Validate(t.Context(), cfg); err == nil {
		t.Error("Validate() should fail for mismatching force entry")
	}
}

func TestGradleAnalyzer_Analyze_ModelBacked(t *testing.T) {
	dir := copyFixture(t, "opensearch-style")

	ga := &GradleAnalyzer{}
	result, err := ga.Analyze(t.Context(), dir)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	dep, exists := result.Dependencies["io.netty:netty-codec"]
	if !exists {
		t.Fatalf("netty-codec not in analysis: %v", result.Dependencies)
	}
	if !dep.UsesProperty || dep.PropertyName != "netty" {
		t.Errorf("netty-codec should resolve through catalog key netty, got %+v", dep)
	}
	if dep.Version != "4.2.12.Final" {
		t.Errorf("netty-codec version = %q, want 4.2.12.Final", dep.Version)
	}
	if result.Properties["netty"] != "4.2.12.Final" {
		t.Errorf("catalog key netty not surfaced as property: %v", result.Properties)
	}
	if source := result.PropertySources["netty"]; source != filepath.Join("gradle", "libs.versions.toml") {
		t.Errorf("PropertySources[netty] = %q", source)
	}
}

func TestGradle_Update_ResolutionRuleBridge(t *testing.T) {
	// kafbat v1.5.0 shape: the netty version lives only in the [versions]
	// catalog key, applied group-wide through the project's own
	// eachDependency rule reading libs.versions.netty.get(). A module dep
	// must route to the catalog key, not the force block.
	dir := copyFixture(t, "kafbat-rule-style")

	updateAndValidate(t, &languages.UpdateConfig{
		RootDir: dir,
		Dependencies: []languages.Dependency{
			{Name: "io.netty:netty-codec", Version: "4.1.133.Final"},
			{Name: "com.signalfx.public:signalfx-java", Version: "1.0.49"},
		},
	})

	catalog := readFile(t, filepath.Join(dir, "gradle", "libs.versions.toml"))
	if !strings.Contains(catalog, `netty = '4.1.133.Final'`) {
		t.Errorf("catalog key not updated through the resolution rule:\n%s", catalog)
	}
	build := readFile(t, filepath.Join(dir, "build.gradle"))
	if !strings.Contains(build, `details.useVersion '1.0.49'`) {
		t.Errorf("literal rule not updated:\n%s", build)
	}
	if strings.Contains(build, "omnibump:resolutionStrategy") {
		t.Errorf("rule-governed deps must not fall through to the force block:\n%s", build)
	}
}

func TestGradle_Update_ManagedBlock_RefusesSymlinkTarget(t *testing.T) {
	// A malicious repo pre-places a symlink at the settings script name,
	// pointing outside the checkout. Discovery skips symlinks, so the project
	// has no usable settings script and the transitive-only dependency drives
	// the new-settings-file path. The write must be refused rather than
	// followed through the symlink.
	tmpDir := t.TempDir()

	outsideDir := t.TempDir()
	victim := filepath.Join(outsideDir, "victim.txt")
	const original = "DO NOT TOUCH"
	if err := os.WriteFile(victim, []byte(original), 0o600); err != nil {
		t.Fatalf("failed to write victim file: %v", err)
	}

	// A real build.gradle makes the dir a Gradle project; with no real
	// settings script, the managed block would be written to a new one.
	if err := os.WriteFile(filepath.Join(tmpDir, "build.gradle"), []byte("apply plugin: 'java'\n"), 0o600); err != nil {
		t.Fatalf("failed to write build.gradle: %v", err)
	}
	// Symlinked settings script pointing at the victim outside the root.
	if err := os.Symlink(victim, filepath.Join(tmpDir, "settings.gradle")); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	g := &Gradle{}
	err := g.Update(t.Context(), &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "com.example:transitive-only", Version: "1.0.0"},
		},
	})
	if !errors.Is(err, ErrSymlinkTarget) {
		t.Fatalf("Update() error = %v, want ErrSymlinkTarget", err)
	}

	got, readErr := os.ReadFile(victim)
	if readErr != nil {
		t.Fatalf("failed to read victim file: %v", readErr)
	}
	if string(got) != original {
		t.Errorf("victim file was modified through the symlink: got %q, want %q", got, original)
	}
}
