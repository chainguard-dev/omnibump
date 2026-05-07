/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package omnibump

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/analyzer"
	"github.com/chainguard-dev/omnibump/pkg/config"
	"github.com/chainguard-dev/omnibump/pkg/languages"
	"github.com/chainguard-dev/omnibump/pkg/languages/golang"
	"github.com/chainguard-dev/omnibump/pkg/languages/java"
	"github.com/chainguard-dev/omnibump/pkg/languages/php"
	"github.com/chainguard-dev/omnibump/pkg/languages/rust"
	"github.com/ghodss/yaml"
	"github.com/spf13/cobra"
)

const (
	languageAuto  = "auto"
	languageJava  = "java"
	languageMaven = "maven" // Deprecated, use java
)

var (
	// ErrAnalyzerNotAvailable is returned when an analyzer is not available for a build tool.
	ErrAnalyzerNotAvailable = errors.New("analyzer not available")

	// ErrAnalysisNotImplemented is returned when analysis is not yet implemented for a language.
	ErrAnalysisNotImplemented = errors.New("analysis not yet implemented")

	// ErrUnsupportedOutputFormat is returned when an invalid output format is specified.
	ErrUnsupportedOutputFormat = errors.New("unsupported output format")
)

type analyzeFlags struct {
	language     string
	outputFormat string
	depsFile     string
	packages     string
	outputDeps   string
	outputProps  string
	searchProps  bool
}

var analyzeF analyzeFlags

func analyzeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "analyze [project-path]",
		Short: "Analyze a project's dependency structure",
		Long: `Analyze a project to understand how dependencies are defined.
This helps determine whether to use direct dependency patches or property updates.

Supports Java (Maven), Go, and Rust projects with automatic language detection.

Examples:
  # Analyze current directory
  omnibump analyze

  # Analyze with proposed patches
  omnibump analyze --packages "io.netty@netty-codec-http@4.1.94.Final"

  # Generate patch files based on analysis
  omnibump analyze --packages "..." --output-deps deps.yaml --output-props properties.yaml`,
		Args: cobra.MaximumNArgs(1),
		RunE: runAnalyze,
	}

	f := cmd.Flags()
	f.StringVarP(&analyzeF.language, "language", "l", "auto", "language to analyze (auto, java, go, rust, or deprecated: maven)")
	f.StringVar(&analyzeF.outputFormat, "output", "text", "output format (text, json, yaml)")
	f.StringVar(&analyzeF.depsFile, "deps", "", "dependencies file to analyze strategy for")
	f.StringVar(&analyzeF.packages, "packages", "", "inline package list to analyze")
	f.StringVar(&analyzeF.outputDeps, "output-deps", "", "write recommended dependency patches to this file")
	f.StringVar(&analyzeF.outputProps, "output-props", "", "write recommended property patches to this file")
	f.BoolVar(&analyzeF.searchProps, "search-props", false, "search for properties in entire project tree")

	return cmd
}

