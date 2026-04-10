/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package composer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/analyzer"
)

// ErrRemoteAnalysisNotImplemented indicates remote analysis is not yet supported.
var ErrRemoteAnalysisNotImplemented = errors.New("remote analysis not implemented")

// Analyzer implements the analyzer.Analyzer interface for PHP Composer projects.
type Analyzer struct{}

// Analyze performs dependency analysis on a Composer project.
func (ca *Analyzer) Analyze(ctx context.Context, projectPath string) (*analyzer.AnalysisResult, error) {
	log := clog.FromContext(ctx)

	// Determine composer.lock file path
	composerLockPath := projectPath
	if info, err := os.Stat(projectPath); err == nil && info.IsDir() {
		composerLockPath = filepath.Join(projectPath, "composer.lock")
	}

	log.Debugf("Analyzing Composer project: %s", composerLockPath)

	// Parse composer.lock
	file, err := os.Open(filepath.Clean(composerLockPath))
	if err != nil {
		return nil, fmt.Errorf("failed to open composer.lock: %w", err)
	}
	defer func() { _ = file.Close() }()

	composerPackages, err := ParseLock(file)
	if err != nil {
		return nil, fmt.Errorf("failed to parse composer.lock: %w", err)
	}

	result := &analyzer.AnalysisResult{
		Language:      "php",
		Dependencies:  make(map[string]*analyzer.DependencyInfo),
		Properties:    make(map[string]string), // Composer doesn't use properties
		PropertyUsage: make(map[string]int),
		Metadata:      make(map[string]any),
	}

	// Analyze packages
	for _, pkg := range composerPackages {
		info := &analyzer.DependencyInfo{
			Name:           pkg.Name,
			Version:        pkg.Version,
			UsesProperty:   false, // Composer doesn't use properties
			UpdateStrategy: "direct",
			Metadata:       make(map[string]any),
		}

		// Store dependency information
		if len(pkg.Require) > 0 {
			info.Metadata["require"] = pkg.Require
		}

		// Use name as key (Composer packages have unique names)
		result.Dependencies[pkg.Name] = info
	}

	// Store total package count
	result.Metadata["totalPackages"] = len(composerPackages)

	log.Infof("Analysis complete: found %d packages", len(composerPackages))

	return result, nil
}

// AnalyzeRemote performs dependency analysis on remotely-fetched manifest files.
func (ca *Analyzer) AnalyzeRemote(_ context.Context, _ map[string][]byte) (*analyzer.RemoteAnalysisResult, error) {
	return nil, fmt.Errorf("%w for Composer", ErrRemoteAnalysisNotImplemented)
}

// RecommendStrategy suggests update strategy for Composer dependencies.
// For Composer, updates are always direct using composer require.
func (ca *Analyzer) RecommendStrategy(ctx context.Context, analysis *analyzer.AnalysisResult, deps []analyzer.Dependency) (*analyzer.Strategy, error) {
	log := clog.FromContext(ctx)

	strategy := &analyzer.Strategy{
		DirectUpdates:        []analyzer.Dependency{},
		PropertyUpdates:      make(map[string]string), // Composer doesn't use properties
		Warnings:             []string{},
		AffectedDependencies: make(map[string][]string),
	}

	for _, dep := range deps {
		// Check if dependency exists
		depInfo, found := analysis.Dependencies[dep.Name]

		if !found {
			strategy.Warnings = append(strategy.Warnings,
				fmt.Sprintf("Dependency %s not found in composer.lock", dep.Name))
			continue
		}

		log.Debugf("Found %s at version %s, will update to %s", dep.Name, depInfo.Version, dep.Version)
		strategy.DirectUpdates = append(strategy.DirectUpdates, dep)
	}

	log.Infof("Strategy: %d direct updates", len(strategy.DirectUpdates))
	return strategy, nil
}
