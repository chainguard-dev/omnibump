/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package omnibump

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/chainguard-dev/clog"
	"github.com/ghodss/yaml"
	"github.com/google/go-github/v75/github"
	"github.com/spf13/cobra"

	"github.com/chainguard-dev/omnibump/pkg/analyzer"
	"github.com/chainguard-dev/omnibump/pkg/config"
	"github.com/chainguard-dev/omnibump/pkg/languages/golang"
	"github.com/chainguard-dev/omnibump/pkg/remote"
)

type analyzeRemoteFlags struct {
	ref          string
	language     string
	outputFormat string
	githubToken  string
	depsFile     string
	packages     string
	manifestFile string
}

var analyzeRemoteF analyzeRemoteFlags

func analyzeRemoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "analyze-remote <repo-url>",
		Short: "Analyze a remote repository's dependency structure without cloning",
		Long: `Analyze a remote repository to understand how dependencies are defined.
Uses GitHub API to fetch dependency manifest files without requiring a full clone.

Supports Go projects with automatic manifest file discovery.

Requires a GitHub token (use --github-token flag or set GITHUB_TOKEN environment variable).

Examples:
  # Analyze spiffe/spire at tag v1.10.3
  omnibump analyze-remote https://github.com/spiffe/spire --ref v1.10.3

  # Parse ref from URL
  omnibump analyze-remote https://github.com/spiffe/spire/tree/v1.10.3

  # Check if a package needs updating
  omnibump analyze-remote https://github.com/rancher/rke2 --ref v1.35.0+rke2r1 \
    --packages "golang.org/x/crypto@v0.47.0"

  # Use custom manifest filename (e.g., vendor.mod)
  omnibump analyze-remote https://github.com/moby/moby --ref v28.5.2 \
    --manifest-file vendor.mod

  # Analyze and output as JSON
  omnibump analyze-remote https://github.com/aws/amazon-ssm-agent --ref 3.3.3270.0 --output json`,
		Args: cobra.ExactArgs(1),
		RunE: runAnalyzeRemote,
	}

	f := cmd.Flags()
	f.StringVar(&analyzeRemoteF.ref, "ref", "", "git reference (tag, branch, or commit)")
	f.StringVarP(&analyzeRemoteF.language, "language", "l", "go", "language to analyze (currently only 'go' supported)")
	f.StringVar(&analyzeRemoteF.outputFormat, "output", "text", "output format (text, json, yaml)")
	f.StringVar(&analyzeRemoteF.githubToken, "github-token", "", "GitHub token (default: $GITHUB_TOKEN)")
	f.StringVar(&analyzeRemoteF.depsFile, "deps", "", "dependencies file to analyze strategy for")
	f.StringVar(&analyzeRemoteF.packages, "packages", "", "inline package list to analyze (e.g., 'golang.org/x/crypto@v0.47.0')")
	f.StringVar(&analyzeRemoteF.manifestFile, "manifest-file", "", "custom manifest filename to search for (e.g., 'vendor.mod', 'custom.mod')")

	return cmd
}

