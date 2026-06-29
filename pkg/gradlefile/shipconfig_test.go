/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradlefile

import (
	"slices"
	"testing"
)

// resolvedShipNames returns the resolved configuration names a build script
// bundles into shipped artifacts, in scan order.
func resolvedShipNames(f *BuildFile) []string {
	var out []string
	for _, ref := range f.ShipConfigs() {
		if ref.Resolved {
			out = append(out, ref.Name)
		}
	}
	return out
}

func TestScanShipConfigs(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string // resolved names, deduped is not applied here; use Contains
	}{
		{
			name: "shadow configurations list",
			content: `shadowJar {
    configurations = [project.configurations.runtimeClasspath, project.configurations.lineageImplementation]
}`,
			want: []string{"runtimeClasspath", "lineageImplementation"},
		},
		{
			name:    "capsule embedConfiguration getByName",
			content: `embedConfiguration = configurations.getByName("runtimeClasspath")`,
			want:    []string{"runtimeClasspath"},
		},
		{
			name:    "generic from property access",
			content: `task fatJar(type: Jar) { from configurations.myBundle }`,
			want:    []string{"myBundle"},
		},
		{
			name:    "generic from closure with collect/zipTree",
			content: `task uber(type: Jar) { from { configurations.shade.collect { it.isDirectory() ? it : zipTree(it) } } }`,
			want:    []string{"shade"},
		},
		{
			name:    "bootJar classpath",
			content: `bootJar { classpath configurations.extraRuntime }`,
			want:    []string{"extraRuntime"},
		},
		{
			name:    "bracket index lookup",
			content: `from(configurations["customBundle"])`,
			want:    []string{"customBundle"},
		},
		{
			name:    "kotlin named map",
			content: `configurations = configurations.named("appBundle").map { listOf(it) }`,
			want:    []string{"appBundle"},
		},
		{
			name:    "project-qualified reference",
			content: `from project.configurations.shadowed`,
			want:    []string{"shadowed"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, err := ParseBuild("build.gradle", []byte(tc.content))
			if err != nil {
				t.Fatalf("ParseBuild error = %v", err)
			}
			got := resolvedShipNames(f)
			for _, w := range tc.want {
				if !slices.Contains(got, w) {
					t.Errorf("missing %q in detected ship configs %v", w, got)
				}
			}
		})
	}
}

func TestScanShipConfigs_FiltersContainerMethods(t *testing.T) {
	// configurations.all / configureEach / getByName method identifiers must
	// not be captured as configuration names by the property-access form.
	content := `configurations.configureEach { }
configurations.all { }
from configurations.realBundle`
	f, err := ParseBuild("build.gradle", []byte(content))
	if err != nil {
		t.Fatalf("ParseBuild error = %v", err)
	}
	got := resolvedShipNames(f)
	for _, bad := range []string{"configureEach", "all"} {
		if slices.Contains(got, bad) {
			t.Errorf("container method %q wrongly captured as config: %v", bad, got)
		}
	}
	if !slices.Contains(got, "realBundle") {
		t.Errorf("missing realBundle in %v", got)
	}
}

func TestScanShipConfigs_IgnoresComments(t *testing.T) {
	content := "// from configurations.commentedOut\nfrom configurations.live"
	f, err := ParseBuild("build.gradle", []byte(content))
	if err != nil {
		t.Fatalf("ParseBuild error = %v", err)
	}
	got := resolvedShipNames(f)
	if slices.Contains(got, "commentedOut") {
		t.Errorf("commented reference captured: %v", got)
	}
	if !slices.Contains(got, "live") {
		t.Errorf("missing live: %v", got)
	}
}

func TestScanShipConfigs_Unresolved(t *testing.T) {
	// A configuration looked up via a variable cannot be resolved to a name and
	// must be recorded as an unresolved bundling site for operator warning.
	content := `from configurations.getByName(myVar)`
	f, err := ParseBuild("build.gradle", []byte(content))
	if err != nil {
		t.Fatalf("ParseBuild error = %v", err)
	}
	var unresolved int
	for _, ref := range f.ShipConfigs() {
		if !ref.Resolved {
			unresolved++
		}
	}
	if unresolved == 0 {
		t.Errorf("expected an unresolved ship-config ref, got %+v", f.ShipConfigs())
	}
}

func TestIsManagedClasspathName(t *testing.T) {
	for _, n := range []string{"runtimeClasspath", "compileClasspath", "testRuntimeClasspath", "releaseRuntimeClasspath"} {
		if !IsManagedClasspathName(n) {
			t.Errorf("%q should be a managed classpath name", n)
		}
	}
	for _, n := range []string{"lineageImplementation", "myBundle", "implementation"} {
		if IsManagedClasspathName(n) {
			t.Errorf("%q should not be a managed classpath name", n)
		}
	}
}

func TestIsNonShippingConfigName(t *testing.T) {
	for _, n := range []string{"groovyDoc", "javadoc", "checkstyle", "spotless", "jacocoAgent", "annotationProcessor", "errorprone"} {
		if !IsNonShippingConfigName(n) {
			t.Errorf("%q should be classified non-shipping", n)
		}
	}
	for _, n := range []string{"lineageImplementation", "runtimeClasspath", "myBundle", "client"} {
		if IsNonShippingConfigName(n) {
			t.Errorf("%q should not be classified non-shipping", n)
		}
	}
}
