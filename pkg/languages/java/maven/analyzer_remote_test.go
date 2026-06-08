/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package maven

import (
	"context"
	"testing"

	"github.com/chainguard-dev/omnibump/pkg/analyzer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pomXML returns a minimal pom.xml byte slice for testing.
func pomXML(properties, deps string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>test</artifactId>
  <version>1.0.0</version>` + properties + deps + `
</project>`)
}

func propertiesBlock(props map[string]string) string {
	if len(props) == 0 {
		return ""
	}
	var s string
	s += "\n  <properties>"
	for k, v := range props {
		s += "\n    <" + k + ">" + v + "</" + k + ">"
	}
	s += "\n  </properties>"
	return s
}

func dependencyBlock(groupID, artifactID, version string) string {
	return `
  <dependencies>
    <dependency>
      <groupId>` + groupID + `</groupId>
      <artifactId>` + artifactID + `</artifactId>
      <version>` + version + `</version>
    </dependency>
  </dependencies>`
}

// TestAnalyzeRemote_DirectDep verifies a dependency with a hardcoded version
// is classified as a direct dep (not a property).
func TestAnalyzeRemote_DirectDep(t *testing.T) {
	files := map[string][]byte{
		"pom.xml": pomXML("", dependencyBlock("org.postgresql", "postgresql", "42.7.10")),
	}
	result, err := (&MavenAnalyzer{}).AnalyzeRemote(context.Background(), files)
	require.NoError(t, err)
	require.Len(t, result.FileAnalyses, 1)

	analysis := result.FileAnalyses[0].Analysis
	dep, ok := analysis.Dependencies["org.postgresql:postgresql"]
	require.True(t, ok, "expected dep to be present")
	assert.False(t, dep.UsesProperty)
	assert.Equal(t, "direct", dep.UpdateStrategy)
}

// TestAnalyzeRemote_PropertyDep verifies a dependency whose version is a
// property reference is classified correctly and the property source is set.
func TestAnalyzeRemote_PropertyDep(t *testing.T) {
	files := map[string][]byte{
		"pom.xml": pomXML(
			propertiesBlock(map[string]string{"netty.version": "4.1.93.Final"}),
			dependencyBlock("io.netty", "netty-codec-http", "${netty.version}"),
		),
	}
	result, err := (&MavenAnalyzer{}).AnalyzeRemote(context.Background(), files)
	require.NoError(t, err)

	analysis := result.FileAnalyses[0].Analysis
	dep, ok := analysis.Dependencies["io.netty:netty-codec-http"]
	require.True(t, ok)
	assert.True(t, dep.UsesProperty)
	assert.Equal(t, "netty.version", dep.PropertyName)
	assert.Equal(t, "4.1.93.Final", analysis.Properties["netty.version"])
	assert.Equal(t, "pom.xml", analysis.PropertySources["netty.version"])
}

// TestAnalyzeRemote_PropertyInParentPom verifies that when a property is
// defined in a parent pom.xml referenced via <relativePath>, AnalyzeRemote
// resolves it from the provided files map and sets PropertySources correctly.
func TestAnalyzeRemote_PropertyInParentPom(t *testing.T) {
	parentPom := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>parent</artifactId>
  <version>1.0.0</version>
  <properties>
    <quarkus.version>3.5.0</quarkus.version>
  </properties>
</project>`)

	childPom := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<project>
  <parent>
    <groupId>com.example</groupId>
    <artifactId>parent</artifactId>
    <version>1.0.0</version>
    <relativePath>../parent/pom.xml</relativePath>
  </parent>
  <artifactId>child</artifactId>
  <dependencies>
    <dependency>
      <groupId>io.quarkus</groupId>
      <artifactId>quarkus-core</artifactId>
      <version>${quarkus.version}</version>
    </dependency>
  </dependencies>
</project>`)

	files := map[string][]byte{
		"pom.xml":        childPom,
		"parent/pom.xml": parentPom,
	}

	result, err := (&MavenAnalyzer{}).AnalyzeRemote(context.Background(), files)
	require.NoError(t, err)

	analysis := result.FileAnalyses[0].Analysis
	dep, ok := analysis.Dependencies["io.quarkus:quarkus-core"]
	require.True(t, ok)
	assert.True(t, dep.UsesProperty)
	assert.Equal(t, "quarkus.version", dep.PropertyName)
	// Property resolved from parent pom via files map.
	assert.Equal(t, "3.5.0", analysis.Properties["quarkus.version"])
	assert.Equal(t, "parent/pom.xml", analysis.PropertySources["quarkus.version"])
}

// TestAnalyzeRemote_MultiplePomsAggregated verifies that properties and deps
// from multiple pom.xml files are all collected in the result.
func TestAnalyzeRemote_MultiplePomsAggregated(t *testing.T) {
	files := map[string][]byte{
		"pom.xml": pomXML(
			propertiesBlock(map[string]string{"spring.version": "6.1.0"}),
			"",
		),
		"module/pom.xml": pomXML(
			"",
			dependencyBlock("org.springframework", "spring-core", "${spring.version}"),
		),
	}

	result, err := (&MavenAnalyzer{}).AnalyzeRemote(context.Background(), files)
	require.NoError(t, err)

	analysis := result.FileAnalyses[0].Analysis
	assert.Equal(t, "6.1.0", analysis.Properties["spring.version"])
	dep, ok := analysis.Dependencies["org.springframework:spring-core"]
	require.True(t, ok)
	assert.True(t, dep.UsesProperty)
}

// TestAnalyzeRemote_EmptyFiles verifies that an empty files map returns an error.
func TestAnalyzeRemote_EmptyFiles(t *testing.T) {
	_, err := (&MavenAnalyzer{}).AnalyzeRemote(context.Background(), map[string][]byte{})
	require.Error(t, err)
}

// TestAnalyzeRemote_RecommendStrategy_PropertyRouting verifies the full path:
// AnalyzeRemote + RecommendStrategy classifies a property dep correctly.
func TestAnalyzeRemote_RecommendStrategy_PropertyRouting(t *testing.T) {
	files := map[string][]byte{
		"pom.xml": pomXML(
			propertiesBlock(map[string]string{"netty.version": "4.1.93.Final"}),
			dependencyBlock("io.netty", "netty-codec-http", "${netty.version}"),
		),
	}

	ma := &MavenAnalyzer{}
	remote, err := ma.AnalyzeRemote(context.Background(), files)
	require.NoError(t, err)
	analysis := remote.FileAnalyses[0].Analysis

	strategy, err := ma.RecommendStrategy(context.Background(), analysis, []analyzer.Dependency{
		{
			Name:    "io.netty:netty-codec-http",
			Version: "4.1.95.Final",
			Metadata: map[string]any{
				"groupId":    "io.netty",
				"artifactId": "netty-codec-http",
			},
		},
	})
	require.NoError(t, err)

	assert.Empty(t, strategy.DirectUpdates, "should have no direct updates")
	assert.Equal(t, "4.1.95.Final", strategy.PropertyUpdates["netty.version"])
}

// TestResolvePropertyFromMap_RootPom verifies resolution when the property is
// in the root pom.xml itself.
func TestResolvePropertyFromMap_RootPom(t *testing.T) {
	files := map[string][]byte{
		"pom.xml": pomXML(
			propertiesBlock(map[string]string{"foo.version": "1.0"}),
			"",
		),
	}
	path, val, ok := resolvePropertyFromMap(files, "pom.xml", "foo.version")
	require.True(t, ok)
	assert.Equal(t, "pom.xml", path)
	assert.Equal(t, "1.0", val)
}

// TestResolvePropertyFromMap_ParentRelativePath verifies that a relative parent
// path like "../parent/pom.xml" is resolved correctly within the files map.
func TestResolvePropertyFromMap_ParentRelativePath(t *testing.T) {
	parentPom := []byte(`<?xml version="1.0"?>
<project>
  <properties>
    <bar.version>2.0</bar.version>
  </properties>
</project>`)

	childPom := []byte(`<?xml version="1.0"?>
<project>
  <parent>
    <relativePath>../parent/pom.xml</relativePath>
  </parent>
</project>`)

	files := map[string][]byte{
		"child/pom.xml":  childPom,
		"parent/pom.xml": parentPom,
	}

	path, val, ok := resolvePropertyFromMap(files, "child/pom.xml", "bar.version")
	require.True(t, ok)
	assert.Equal(t, "parent/pom.xml", path)
	assert.Equal(t, "2.0", val)
}

// TestResolvePropertyFromMap_NotFound verifies that a missing property returns false.
func TestResolvePropertyFromMap_NotFound(t *testing.T) {
	files := map[string][]byte{
		"pom.xml": pomXML("", ""),
	}
	_, _, ok := resolvePropertyFromMap(files, "pom.xml", "missing.property")
	assert.False(t, ok)
}

// TestParseProjectFromBytes verifies that in-memory parsing produces the same
// result as gopom.Parse would for the same content.
func TestParseProjectFromBytes(t *testing.T) {
	content := []byte(`<?xml version="1.0"?>
<project>
  <groupId>com.example</groupId>
  <artifactId>test</artifactId>
  <version>1.0.0</version>
  <properties>
    <my.prop>hello</my.prop>
  </properties>
</project>`)

	project, err := parseProjectFromBytes(content)
	require.NoError(t, err)
	assert.Equal(t, "com.example", project.GroupID)
	assert.Equal(t, "test", project.ArtifactID)
	assert.Equal(t, "hello", project.Properties.Entries["my.prop"])
}