func runAnalyze(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	log := clog.FromContext(ctx)

	// Determine project path
	projectPath := "."
	if len(args) > 0 {
		projectPath = args[0]
	}

	// Detect language
	detectedLang := analyzeF.language

	// Handle backward compatibility: "maven" -> "java"
	if detectedLang == languageMaven {
		log.Warnf("Language 'maven' is deprecated, use 'java' instead")
		detectedLang = languageJava
	}

	if detectedLang == languageAuto || detectedLang == "" {
		var err error
		detectedLang, err = languages.DetectLanguage(ctx, projectPath)
		if err != nil {
			return fmt.Errorf("failed to detect language: %w", err)
		}
		log.Infof("Detected language: %s", detectedLang)
	}

	// Get analyzer implementation
	var projectAnalyzer analyzer.Analyzer
	switch detectedLang {
	case languageJava:
		// Get the Java language and detect build tool
		javaLang := &java.Java{}
		buildTool, err := javaLang.GetBuildTool(ctx, projectPath)
		if err != nil {
			return fmt.Errorf("failed to detect Java build tool: %w", err)
		}
		projectAnalyzer = buildTool.GetAnalyzer()
		if projectAnalyzer == nil {
			return fmt.Errorf("%w for build tool: %s", ErrAnalyzerNotAvailable, buildTool.Name())
		}
	case "go":
		projectAnalyzer = &golang.GolangAnalyzer{}
	case "rust":
		projectAnalyzer = &rust.RustAnalyzer{}
	case "php":
		// Get the PHP language and detect build tool
		phpLang := &php.PHP{}
		buildTool, err := phpLang.GetBuildTool(ctx, projectPath)
		if err != nil {
			return fmt.Errorf("failed to detect PHP build tool: %w", err)
		}
		projectAnalyzer = buildTool.GetAnalyzer()
		if projectAnalyzer == nil {
			return fmt.Errorf("%w for build tool: %s", ErrAnalyzerNotAvailable, buildTool.Name())
		}
	default:
		return fmt.Errorf("%w for language: %s", ErrAnalysisNotImplemented, detectedLang)
	}

	// Perform analysis
	analysis, err := projectAnalyzer.Analyze(ctx, projectPath)
	if err != nil {
		return fmt.Errorf("analysis failed: %w", err)
	}

	// If dependencies are provided, recommend strategy
	var strategy *analyzer.Strategy
	if analyzeF.depsFile != "" || analyzeF.packages != "" {
		var err error
		strategy, err = loadDepsAndRecommendStrategy(ctx, projectAnalyzer, analysis, analyzeF.depsFile, analyzeF.packages)
		if err != nil {
			return err
		}
	}

	// Output results
	if err := outputAnalysisResults(analysis, strategy, analyzeF.outputFormat); err != nil {
		return err
	}

	// Write output files if requested
	if analyzeF.outputDeps != "" && strategy != nil && len(strategy.DirectUpdates) > 0 {
		if err := writeDirectUpdatesFile(analyzeF.outputDeps, strategy.DirectUpdates); err != nil {
			return fmt.Errorf("failed to write deps file: %w", err)
		}
		fmt.Printf("\nWrote %d patches to %s\n", len(strategy.DirectUpdates), analyzeF.outputDeps)
	}

	if analyzeF.outputProps != "" && strategy != nil && len(strategy.PropertyUpdates) > 0 {
		if err := writePropertiesFile(analyzeF.outputProps, strategy.PropertyUpdates); err != nil {
			return fmt.Errorf("failed to write properties file: %w", err)
		}
		fmt.Printf("Wrote %d properties to %s\n", len(strategy.PropertyUpdates), analyzeF.outputProps)
	}

	return nil
}

func outputAnalysisResults(analysis *analyzer.AnalysisResult, strategy *analyzer.Strategy, format string) error {
	switch format {
	case "json":
		return outputJSON(analysis, strategy)
	case "yaml":
		return outputYAML(analysis, strategy)
	case "text":
		return outputText(analysis, strategy)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedOutputFormat, format)
	}
}

