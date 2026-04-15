/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package golang

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/analyzer"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

const (
	updateStrategyReplace = "replace"
)

// ErrNoFilesProvided is returned when no files are provided for analysis.
var ErrNoFilesProvided = errors.New("no files provided for analysis")

// GolangAnalyzer implements the Analyzer interface for Go projects.
//
//nolint:revive // Explicit name preferred for clarity
type GolangAnalyzer struct{}

// Analyze performs dependency analysis on a Go project.
// If a go.work file is present, analyzes all modules in the workspace.
func (ga *GolangAnalyzer) Analyze(ctx context.Context, projectPath string) (*analyzer.AnalysisResult, error) {
	log := clog.FromContext(ctx)

	// Check if projectPath is a directory
	info, err := os.Stat(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat project path: %w", err)
	}

	// If it's a file, analyze just that go.mod
	if !info.IsDir() {
		return ga.analyzeSingleModule(ctx, projectPath)
	}

	// Check for go.work file
	workPath := filepath.Join(projectPath, "go.work")
	if _, err := os.Stat(workPath); err == nil {
		log.Infof("Found go.work file, analyzing all workspace modules")
		return ga.analyzeWorkspace(ctx, projectPath, workPath)
	}

	// No workspace, analyze single module
	goModPath := filepath.Join(projectPath, "go.mod")
	return ga.analyzeSingleModule(ctx, goModPath)
}

