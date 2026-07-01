/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradle

import (
	"context"
	"strings"
	"testing"

	"github.com/chainguard-dev/omnibump/pkg/languages"
)

func TestModelShipConfigurations(t *testing.T) {
	files := map[string][]byte{
		"settings.gradle": []byte("rootProject.name = 'demo'\n"),
		// Core module: shadowJar bundles a custom configuration plus a doc config
		// pulled into a javadoc jar, which must be filtered out.
		"app/build.gradle": []byte(`
shadowJar {
    configurations = [project.configurations.runtimeClasspath, project.configurations.lineageImplementation]
}
task javadocJar(type: Jar) {
    from configurations.groovyDoc
}
`),
	}
	m := buildModelFromFiles(context.Background(), files)
	got := m.shipConfigurations()
	if len(got) != 1 || got[0] != "lineageImplementation" {
		t.Fatalf("shipConfigurations() = %v, want [lineageImplementation] (runtimeClasspath + groovyDoc filtered)", got)
	}
}

func TestAnalyzeModel_SurfacesShipConfigurations(t *testing.T) {
	files := map[string][]byte{
		"settings.gradle":  []byte("rootProject.name = 'demo'\n"),
		"app/build.gradle": []byte("shadowJar { configurations = [project.configurations.runtimeClasspath, project.configurations.lineageImplementation] }\n"),
	}
	m := buildModelFromFiles(context.Background(), files)
	result := analyzeModel(m)
	got, ok := result.Metadata["ship_configurations"].([]string)
	if !ok {
		t.Fatalf("ship_configurations not surfaced in metadata: %#v", result.Metadata)
	}
	if len(got) != 1 || got[0] != "lineageImplementation" {
		t.Fatalf("ship_configurations = %v, want [lineageImplementation]", got)
	}
}

func TestComputeExtraConfigs_UnionAndSort(t *testing.T) {
	files := map[string][]byte{
		"settings.gradle":  []byte("rootProject.name = 'demo'\n"),
		"app/build.gradle": []byte("task uber(type: Jar) { from configurations.lineageImplementation }\n"),
	}
	m := buildModelFromFiles(context.Background(), files)
	cfg := &languages.UpdateConfig{
		GradleForceConfigurations: []string{"opOptIn", "lineageImplementation"}, // dup + extra
	}
	got := computeExtraConfigs(m, cfg)
	want := "lineageImplementation,opOptIn" // unioned, deduped, sorted
	if strings.Join(got, ",") != want {
		t.Fatalf("computeExtraConfigs = %v, want %q", got, want)
	}
}
