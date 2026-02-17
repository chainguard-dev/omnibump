// go:build integration
//go:build integration

/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package golang

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/chainguard-dev/omnibump/pkg/remote"
	"github.com/stretchr/testify/require"
)

// Integration tests for remote Go module analysis
// Run with: go test -tags=integration ./pkg/languages/golang/... -run Remote
// Requires: GITHUB_TOKEN environment variable

// Simple GitHub client for tests
type testGitHubClient struct {
	token  string
	client *http.Client
}

func newTestGitHubClient(token string) *testGitHubClient {
	return &testGitHubClient{
		token:  token,
		client: &http.Client{},
	}
}

func (c *testGitHubClient) SearchFiles(ctx context.Context, owner, repo, pattern string) ([]string, error) {
	url := fmt.Sprintf("https://api.github.com/search/code?q=filename:%s+repo:%s/%s", pattern, owner, repo)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API error: %d - %s", resp.StatusCode, string(body))
	}

	var result struct {
		Items []struct {
			Path string `json:"path"`
		} `json:"items"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(result.Items))
	for _, item := range result.Items {
		paths = append(paths, item.Path)
	}

	return paths, nil
}

func (c *testGitHubClient) GetFileContent(ctx context.Context, owner, repo, path, ref string) ([]byte, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s?ref=%s", owner, repo, path, ref)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API error: %d", resp.StatusCode)
	}

	var result struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if result.Encoding != "base64" {
		return nil, fmt.Errorf("unexpected encoding: %s", result.Encoding)
	}

	return base64.StdEncoding.DecodeString(result.Content)
}

func setupRemoteTest(t *testing.T) *remote.GitHubFetcher {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		t.Skip("GITHUB_TOKEN not set, skipping integration test")
	}

	client := newTestGitHubClient(token)
	return remote.NewGitHubFetcher(client)
}

func TestRemoteAnalysis_SpireServer(t *testing.T) {
	ctx := context.Background()
	fetcher := setupRemoteTest(t)

	// Fetch go.mod files from spiffe/spire
	repo := remote.RepositoryRef{
		Owner: "spiffe",
		Repo:  "spire",
		Ref:   "v1.10.3",
	}

	files, err := fetcher.SearchFiles(ctx, repo, []string{"go.mod"})
	require.NoError(t, err)
	require.NotEmpty(t, files)

	// Convert to map for AnalyzeRemote
	fileMap := make(map[string][]byte)
	for _, f := range files {
		fileMap[f.Path] = f.Content
	}

	// Analyze with omnibump
	analyzer := &GolangAnalyzer{}
	result, err := analyzer.AnalyzeRemote(ctx, fileMap)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "go", result.Language)
	require.NotEmpty(t, result.FileAnalyses)

	// Find root go.mod analysis
	var rootAnalysis *FileAnalysis
	for i := range result.FileAnalyses {
		if result.FileAnalyses[i].FilePath == "go.mod" {
			rootAnalysis = &result.FileAnalyses[i]
			break
		}
	}

	require.NotNil(t, rootAnalysis, "should have analyzed root go.mod")

	// Verify expected dependencies exist (based on PR 22139)
	expectedDeps := []string{
		"github.com/sigstore/cosign/v2",
		"github.com/theupdateframework/go-tuf/v2",
	}

	for _, dep := range expectedDeps {
		depInfo, exists := rootAnalysis.Analysis.Dependencies[dep]
		require.True(t, exists, "expected dependency %s to exist", dep)
		require.NotEmpty(t, depInfo.Version)
		t.Logf("Found %s at version %s", dep, depInfo.Version)
	}

	t.Logf("Analyzed %d files with %d total dependencies", len(result.FileAnalyses), len(rootAnalysis.Analysis.Dependencies))
}

func TestRemoteAnalysis_AmazonSSMAgent(t *testing.T) {
	ctx := context.Background()
	fetcher := setupRemoteTest(t)

	repo := remote.RepositoryRef{
		Owner: "aws",
		Repo:  "amazon-ssm-agent",
		Ref:   "3.3.3270.0",
	}

	files, err := fetcher.SearchFiles(ctx, repo, []string{"go.mod"})
	require.NoError(t, err)
	require.NotEmpty(t, files)

	fileMap := make(map[string][]byte)
	for _, f := range files {
		fileMap[f.Path] = f.Content
	}

	analyzer := &GolangAnalyzer{}
	result, err := analyzer.AnalyzeRemote(ctx, fileMap)
	require.NoError(t, err)

	// Check if golang.org/x/crypto exists (CVE remediation scenario)
	foundCrypto := false
	for _, fa := range result.FileAnalyses {
		if depInfo, exists := fa.Analysis.Dependencies["golang.org/x/crypto"]; exists {
			foundCrypto = true
			t.Logf("Found golang.org/x/crypto at version %s in %s", depInfo.Version, fa.FilePath)
			require.NotEmpty(t, depInfo.Version)
		}
	}

	require.True(t, foundCrypto, "amazon-ssm-agent should have golang.org/x/crypto dependency")
}

func TestRemoteAnalysis_ConsulWithReplace(t *testing.T) {
	ctx := context.Background()
	fetcher := setupRemoteTest(t)

	repo := remote.RepositoryRef{
		Owner: "hashicorp",
		Repo:  "consul",
		Ref:   "v1.20.6",
	}

	files, err := fetcher.SearchFiles(ctx, repo, []string{"go.mod"})
	require.NoError(t, err)
	require.NotEmpty(t, files)

	fileMap := make(map[string][]byte)
	for _, f := range files {
		fileMap[f.Path] = f.Content
	}

	analyzer := &GolangAnalyzer{}
	result, err := analyzer.AnalyzeRemote(ctx, fileMap)
	require.NoError(t, err)

	// Check for replaced dependencies
	totalReplaced := 0
	for _, fa := range result.FileAnalyses {
		for depName, depInfo := range fa.Analysis.Dependencies {
			if depInfo.UpdateStrategy == "replace" {
				totalReplaced++
				t.Logf("Found replaced dependency: %s in %s", depName, fa.FilePath)

				replaced, ok := depInfo.Metadata["replaced"].(bool)
				require.True(t, ok)
				require.True(t, replaced)
			}
		}
	}

	t.Logf("Found %d total replaced dependencies across all files", totalReplaced)
	require.Greater(t, totalReplaced, 0, "consul should have replace directives")
}

func TestRemoteAnalysis_KubernetesMultiModule(t *testing.T) {
	ctx := context.Background()
	fetcher := setupRemoteTest(t)

	repo := remote.RepositoryRef{
		Owner: "kubernetes",
		Repo:  "kubernetes",
		Ref:   "v1.32.0",
	}

	files, err := fetcher.SearchFiles(ctx, repo, []string{"go.mod"})
	require.NoError(t, err)
	require.NotEmpty(t, files)

	t.Logf("Found %d go.mod files in kubernetes", len(files))
	require.Greater(t, len(files), 1, "kubernetes should have multiple go.mod files")

	fileMap := make(map[string][]byte)
	for _, f := range files {
		fileMap[f.Path] = f.Content
	}

	analyzer := &GolangAnalyzer{}
	result, err := analyzer.AnalyzeRemote(ctx, fileMap)
	require.NoError(t, err)
	require.NotEmpty(t, result.FileAnalyses)

	// Verify we analyzed multiple modules
	t.Logf("Successfully analyzed %d modules", len(result.FileAnalyses))
	require.Greater(t, len(result.FileAnalyses), 1, "should analyze multiple modules")

	// Each module should have dependencies
	for _, fa := range result.FileAnalyses {
		t.Logf("  %s: %d dependencies", fa.FilePath, len(fa.Analysis.Dependencies))
		require.NotEmpty(t, fa.Analysis.Dependencies, "module %s should have dependencies", fa.FilePath)
	}
}

func TestRemoteAnalysis_CheckDependencyVersion(t *testing.T) {
	ctx := context.Background()
	fetcher := setupRemoteTest(t)

	// Scenario: Check current version of a dependency for CVE remediation
	repo := remote.RepositoryRef{
		Owner: "spiffe",
		Repo:  "spire",
		Ref:   "v1.10.3",
	}

	files, err := fetcher.SearchFiles(ctx, repo, []string{"go.mod"})
	require.NoError(t, err)

	fileMap := make(map[string][]byte)
	for _, f := range files {
		fileMap[f.Path] = f.Content
	}

	analyzer := &GolangAnalyzer{}
	result, err := analyzer.AnalyzeRemote(ctx, fileMap)
	require.NoError(t, err)

	// Check which files contain sigstore/cosign
	targetDep := "github.com/sigstore/cosign/v2"

	for _, fa := range result.FileAnalyses {
		if depInfo, exists := fa.Analysis.Dependencies[targetDep]; exists {
			t.Logf("File: %s", fa.FilePath)
			t.Logf("  Current version: %s", depInfo.Version)
			t.Logf("  Update strategy: %s", depInfo.UpdateStrategy)
			t.Logf("  Transitive: %t", depInfo.Transitive)

			// Simulate CVE remediation check
			targetVersion := "v2.6.2"
			t.Logf("  Target version for CVE fix: %s", targetVersion)

			// This file would need to be updated if current < target
			require.NotEmpty(t, depInfo.Version)
		}
	}
}