func outputText(analysis *analyzer.AnalysisResult, strategy *analyzer.Strategy) error {
	fmt.Println()
	fmt.Println("Dependency Analysis")
	fmt.Println("==================")
	fmt.Println()

	fmt.Printf("Language: %s\n", analysis.Language)

	// Show workspace information if applicable
	if isWorkspace, ok := analysis.Metadata["workspace"].(bool); ok && isWorkspace {
		if moduleCount, ok := analysis.Metadata["moduleCount"].(int); ok {
			fmt.Printf("Workspace: %d modules\n", moduleCount)
		}
	}

	fmt.Printf("Total dependencies: %d\n", len(analysis.Dependencies))

	// Count dependencies using properties
	usingProps := 0
	for _, dep := range analysis.Dependencies {
		if dep.UsesProperty {
			usingProps++
		}
	}
	fmt.Printf("Dependencies using properties: %d\n", usingProps)
	fmt.Printf("Properties defined: %d\n", len(analysis.Properties))
	fmt.Println()

	// Show property usage
	if len(analysis.PropertyUsage) > 0 {
		fmt.Println("Property Usage:")
		fmt.Println("---------------")
		for prop, count := range analysis.PropertyUsage {
			currentValue := analysis.Properties[prop]
			if currentValue != "" {
				fmt.Printf("  %s = %s (used by %d dependencies)\n", prop, currentValue, count)
			} else {
				fmt.Printf("  %s (used by %d dependencies) - NOT DEFINED\n", prop, count)
			}
		}
		fmt.Println()
	}

	// Show strategy if provided
	if strategy != nil {
		printUpdateStrategy(analysis, strategy)
		fmt.Printf("Summary: %d property updates, %d direct dependency updates\n",
			len(strategy.PropertyUpdates), len(strategy.DirectUpdates))
	}

	return nil
}

func outputJSON(analysis *analyzer.AnalysisResult, strategy *analyzer.Strategy) error {
	output := map[string]any{
		"analysis": analysis,
	}
	if strategy != nil {
		output["strategy"] = strategy
	}

	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

func outputYAML(analysis *analyzer.AnalysisResult, strategy *analyzer.Strategy) error {
	output := map[string]any{
		"analysis": analysis,
	}
	if strategy != nil {
		output["strategy"] = strategy
	}

	data, err := yaml.Marshal(output)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

// loadDepsAndRecommendStrategy loads dependencies and gets a strategy recommendation.
func loadDepsAndRecommendStrategy(ctx context.Context, projectAnalyzer analyzer.Analyzer, analysis *analyzer.AnalysisResult, depsFile, packages string) (*analyzer.Strategy, error) {
	var deps []analyzer.Dependency
	if depsFile != "" {
		cfg, err := config.LoadConfig(ctx, depsFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load deps file: %w", err)
		}
		deps = convertPackagesToAnalyzerDeps(cfg.Packages)
	} else {
		pkgs, err := config.ParseInlinePackages(packages)
		if err != nil {
			return nil, fmt.Errorf("failed to parse packages: %w", err)
		}
		deps = convertPackagesToAnalyzerDeps(pkgs)
	}

	// Get strategy recommendation
	strategy, err := projectAnalyzer.RecommendStrategy(ctx, analysis, deps)
	if err != nil {
		return nil, fmt.Errorf("failed to recommend strategy: %w", err)
	}
	return strategy, nil
}

func convertPackagesToAnalyzerDeps(packages []config.Package) []analyzer.Dependency {
	deps := make([]analyzer.Dependency, 0, len(packages))
	for _, pkg := range packages {
		dep := analyzer.Dependency{
			Name:     pkg.Name,
			Version:  pkg.Version,
			Scope:    pkg.Scope,
			Type:     pkg.Type,
			Metadata: make(map[string]any),
		}

		if pkg.GroupID != "" {
			dep.Metadata["groupId"] = pkg.GroupID
			dep.Metadata["artifactId"] = pkg.ArtifactID
			// For Maven, the name is groupId:artifactId
			dep.Name = fmt.Sprintf("%s:%s", pkg.GroupID, pkg.ArtifactID)
		}

		deps = append(deps, dep)
	}
	return deps
}

func writeDirectUpdatesFile(filename string, deps []analyzer.Dependency) error {
	// Convert to config.Package format
	packages := make([]config.Package, 0, len(deps))
	for _, dep := range deps {
		pkg := config.Package{
			Name:    dep.Name,
			Version: dep.Version,
			Scope:   dep.Scope,
			Type:    dep.Type,
		}

		if groupID, ok := dep.Metadata["groupId"].(string); ok {
			pkg.GroupID = groupID
		}
		if artifactID, ok := dep.Metadata["artifactId"].(string); ok {
			pkg.ArtifactID = artifactID
		}

		packages = append(packages, pkg)
	}

	cfg := config.Config{Packages: packages}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}

	return os.WriteFile(filename, data, 0o600)
}

func writePropertiesFile(filename string, properties map[string]string) error {
	props := make([]config.Property, 0, len(properties))
	for k, v := range properties {
		props = append(props, config.Property{
			Property: k,
			Value:    v,
		})
	}

	cfg := struct {
		Properties []config.Property `yaml:"properties"`
	}{Properties: props}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}

	return os.WriteFile(filename, data, 0o600)
}