func runAnalyzeRemote(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	log := clog.FromContext(ctx)

	repoURL := args[0]

	// Parse GitHub URL
	owner, repo, urlRef, err := parseGitHubURL(repoURL)
	if err != nil {
		return fmt.Errorf("failed to parse repository URL: %w", err)
	}

	// Use ref from URL if not specified via flag
	ref := analyzeRemoteF.ref
	if ref == "" && urlRef != "" {
		ref = urlRef
	}

	if ref == "" {
		return fmt.Errorf("git reference required: use --ref flag or include in URL (e.g., /tree/v1.0.0)")
	}

	// Get GitHub token (required for Code Search API)
	token := analyzeRemoteF.githubToken
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token == "" {
		return fmt.Errorf("GitHub token required: set GITHUB_TOKEN environment variable (e.g., export GITHUB_TOKEN=$(gh auth token)) or use --github-token flag")
	}

	// Create GitHub client and fetcher
	githubClient := newGitHubClient(token)
	fetcher := remote.NewGitHubFetcher(githubClient)

	// Create repository reference
	repoRef := remote.RepositoryRef{
		Owner: owner,
		Repo:  repo,
		Ref:   ref,
	}

	log.Infof("Analyzing remote repository: %s/%s@%s", repoRef.Owner, repoRef.Repo, repoRef.Ref)

	// Determine manifest patterns based on language
	var manifestPatterns []string
	var projectAnalyzer analyzer.Analyzer

	switch analyzeRemoteF.language {
	case "go":
		// If custom manifest file specified, use only that
		if analyzeRemoteF.manifestFile != "" {
			manifestPatterns = []string{analyzeRemoteF.manifestFile}
			log.Infof("Using custom manifest file: %s", analyzeRemoteF.manifestFile)
		} else {
			// Search for both standard and alternative Go module files
			manifestPatterns = []string{"go.mod", "vendor.mod"}
		}
		projectAnalyzer = &golang.GolangAnalyzer{}
	default:
		return fmt.Errorf("remote analysis not yet implemented for language: %s", analyzeRemoteF.language)
	}

	// Search for manifest files with fallback
	files, err := searchManifestFilesWithFallback(ctx, fetcher, repoRef, manifestPatterns, analyzeRemoteF.language)
	if err != nil {
		return err
	}

	log.Infof("Found %d manifest file(s)", len(files))

	// Convert to map for analyzer
	fileMap := make(map[string][]byte)
	for _, f := range files {
		fileMap[f.Path] = f.Content
		log.Debugf("  - %s (%d bytes)", f.Path, len(f.Content))
	}

	// Perform remote analysis
	result, err := projectAnalyzer.AnalyzeRemote(ctx, fileMap)
	if err != nil {
		return fmt.Errorf("remote analysis failed: %w", err)
	}

	log.Infof("Analysis complete: analyzed %d file(s)", len(result.FileAnalyses))

	// If packages are provided, recommend update strategy
	var strategies map[string]*analyzer.Strategy
	if analyzeRemoteF.depsFile != "" || analyzeRemoteF.packages != "" {
		// Load dependencies
		var deps []analyzer.Dependency
		if analyzeRemoteF.depsFile != "" {
			cfg, err := config.LoadConfig(ctx, analyzeRemoteF.depsFile)
			if err != nil {
				return fmt.Errorf("failed to load deps file: %w", err)
			}
			deps = convertPackagesToAnalyzerDeps(cfg.Packages)
		} else {
			packages, err := config.ParseInlinePackages(analyzeRemoteF.packages)
			if err != nil {
				return fmt.Errorf("failed to parse packages: %w", err)
			}
			deps = convertPackagesToAnalyzerDeps(packages)
		}

		log.Infof("Checking %d package(s) for update recommendations", len(deps))

		// Get strategy recommendation for each file
		strategies = make(map[string]*analyzer.Strategy)
		for _, fa := range result.FileAnalyses {
			strategy, err := projectAnalyzer.RecommendStrategy(ctx, fa.Analysis, deps)
			if err != nil {
				log.Warnf("Failed to recommend strategy for %s: %v", fa.FilePath, err)
				continue
			}
			strategies[fa.FilePath] = strategy
		}
	}

	// Output results
	if err := outputRemoteAnalysisResults(result, strategies, analyzeRemoteF.outputFormat); err != nil {
		return err
	}

	return nil
}

// searchManifestFilesWithFallback searches for manifest files with prioritization
// For Go, it searches for go.mod and vendor.mod. If both exist in the same directory,
// go.mod is prioritized.
func searchManifestFilesWithFallback(ctx context.Context, fetcher *remote.GitHubFetcher, repoRef remote.RepositoryRef, patterns []string, language string) ([]remote.RemoteFile, error) {
	log := clog.FromContext(ctx)

	// Search for all patterns
	log.Infof("Searching for manifest files: %v", patterns)
	files, err := fetcher.SearchFiles(ctx, repoRef, patterns)
	if err != nil {
		return nil, fmt.Errorf("failed to search files: %w", err)
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no manifest files found for language: %s (tried: %v)", language, patterns)
	}

	// For Go projects, prioritize go.mod over vendor.mod when both exist in same directory
	if language == "go" && len(files) > 1 {
		files = prioritizeGoModFiles(files)
	}

	log.Infof("Found %d manifest file(s)", len(files))
	return files, nil
}

