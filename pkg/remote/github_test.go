/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package remote

import (
	"context"
	"fmt"
	"testing"
)

// mockGitHubClient is a test double for GitHubSearcher that serves files from
// a fixed tree listing.
type mockGitHubClient struct {
	// treePaths is what ListFilePaths returns (the files at the ref).
	treePaths []string

	// fetchedPaths records every path requested via GetFileContent.
	fetchedPaths []string
}

// SearchFiles implements GitHubSearcher. Discovery no longer uses Code Search,
// so this must never be called.
func (m *mockGitHubClient) SearchFiles(_ context.Context, _, _, _ string) ([]string, error) {
	return nil, fmt.Errorf("unexpected Code Search call: discovery must use ListFilePaths")
}

func (m *mockGitHubClient) GetFileContent(_ context.Context, _, _, path, _ string) ([]byte, error) {
	m.fetchedPaths = append(m.fetchedPaths, path)
	for _, p := range m.treePaths {
		if p == path {
			return []byte("content of " + path), nil
		}
	}
	return nil, fmt.Errorf("get file content failed: 404 Not Found: %s", path)
}

func (m *mockGitHubClient) ListFilePaths(_ context.Context, _, _, _ string) ([]string, error) {
	return m.treePaths, nil
}

func testRepoRef() RepositoryRef {
	return RepositoryRef{Owner: "apache", Repo: "tika", Ref: "3.1.0"}
}

func TestGitHubFetcherSearchFiles_OnlyFilesAtRef(t *testing.T) {
	client := &mockGitHubClient{
		treePaths: []string{
			"pom.xml",
			"module-a/pom.xml",
			"module-a/src/Main.java",
			"README.md",
			"docs/guide.md",
		},
	}
	fetcher := NewGitHubFetcher(client)

	files, err := fetcher.SearchFiles(context.Background(), testRepoRef(), []string{"pom.xml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := map[string]bool{"pom.xml": true, "module-a/pom.xml": true}
	if len(files) != len(want) {
		t.Fatalf("expected %d files, got %d", len(want), len(files))
	}
	for _, f := range files {
		if !want[f.Path] {
			t.Errorf("unexpected file returned: %s", f.Path)
		}
		if len(f.Content) == 0 {
			t.Errorf("file %s has empty content", f.Path)
		}
		if f.Metadata["ref"] != "3.1.0" {
			t.Errorf("file %s has ref %q, want %q", f.Path, f.Metadata["ref"], "3.1.0")
		}
	}

	// Regression for Code Search-based discovery: every fetched path must
	// come from the tree listing at the ref, so no fetch can 404.
	for _, p := range client.fetchedPaths {
		found := false
		for _, tp := range client.treePaths {
			if tp == p {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("fetched path %s does not exist at the ref", p)
		}
	}
}

func TestGitHubFetcherSearchFiles_MultiplePatterns(t *testing.T) {
	client := &mockGitHubClient{
		treePaths: []string{
			"pom.xml",
			"build.gradle",
			"app/build.gradle",
			"settings.gradle",
			"src/main/App.java",
		},
	}
	fetcher := NewGitHubFetcher(client)

	files, err := fetcher.SearchFiles(context.Background(), testRepoRef(), []string{"pom.xml", "build.gradle", "settings.gradle"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := make(map[string]bool, len(files))
	for _, f := range files {
		got[f.Path] = true
	}
	for _, want := range []string{"pom.xml", "build.gradle", "app/build.gradle", "settings.gradle"} {
		if !got[want] {
			t.Errorf("expected file %s in results", want)
		}
	}
	if len(files) != 4 {
		t.Errorf("expected 4 files, got %d", len(files))
	}
}

func TestGitHubFetcherSearchFiles_PathPrefixedPattern(t *testing.T) {
	client := &mockGitHubClient{
		treePaths: []string{
			"gradle/libs.versions.toml",
			"build.gradle.kts",
		},
	}
	fetcher := NewGitHubFetcher(client)

	// Patterns with a path prefix are matched on filename only.
	files, err := fetcher.SearchFiles(context.Background(), testRepoRef(), []string{"gradle/libs.versions.toml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(files) != 1 || files[0].Path != "gradle/libs.versions.toml" {
		t.Fatalf("expected exactly gradle/libs.versions.toml, got %+v", files)
	}
}

func TestGitHubFetcherSearchFiles_NoMatches(t *testing.T) {
	client := &mockGitHubClient{
		treePaths: []string{"README.md", "src/main.go"},
	}
	fetcher := NewGitHubFetcher(client)

	files, err := fetcher.SearchFiles(context.Background(), testRepoRef(), []string{"pom.xml"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected no files, got %d", len(files))
	}
	if len(client.fetchedPaths) != 0 {
		t.Errorf("expected no fetches, got %v", client.fetchedPaths)
	}
}
