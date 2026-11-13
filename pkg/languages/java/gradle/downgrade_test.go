/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradle

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/chainguard-dev/omnibump/pkg/languages"
)

// managedPins returns the omnibump-managed force pins in the settings script
// the updater writes them to, or nil when the project has none.
func managedPins(t *testing.T, dir string) map[string]string {
	t.Helper()
	model, err := buildProjectModel(t.Context(), dir)
	if err != nil {
		t.Fatalf("buildProjectModel() error = %v", err)
	}
	settings := model.rootSettingsFile()
	if settings == nil {
		return nil
	}
	return settings.ManagedCoordinates()
}

// TestGradle_Update_DowngradeSkipped requests versions older than the ones
// each fixture declares. The declared version must stay, and no force pin may
// be written for it: a force pin at the older version would lower it on every
// subproject classpath. Upgrades requested alongside a downgrade must still
// be applied.
func TestGradle_Update_DowngradeSkipped(t *testing.T) {
	tests := []struct {
		name       string
		fixture    string
		deps       []languages.Dependency
		properties map[string]string
		// wantContains maps a fixture file to text it must contain afterwards.
		wantContains map[string][]string
		// wantNoPin lists modules that must not be force-pinned.
		wantNoPin []string
	}{{
		name:         "literal declaration",
		fixture:      "kayenta-style",
		deps:         []languages.Dependency{{Name: "com.netflix.kayenta:kayenta-core", Version: "0.9.0"}},
		wantContains: map[string][]string{"build.gradle": {`"com.netflix.kayenta:kayenta-core:1.0.0"`}},
		wantNoPin:    []string{"com.netflix.kayenta:kayenta-core"},
	}, {
		name:         "catalog version.ref",
		fixture:      "opensearch-style",
		deps:         []languages.Dependency{{Name: "org.apache.logging.log4j:log4j-core", Version: "2.25.1"}},
		wantContains: map[string][]string{"gradle/libs.versions.toml": {`log4j                   = "2.25.3"`}},
		wantNoPin:    []string{"org.apache.logging.log4j:log4j-core"},
	}, {
		name:         "catalog inline version",
		fixture:      "opensearch-style",
		deps:         []languages.Dependency{{Name: "com.squareup.okio:okio", Version: "3.2.0"}},
		wantContains: map[string][]string{"gradle/libs.versions.toml": {`version = "3.4.0"`}},
		wantNoPin:    []string{"com.squareup.okio:okio"},
	}, {
		name:         "gradle.properties variable",
		fixture:      "sonarqube-style",
		deps:         []languages.Dependency{{Name: "io.netty:netty-handler", Version: "4.1.118.Final"}},
		wantContains: map[string][]string{"gradle.properties": {"nettyVersion=4.1.125.Final"}},
		wantNoPin:    []string{"io.netty:netty-handler"},
	}, {
		name:         "explicit property",
		fixture:      "sonarqube-style",
		properties:   map[string]string{"nettyVersion": "4.1.118.Final"},
		wantContains: map[string][]string{"gradle.properties": {"nettyVersion=4.1.125.Final"}},
	}, {
		name:    "upgrade applied next to a skipped downgrade",
		fixture: "opensearch-style",
		deps: []languages.Dependency{
			{Name: "org.apache.logging.log4j:log4j-core", Version: "2.25.1"},
			{Name: "com.squareup.okio:okio", Version: "3.9.1"},
		},
		wantContains: map[string][]string{"gradle/libs.versions.toml": {
			`log4j                   = "2.25.3"`,
			`version = "3.9.1"`,
		}},
		wantNoPin: []string{"org.apache.logging.log4j:log4j-core"},
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := copyFixture(t, tt.fixture)
			cfg := &languages.UpdateConfig{RootDir: dir, Dependencies: tt.deps, Properties: tt.properties}
			if err := (&Gradle{}).Update(t.Context(), cfg); err != nil {
				t.Fatalf("Update() error = %v", err)
			}

			for file, wants := range tt.wantContains {
				got := readFile(t, filepath.Join(dir, file))
				for _, want := range wants {
					if !strings.Contains(got, want) {
						t.Errorf("%s does not contain %q:\n%s", file, want, got)
					}
				}
			}
			pins := managedPins(t, dir)
			for _, module := range tt.wantNoPin {
				if version, pinned := pins[module]; pinned {
					t.Errorf("%s force-pinned to %s, want no pin", module, version)
				}
			}
		})
	}
}

// TestGradle_Update_FamilyKeptTogether pins log4j-core, which the catalog
// keeps at a newer 2.25.3, together with log4j-slf4j2-impl, which the build
// does not declare. Force-pinning log4j-slf4j2-impl at the older version
// would split the log4j family, so an undeclared pin is skipped when a pin in
// the same group at the same version was skipped as a downgrade. Other
// undeclared pins are still force-pinned.
func TestGradle_Update_FamilyKeptTogether(t *testing.T) {
	const undeclared = "org.apache.logging.log4j:log4j-slf4j2-impl"
	tests := []struct {
		name    string
		deps    []languages.Dependency
		wantPin string // "" when undeclared must not be pinned
	}{{
		name: "undeclared member at the skipped version",
		deps: []languages.Dependency{
			{Name: "org.apache.logging.log4j:log4j-core", Version: "2.25.1"},
			{Name: undeclared, Version: "2.25.1"},
		},
	}, {
		name: "undeclared member at a different version",
		deps: []languages.Dependency{
			{Name: "org.apache.logging.log4j:log4j-core", Version: "2.25.1"},
			{Name: undeclared, Version: "2.25.4"},
		},
		wantPin: "2.25.4",
	}, {
		name:    "undeclared pin with no skipped member",
		deps:    []languages.Dependency{{Name: undeclared, Version: "2.25.1"}},
		wantPin: "2.25.1",
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := copyFixture(t, "opensearch-style")
			updateAndValidate(t, &languages.UpdateConfig{RootDir: dir, Dependencies: tt.deps})

			if got := readFile(t, filepath.Join(dir, "gradle", "libs.versions.toml")); !strings.Contains(got, `log4j                   = "2.25.3"`) {
				t.Errorf("catalog log4j changed:\n%s", got)
			}
			pins := managedPins(t, dir)
			if got := pins[undeclared]; got != tt.wantPin {
				t.Errorf("%s pin: got = %q, want = %q", undeclared, got, tt.wantPin)
			}
		})
	}
}

// TestGradle_Update_KeepsClassifierInVersionVariable uses linkedin/ambry,
// which keeps the netty-tcnative classifier inside the version variable
// ("2.0.61.Final:linux-x86_64"). Bumping the version must keep each
// variable's classifier, or Gradle resolves the classifier-less jar, which
// has no native library.
func TestGradle_Update_KeepsClassifierInVersionVariable(t *testing.T) {
	dir := copyFixture(t, "ambry-real")
	updateAndValidate(t, &languages.UpdateConfig{
		RootDir:      dir,
		Dependencies: []languages.Dependency{{Name: "io.netty:netty-tcnative-boringssl-static", Version: "2.0.70.Final"}},
	})

	got := readFile(t, filepath.Join(dir, "gradle", "dependency-versions.gradle"))
	for _, want := range []string{
		`nettyTcNativeBoringSSLStatic_linux_x86_64 = "2.0.70.Final:linux-x86_64"`,
		`nettyTcNativeBoringSSLStatic_osx_aarch_64 = "2.0.70.Final:osx-aarch_64"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("dependency-versions.gradle does not contain %q:\n%s", want, got)
		}
	}
}
