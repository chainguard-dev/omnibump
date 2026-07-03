/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package maven

import (
	"context"
	"encoding/xml"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/gopom"
	"github.com/chainguard-dev/omnibump/pkg/analyzer"
)

// AnalyzeRemote performs dependency analysis on remotely-fetched Maven pom.xml files.
// files is a map from relative path (e.g. "pom.xml", "parent/pom.xml") to file content.
func (ma *MavenAnalyzer) AnalyzeRemote(ctx context.Context, files map[string][]byte) (*analyzer.RemoteAnalysisResult, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("%w: no pom.xml files provided", ErrNoPOMsFound)
	}

	result := &analyzer.AnalysisResult{
		Language:        "java",
		Dependencies:    make(map[string]*analyzer.DependencyInfo),
		Properties:      make(map[string]string),
		PropertySources: make(map[string]string),
		PropertyUsage:   make(map[string]int),
		Metadata:        map[string]any{"buildTool": mavenToolName},
	}

	// Process files in deterministic order so results are reproducible.
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, path := range paths {
		content := files[path]
		project, err := parseProjectFromBytes(content)
		if err != nil {
			clog.DebugContextf(ctx, "Skipping %s: %v", path, err)
			continue
		}
		for k, v := range extractPropertiesFromProject(project) {
			mergeProperty(ctx, result, k, v, path)
		}
		if project.Dependencies != nil {
			for _, dep := range *project.Dependencies {
				analyzeDependency(ctx, dep, result)
			}
		}
		if project.DependencyManagement != nil && project.DependencyManagement.Dependencies != nil {
			for _, dep := range *project.DependencyManagement.Dependencies {
				analyzeDependency(ctx, dep, result)
			}
		}
	}

	clog.InfoContextf(ctx, "Remote analysis: %d pom.xml files, %d dependencies, %d using properties",
		len(paths), len(result.Dependencies), countPropertiesUsage(result))

	// For any property referenced by deps but not found in the provided files,
	// follow the <parent><relativePath> chain within the files map.
	for propName := range result.PropertyUsage {
		if _, found := result.Properties[propName]; found {
			continue
		}
		if path, value, ok := resolvePropertyFromMap(files, "pom.xml", propName); ok {
			result.Properties[propName] = value
			result.PropertySources[propName] = path
			clog.InfoContextf(ctx, "Resolved property %s = %s from parent POM %s", propName, value, path)
		}
	}

	return &analyzer.RemoteAnalysisResult{
		Language: "java",
		FileAnalyses: []analyzer.FileAnalysis{{
			FilePath: "pom.xml",
			Analysis: result,
		}},
		Metadata: map[string]any{"buildTool": mavenToolName},
	}, nil
}

// parseProjectFromBytes parses a Maven POM from its raw XML content.
// gopom.Parse reads a file path; this is the in-memory equivalent.
func parseProjectFromBytes(content []byte) (*gopom.Project, error) {
	var project gopom.Project
	if err := xml.Unmarshal(content, &project); err != nil {
		return nil, fmt.Errorf("failed to parse POM XML: %w", err)
	}
	return &project, nil
}

// resolvePropertyFromMap follows <parent><relativePath> chains within the
// provided files map to find a property that is not in the root pom.xml.
// Returns (sourceFile, value, true) if found, ("", "", false) otherwise.
func resolvePropertyFromMap(files map[string][]byte, startPath, propName string) (string, string, bool) {
	current := startPath
	visited := make(map[string]struct{})

	for {
		if _, seen := visited[current]; seen {
			return "", "", false // cycle guard
		}
		visited[current] = struct{}{}

		content, ok := files[current]
		if !ok {
			return "", "", false
		}
		project, err := parseProjectFromBytes(content)
		if err != nil {
			return "", "", false
		}

		if project.Properties != nil && project.Properties.Entries != nil {
			if val, exists := project.Properties.Entries[propName]; exists {
				return current, val, true
			}
		}

		// Follow <parent><relativePath> if present.
		if project.Parent == nil || project.Parent.RelativePath == "" {
			return "", "", false
		}

		parentRel := project.Parent.RelativePath
		// RelativePath may point to a directory (default pom.xml) or a specific file.
		if !strings.HasSuffix(parentRel, ".xml") {
			parentRel = filepath.Join(parentRel, "pom.xml")
		}
		// Resolve relative to the directory of the current pom.
		next := filepath.Join(filepath.Dir(current), parentRel)
		// Normalise to forward slashes so map keys match.
		next = filepath.ToSlash(next)
		// Remove any leading "./" produced by filepath.Join.
		next = strings.TrimPrefix(next, "./")

		current = next
	}
}
