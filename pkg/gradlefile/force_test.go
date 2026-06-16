/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradlefile

import (
	"strings"
	"testing"
)

func TestEnsureForceBlock_NewBlockGroovy(t *testing.T) {
	f := mustParseBuild(t, "build.gradle", "apply plugin: 'java'\n")
	coords := map[string]string{
		"io.netty:netty-codec":  "4.1.133.Final",
		"io.netty:netty-buffer": "4.1.133.Final",
	}
	if err := f.EnsureForceBlock(coords); err != nil {
		t.Fatalf("EnsureForceBlock() error = %v", err)
	}
	updated := string(f.Content())

	for _, want := range []string{
		ForceBlockBegin,
		ForceBlockEnd,
		"allprojects {",
		"afterEvaluate {",
		"configurations.matching { it.name ==~ /.*([Cc]ompileClasspath|[Rr]untimeClasspath)/ }.all {",
		"resolutionStrategy {",
		"force 'io.netty:netty-buffer:4.1.133.Final'",
		"force 'io.netty:netty-codec:4.1.133.Final'",
		"eachDependency {",
		"if (it.requested.group == 'io.netty' && it.requested.name == 'netty-buffer') { it.useVersion('4.1.133.Final') }",
	} {
		if !strings.Contains(updated, want) {
			t.Errorf("missing %q in:\n%s", want, updated)
		}
	}
	// Sorted for determinism: buffer before codec.
	if strings.Index(updated, "netty-buffer") > strings.Index(updated, "netty-codec") {
		t.Errorf("force entries not sorted:\n%s", updated)
	}
}

func TestEnsureForceBlock_NewBlockKotlin(t *testing.T) {
	f := mustParseBuild(t, "build.gradle.kts", "plugins { java }\n")
	if err := f.EnsureForceBlock(map[string]string{"a.b:c": "1.0"}); err != nil {
		t.Fatalf("EnsureForceBlock() error = %v", err)
	}
	updated := string(f.Content())
	if !strings.Contains(updated, `force("a.b:c:1.0")`) {
		t.Errorf("kotlin force syntax missing:\n%s", updated)
	}
	if !strings.Contains(updated, `configurations.matching { it.name.matches(Regex(".*([Cc]ompileClasspath|[Rr]untimeClasspath)")) }.all {`) {
		t.Errorf("kotlin classpath allowlist missing:\n%s", updated)
	}
	if !strings.Contains(updated, `if (requested.group == "a.b" && requested.name == "c") { useVersion("1.0") }`) {
		t.Errorf("kotlin eachDependency rule missing:\n%s", updated)
	}
}

func TestEnsureForceBlock_MergeAndIdempotency(t *testing.T) {
	f := mustParseBuild(t, "build.gradle", "apply plugin: 'java'\n")
	if err := f.EnsureForceBlock(map[string]string{"a.b:c": "1.0"}); err != nil {
		t.Fatalf("first EnsureForceBlock() error = %v", err)
	}
	first := string(f.Content())

	// Re-parse the result and merge a second module plus a version bump of
	// the first.
	f2 := mustParseBuild(t, "build.gradle", first)
	if err := f2.EnsureForceBlock(map[string]string{"a.b:c": "2.0", "x.y:z": "3.0"}); err != nil {
		t.Fatalf("second EnsureForceBlock() error = %v", err)
	}
	second := string(f2.Content())

	if strings.Count(second, ForceBlockBegin) != 1 || strings.Count(second, ForceBlockEnd) != 1 {
		t.Errorf("markers duplicated:\n%s", second)
	}
	if !strings.Contains(second, "force 'a.b:c:2.0'") {
		t.Errorf("merged version bump missing:\n%s", second)
	}
	if strings.Contains(second, "force 'a.b:c:1.0'") {
		t.Errorf("stale force entry not replaced:\n%s", second)
	}
	if !strings.Contains(second, "force 'x.y:z:3.0'") {
		t.Errorf("new force entry missing:\n%s", second)
	}

	// Idempotency: same coords again produce no change.
	f3 := mustParseBuild(t, "build.gradle", second)
	if err := f3.EnsureForceBlock(map[string]string{"a.b:c": "2.0", "x.y:z": "3.0"}); err != nil {
		t.Fatalf("third EnsureForceBlock() error = %v", err)
	}
	if f3.Changed() {
		t.Errorf("idempotent re-run should not change content:\n%s", f3.Content())
	}
}

func TestForcedCoordinates(t *testing.T) {
	f := mustParseBuild(t, "build.gradle", "x\n")
	if err := f.EnsureForceBlock(map[string]string{"a.b:c": "1.0"}); err != nil {
		t.Fatalf("EnsureForceBlock() error = %v", err)
	}
	f2 := mustParseBuild(t, "build.gradle", string(f.Content()))
	coords := f2.ForcedCoordinates()
	if coords["a.b:c"] != "1.0" {
		t.Errorf("ForcedCoordinates() = %v", coords)
	}
}

func TestEnsureForceBlock_RejectsInjection(t *testing.T) {
	f := mustParseBuild(t, "build.gradle", "x\n")
	for _, coords := range []map[string]string{
		{"a.b:c": `1.0' } } } }; exec('rm')`},
		{"a.b':c": "1.0"},
		{`a.b:c"`: "1.0"},
		{"nocolon": "1.0"},
	} {
		if err := f.EnsureForceBlock(coords); err == nil {
			t.Errorf("EnsureForceBlock(%v) should reject unsafe input", coords)
		}
	}
}

func TestNewBuildFileContent(t *testing.T) {
	content, err := NewBuildFileContent(Kotlin, map[string]string{"a.b:c": "1.0"})
	if err != nil {
		t.Fatalf("NewBuildFileContent() error = %v", err)
	}
	if !strings.HasPrefix(content, ForceBlockBegin) {
		t.Errorf("new file should start with the marker:\n%s", content)
	}
	if !strings.Contains(content, `force("a.b:c:1.0")`) {
		t.Errorf("kotlin force entry missing:\n%s", content)
	}
}
