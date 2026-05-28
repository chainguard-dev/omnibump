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

// RubyAnalyzer implements the Analyzer interface for Ruby projects.
//
//nolint:revive // Explicit name preferred for clarity
type RubyAnalyzer struct{}

// Analyze performs dependency analysis on a Ruby project.
func (ra *RubyAnalyzer) Analyze(ctx context.Context, projectPath string) (*analyzer.AnalysisResult, error) {
	log := clog.FromContext(ctx)

	// Determine Gemfile.lock file path
	gemfileLockPath := projectPath
	if info, err := os.Stat(projectPath); err == nil && info.IsDir() {
		gemfileLockPath = filepath.Join(projectPath, "Gemfile.lock")
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
		Language:      "ruby",
		Dependencies:  make(map[string]*analyzer.DependencyInfo),
		Properties:    make(map[string]string), // Ruby doesn't use properties
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
			UsesProperty:   false, // Ruby doesn't use properties
			UpdateStrategy: "direct",
			Metadata:       make(map[string]any),
		}

		// Store source information
		if pkg.Source != "" {
			info.Metadata["source"] = pkg.Source
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
//
//nolint:revive // Parameters will be used when implementation is added
func (ra *RubyAnalyzer) AnalyzeRemote(ctx context.Context, files map[string][]byte) (*analyzer.RemoteAnalysisResult, error) {
	return nil, fmt.Errorf("%w for Ruby", ErrRemoteAnalysisNotImplemented)
}

// RecommendStrategy suggests update strategy for Ruby dependencies.
// For Ruby, updates are always direct lockfile edits.
func (ra *RubyAnalyzer) RecommendStrategy(ctx context.Context, analysis *analyzer.AnalysisResult, deps []analyzer.Dependency) (*analyzer.Strategy, error) {
	log := clog.FromContext(ctx)

	strategy := &analyzer.Strategy{
		DirectUpdates:        []analyzer.Dependency{},
		PropertyUpdates:      make(map[string]string), // Ruby doesn't use properties
		Warnings:             []string{},
		AffectedDependencies: make(map[string][]string),
	}

	for _, dep := range deps {
		// Check if dependency exists
		found := false
		var existingVersions []string

		for _, depInfo := range analysis.Dependencies {
			if depInfo.Name == dep.Name {
				found = true
				existingVersions = append(existingVersions, depInfo.Version)
			}
		}

		if !found {
			strategy.Warnings = append(strategy.Warnings,
				fmt.Sprintf("Dependency %s not found in Gemfile.lock", dep.Name))
			continue
		}

		// Warn if multiple versions exist
		if len(existingVersions) > 1 {
			strategy.Warnings = append(strategy.Warnings,
				fmt.Sprintf("Multiple versions of %s found: %v — all will be updated", dep.Name, existingVersions))
		}

		strategy.DirectUpdates = append(strategy.DirectUpdates, dep)
		log.Infof("Will update %s to %s", dep.Name, dep.Version)
	}

	log.Infof("Strategy: %d direct updates", len(strategy.DirectUpdates))
	return strategy, nil
}
