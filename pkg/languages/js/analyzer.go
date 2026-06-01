/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package js

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/analyzer"
	"github.com/tidwall/gjson"
)

// ErrRemoteAnalysisNotImplemented is returned by AnalyzeRemote until a
// remote-fetch backend is added.
var ErrRemoteAnalysisNotImplemented = errors.New("remote analysis not yet implemented")

// JSAnalyzer implements analyzer.Analyzer for JavaScript projects.
//
//nolint:revive // Explicit name preferred for clarity.
type JSAnalyzer struct{}

// Analyze performs dependency analysis on a JavaScript project.
func (a *JSAnalyzer) Analyze(ctx context.Context, projectPath string) (*analyzer.AnalysisResult, error) {
	log := clog.FromContext(ctx)

	dir := projectPath
	if info, err := os.Stat(projectPath); err == nil && !info.IsDir() {
		dir = filepath.Dir(projectPath)
	}

	pkgPath := filepath.Join(dir, PackageJSON)
	data, err := os.ReadFile(filepath.Clean(pkgPath))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", pkgPath, err)
	}

	result := &analyzer.AnalysisResult{
		Language:      "js",
		Dependencies:  make(map[string]*analyzer.DependencyInfo),
		Properties:    make(map[string]string),
		PropertyUsage: make(map[string]int),
		Metadata:      make(map[string]any),
	}

	for _, m := range AllManagers {
		path := m.OverridesPath()
		if path == "" {
			continue
		}
		section := gjson.GetBytes(data, path)
		if !section.Exists() || !section.IsObject() {
			continue
		}

		section.ForEach(func(key, value gjson.Result) bool {
			selector := key.String()
			info := &analyzer.DependencyInfo{
				Name:           selector,
				Version:        value.String(),
				UpdateStrategy: "override",
				Metadata: map[string]any{
					"manager": string(m),
				},
			}
			result.Dependencies[string(m)+":"+selector] = info
			return true
		})
	}

	result.Metadata["totalOverrides"] = len(result.Dependencies)
	log.Infof("Analysis complete: found %d existing override(s) in %s", len(result.Dependencies), pkgPath)

	return result, nil
}

// AnalyzeRemote is not yet implemented for JS.
func (a *JSAnalyzer) AnalyzeRemote(_ context.Context, _ map[string][]byte) (*analyzer.RemoteAnalysisResult, error) {
	return nil, fmt.Errorf("%w for JS", ErrRemoteAnalysisNotImplemented)
}

// RecommendStrategy suggests update strategy for JS dependencies.
// JS updates are always direct overrides.
func (a *JSAnalyzer) RecommendStrategy(ctx context.Context, _ *analyzer.AnalysisResult, deps []analyzer.Dependency) (*analyzer.Strategy, error) {
	log := clog.FromContext(ctx)

	strategy := &analyzer.Strategy{
		DirectUpdates:        append([]analyzer.Dependency{}, deps...),
		PropertyUpdates:      map[string]string{},
		Warnings:             []string{},
		AffectedDependencies: map[string][]string{},
	}

	log.Infof("Strategy: %d direct override(s) recommended", len(strategy.DirectUpdates))
	return strategy, nil
}
