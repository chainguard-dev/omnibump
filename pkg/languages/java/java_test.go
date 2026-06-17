/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package java

import (
	"os"
	"path/filepath"
	"testing"
)

// minimalPom is a valid Maven POM: detection parses the XML and requires the
// Maven namespace on the <project> root element.
const minimalPom = `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>demo</artifactId>
  <version>1.0.0</version>
</project>
`

// writeProjectFiles materializes the given relative-path -> content map in a
// fresh temp dir and returns its path.
func writeProjectFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("failed to create %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("failed to write %s: %v", rel, err)
		}
	}
	return dir
}

func TestDetectBuildTool(t *testing.T) {
	tests := []struct {
		name     string
		files    map[string]string
		wantTool string // "maven", "gradle" or "" for no detection
	}{
		{
			name:     "maven project with root pom",
			files:    map[string]string{"pom.xml": minimalPom},
			wantTool: "maven",
		},
		{
			name:     "gradle project with root groovy build script",
			files:    map[string]string{"build.gradle": "apply plugin: 'java'\n"},
			wantTool: "gradle",
		},
		{
			name:     "gradle project with root kotlin build script",
			files:    map[string]string{"build.gradle.kts": "plugins { java }\n"},
			wantTool: "gradle",
		},
		{
			name:     "gradle project with only a settings script",
			files:    map[string]string{"settings.gradle": "rootProject.name = 'demo'\n"},
			wantTool: "gradle",
		},
		{
			name:     "gradle project with only a kotlin settings script",
			files:    map[string]string{"settings.gradle.kts": `rootProject.name = "demo"` + "\n"},
			wantTool: "gradle",
		},
		{
			// The Kafka case: a Gradle project vendoring Maven archetype
			// POMs in a subdirectory must still resolve to Gradle. Maven's
			// recursive pom scan would otherwise win because Maven is first
			// in the registered build tool order.
			name: "gradle project with nested pom resolves to gradle",
			files: map[string]string{
				"build.gradle":                   "apply plugin: 'java'\n",
				"settings.gradle":                "rootProject.name = 'kafka'\n",
				"streams/quickstart/pom.xml":     minimalPom,
				"streams/quickstart/sub/pom.xml": minimalPom,
			},
			wantTool: "gradle",
		},
		{
			// The mirror case: a Maven project with a stray Gradle script in
			// a subdirectory must still resolve to Maven.
			name: "maven project with nested gradle script resolves to maven",
			files: map[string]string{
				"pom.xml":                 minimalPom,
				"tools/build.gradle":      "apply plugin: 'java'\n",
				"tools/gradle.properties": "x=1\n",
			},
			wantTool: "maven",
		},
		{
			// Both manifests at the root: Maven wins by registration order
			// (pre-existing behavior, kept stable).
			name: "both root manifests prefers maven",
			files: map[string]string{
				"pom.xml":      minimalPom,
				"build.gradle": "apply plugin: 'java'\n",
			},
			wantTool: "maven",
		},
		{
			// No root manifest at all: the recursive fallback still finds
			// Maven projects that keep their POM in a subdirectory.
			name: "nested pom only falls back to recursive maven detection",
			files: map[string]string{
				"modules/core/pom.xml": minimalPom,
			},
			wantTool: "maven",
		},
		{
			name: "version catalog at conventional path detects gradle",
			files: map[string]string{
				"gradle/libs.versions.toml": "[versions]\nnetty = \"4.1.0\"\n",
			},
			wantTool: "gradle",
		},
		{
			name:     "empty directory detects nothing",
			files:    map[string]string{},
			wantTool: "",
		},
		{
			name:     "unrelated files detect nothing",
			files:    map[string]string{"README.md": "hello\n", "src/main.go": "package main\n"},
			wantTool: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeProjectFiles(t, tt.files)

			tool := detectBuildTool(t.Context(), dir)
			gotTool := ""
			if tool != nil {
				gotTool = tool.Name()
			}
			if gotTool != tt.wantTool {
				t.Errorf("detectBuildTool() = %q, want %q", gotTool, tt.wantTool)
			}
		})
	}
}

func TestJava_Detect(t *testing.T) {
	tests := []struct {
		name      string
		files     map[string]string
		wantFound bool
	}{
		{
			name:      "maven project",
			files:     map[string]string{"pom.xml": minimalPom},
			wantFound: true,
		},
		{
			name:      "gradle project",
			files:     map[string]string{"build.gradle.kts": "plugins { java }\n"},
			wantFound: true,
		},
		{
			name:      "no java project",
			files:     map[string]string{"go.mod": "module example.com/x\n"},
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeProjectFiles(t, tt.files)

			j := &Java{}
			found, err := j.Detect(t.Context(), dir)
			if err != nil {
				t.Fatalf("Detect() error = %v", err)
			}
			if found != tt.wantFound {
				t.Errorf("Detect() = %v, want %v", found, tt.wantFound)
			}
		})
	}
}

func TestJava_GetBuildTool(t *testing.T) {
	t.Run("returns detected tool and caches it", func(t *testing.T) {
		dir := writeProjectFiles(t, map[string]string{"build.gradle": "apply plugin: 'java'\n"})

		j := &Java{}
		tool, err := j.GetBuildTool(t.Context(), dir)
		if err != nil {
			t.Fatalf("GetBuildTool() error = %v", err)
		}
		if tool.Name() != "gradle" {
			t.Errorf("GetBuildTool().Name() = %q, want gradle", tool.Name())
		}

		// A second call returns the cached tool even for another directory.
		again, err := j.GetBuildTool(t.Context(), t.TempDir())
		if err != nil {
			t.Fatalf("second GetBuildTool() error = %v", err)
		}
		if again != tool {
			t.Error("GetBuildTool() should return the cached build tool")
		}
	})

	t.Run("errors when nothing is detected", func(t *testing.T) {
		j := &Java{}
		if _, err := j.GetBuildTool(t.Context(), t.TempDir()); err == nil {
			t.Error("GetBuildTool() should error for an empty directory")
		}
	})
}

func TestDetectBuildToolFromPaths(t *testing.T) {
	tests := []struct {
		name  string
		paths []string
		want  string // build tool name, or "" for nil
	}{
		{
			name:  "root pom.xml wins over nested gradle",
			paths: []string{"pom.xml", "subproject/build.gradle"},
			want:  "maven",
		},
		{
			name:  "root build.gradle",
			paths: []string{"build.gradle", "src/main/Foo.java"},
			want:  "gradle",
		},
		{
			name:  "version catalog only",
			paths: []string{"gradle/libs.versions.toml", "settings.gradle.kts"},
			want:  "gradle",
		},
		{
			name:  "nested pom only",
			paths: []string{"service/pom.xml", "README.md"},
			want:  "maven",
		},
		{
			name:  "nested build.gradle only",
			paths: []string{"app/build.gradle"},
			want:  "gradle",
		},
		{
			name:  "no java manifests",
			paths: []string{"go.mod", "main.go"},
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := DetectBuildToolFromPaths(tt.paths)
			got := ""
			if tool != nil {
				got = tool.Name()
			}
			if got != tt.want {
				t.Errorf("DetectBuildToolFromPaths(%v) = %q, want %q", tt.paths, got, tt.want)
			}
		})
	}
}
