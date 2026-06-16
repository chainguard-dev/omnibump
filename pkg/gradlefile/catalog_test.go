/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradlefile

import (
	"strings"
	"testing"
)

// opensearch-style catalog with aligned keys, version.ref entries, an inline
// version entry and a group/name entry.
const sampleCatalog = `# Dependency catalog
[versions]
log4j                   = "2.25.3"
netty             = "4.2.12.Final"
bouncycastle = { strictly = "1.78" }

[libraries]
netty-codec      = { module = "io.netty:netty-codec",      version.ref = "netty" }
netty-handler    = { module = "io.netty:netty-handler",    version.ref = "netty" }
log4j-core       = { module = "org.apache.logging.log4j:log4j-core", version.ref = "log4j" }
okio             = { module = "com.squareup.okio:okio",    version = "3.4.0" }
guava            = { group = "com.google.guava", name = "guava", version.ref = "netty" }
noversion        = { module = "com.example:noversion" }

[plugins]
shadow = { id = "com.github.johnrengelman.shadow", version = "8.1.1" }
`

func mustParseCatalog(t *testing.T, content string) *CatalogFile {
	t.Helper()
	f, err := ParseCatalog("gradle/libs.versions.toml", []byte(content))
	if err != nil {
		t.Fatalf("ParseCatalog() error = %v", err)
	}
	return f
}

func TestParseCatalog_Versions(t *testing.T) {
	f := mustParseCatalog(t, sampleCatalog)

	got := map[string]string{}
	for _, v := range f.Versions() {
		got[v.Key] = v.Value
	}
	want := map[string]string{
		"log4j": "2.25.3",
		"netty": "4.2.12.Final",
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("version %s = %q, want %q", key, got[key], value)
		}
	}
	// Rich versions ({ strictly = ... }) are not string entries.
	if _, exists := got["bouncycastle"]; exists {
		t.Error("rich version entry should not be recorded as a plain version")
	}
}

func TestParseCatalog_Libraries(t *testing.T) {
	f := mustParseCatalog(t, sampleCatalog)

	libs := map[string]CatalogLibrary{}
	for _, l := range f.Libraries() {
		libs[l.Alias] = l
	}

	if l := libs["netty-codec"]; l.Module() != "io.netty:netty-codec" || l.VersionRef != "netty" {
		t.Errorf("netty-codec = %+v, want module io.netty:netty-codec ref netty", l)
	}
	if l := libs["okio"]; l.Version != "3.4.0" || l.VersionRef != "" {
		t.Errorf("okio = %+v, want inline version 3.4.0", l)
	}
	if l := libs["guava"]; l.Module() != "com.google.guava:guava" || l.VersionRef != "netty" {
		t.Errorf("guava (group/name form) = %+v", l)
	}
	if l := libs["noversion"]; l.Version != "" || l.VersionRef != "" {
		t.Errorf("noversion = %+v, want no version info", l)
	}
}

func TestCatalog_SetVersion_PreservesAlignment(t *testing.T) {
	f := mustParseCatalog(t, sampleCatalog)

	if err := f.SetVersion("netty", "4.2.13.Final"); err != nil {
		t.Fatalf("SetVersion() error = %v", err)
	}
	updated := string(f.Content())
	if !strings.Contains(updated, `netty             = "4.2.13.Final"`) {
		t.Errorf("alignment not preserved:\n%s", updated)
	}
	if !strings.Contains(updated, `log4j                   = "2.25.3"`) {
		t.Errorf("unrelated entry modified:\n%s", updated)
	}
}

func TestCatalog_SetVersion_UnknownKey(t *testing.T) {
	f := mustParseCatalog(t, sampleCatalog)
	if err := f.SetVersion("nope", "1.0.0"); err == nil {
		t.Error("SetVersion() on unknown key should error")
	}
}

func TestCatalog_SetLibraryVersion(t *testing.T) {
	f := mustParseCatalog(t, sampleCatalog)

	var okio CatalogLibrary
	for _, l := range f.Libraries() {
		if l.Alias == "okio" {
			okio = l
		}
	}
	if err := f.SetLibraryVersion(okio, "3.6.0"); err != nil {
		t.Fatalf("SetLibraryVersion() error = %v", err)
	}
	if !strings.Contains(string(f.Content()), `version = "3.6.0"`) {
		t.Errorf("inline library version not updated:\n%s", f.Content())
	}
}

func TestCatalog_SetLibraryVersion_RefIsNotEditable(t *testing.T) {
	f := mustParseCatalog(t, sampleCatalog)
	for _, l := range f.Libraries() {
		if l.Alias == "netty-codec" {
			if err := f.SetLibraryVersion(l, "1.0.0"); err == nil {
				t.Error("SetLibraryVersion() on a version.ref library should error")
			}
		}
	}
}

func TestParseCatalog_InvalidToml(t *testing.T) {
	if _, err := ParseCatalog("libs.versions.toml", []byte("[versions\nbroken")); err == nil {
		t.Error("ParseCatalog() should error on invalid TOML")
	}
}

func TestParseCatalog_QuotedKeys(t *testing.T) {
	content := `[versions]
"netty.codec" = "4.1.0"

[libraries]
"netty-codec" = { module = "io.netty:netty-codec", version.ref = "netty.codec" }
`
	f := mustParseCatalog(t, content)
	if err := f.SetVersion("netty.codec", "4.2.0"); err != nil {
		t.Fatalf("SetVersion() on quoted key error = %v", err)
	}
	if !strings.Contains(string(f.Content()), `"netty.codec" = "4.2.0"`) {
		t.Errorf("quoted key not updated:\n%s", f.Content())
	}
}
