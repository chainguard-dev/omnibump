/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package python

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chainguard-dev/omnibump/pkg/analyzer"
)

// PythonAnalyzer implements analyzer.Analyzer for Python projects.
type PythonAnalyzer struct{}

// Analyze parses all manifest files in projectPath and returns a dependency map.
func (a *PythonAnalyzer) Analyze(ctx context.Context, projectPath string) (*analyzer.AnalysisResult, error) {
	_ = ctx

	absPath, err := filepath.Abs(projectPath)
	if err != nil {
		return nil, fmt.Errorf("resolving path: %w", err)
	}

	manifest, err := DetectManifest(absPath)
	if err != nil {
		return nil, fmt.Errorf("detecting manifest in %s: %w", absPath, err)
	}

	result := &analyzer.AnalysisResult{
		Language:      "python",
		Dependencies:  make(map[string]*analyzer.DependencyInfo),
		Properties:    make(map[string]string),
		PropertyUsage: make(map[string]int),
		Metadata:      map[string]any{"buildTool": string(manifest.BuildTool), "manifest": manifest.Type},
	}

	specs, err := readSpecsFromManifest(manifest)
	if err != nil {
		return nil, err
	}

	for _, spec := range specs {
		result.Dependencies[spec.Package] = &analyzer.DependencyInfo{
			Name:           spec.Package,
			Version:        spec.Version,
			UpdateStrategy: "direct",
			Metadata: map[string]any{
				"specifier": spec.Specifier,
				"rawLine":   spec.RawLine,
			},
		}
	}

	return result, nil
}

// AnalyzeRemote analyzes manifest files provided as raw bytes.
// files is a map of filename to content (e.g. "pyproject.toml" -> bytes).
func (a *PythonAnalyzer) AnalyzeRemote(_ context.Context, files map[string][]byte) (*analyzer.RemoteAnalysisResult, error) {
	result := &analyzer.RemoteAnalysisResult{Language: "python"}

	for _, name := range manifestPriority {
		data, ok := files[name]
		if !ok {
			continue
		}

		bt := BuildToolUnknown
		var specs []VersionSpec

		switch name {
		case "pyproject.toml":
			// Write to a temp file to reuse DetectBuildToolFromPyproject
			tmp, err := os.CreateTemp("", "pyproject-*.toml")
			if err != nil {
				return nil, err
			}
			tmpPath := tmp.Name()
			if _, err := tmp.Write(data); err != nil {
				tmp.Close()
				os.Remove(tmpPath)
				return nil, err
			}
			tmp.Close()
			defer os.Remove(tmpPath)

			bt, _ = DetectBuildToolFromPyproject(tmpPath)
			specs, _ = ParsePyprojectDeps(data, bt)
		case "requirements.txt":
			bt = BuildToolPip
			specs = ParseRequirements(data)
		case "setup.cfg":
			bt = BuildToolSetuptools
			specs = ParseSetupCfg(data)
		case "setup.py":
			bt = BuildToolSetuptools
			specs = ParseSetupPy(data)
		case "Pipfile":
			bt = BuildToolPip
			specs, _ = ParsePipfile(data)
		}

		if len(specs) == 0 {
			continue
		}

		ar := &analyzer.AnalysisResult{
			Language:      "python",
			Dependencies:  make(map[string]*analyzer.DependencyInfo),
			Properties:    make(map[string]string),
			PropertyUsage: make(map[string]int),
			Metadata:      map[string]any{"buildTool": string(bt), "manifest": name},
		}
		for _, spec := range specs {
			ar.Dependencies[spec.Package] = &analyzer.DependencyInfo{
				Name:           spec.Package,
				Version:        spec.Version,
				UpdateStrategy: "direct",
				Metadata: map[string]any{
					"specifier": spec.Specifier,
					"rawLine":   spec.RawLine,
				},
			}
		}
		result.FileAnalyses = append(result.FileAnalyses, analyzer.FileAnalysis{
			FilePath: name,
			Analysis: ar,
		})
		break // Use only the highest-priority manifest
	}

	return result, nil
}

// RecommendStrategy always recommends direct updates for Python deps.
// Python doesn't have a "property" abstraction like Maven.
func (a *PythonAnalyzer) RecommendStrategy(_ context.Context, _ *analyzer.AnalysisResult, deps []analyzer.Dependency) (*analyzer.Strategy, error) {
	strategy := &analyzer.Strategy{
		DirectUpdates:        make([]analyzer.Dependency, 0, len(deps)),
		PropertyUpdates:      make(map[string]string),
		Warnings:             []string{},
		AffectedDependencies: make(map[string][]string),
	}
	strategy.DirectUpdates = append(strategy.DirectUpdates, deps...)
	return strategy, nil
}

// readSpecsFromManifest reads dependency specs from a detected manifest.
func readSpecsFromManifest(manifest *ManifestInfo) ([]VersionSpec, error) {
	data, err := os.ReadFile(manifest.Path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", manifest.Path, err)
	}

	switch manifest.Type {
	case "pyproject.toml":
		return ParsePyprojectDeps(data, manifest.BuildTool)
	case "requirements.txt":
		return ParseRequirements(data), nil
	case "setup.cfg":
		return ParseSetupCfg(data), nil
	case "setup.py":
		return ParseSetupPy(data), nil
	case "Pipfile":
		specs, err := ParsePipfile(data)
		if err != nil {
			return nil, err
		}
		return specs, nil
	default:
		return nil, fmt.Errorf("unsupported manifest type: %s", manifest.Type)
	}
}
