/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradlefile

import (
	"strings"
	"testing"
)

func TestRenderManagedBlockWithConfigs_EmptyIsIdentical(t *testing.T) {
	constraints := map[string]string{"io.netty:netty-handler": "4.1.135.Final"}
	for _, dsl := range []DSL{Groovy, Kotlin} {
		base := renderManagedBlock(constraints, nil, dsl)
		withNil := renderManagedBlockWithConfigs(constraints, nil, dsl, nil)
		withEmpty := renderManagedBlockWithConfigs(constraints, nil, dsl, []string{})
		// Extra configs that are all classpath names filter down to nothing.
		withClasspathOnly := renderManagedBlockWithConfigs(constraints, nil, dsl, []string{"runtimeClasspath"})
		for _, got := range []string{withNil, withEmpty, withClasspathOnly} {
			if got != base {
				t.Errorf("dsl %v: output changed when no extra configs apply:\n--- base ---\n%s\n--- got ---\n%s", dsl, base, got)
			}
		}
	}
}

func TestRenderManagedBlockWithConfigs_Groovy(t *testing.T) {
	out := renderManagedBlockWithConfigs(
		map[string]string{"a.b:c": "1.0"}, nil, Groovy,
		[]string{"lineageImplementation", "myBundle", "runtimeClasspath"}, // classpath filtered out
	)
	want := "if (configuration.canBeResolved && (configuration.name ==~ /.*([Cc]ompileClasspath|[Rr]untimeClasspath)/ || configuration.name in ['lineageImplementation', 'myBundle'])) {"
	if !strings.Contains(out, want) {
		t.Errorf("missing guard %q in:\n%s", want, out)
	}
}

func TestRenderManagedBlockWithConfigs_Kotlin(t *testing.T) {
	out := renderManagedBlockWithConfigs(
		map[string]string{"a.b:c": "1.0"}, nil, Kotlin,
		[]string{"lineageImplementation"},
	)
	want := `if (isCanBeResolved && (name.matches(Regex(".*([Cc]ompileClasspath|[Rr]untimeClasspath)")) || name in listOf("lineageImplementation"))) {`
	if !strings.Contains(out, want) {
		t.Errorf("missing guard %q in:\n%s", want, out)
	}
}

func TestFilterExtraConfigs(t *testing.T) {
	got := filterExtraConfigs([]string{"myBundle", "", "runtimeClasspath", "myBundle", "appCfg", "testCompileClasspath"})
	want := []string{"appCfg", "myBundle"} // empty/classpath dropped, deduped, sorted
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("filterExtraConfigs = %v, want %v", got, want)
	}
}

func TestValidateConfigNames(t *testing.T) {
	if err := validateConfigNames([]string{"lineageImplementation", "myBundle", ""}); err != nil {
		t.Errorf("valid names rejected: %v", err)
	}
	for _, bad := range []string{"bad name", "rm -rf", "a'b", "1abc", "a.b"} {
		if err := validateConfigNames([]string{bad}); err == nil {
			t.Errorf("invalid name %q accepted", bad)
		}
	}
}

func TestEnsureManagedBlockWithConfigs_RejectsInvalid(t *testing.T) {
	f := mustParseSettings(t, "settings.gradle", "rootProject.name = 'x'\n")
	err := f.EnsureManagedBlockWithConfigs(map[string]string{"a.b:c": "1.0"}, nil, []string{"bad name"})
	if err == nil {
		t.Fatal("expected error for invalid extra configuration name")
	}
}

func TestEnsureManagedBlockWithConfigs_EmitsGuard(t *testing.T) {
	f := mustParseSettings(t, "settings.gradle", "rootProject.name = 'x'\n")
	if err := f.EnsureManagedBlockWithConfigs(map[string]string{"a.b:c": "1.0"}, nil, []string{"lineageImplementation"}); err != nil {
		t.Fatalf("EnsureManagedBlockWithConfigs error = %v", err)
	}
	out := string(f.Content())
	if !strings.Contains(out, "configuration.name in ['lineageImplementation']") {
		t.Errorf("managed block missing extra config guard:\n%s", out)
	}
}
