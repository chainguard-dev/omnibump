/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package ruby

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/analyzer"
)

// Analyzer implements the analyzer.Analyzer interface for Ruby projects.
type Analyzer struct{}

// Verify Analyzer implements the analyzer.Analyzer interface at compile time.
var _ analyzer.Analyzer = (*Analyzer)(nil)

// Analyze performs dependency analysis on a Ruby project.
func (a *Analyzer) Analyze(ctx context.Context, projectPath string) (*analyzer.AnalysisResult, error) {
	log := clog.FromContext(ctx)

	// Determine Gemfile.lock file path
	gemfileLockPath := projectPath
	if info, err := os.Stat(projectPath); err == nil && info.IsDir() {
		gemfileLockPath = filepath.Join(projectPath, ManifestGemfileLock)
	}

	log.Debugf("Analyzing Ruby project: %s", gemfileLockPath)

	// Parse Gemfile.lock
	file, err := os.Open(filepath.Clean(gemfileLockPath))
	if err != nil {
		return nil, fmt.Errorf("failed to open Gemfile.lock: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			log.Warnf("failed to close Gemfile.lock: %v", closeErr)
		}
	}()

	gemPackages, err := ParseGemfileLock(file)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Gemfile.lock: %w", err)
	}

	result := &analyzer.AnalysisResult{
		Language:      LanguageRuby,
		Dependencies:  make(map[string]*analyzer.DependencyInfo),
		Properties:    make(map[string]string),
		PropertyUsage: make(map[string]int),
		Metadata:      make(map[string]any),
	}

	// Track unique package names (gems may appear in multiple source sections)
	packageVersions := make(map[string][]string)

	// Analyze packages
	for _, pkg := range gemPackages {
		info := &analyzer.DependencyInfo{
			Name:           pkg.Name,
			Version:        pkg.Version,
			UpdateStrategy: "direct",
			Metadata: map[string]any{
				"source": pkg.Source,
			},
		}

		// Store dependency constraints
		if len(pkg.Dependencies) > 0 {
			info.Metadata["dependencies"] = pkg.Dependencies
		}

		// Track multiple versions of same package
		packageVersions[pkg.Name] = append(packageVersions[pkg.Name], pkg.Version)

		// Use name as key (gems normally have one version in a lockfile)
		key := pkg.Name
		// If multiple versions exist, use name@version
		if _, exists := result.Dependencies[key]; exists {
			key = fmt.Sprintf("%s@%s", pkg.Name, pkg.Version)
		}
		result.Dependencies[key] = info
	}

	// Mark packages with multiple versions
	for name, versions := range packageVersions {
		if len(versions) > 1 {
			result.Metadata[fmt.Sprintf("%s_versions", name)] = versions
		}
	}

	// Store total package count
	result.Metadata["totalPackages"] = len(gemPackages)
	result.Metadata["uniquePackages"] = len(packageVersions)

	log.Infof("Analysis complete: found %d packages (%d unique)", len(gemPackages), len(packageVersions))

	return result, nil
}

// AnalyzeRemote performs dependency analysis on remotely-fetched Ruby files.
// Not yet implemented for Ruby — returns error.
func (a *Analyzer) AnalyzeRemote(_ context.Context, _ map[string][]byte) (*analyzer.RemoteAnalysisResult, error) {
	return nil, fmt.Errorf("%w for Ruby", ErrRemoteAnalysisNotImplemented)
}

// RecommendStrategy always recommends direct updates for Ruby dependencies.
// Ruby doesn't have a "property" abstraction like Maven.
func (a *Analyzer) RecommendStrategy(_ context.Context, _ *analyzer.AnalysisResult, deps []analyzer.Dependency) (*analyzer.Strategy, error) {
	strategy := &analyzer.Strategy{
		DirectUpdates:        make([]analyzer.Dependency, 0, len(deps)),
		PropertyUpdates:      make(map[string]string),
		Warnings:             []string{},
		AffectedDependencies: make(map[string][]string),
	}
	strategy.DirectUpdates = append(strategy.DirectUpdates, deps...)
	return strategy, nil
}
