/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradlefile

import (
	"strings"
	"testing"
)

const sampleProperties = `# build configuration
org.gradle.jvmargs=-Xmx2g

nettyVersion=4.1.125.Final
elasticSearchServerVersion=8.19.8
jackson = 2.15.0
spaced.key : 1.2.3
! another comment style
`

func mustParseProperties(t *testing.T, content string) *PropertiesFile {
	t.Helper()
	f, err := ParseProperties("gradle.properties", []byte(content))
	if err != nil {
		t.Fatalf("ParseProperties() error = %v", err)
	}
	return f
}

func TestParseProperties_Forms(t *testing.T) {
	f := mustParseProperties(t, sampleProperties)

	tests := map[string]string{
		"nettyVersion":               "4.1.125.Final",
		"elasticSearchServerVersion": "8.19.8",
		"jackson":                    "2.15.0", // version.properties style "key = value"
		"spaced.key":                 "1.2.3",  // colon separator
		"org.gradle.jvmargs":         "-Xmx2g",
	}
	for key, want := range tests {
		got, ok := f.Get(key)
		if !ok || got != want {
			t.Errorf("Get(%q) = %q, %v; want %q", key, got, ok, want)
		}
	}
}

func TestProperties_Set(t *testing.T) {
	f := mustParseProperties(t, sampleProperties)

	if err := f.Set("nettyVersion", "4.1.133.Final"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := f.Set("jackson", "2.18.6"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	updated := string(f.Content())
	for _, want := range []string{
		"nettyVersion=4.1.133.Final",
		"jackson = 2.18.6",                  // separator style preserved
		"# build configuration",             // comments preserved
		"elasticSearchServerVersion=8.19.8", // unrelated entries untouched
	} {
		if !strings.Contains(updated, want) {
			t.Errorf("updated content missing %q:\n%s", want, updated)
		}
	}
}

func TestProperties_Set_UnknownKey(t *testing.T) {
	f := mustParseProperties(t, sampleProperties)
	if err := f.Set("missing", "1.0.0"); err == nil {
		t.Error("Set() on unknown key should error")
	}
}

func TestProperties_Set_AllOccurrences(t *testing.T) {
	f := mustParseProperties(t, "v=1.0\nother=x\nv=1.0\n")
	if err := f.Set("v", "2.0"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if got := strings.Count(string(f.Content()), "v=2.0"); got != 2 {
		t.Errorf("expected both occurrences updated, got:\n%s", f.Content())
	}
}

func TestProperties_CommentsNotParsed(t *testing.T) {
	f := mustParseProperties(t, sampleProperties)
	for _, key := range f.Keys() {
		if strings.HasPrefix(key, "#") || strings.HasPrefix(key, "!") {
			t.Errorf("comment line parsed as key: %q", key)
		}
	}
}
