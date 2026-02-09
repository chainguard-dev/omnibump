// go:build integration
//go:build integration

/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package remote

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// Simple GitHub client for integration tests
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

// SearchFiles implements GitHubSearcher
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

// GetFileContent implements GitHubSearcher
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

func setupTestClient(t *testing.T) *GitHubFetcher {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		t.Skip("GITHUB_TOKEN not set, skipping integration test")
	}

	client := newTestGitHubClient(token)
	return NewGitHubFetcher(client)
}

func TestGitHubFetcher_SearchFiles_SpireServer(t *testing.T) {
	ctx := context.Background()
	fetcher := setupTestClient(t)

	repo := RepositoryRef{
		Owner: "spiffe",
		Repo:  "spire",
		Ref:   "v1.10.3",
	}

	files, err := fetcher.SearchFiles(ctx, repo, []string{"go.mod"})
	require.NoError(t, err)
	require.NotEmpty(t, files)

	// Should find at least the root go.mod
	foundRoot := false
	for _, f := range files {
		t.Logf("Found: %s (size: %d bytes)", f.Path, len(f.Content))

		if f.Path == "go.mod" {
			foundRoot = true
			require.Contains(t, string(f.Content), "module github.com/spiffe/spire")
			require.Contains(t, string(f.Content), "require")
		}

		// All files should have content
		require.NotEmpty(t, f.Content)
		require.Equal(t, "v1.10.3", f.Metadata["ref"])
	}

	require.True(t, foundRoot, "should find root go.mod")
}

func TestGitHubFetcher_SearchFiles_Kubernetes(t *testing.T) {
	ctx := context.Background()
	fetcher := setupTestClient(t)

	repo := RepositoryRef{
		Owner: "kubernetes",
		Repo:  "kubernetes",
		Ref:   "v1.32.0",
	}

	files, err := fetcher.SearchFiles(ctx, repo, []string{"go.mod"})
	require.NoError(t, err)
	require.NotEmpty(t, files)

	// Kubernetes has multiple go.mod files
	t.Logf("Found %d go.mod files in kubernetes", len(files))
	require.Greater(t, len(files), 1, "kubernetes should have multiple go.mod files")

	foundRoot := false
	for _, f := range files {
		if f.Path == "go.mod" {
			foundRoot = true
		}
		require.NotEmpty(t, f.Content)
	}

	require.True(t, foundRoot)
}

func TestGitHubFetcher_GetFile(t *testing.T) {
	ctx := context.Background()
	fetcher := setupTestClient(t)

	repo := RepositoryRef{
		Owner: "spiffe",
		Repo:  "spire",
		Ref:   "v1.10.3",
	}

	file, err := fetcher.GetFile(ctx, repo, "go.mod")
	require.NoError(t, err)
	require.NotNil(t, file)

	require.Equal(t, "go.mod", file.Path)
	require.NotEmpty(t, file.Content)
	require.Contains(t, string(file.Content), "module github.com/spiffe/spire")
}

func TestGitHubFetcher_NonExistentFile(t *testing.T) {
	ctx := context.Background()
	fetcher := setupTestClient(t)

	repo := RepositoryRef{
		Owner: "spiffe",
		Repo:  "spire",
		Ref:   "v1.10.3",
	}

	_, err := fetcher.GetFile(ctx, repo, "nonexistent-file-12345.txt")
	require.Error(t, err)
}

func TestGitHubFetcher_MultiplePatterns(t *testing.T) {
	ctx := context.Background()
	fetcher := setupTestClient(t)

	repo := RepositoryRef{
		Owner: "spiffe",
		Repo:  "spire",
		Ref:   "v1.10.3",
	}

	// Search for both go.mod and go.sum
	files, err := fetcher.SearchFiles(ctx, repo, []string{"go.mod", "go.sum"})
	require.NoError(t, err)
	require.NotEmpty(t, files)

	foundGoMod := false
	foundGoSum := false

	for _, f := range files {
		if f.Path == "go.mod" {
			foundGoMod = true
		}
		if f.Path == "go.sum" {
			foundGoSum = true
		}
	}

	require.True(t, foundGoMod, "should find go.mod")
	require.True(t, foundGoSum, "should find go.sum")
}
