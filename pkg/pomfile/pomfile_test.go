/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package pomfile

import (
	"errors"
	"strings"
	"testing"
)

const licenseHeaderPom = `<?xml version="1.0" encoding="UTF-8"?>
<!--

    Copyright DataStax, Inc.

    Please see the included license file for details.

-->
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <artifactId>example</artifactId>
  <properties>
    <slf4j.version>2.0.17</slf4j.version>
    <!-- keep in sync with the agent module -->
    <logback.version>1.5.32</logback.version>
    <netty.version>4.1.135.Final</netty.version>
  </properties>
</project>
`

func TestParseCollectsTopLevelProperties(t *testing.T) {
	f, err := Parse("pom.xml", []byte(licenseHeaderPom))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	want := map[string]string{
		"slf4j.version":   "2.0.17",
		"logback.version": "1.5.32",
		"netty.version":   "4.1.135.Final",
	}
	for name, wantValue := range want {
		got, ok := f.Get(name)
		if !ok {
			t.Errorf("Get(%q) not found", name)
			continue
		}
		if got != wantValue {
			t.Errorf("Get(%q) = %q, want %q", name, got, wantValue)
		}
	}
	if len(f.Properties()) != len(want) {
		t.Errorf("Properties() returned %d entries, want %d", len(f.Properties()), len(want))
	}
}

// A property update must leave every other byte of the file alone. This is the
// behaviour that keeps an enforced license header intact.
func TestSetPropertyPreservesCommentsAndFormatting(t *testing.T) {
	f, err := Parse("pom.xml", []byte(licenseHeaderPom))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if err := f.SetProperty("logback.version", "1.5.35"); err != nil {
		t.Fatalf("SetProperty() error = %v", err)
	}

	got := string(f.Content())
	want := strings.Replace(licenseHeaderPom,
		"<logback.version>1.5.32</logback.version>",
		"<logback.version>1.5.35</logback.version>", 1)
	if got != want {
		t.Errorf("Content() changed more than the property value.\ngot:\n%s\nwant:\n%s", got, want)
	}
	if !f.Changed() {
		t.Error("Changed() = false after a value edit, want true")
	}
}

func TestSetPropertyUndeclaredIsAnError(t *testing.T) {
	f, err := Parse("pom.xml", []byte(licenseHeaderPom))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	err = f.SetProperty("jackson.version", "2.21.5")
	if !errors.Is(err, ErrPropertyNotFound) {
		t.Errorf("SetProperty() error = %v, want ErrPropertyNotFound", err)
	}
	if f.Changed() {
		t.Error("Changed() = true after a failed edit, want false")
	}
}

// Rewriting to the same value is not a change, so callers can skip the write.
func TestSetPropertySameValueIsNotAChange(t *testing.T) {
	f, err := Parse("pom.xml", []byte(licenseHeaderPom))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if err := f.SetProperty("logback.version", "1.5.32"); err != nil {
		t.Fatalf("SetProperty() error = %v", err)
	}
	if f.Changed() {
		t.Error("Changed() = true for a no-op edit, want false")
	}
	if string(f.Content()) != licenseHeaderPom {
		t.Error("Content() differs from the original after a no-op edit")
	}
}

// A property that only applies under a profile must not be mistaken for the
// project-level default: rewriting it would change a conditional value and
// leave the real one untouched.
func TestParseIgnoresProfileProperties(t *testing.T) {
	const pom = `<project xmlns="http://maven.apache.org/POM/4.0.0">
  <properties>
    <logback.version>1.5.32</logback.version>
  </properties>
  <profiles>
    <profile>
      <id>fips</id>
      <properties>
        <bouncycastle.version>1.80</bouncycastle.version>
      </properties>
    </profile>
  </profiles>
</project>
`
	f, err := Parse("pom.xml", []byte(pom))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if f.Has("bouncycastle.version") {
		t.Error("Has(\"bouncycastle.version\") = true for a profile-scoped property, want false")
	}
	if !f.Has("logback.version") {
		t.Error("Has(\"logback.version\") = false, want true")
	}
}

func TestSetPropertyUpdatesEveryOccurrence(t *testing.T) {
	// A duplicated key is legal XML and Maven takes the last one; updating only
	// one occurrence would leave the effective value stale.
	const pom = `<project xmlns="http://maven.apache.org/POM/4.0.0">
  <properties>
    <assertj.version>3.17.2</assertj.version>
    <assertj.version>3.27.7</assertj.version>
  </properties>
</project>
`
	f, err := Parse("pom.xml", []byte(pom))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if err := f.SetProperty("assertj.version", "3.28.0"); err != nil {
		t.Fatalf("SetProperty() error = %v", err)
	}
	got := string(f.Content())
	if strings.Contains(got, "3.17.2") || strings.Contains(got, "3.27.7") {
		t.Errorf("Content() left a stale occurrence:\n%s", got)
	}
	if strings.Count(got, "3.28.0") != 2 {
		t.Errorf("Content() should have both occurrences updated:\n%s", got)
	}
}

func TestParseEmptyAndSelfClosingProperties(t *testing.T) {
	const pom = `<project xmlns="http://maven.apache.org/POM/4.0.0">
  <properties>
    <empty.version></empty.version>
    <selfclosing.version/>
    <real.version>1.0</real.version>
  </properties>
</project>
`
	f, err := Parse("pom.xml", []byte(pom))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	// An empty element still has a (zero-length) value range and can be set.
	if err := f.SetProperty("empty.version", "9.9"); err != nil {
		t.Fatalf("SetProperty(empty) error = %v", err)
	}
	if !strings.Contains(string(f.Content()), "<empty.version>9.9</empty.version>") {
		t.Errorf("empty element not filled in:\n%s", f.Content())
	}
	// A self-closing element has no range to rewrite and is skipped entirely.
	if f.Has("selfclosing.version") {
		t.Error("Has(\"selfclosing.version\") = true, want false (no editable value range)")
	}
	if !f.Has("real.version") {
		t.Error("Has(\"real.version\") = false, want true")
	}
}

func TestParseNoPropertiesBlock(t *testing.T) {
	const pom = `<project xmlns="http://maven.apache.org/POM/4.0.0">
  <artifactId>example</artifactId>
</project>
`
	f, err := Parse("pom.xml", []byte(pom))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(f.Properties()) != 0 {
		t.Errorf("Properties() = %d entries, want 0", len(f.Properties()))
	}
	if string(f.Content()) != pom {
		t.Error("Content() differs from the original when nothing was edited")
	}
}
