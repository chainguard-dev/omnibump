/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package rust

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/analyzer"
)

// RustAnalyzer implements the Analyzer interface for Rust projects.
//
//nolint:revive // Explicit name preferred for clarity
type RustAnalyzer struct {
	// resolveLatest resolves a crate target spec (e.g. "rand" or "rand@0.9.0")
	// to the latest compatible published version. It defaults to
	// LatestCrateVersion when nil; tests override it to avoid network access.
	resolveLatest func(ctx context.Context, spec string) (string, error)
}

// latestVersion resolves spec to the latest compatible published version,
// using the injected resolver when set and falling back to the live
// crates.io-backed LatestCrateVersion otherwise.
func (ra *RustAnalyzer) latestVersion(ctx context.Context, spec string) (string, error) {
	if ra.resolveLatest != nil {
		return ra.resolveLatest(ctx, spec)
	}
	return LatestCrateVersion(ctx, spec)
}

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
	file, err := os.Open(filepath.Clean(cargoLockPath))
	if err != nil {
		return nil, fmt.Errorf("failed to open Cargo.lock: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			log.Warnf("failed to close Cargo.lock: %v", closeErr)
		}
	}()

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

	// Partition packages into the project's own crates and its direct
	// dependencies. A direct dependency is one listed in a local crate's
	// dependencies array (i.e. declared in Cargo.toml); everything else pulled
	// in through the graph is indirect.
	direct, roots := classifyDependencies(cargoPackages)

	// Analyze packages
	var directCount, indirectCount int
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

		// Use name@version as key to handle multiple versions
		key := fmt.Sprintf("%s@%s", pkg.Name, pkg.Version)

		// Classify direct vs indirect. The project's own crate(s) are neither -
		// they are flagged root and excluded from the tallies below.
		switch {
		case roots[key]:
			info.Metadata["root"] = true
		case direct[key]:
			directCount++
		default:
			info.Transitive = true
			info.Metadata["indirect"] = true
			indirectCount++
		}

		// Track multiple versions of same package
		packageVersions[pkg.Name] = append(packageVersions[pkg.Name], pkg.Version)

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
	result.Metadata["directCount"] = directCount
	result.Metadata["indirectCount"] = indirectCount

	log.Infof("Analysis complete: found %d packages (%d unique, %d direct, %d indirect)",
		len(cargoPackages), len(packageVersions), directCount, indirectCount)

	return result, nil
}

// AnalyzeRemote performs dependency analysis on remotely-fetched Rust files.
// Not yet implemented for Rust - returns error.
// TODO: Implement this function and use ctx for logging and files for analysis.
//
//nolint:revive // Parameters will be used when implementation is added
func (ra *RustAnalyzer) AnalyzeRemote(ctx context.Context, files map[string][]byte) (*analyzer.RemoteAnalysisResult, error) {
	return nil, fmt.Errorf("%w for Rust", ErrRemoteAnalysisNotImplemented)
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
		// Build a target spec from the incoming dependency, which arrives in one
		// of three shapes:
		//   {Name: "serde", Version: ""}           bare crate -> latest published
		//   {Name: "serde", Version: "1.2.0"}       crate + version constraint (caret line)
		//   {Name: "serde@1.0.0", Version: "1.1.0"} precise pin of an already-pinned dep
		spec := dep.Name
		switch {
		case strings.Contains(dep.Name, "@") && dep.Version != "":
			spec += "=" + dep.Version // precise pin: name@from=to
		case dep.Version != "":
			spec += "@" + dep.Version // version constraint: name@version
		}
		target := parseTarget(spec)

		// Confirm the crate is present in the lockfile, matched by base name. The
		// analysis map is keyed name@version, so collect every locked version.
		found := false
		var lockedVersions []string
		for _, depInfo := range analysis.Dependencies {
			if depInfo.Name == target.name {
				found = true
				lockedVersions = append(lockedVersions, depInfo.Version)
			}
		}
		if !found {
			strategy.Warnings = append(strategy.Warnings,
				fmt.Sprintf("Dependency %s not found in Cargo.lock", target.name))
			continue
		}

		// Resolve the version to update to. A precise pin already names its exact
		// target; otherwise ask the index for the latest version within the
		// constraint. A resolver failure (crate missing upstream, no compatible
		// version, network error) warns and skips this dependency rather than
		// aborting the whole strategy.
		targetVersion := target.precise
		if !target.isPrecise {
			resolved, err := ra.latestVersion(ctx, spec)
			if err != nil {
				strategy.Warnings = append(strategy.Warnings,
					fmt.Sprintf("Could not resolve latest version for %s: %v", target.name, err))
				continue
			}
			targetVersion = resolved
		}

		// A bare-name target updates every locked version, so warn when more than
		// one is present. A version-constrained or precise target acts on a single
		// line and never triggers this warning.
		if !target.hasVersion && len(lockedVersions) > 1 {
			strategy.Warnings = append(strategy.Warnings,
				fmt.Sprintf("Multiple versions of %s found: %v - all will be updated", target.name, lockedVersions))
		}

		// Record the version we're upgrading from for reporting: the locked
		// version in the request's compatible line, falling back to the highest
		// locked version (a cross-line bump) so output never mislabels an existing
		// crate as new.
		lineVersion := target.version
		if lineVersion == "" {
			lineVersion = targetVersion
		}
		fromVersion := inLineVersion(lineVersion, lockedVersions)
		if fromVersion == "" {
			fromVersion = maxVersion(lockedVersions)
		}

		md := make(map[string]any, len(dep.Metadata)+1)
		for k, v := range dep.Metadata {
			md[k] = v
		}
		if fromVersion != "" {
			md[analyzer.FromVersionMetadataKey] = fromVersion
		}

		strategy.DirectUpdates = append(strategy.DirectUpdates, analyzer.Dependency{
			Name:     target.name,
			Version:  targetVersion,
			Scope:    dep.Scope,
			Type:     dep.Type,
			Metadata: md,
		})
		log.Infof("Will update %s from %s to %s", target.name, fromVersion, targetVersion)
	}

	log.Infof("Strategy: %d direct updates", len(strategy.DirectUpdates))
	return strategy, nil
}