// analyzeSingleModule analyzes a single Go module.
func (ga *GolangAnalyzer) analyzeSingleModule(ctx context.Context, goModPath string) (*analyzer.AnalysisResult, error) {
	log := clog.FromContext(ctx)

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

	// Store module root
	result.Metadata["moduleRoot"] = filepath.Dir(goModPath)

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
			info.UpdateStrategy = updateStrategyReplace
		} else {
			// Create entry for replaced dependency
			info := &analyzer.DependencyInfo{
				Name:           repl.Old.Path,
				Version:        repl.Old.Version,
				UpdateStrategy: updateStrategyReplace,
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

// analyzeWorkspace analyzes all modules in a Go workspace.
func (ga *GolangAnalyzer) analyzeWorkspace(ctx context.Context, projectPath, workPath string) (*analyzer.AnalysisResult, error) {
	log := clog.FromContext(ctx)

	// Parse go.work file
	workFile, err := parseGoWork(workPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse go.work: %w", err)
	}

	// Get all module paths from workspace
	modulePaths := getWorkspaceModulePaths(workFile)
	log.Infof("Found %d modules in workspace", len(modulePaths))

	// Analyze each module and aggregate dependencies
	result := &analyzer.AnalysisResult{
		Language:      "go",
		Dependencies:  make(map[string]*analyzer.DependencyInfo),
		Properties:    make(map[string]string),
		PropertyUsage: make(map[string]int),
		Metadata:      make(map[string]any),
	}

	result.Metadata["workspace"] = true
	result.Metadata["workspacePath"] = workPath
	result.Metadata["moduleCount"] = len(modulePaths)
	result.Metadata["moduleRoot"] = projectPath

	// Track which modules contain each dependency
	depModules := make(map[string][]string)

	for _, modPath := range modulePaths {
		fullModPath := filepath.Join(projectPath, modPath, "go.mod")

		log.Debugf("Analyzing module: %s", modPath)

		modResult, err := ga.analyzeSingleModule(ctx, fullModPath)
		if err != nil {
			log.Warnf("Failed to analyze module %s: %v", modPath, err)
			continue
		}

		// Merge dependencies
		for depName, depInfo := range modResult.Dependencies {
			if existing, exists := result.Dependencies[depName]; exists {
				// Dependency exists in multiple modules - track which modules
				depModules[depName] = append(depModules[depName], modPath)

				// Keep the newer version if different
				if depInfo.Version != existing.Version {
					log.Debugf("Dependency %s has different versions: %s vs %s",
						depName, existing.Version, depInfo.Version)
				}
			} else {
				// First time seeing this dependency
				result.Dependencies[depName] = depInfo
				depModules[depName] = []string{modPath}
			}
		}
	}

	// Add module information to dependencies
	for depName, modules := range depModules {
		result.Dependencies[depName].Metadata["foundInModules"] = modules
	}

	log.Infof("Workspace analysis complete: found %d unique dependencies across %d modules",
		len(result.Dependencies), len(modulePaths))

	return result, nil
}

// parseGoWork parses a go.work file.
func parseGoWork(workPath string) (*modfile.WorkFile, error) {
	// workPath is constructed from validated directory path + "go.work"
	contents, err := os.ReadFile(workPath) //nolint:gosec // G304: workPath is from validated directory
	if err != nil {
		return nil, fmt.Errorf("failed to read go.work: %w", err)
	}

	workFile, err := modfile.ParseWork(workPath, contents, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to parse go.work: %w", err)
	}

	return workFile, nil
}

// getWorkspaceModulePaths extracts module paths from a go.work file.
func getWorkspaceModulePaths(workFile *modfile.WorkFile) []string {
	paths := make([]string, 0, len(workFile.Use))
	for _, use := range workFile.Use {
		if use != nil {
			paths = append(paths, use.Path)
		}
	}
	return paths
}

// AnalyzeFromContent performs dependency analysis on a Go project from go.mod file content.
// This is useful for analyzing remotely-fetched go.mod files without requiring a local clone.
func (ga *GolangAnalyzer) AnalyzeFromContent(ctx context.Context, filename string, content []byte) (*analyzer.AnalysisResult, error) {
	log := clog.FromContext(ctx)

	log.Debugf("Analyzing Go project from content: %s", filename)

	// Parse go.mod from content
	modFile, err := ParseGoModfileFromContent(filename, content)
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
			info.UpdateStrategy = updateStrategyReplace
		} else {
			// Create entry for replaced dependency
			info := &analyzer.DependencyInfo{
				Name:           repl.Old.Path,
				Version:        repl.Old.Version,
				UpdateStrategy: updateStrategyReplace,
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

// AnalyzeRemote performs dependency analysis on remotely-fetched go.mod files.
// For multi-module repos, this analyzes each go.mod separately and returns all results.
func (ga *GolangAnalyzer) AnalyzeRemote(ctx context.Context, files map[string][]byte) (*analyzer.RemoteAnalysisResult, error) {
	log := clog.FromContext(ctx)

	if len(files) == 0 {
		return nil, ErrNoFilesProvided
	}

	result := &analyzer.RemoteAnalysisResult{
		Language:     "go",
		FileAnalyses: make([]analyzer.FileAnalysis, 0, len(files)),
	}

	log.Infof("Analyzing %d remote go.mod files", len(files))

	// Analyze each go.mod file
	for filePath, content := range files {
		log.Debugf("Analyzing %s", filePath)

		analysis, err := ga.AnalyzeFromContent(ctx, filePath, content)
		if err != nil {
			log.Warnf("Failed to analyze %s: %v", filePath, err)
			continue
		}

		result.FileAnalyses = append(result.FileAnalyses, analyzer.FileAnalysis{
			FilePath: filePath,
			Analysis: analysis,
		})

		log.Infof("  %s: %d dependencies found", filePath, len(analysis.Dependencies))
	}

	log.Infof("Remote analysis complete: analyzed %d files", len(result.FileAnalyses))

	return result, nil
}

// RecommendStrategy suggests update strategy for Go dependencies.
// For Go, it's simpler than Maven - either direct update or replace directive.
// For indirect dependencies, uses ResolveIndirectDependency to find direct parents.
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

			// Check if it's an indirect dependency and try to resolve
			if depInfo.Transitive {
				ga.handleIndirectDependency(ctx, analysis, dep, strategy)
				// Note: handleIndirectDependency adds parent bump alternatives to strategy.
				// We still add the original package since the user explicitly requested it.
			}
		}

		// Default: add to direct updates
		strategy.DirectUpdates = append(strategy.DirectUpdates, dep)
		log.Infof("Will update %s to %s", dep.Name, dep.Version)
	}

	// Check transitive requirements for all packages being updated
	ga.checkTransitiveRequirementsForStrategy(ctx, analysis, strategy)

	// Deduplicate DirectUpdates — the same package can be added from multiple paths
	// (direct update, parent bump for an indirect dep, transitive co-update).
	// Keep the highest required version for each package.
	strategy.DirectUpdates = deduplicateDependencies(strategy.DirectUpdates)

	log.Infof("Strategy: %d direct updates", len(strategy.DirectUpdates))
	return strategy, nil
}

// checkTransitiveRequirementsForStrategy checks if the updates in the strategy
// require additional co-updates and adds them to DirectUpdates.
func (ga *GolangAnalyzer) checkTransitiveRequirementsForStrategy(
	ctx context.Context,
	analysis *analyzer.AnalysisResult,
	strategy *analyzer.Strategy,
) {
	log := clog.FromContext(ctx)

	// Get module root from analysis
	modRoot := "."
	if rootPath, ok := analysis.Metadata["moduleRoot"].(string); ok {
		modRoot = rootPath
	}

	// Parse go.mod (if moduleRoot is set and file exists)
	modFilePath := filepath.Join(modRoot, "go.mod")
	modFile, _, err := ParseGoModfile(modFilePath)
	if err != nil {
		// If we can't parse go.mod, skip transitive checking
		// This can happen in tests or when analyzing remotely
		log.Debugf("Could not parse go.mod for transitive checking: %v", err)
		return
	}

	// Build map of packages being updated
	packagesBeingUpdated := make(map[string]string)
	for _, dep := range strategy.DirectUpdates {
		packagesBeingUpdated[dep.Name] = dep.Version
	}

	// Check each package for missing transitive requirements
	allMissingDeps := make(map[string]MissingDependency)

	for _, dep := range strategy.DirectUpdates {
		missingDeps, err := CheckTransitiveRequirements(ctx, dep.Name, dep.Version, modFile)
		if err != nil {
			log.Warnf("Could not check transitive requirements for %s@%s: %v", dep.Name, dep.Version, err)
			continue
		}

		// Also check for potential API/schema incompatibilities in other packages
		apiIssues, err := CheckAPICompatibility(ctx, dep.Name, dep.Version, modFile)
		if err != nil {
			log.Debugf("Could not check API compatibility for %s@%s: %v", dep.Name, dep.Version, err)
		} else if len(apiIssues) > 0 {
			// Collect API compatibility issues for grouped warning
			for _, issue := range apiIssues {
				strategy.Warnings = append(strategy.Warnings,
					fmt.Sprintf("API Compatibility Alert - %s imports %s which is being updated to %s (may require manual version bump)",
						issue.Package, dep.Name, dep.Version))
				log.Infof("API compatibility alert for %s", issue.Package)
			}
		}

		// Only add missing deps that are NOT already being updated
		for _, missing := range missingDeps {
			// Skip if this dependency is already in the update list
			if targetVer, beingUpdated := packagesBeingUpdated[missing.Package]; beingUpdated {
				// Check if the version being updated is sufficient
				if semver.IsValid(targetVer) && semver.IsValid(missing.RequiredVersion) {
					if semver.Compare(targetVer, missing.RequiredVersion) >= 0 {
						log.Debugf("Dependency %s requirement satisfied by update to %s", missing.Package, targetVer)
						continue
					}
				}
			}

			// Add to missing deps (deduplicate, keep highest required version)
			if existing, exists := allMissingDeps[missing.Package]; exists {
				if semver.IsValid(missing.RequiredVersion) && semver.IsValid(existing.RequiredVersion) {
					if semver.Compare(missing.RequiredVersion, existing.RequiredVersion) > 0 {
						allMissingDeps[missing.Package] = missing
					}
				}
			} else {
				allMissingDeps[missing.Package] = missing
			}
		}
	}

	// Add missing dependencies to DirectUpdates, skipping no-ops (where version isn't changing)
	if len(allMissingDeps) > 0 {
		log.Infof("Found %d additional dependencies that need co-updating", len(allMissingDeps))
		for _, missing := range allMissingDeps {
			// Skip no-op updates (where required version equals current version)
			if missing.CurrentVersion == missing.RequiredVersion {
				log.Debugf("Skipping no-op update for %s (already at %s)", missing.Package, missing.CurrentVersion)
				continue
			}

			strategy.DirectUpdates = append(strategy.DirectUpdates, analyzer.Dependency{
				Name:    missing.Package,
				Version: missing.RequiredVersion,
				Metadata: map[string]any{
					"required_by": "transitive dependency check",
					"reason":      missing.Reason,
				},
			})
			strategy.Warnings = append(strategy.Warnings,
				fmt.Sprintf("Also updating %s to %s (required by other updates)", missing.Package, missing.RequiredVersion))
			log.Infof("Adding co-update: %s@%s", missing.Package, missing.RequiredVersion)
		}
	}
}

// handleIndirectDependency resolves an indirect dependency to parent bumps.
// Returns true if the dependency was handled as indirect, false otherwise.
func (ga *GolangAnalyzer) handleIndirectDependency(
	ctx context.Context,
	analysis *analyzer.AnalysisResult,
	dep analyzer.Dependency,
	strategy *analyzer.Strategy,
) bool {
	log := clog.FromContext(ctx)

	log.Info("Dependency is indirect - finding direct parent options", "dependency", dep.Name)

	// Get the module root from analysis metadata
	modRoot := "."
	if rootPath, ok := analysis.Metadata["moduleRoot"].(string); ok {
		modRoot = rootPath
	}

	// Resolve indirect dependency
	resolution, err := ResolveIndirectDependency(ctx, modRoot, dep.Name, dep.Version)
	if err != nil {
		log.Warn("Could not resolve indirect dependency", "dependency", dep.Name, "error", err)
		strategy.Warnings = append(strategy.Warnings,
			fmt.Sprintf("Dependency %s is indirect but resolution failed - will update directly", dep.Name))
		return false // Not handled, will be added as direct update
	}

	if resolution.IsIndirect && len(resolution.PossibleBumps) > 0 {
		// Found parent bump options
		ga.addParentBumpsToStrategy(ctx, dep, resolution, strategy)
		return true // Handled
	}

	// No parent fix found - allow bumping indirect
	if resolution.FallbackAllowed {
		strategy.Warnings = append(strategy.Warnings,
			fmt.Sprintf("Dependency %s is indirect with no parent fix available - will bump directly (not ideal)", dep.Name))
	}

	return false // Not handled, will be added as direct update
}

// addParentBumpsToStrategy adds parent bump options to the strategy.
func (ga *GolangAnalyzer) addParentBumpsToStrategy(
	ctx context.Context,
	originalDep analyzer.Dependency,
	resolution *IndirectResolution,
	strategy *analyzer.Strategy,
) {
	log := clog.FromContext(ctx)

	log.Info("Found parents that can provide fix", "count", len(resolution.PossibleBumps))

	// Add all possible parent bumps to strategy
	for _, parentBump := range resolution.PossibleBumps {
		log.Info("Parent bump option",
			"package", parentBump.Package,
			"from_version", parentBump.FromVersion,
			"to_version", parentBump.ToVersion,
			"brings_in", parentBump.WillBringIn,
			"brings_in_version", parentBump.WillBringInVersion)

		// Add parent bump to direct updates
		parentDep := analyzer.Dependency{
			Name:     parentBump.Package,
			Version:  parentBump.ToVersion,
			Metadata: make(map[string]any),
		}
		parentDep.Metadata["fixes_indirect"] = parentBump.WillBringIn
		parentDep.Metadata["indirect_target_version"] = parentBump.WillBringInVersion

		strategy.DirectUpdates = append(strategy.DirectUpdates, parentDep)
	}

	// Add informative warning
	if len(resolution.PossibleBumps) == 1 {
		strategy.Warnings = append(strategy.Warnings,
			fmt.Sprintf("Dependency %s is indirect - recommending bump of %s to %s instead",
				originalDep.Name,
				resolution.PossibleBumps[0].Package,
				resolution.PossibleBumps[0].ToVersion))
	} else {
		strategy.Warnings = append(strategy.Warnings,
			fmt.Sprintf("Dependency %s is indirect - found %d parent options (see direct updates)",
				originalDep.Name,
				len(resolution.PossibleBumps)))
	}
}

// deduplicateDependencies removes duplicate entries from a dependency list.
// When the same package appears more than once, the entry with the highest
// semver version is kept. Non-semver versions are kept as-is (last write wins).
func deduplicateDependencies(deps []analyzer.Dependency) []analyzer.Dependency {
	seen := make(map[string]int) // package name -> index in result
	result := make([]analyzer.Dependency, 0, len(deps))

	for _, dep := range deps {
		idx, exists := seen[dep.Name]
		if !exists {
			seen[dep.Name] = len(result)
			result = append(result, dep)
			continue
		}

		// Package already seen — keep whichever has the higher version.
		existing := result[idx]
		if semver.IsValid(dep.Version) && semver.IsValid(existing.Version) {
			if semver.Compare(dep.Version, existing.Version) > 0 {
				result[idx] = dep
			}
		}
		// For non-semver versions (pseudo-versions, etc.) keep the first occurrence.
	}

	return result
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
