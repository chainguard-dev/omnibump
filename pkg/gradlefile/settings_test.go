/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradlefile

import (
	"strings"
	"testing"
)

const sampleSettings = `rootProject.name = "demo"

dependencyResolutionManagement {
    versionCatalogs {
        create("libs") {
            version("netty", "4.1.100.Final")
            version("jackson", "2.17.2")
            library("netty-codec", "io.netty", "netty-codec").versionRef("netty")
            // version("commented", "1.0.0")
        }
    }
}
`

func TestParseSettings_InlineCatalog(t *testing.T) {
	f, err := ParseSettings("settings.gradle.kts", []byte(sampleSettings))
	if err != nil {
		t.Fatalf("ParseSettings() error = %v", err)
	}
	if f.DSL() != Kotlin {
		t.Errorf("DSL() = %v, want Kotlin", f.DSL())
	}

	versions := map[string]CatalogVersion{}
	for _, v := range f.CatalogVersions() {
		versions[v.Key] = v
	}
	if versions["netty"].Value != "4.1.100.Final" {
		t.Errorf("netty = %+v", versions["netty"])
	}
	if versions["jackson"].Value != "2.17.2" {
		t.Errorf("jackson = %+v", versions["jackson"])
	}
	if _, exists := versions["commented"]; exists {
		t.Error("commented-out version() should be skipped")
	}

	libraries := f.CatalogLibraries()
	if len(libraries) != 1 || libraries[0].Module() != "io.netty:netty-codec" || libraries[0].VersionRef != "netty" {
		t.Errorf("CatalogLibraries() = %+v", libraries)
	}
}

func TestSettings_SetCatalogVersion(t *testing.T) {
	f, err := ParseSettings("settings.gradle", []byte(sampleSettings))
	if err != nil {
		t.Fatalf("ParseSettings() error = %v", err)
	}
	for _, v := range f.CatalogVersions() {
		if v.Key == "netty" {
			if err := f.SetCatalogVersion(v, "4.1.133.Final"); err != nil {
				t.Fatalf("SetCatalogVersion() error = %v", err)
			}
		}
	}
	updated := string(f.Content())
	if !strings.Contains(updated, `version("netty", "4.1.133.Final")`) {
		t.Errorf("netty version not updated:\n%s", updated)
	}
	if !strings.Contains(updated, `version("jackson", "2.17.2")`) {
		t.Errorf("unrelated version modified:\n%s", updated)
	}
}
