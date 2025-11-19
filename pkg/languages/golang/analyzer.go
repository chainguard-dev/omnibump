/*
Copyright 2025 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package golang

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/analyzer"
)

// GolangAnalyzer implements the Analyzer interface for Go projects.
type GolangAnalyzer struct{}

// Analyze performs dependency analysis on a Go project.
func (ga *GolangAnalyzer) Analyze(ctx context.Context, projectPath string) (*analyzer.AnalysisResult, error) {
	log := clog.FromContext(ctx)

	// Determine go.mod file path
	goModPath := projectPath
	if info, err := os.Stat(projectPath); err == nil && info.IsDir() {
		goModPath = filepath.Join(projectPath, "go.mod")
	}

	log.Debugf("Analyzing Go project: %s", goModPath)

	// Parse go.mod
	modFile, _, err := ParseGoModfile(goModPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse go.mod: %w", err)
	}

	result := &analyzer.AnalysisResult{
		Language:      "go",
		Dependencies:  make(map[string]*analyzer.DependencyInfo),
		Properties:    make(map[string]string), // Go doesn't use properties
		PropertyUsage: make(map[string]int),
		Metadata:      make(map[string]any),
	}

	// Store Go version
	if modFile.Go != nil {
		result.Metadata["goVersion"] = modFile.Go.Version
	}

	// Analyze require directives
	for _, req := range modFile.Require {
		if req == nil {
			continue
		}

		info := &analyzer.DependencyInfo{
			Name:           req.Mod.Path,
			Version:        req.Mod.Version,
			UsesProperty:   false, // Go doesn't use properties
			UpdateStrategy: "direct",
			Metadata:       make(map[string]any),
		}

		// Mark indirect dependencies
		if req.Indirect {
			info.Transitive = true
			info.Metadata["indirect"] = true
		}

		result.Dependencies[req.Mod.Path] = info
	}

	// Analyze replace directives
	for _, repl := range modFile.Replace {
		if repl == nil {
			continue
		}

		// Update or add dependency info
		if info, exists := result.Dependencies[repl.Old.Path]; exists {
			info.Metadata["replaced"] = true
			info.Metadata["replacedWith"] = repl.New.Path
			info.Metadata["replaceVersion"] = repl.New.Version
			info.UpdateStrategy = "replace"
		} else {
			// Create entry for replaced dependency
			info := &analyzer.DependencyInfo{
				Name:           repl.Old.Path,
				Version:        repl.Old.Version,
				UpdateStrategy: "replace",
				Metadata: map[string]any{
					"replaced":       true,
					"replacedWith":   repl.New.Path,
					"replaceVersion": repl.New.Version,
				},
			}
			result.Dependencies[repl.Old.Path] = info
		}
	}

	log.Infof("Analysis complete: found %d dependencies (%d direct, %d indirect)",
		len(result.Dependencies), countDirect(result), countIndirect(result))

	return result, nil
}

// RecommendStrategy suggests update strategy for Go dependencies.
// For Go, it's simpler than Maven - either direct update or replace directive.
func (ga *GolangAnalyzer) RecommendStrategy(ctx context.Context, analysis *analyzer.AnalysisResult, deps []analyzer.Dependency) (*analyzer.Strategy, error) {
	log := clog.FromContext(ctx)

	strategy := &analyzer.Strategy{
		DirectUpdates:        []analyzer.Dependency{},
		PropertyUpdates:      make(map[string]string), // Go doesn't use properties
		Warnings:             []string{},
		AffectedDependencies: make(map[string][]string),
	}

	for _, dep := range deps {
		if depInfo, exists := analysis.Dependencies[dep.Name]; exists {
			// Check if this is a replaced dependency
			if replaced, ok := depInfo.Metadata["replaced"].(bool); ok && replaced {
				strategy.Warnings = append(strategy.Warnings,
					fmt.Sprintf("Dependency %s is replaced with %s - update may require changing replace directive",
						dep.Name, depInfo.Metadata["replacedWith"]))
			}

			// Check if it's an indirect dependency
			if depInfo.Transitive {
				strategy.Warnings = append(strategy.Warnings,
					fmt.Sprintf("Dependency %s is indirect - consider if direct update is needed", dep.Name))
			}
		}

		strategy.DirectUpdates = append(strategy.DirectUpdates, dep)
		log.Infof("Will update %s to %s", dep.Name, dep.Version)
	}

	log.Infof("Strategy: %d direct updates", len(strategy.DirectUpdates))
	return strategy, nil
}

// countDirect counts direct dependencies.
func countDirect(result *analyzer.AnalysisResult) int {
	count := 0
	for _, dep := range result.Dependencies {
		if !dep.Transitive {
			count++
		}
	}
	return count
}

// countIndirect counts indirect dependencies.
func countIndirect(result *analyzer.AnalysisResult) int {
	count := 0
	for _, dep := range result.Dependencies {
		if dep.Transitive {
			count++
		}
	}
	return count
}
