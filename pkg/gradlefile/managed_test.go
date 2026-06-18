/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradlefile

import (
	"strings"
	"testing"
)

func mustParseSettings(t *testing.T, path, content string) *SettingsFile {
	t.Helper()
	f, err := ParseSettings(path, []byte(content))
	if err != nil {
		t.Fatalf("ParseSettings(%q) error = %v", path, err)
	}
	return f
}

func TestRenderManagedBlock_Groovy(t *testing.T) {
	out := renderManagedBlock(
		map[string]string{"io.netty:netty-handler": "4.1.135.Final", "commons-io:commons-io": "2.17.0"},
		[]Substitution{{OldModule: "org.lz4:lz4-java", NewModule: "at.yawk.lz4:lz4-java", Version: "1.10.1"}},
		Groovy,
	)
	for _, want := range []string{
		ForceBlockBegin,
		ForceBlockEnd,
		"gradle.beforeProject { project ->",
		"project.plugins.withId('java') {",
		"add('implementation', 'commons-io:commons-io:2.17.0')",
		"add('implementation', 'io.netty:netty-handler:4.1.135.Final')",
		"configuration.name ==~ /.*([Cc]ompileClasspath|[Rr]untimeClasspath)/",
		"substitute module('org.lz4:lz4-java') using module('at.yawk.lz4:lz4-java:1.10.1') because 'omnibump coordinate swap'",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// Sorted for determinism: commons-io before io.netty.
	if strings.Index(out, "commons-io:commons-io") > strings.Index(out, "io.netty:netty-handler") {
		t.Errorf("constraints not sorted:\n%s", out)
	}
}

func TestRenderManagedBlock_Kotlin(t *testing.T) {
	out := renderManagedBlock(
		map[string]string{"a.b:c": "1.0"},
		[]Substitution{{OldModule: "old.g:old-a", NewModule: "new.g:new-a", Version: "2.0"}},
		Kotlin,
	)
	for _, want := range []string{
		"gradle.beforeProject {",
		`plugins.withId("java") {`,
		`add("implementation", "a.b:c:1.0")`,
		`name.matches(Regex(".*([Cc]ompileClasspath|[Rr]untimeClasspath)"))`,
		`substitute(module("old.g:old-a")).using(module("new.g:new-a:2.0")).because("omnibump coordinate swap")`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestEnsureManagedBlock_MergeAndIdempotency(t *testing.T) {
	f := mustParseSettings(t, "settings.gradle", "rootProject.name = 'demo'\n")
	if err := f.EnsureManagedBlock(map[string]string{"a.b:c": "1.0"}, nil); err != nil {
		t.Fatalf("first EnsureManagedBlock() error = %v", err)
	}
	first := string(f.Content())
	if !strings.Contains(first, "rootProject.name = 'demo'") {
		t.Errorf("existing settings content dropped:\n%s", first)
	}

	f2 := mustParseSettings(t, "settings.gradle", first)
	if err := f2.EnsureManagedBlock(
		map[string]string{"a.b:c": "2.0", "x.y:z": "3.0"},
		[]Substitution{{OldModule: "org.lz4:lz4-java", NewModule: "at.yawk.lz4:lz4-java", Version: "1.10.1"}},
	); err != nil {
		t.Fatalf("second EnsureManagedBlock() error = %v", err)
	}
	second := string(f2.Content())

	if strings.Count(second, ForceBlockBegin) != 1 || strings.Count(second, ForceBlockEnd) != 1 {
		t.Errorf("markers duplicated:\n%s", second)
	}
	if !strings.Contains(second, "add('implementation', 'a.b:c:2.0')") {
		t.Errorf("merged version bump missing:\n%s", second)
	}
	if strings.Contains(second, "a.b:c:1.0") {
		t.Errorf("stale constraint not replaced:\n%s", second)
	}
	if !strings.Contains(second, "add('implementation', 'x.y:z:3.0')") || !strings.Contains(second, "substitute module('org.lz4:lz4-java')") {
		t.Errorf("new entries missing:\n%s", second)
	}

	f3 := mustParseSettings(t, "settings.gradle", second)
	if err := f3.EnsureManagedBlock(
		map[string]string{"a.b:c": "2.0", "x.y:z": "3.0"},
		[]Substitution{{OldModule: "org.lz4:lz4-java", NewModule: "at.yawk.lz4:lz4-java", Version: "1.10.1"}},
	); err != nil {
		t.Fatalf("third EnsureManagedBlock() error = %v", err)
	}
	if f3.Changed() {
		t.Errorf("idempotent re-run should not change content:\n%s", f3.Content())
	}
}

func TestEnsureManagedBlock_Kotlin(t *testing.T) {
	f := mustParseSettings(t, "settings.gradle.kts", `rootProject.name = "demo"`+"\n")
	if err := f.EnsureManagedBlock(
		map[string]string{"a.b:c": "1.0"},
		[]Substitution{{OldModule: "old.g:old-a", NewModule: "new.g:new-a", Version: "2.0"}},
	); err != nil {
		t.Fatalf("EnsureManagedBlock() error = %v", err)
	}
	first := string(f.Content())
	for _, want := range []string{
		"gradle.beforeProject {",
		`add("implementation", "a.b:c:1.0")`,
		`substitute(module("old.g:old-a")).using(module("new.g:new-a:2.0"))`,
	} {
		if !strings.Contains(first, want) {
			t.Errorf("missing %q in:\n%s", want, first)
		}
	}

	// Re-running merges and stays idempotent in the Kotlin DSL too.
	f2 := mustParseSettings(t, "settings.gradle.kts", first)
	if err := f2.EnsureManagedBlock(map[string]string{"a.b:c": "1.0"},
		[]Substitution{{OldModule: "old.g:old-a", NewModule: "new.g:new-a", Version: "2.0"}}); err != nil {
		t.Fatalf("second EnsureManagedBlock() error = %v", err)
	}
	if f2.Changed() {
		t.Errorf("idempotent re-run should not change content:\n%s", f2.Content())
	}
}

func TestManagedCoordinates(t *testing.T) {
	f := mustParseSettings(t, "settings.gradle", "")
	if err := f.EnsureManagedBlock(
		map[string]string{"io.netty:netty-handler": "4.1.135.Final"},
		[]Substitution{{OldModule: "org.lz4:lz4-java", NewModule: "at.yawk.lz4:lz4-java", Version: "1.10.1"}},
	); err != nil {
		t.Fatalf("EnsureManagedBlock() error = %v", err)
	}
	reparsed := mustParseSettings(t, "settings.gradle", string(f.Content()))
	coords := reparsed.ManagedCoordinates()
	if coords["io.netty:netty-handler"] != "4.1.135.Final" {
		t.Errorf("constraint not surfaced: %v", coords)
	}
	// A substitution surfaces the NEW module at its target version (what
	// validation checks for a replace dependency).
	if coords["at.yawk.lz4:lz4-java"] != "1.10.1" {
		t.Errorf("substitution target not surfaced: %v", coords)
	}
}

func TestEnsureManagedBlock_RejectsInjection(t *testing.T) {
	f := mustParseSettings(t, "settings.gradle", "")
	cases := []struct {
		constraints map[string]string
		subs        []Substitution
	}{
		{constraints: map[string]string{"a.b:c": `1.0') }; exec('rm`}},
		{constraints: map[string]string{"a.b':c": "1.0"}},
		{constraints: map[string]string{"nocolon": "1.0"}},
		{subs: []Substitution{{OldModule: "a.b:c", NewModule: "x.y:z", Version: `1.0"; exec("rm")`}}},
		{subs: []Substitution{{OldModule: "bad", NewModule: "x.y:z", Version: "1.0"}}},
	}
	for _, tc := range cases {
		if err := f.EnsureManagedBlock(tc.constraints, tc.subs); err == nil {
			t.Errorf("EnsureManagedBlock(%v, %v) should reject unsafe input", tc.constraints, tc.subs)
		}
	}
}

func TestNewSettingsFileContent(t *testing.T) {
	content, err := NewSettingsFileContent(Kotlin, map[string]string{"a.b:c": "1.0"}, nil)
	if err != nil {
		t.Fatalf("NewSettingsFileContent() error = %v", err)
	}
	if !strings.HasPrefix(content, ForceBlockBegin) {
		t.Errorf("new settings file should start with the marker:\n%s", content)
	}
	if !strings.Contains(content, `add("implementation", "a.b:c:1.0")`) {
		t.Errorf("kotlin constraint missing:\n%s", content)
	}
}