// prioritizeGoModFiles filters files to prefer go.mod over vendor.mod in the same directory
func prioritizeGoModFiles(files []remote.RemoteFile) []remote.RemoteFile {
	// Build a map of directory -> files
	dirFiles := make(map[string][]remote.RemoteFile)
	for _, f := range files {
		dir := filepath.Dir(f.Path)
		dirFiles[dir] = append(dirFiles[dir], f)
	}

	// For each directory, if both go.mod and vendor.mod exist, keep only go.mod
	var result []remote.RemoteFile
	for _, filesInDir := range dirFiles {
		if len(filesInDir) == 1 {
			result = append(result, filesInDir[0])
			continue
		}

		// Check if both go.mod and vendor.mod exist
		hasGoMod := false
		hasVendorMod := false
		var goModFile remote.RemoteFile

		for _, f := range filesInDir {
			base := filepath.Base(f.Path)
			switch base {
			case "go.mod":
				hasGoMod = true
				goModFile = f
			case "vendor.mod":
				hasVendorMod = true
			}
		}

		// If both exist, prefer go.mod
		if hasGoMod && hasVendorMod {
			result = append(result, goModFile)
		} else {
			// Add all files from this directory
			result = append(result, filesInDir...)
		}
	}

	return result
}

// parseGitHubURL parses a GitHub URL and returns owner, repo, and optional ref
// Supports formats:
//   - https://github.com/owner/repo
//   - https://github.com/owner/repo.git
//   - https://github.com/owner/repo/tree/ref
//   - github.com/owner/repo
func parseGitHubURL(url string) (owner, repo, ref string, err error) {
	// Remove protocol
	url = strings.TrimPrefix(url, "https://")
	url = strings.TrimPrefix(url, "http://")
	url = strings.TrimPrefix(url, "git://")

	// Remove trailing .git
	url = strings.TrimSuffix(url, ".git")

	// Match github.com/owner/repo or github.com/owner/repo/tree/ref
	re := regexp.MustCompile(`^github\.com/([^/]+)/([^/]+)(?:/tree/(.+))?$`)
	matches := re.FindStringSubmatch(url)

	if len(matches) < 3 {
		return "", "", "", fmt.Errorf("invalid GitHub URL format: %s", url)
	}

	owner = matches[1]
	repo = matches[2]
	if len(matches) > 3 {
		ref = matches[3]
	}

	return owner, repo, ref, nil
}

func outputRemoteAnalysisResults(result *analyzer.RemoteAnalysisResult, strategies map[string]*analyzer.Strategy, format string) error {
	switch format {
	case "json":
		return outputRemoteJSON(result, strategies)
	case "yaml":
		return outputRemoteYAML(result, strategies)
	case "text":
		return outputRemoteText(result, strategies)
	default:
		return fmt.Errorf("unsupported output format: %s", format)
	}
}

func outputRemoteText(result *analyzer.RemoteAnalysisResult, strategies map[string]*analyzer.Strategy) error {
	fmt.Println()
	fmt.Println("Remote Dependency Analysis")
	fmt.Println("==========================")
	fmt.Println()

	fmt.Printf("Language: %s\n", result.Language)
	fmt.Printf("Files analyzed: %d\n", len(result.FileAnalyses))
	fmt.Println()

	for i, fa := range result.FileAnalyses {
		fmt.Printf("File %d: %s\n", i+1, fa.FilePath)
		fmt.Println("-------------------")

		analysis := fa.Analysis
		fmt.Printf("  Total dependencies: %d\n", len(analysis.Dependencies))

		// Count direct vs indirect
		directCount := 0
		indirectCount := 0
		replacedCount := 0
		for _, dep := range analysis.Dependencies {
			if dep.UpdateStrategy == "replace" {
				replacedCount++
			}
			if dep.Transitive {
				indirectCount++
			} else {
				directCount++
			}
		}

		fmt.Printf("  Direct: %d, Indirect: %d, Replaced: %d\n", directCount, indirectCount, replacedCount)

		// Show Go version if available
		if goVer, ok := analysis.Metadata["goVersion"].(string); ok {
			fmt.Printf("  Go version: %s\n", goVer)
		}

		fmt.Println()

		// Show strategy if provided
		if strategies != nil {
			if strategy, ok := strategies[fa.FilePath]; ok {
				fmt.Println("  Update Strategy:")
				fmt.Println("  ----------------")

				if len(strategy.DirectUpdates) > 0 {
					fmt.Println("  Direct Updates:")
					for _, dep := range strategy.DirectUpdates {
						if depInfo, exists := analysis.Dependencies[dep.Name]; exists {
							fmt.Printf("    %s: %s -> %s\n", dep.Name, depInfo.Version, dep.Version)
						} else {
							fmt.Printf("    %s: (not found) -> %s\n", dep.Name, dep.Version)
						}
					}
				}

				if len(strategy.PropertyUpdates) > 0 {
					fmt.Println("  Property Updates:")
					for prop, version := range strategy.PropertyUpdates {
						currentValue := analysis.Properties[prop]
						if currentValue != "" {
							fmt.Printf("    %s: %s -> %s\n", prop, currentValue, version)
						} else {
							fmt.Printf("    %s: (new) -> %s\n", prop, version)
						}
					}
				}

				if len(strategy.Warnings) > 0 {
					fmt.Println("  Warnings:")
					for _, warning := range strategy.Warnings {
						fmt.Printf("    ⚠ %s\n", warning)
					}
				}
				fmt.Println()
			}
		}

		// Show some key dependencies (if no strategy shown)
		if strategies == nil && len(analysis.Dependencies) > 0 {
			fmt.Println("  Key Dependencies:")
			count := 0
			for name, dep := range analysis.Dependencies {
				if count >= 10 {
					fmt.Printf("  ... and %d more\n", len(analysis.Dependencies)-10)
					break
				}
				flags := ""
				if dep.Transitive {
					flags += " (indirect)"
				}
				if dep.UpdateStrategy == "replace" {
					if replacedWith, ok := dep.Metadata["replacedWith"].(string); ok {
						flags += fmt.Sprintf(" [replaced with %s]", replacedWith)
					}
				}
				fmt.Printf("    - %s@%s%s\n", name, dep.Version, flags)
				count++
			}
			fmt.Println()
		}
	}

	// Summary
	totalDeps := 0
	for _, fa := range result.FileAnalyses {
		totalDeps += len(fa.Analysis.Dependencies)
	}
	fmt.Printf("Summary: %d files, %d total dependencies\n", len(result.FileAnalyses), totalDeps)

	return nil
}