// printUpdateStrategy prints the update strategy in human-readable format.
func printUpdateStrategy(analysis *analyzer.AnalysisResult, strategy *analyzer.Strategy) {
	fmt.Println("Update Strategy")
	fmt.Println("===============")
	fmt.Println()

	if len(strategy.PropertyUpdates) > 0 {
		printPropertyUpdates(analysis, strategy)
	}

	if len(strategy.DirectUpdates) > 0 {
		printDirectUpdates(analysis, strategy)
	}

	if len(strategy.Warnings) > 0 {
		printStrategyWarnings(strategy)
	}
}

// printPropertyUpdates prints property-based updates.
func printPropertyUpdates(analysis *analyzer.AnalysisResult, strategy *analyzer.Strategy) {
	fmt.Println("Property Updates:")
	fmt.Println("-----------------")
	for prop, version := range strategy.PropertyUpdates {
		currentValue := analysis.Properties[prop]
		if currentValue != "" {
			fmt.Printf("  %s: %s -> %s\n", prop, currentValue, version)
		} else {
			fmt.Printf("  %s: (new) -> %s\n", prop, version)
		}

		// Show affected dependencies
		if affected, ok := strategy.AffectedDependencies[prop]; ok && len(affected) > 0 {
			fmt.Printf("    Affects %d dependencies:\n", len(affected))
			for _, dep := range affected {
				fmt.Printf("      - %s\n", dep)
			}
		}
	}
	fmt.Println()
}

// printDirectUpdates prints direct dependency updates.
func printDirectUpdates(analysis *analyzer.AnalysisResult, strategy *analyzer.Strategy) {
	fmt.Println("Direct Dependency Updates:")
	fmt.Println("--------------------------")
	// Sort dependencies by name for consistent output
	sorted := make([]analyzer.Dependency, len(strategy.DirectUpdates))
	copy(sorted, strategy.DirectUpdates)
	slices.SortStableFunc(sorted, func(a, b analyzer.Dependency) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	for _, dep := range sorted {
		printDependencyUpdate(analysis, dep)
	}
	fmt.Println()
}

// printDependencyUpdate prints a single dependency update.
func printDependencyUpdate(analysis *analyzer.AnalysisResult, dep analyzer.Dependency) {
	depInfo, exists := analysis.Dependencies[dep.Name]
	if !exists {
		fmt.Printf("  %s: (new) -> %s\n", dep.Name, dep.Version)
		return
	}

	fmt.Printf("  %s: %s -> %s\n", dep.Name, depInfo.Version, dep.Version)
	printModuleInfo(depInfo)
}

// printModuleInfo prints module information for a dependency if available.
func printModuleInfo(depInfo *analyzer.DependencyInfo) {
	modules, ok := depInfo.Metadata["foundInModules"].([]string)
	if !ok || len(modules) == 0 {
		return
	}

	if len(modules) == 1 {
		fmt.Printf("    Found in: %s\n", modules[0])
		return
	}

	fmt.Printf("    Found in %d modules:\n", len(modules))
	for _, mod := range modules {
		fmt.Printf("      - %s\n", mod)
	}
}

// printStrategyWarnings prints strategy warnings.
func printStrategyWarnings(strategy *analyzer.Strategy) {
	fmt.Println("Warnings:")
	fmt.Println("---------")
	for _, warning := range strategy.Warnings {
		fmt.Printf("  ⚠ %s\n", warning)
	}
	fmt.Println()
}
