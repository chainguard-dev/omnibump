/*
Copyright 2025 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package rust

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/analyzer"
)

// RustAnalyzer implements the Analyzer interface for Rust projects.
type RustAnalyzer struct{}

// Analyze performs dependency analysis on a Rust project.
func (ra *RustAnalyzer) Analyze(ctx context.Context, projectPath string) (*analyzer.AnalysisResult, error) {
	log := clog.FromContext(ctx)

	// Determine Cargo.lock file path
	cargoLockPath := projectPath
	if info, err := os.Stat(projectPath); err == nil && info.IsDir() {
		cargoLockPath = filepath.Join(projectPath, "Cargo.lock")
	}

	log.Debugf("Analyzing Rust project: %s", cargoLockPath)

	// Parse Cargo.lock
	file, err := os.Open(cargoLockPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open Cargo.lock: %w", err)
	}
	defer file.Close()

	cargoPackages, err := ParseCargoLock(file)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Cargo.lock: %w", err)
	}

	result := &analyzer.AnalysisResult{
		Language:      "rust",
		Dependencies:  make(map[string]*analyzer.DependencyInfo),
		Properties:    make(map[string]string), // Rust doesn't use properties
		PropertyUsage: make(map[string]int),
		Metadata:      make(map[string]any),
	}

	// Track unique package names (some packages may have multiple versions)
	packageVersions := make(map[string][]string)

	// Analyze packages
	for _, pkg := range cargoPackages {
		info := &analyzer.DependencyInfo{
			Name:           pkg.Name,
			Version:        pkg.Version,
			UsesProperty:   false, // Rust doesn't use properties
			UpdateStrategy: "direct",
			Metadata:       make(map[string]any),
		}

		// Store dependency information
		if len(pkg.Dependencies) > 0 {
			info.Metadata["dependencies"] = pkg.Dependencies
		}

		// Track multiple versions of same package
		packageVersions[pkg.Name] = append(packageVersions[pkg.Name], pkg.Version)

		// Use name@version as key to handle multiple versions
		key := fmt.Sprintf("%s@%s", pkg.Name, pkg.Version)
		result.Dependencies[key] = info
	}

	// Mark packages with multiple versions
	for name, versions := range packageVersions {
		if len(versions) > 1 {
			result.Metadata[fmt.Sprintf("%s_versions", name)] = versions
		}
	}

	// Store total package count
	result.Metadata["totalPackages"] = len(cargoPackages)
	result.Metadata["uniquePackages"] = len(packageVersions)

	log.Infof("Analysis complete: found %d packages (%d unique)", len(cargoPackages), len(packageVersions))

	return result, nil
}

// RecommendStrategy suggests update strategy for Rust dependencies.
// For Rust, updates are always direct using cargo update.
func (ra *RustAnalyzer) RecommendStrategy(ctx context.Context, analysis *analyzer.AnalysisResult, deps []analyzer.Dependency) (*analyzer.Strategy, error) {
	log := clog.FromContext(ctx)

	strategy := &analyzer.Strategy{
		DirectUpdates:        []analyzer.Dependency{},
		PropertyUpdates:      make(map[string]string), // Rust doesn't use properties
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
				fmt.Sprintf("Dependency %s not found in Cargo.lock", dep.Name))
			continue
		}

		// Warn if multiple versions exist
		if len(existingVersions) > 1 {
			strategy.Warnings = append(strategy.Warnings,
				fmt.Sprintf("Multiple versions of %s found: %v - all will be updated", dep.Name, existingVersions))
		}

		strategy.DirectUpdates = append(strategy.DirectUpdates, dep)
		log.Infof("Will update %s to %s", dep.Name, dep.Version)
	}

	log.Infof("Strategy: %d direct updates", len(strategy.DirectUpdates))
	return strategy, nil
}