func outputRemoteJSON(result *analyzer.RemoteAnalysisResult, strategies map[string]*analyzer.Strategy) error {
	output := map[string]any{
		"analysis": result,
	}
	if len(strategies) > 0 {
		output["strategies"] = strategies
	}

	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

func outputRemoteYAML(result *analyzer.RemoteAnalysisResult, strategies map[string]*analyzer.Strategy) error {
	output := map[string]any{
		"analysis": result,
	}
	if len(strategies) > 0 {
		output["strategies"] = strategies
	}

	data, err := yaml.Marshal(output)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

// Simple GitHub client for remote analysis
type githubClient struct {
	client *github.Client
}

func newGitHubClient(token string) *githubClient {
	var httpClient *http.Client
	if token != "" {
		httpClient = &http.Client{
			Transport: &tokenTransport{token: token},
		}
	} else {
		httpClient = &http.Client{}
	}

	return &githubClient{
		client: github.NewClient(httpClient),
	}
}

// tokenTransport adds Authorization header to requests
type tokenTransport struct {
	token string
}

func (t *tokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Only add Authorization header for GitHub API requests to prevent
	// token leakage via cross-host redirects
	if req.URL.Host == "api.github.com" {
		req.Header.Set("Authorization", "Bearer "+t.token)
	}
	return http.DefaultTransport.RoundTrip(req)
}

func (c *githubClient) SearchFiles(ctx context.Context, owner, repo, pattern string) ([]string, error) {
	query := fmt.Sprintf("filename:%s repo:%s/%s", pattern, owner, repo)

	result, _, err := c.client.Search.Code(ctx, query, &github.SearchOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	})
	if err != nil {
		return nil, fmt.Errorf("code search failed: %w", err)
	}

	paths := make([]string, 0, len(result.CodeResults))
	for _, item := range result.CodeResults {
		if item.Path != nil {
			paths = append(paths, *item.Path)
		}
	}

	return paths, nil
}

func (c *githubClient) GetFileContent(ctx context.Context, owner, repo, path, ref string) ([]byte, error) {
	opts := &github.RepositoryContentGetOptions{
		Ref: ref,
	}

	fileContent, _, _, err := c.client.Repositories.GetContents(ctx, owner, repo, path, opts)
	if err != nil {
		return nil, fmt.Errorf("get file content failed: %w", err)
	}

	if fileContent == nil {
		return nil, fmt.Errorf("file content is nil")
	}

	content, err := fileContent.GetContent()
	if err != nil {
		return nil, fmt.Errorf("failed to decode content: %w", err)
	}

	return []byte(content), nil
}
