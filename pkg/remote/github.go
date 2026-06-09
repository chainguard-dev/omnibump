/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package remote

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/chainguard-dev/clog"
)

// codeSearchDelay is the minimum pause between consecutive GitHub Code Search
// API calls to stay well within the 10 req/min rate limit.
const codeSearchDelay = 7 * time.Second

// GitHubSearcher defines the minimal interface needed to search and fetch files from GitHub.
// This allows omnibump to work with different GitHub client implementations.
type GitHubSearcher interface {
	// SearchFiles searches for files matching the given pattern in a repository.
	// Returns a list of file paths found.
	SearchFiles(ctx context.Context, owner, repo, pattern string) ([]string, error)

	// GetFileContent fetches the content of a file at a specific ref.
	GetFileContent(ctx context.Context, owner, repo, path, ref string) ([]byte, error)

	// ListFilePaths returns all file paths in a repository at the given ref using
	// the Git Tree API (one request, not subject to Code Search rate limits).
	// Useful for language detection before calling SearchFiles.
	ListFilePaths(ctx context.Context, owner, repo, ref string) ([]string, error)
}

// GitHubFetcher implements RemoteFetcher using the GitHub API.
type GitHubFetcher struct {
	client GitHubSearcher
}

// NewGitHubFetcher creates a new GitHub-based remote fetcher.
func NewGitHubFetcher(client GitHubSearcher) *GitHubFetcher {
	return &GitHubFetcher{
		client: client,
	}
}

// SearchFiles searches for files matching the given patterns in a repository.
// Implements RemoteFetcher.SearchFiles.
func (g *GitHubFetcher) SearchFiles(ctx context.Context, repo RepositoryRef, patterns []string) ([]RemoteFile, error) {
	if err := repo.Validate(); err != nil {
		return nil, fmt.Errorf("invalid repository reference: %w", err)
	}

	log := clog.FromContext(ctx).With("owner", repo.Owner, "repo", repo.Repo, "ref", repo.Ref)
	var allFiles []RemoteFile

	for i, pattern := range patterns {
		// Throttle between Code Search calls to stay within the 10 req/min rate limit.
		if i > 0 {
			log.Infof("Throttling code search to avoid rate limits (%s between requests)...", codeSearchDelay)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(codeSearchDelay):
			}
		}

		// GitHub Code Search matches on filename only; strip any path prefix so that
		// patterns like "gradle/libs.versions.toml" become "libs.versions.toml".
		pattern = filepath.Base(pattern)
		if err := validatePattern(pattern); err != nil {
			return nil, fmt.Errorf("invalid pattern: %w", err)
		}

		log.Debugf("Searching for pattern: %s", pattern)
		paths, err := g.client.SearchFiles(ctx, repo.Owner, repo.Repo, pattern)
		if err != nil {
			return nil, fmt.Errorf("failed to search for %s: %w", pattern, err)
		}

		log.Infof("Found %d files matching %s", len(paths), pattern)

		// Fetch and validate each file
		for _, path := range paths {
			file, err := g.fetchAndValidateFile(ctx, repo, path, pattern)
			if err != nil {
				log.Warnf("Skipping %s: %v", path, err)
				continue
			}
			if file != nil {
				allFiles = append(allFiles, *file)
			}
		}
	}

	return allFiles, nil
}

// ListFilePaths returns all file paths in a repository at the given ref using
// the Git Tree API (one request, not subject to Code Search rate limits).
// Useful for language detection before calling SearchFiles.
func (g *GitHubFetcher) ListFilePaths(ctx context.Context, repo RepositoryRef) ([]string, error) {
	if err := repo.Validate(); err != nil {
		return nil, fmt.Errorf("invalid repository reference: %w", err)
	}
	return g.client.ListFilePaths(ctx, repo.Owner, repo.Repo, repo.Ref)
}

// fetchAndValidateFile fetches a file and performs all necessary validations.
// Returns nil file if the file should be skipped (e.g., basename doesn't match).
func (g *GitHubFetcher) fetchAndValidateFile(ctx context.Context, repo RepositoryRef, path, pattern string) (*RemoteFile, error) {
	// Validate path for security
	if err := ValidatePath(path); err != nil {
		return nil, err
	}

	// Filter to exact matches (basename must match pattern)
	if filepath.Base(path) != pattern && path != pattern {
		return nil, nil //nolint:nilnil // Returning (nil, nil) to skip file is appropriate here
	}

	// Fetch content
	content, err := g.client.GetFileContent(ctx, repo.Owner, repo.Repo, path, repo.Ref)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch: %w", err)
	}

	// Validate content size
	if err := ValidateContentSize(len(content), path); err != nil {
		return nil, err
	}

	return &RemoteFile{
		Path:     path,
		Content:  content,
		Metadata: map[string]string{"ref": repo.Ref},
	}, nil
}

// GetFile fetches a specific file from a repository.
// Implements RemoteFetcher.GetFile.
func (g *GitHubFetcher) GetFile(ctx context.Context, repo RepositoryRef, path string) (*RemoteFile, error) {
	if err := repo.Validate(); err != nil {
		return nil, fmt.Errorf("invalid repository reference: %w", err)
	}

	if err := ValidatePath(path); err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	log := clog.FromContext(ctx).With("owner", repo.Owner, "repo", repo.Repo, "ref", repo.Ref, "path", path)
	log.Debugf("Fetching file")

	content, err := g.client.GetFileContent(ctx, repo.Owner, repo.Repo, path, repo.Ref)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch %s: %w", path, err)
	}

	if err := ValidateContentSize(len(content), path); err != nil {
		return nil, err
	}

	return &RemoteFile{
		Path:     path,
		Content:  content,
		Metadata: map[string]string{"ref": repo.Ref},
	}, nil
}

// validatePattern validates a search pattern to prevent injection attacks.
// Patterns must be simple filenames without path components or GitHub search operators.
func validatePattern(pattern string) error {
	if pattern == "" {
		return fmt.Errorf("%w: pattern", ErrEmptyField)
	}

	if len(pattern) > 256 {
		return fmt.Errorf("%w: pattern (max: 256)", ErrFieldTooLong)
	}

	// Security: Prevent GitHub search operator injection
	githubOperators := []string{"repo:", "user:", "org:", "path:", "extension:", "language:"}
	lowerPattern := strings.ToLower(pattern)
	for _, op := range githubOperators {
		if strings.Contains(lowerPattern, op) {
			return fmt.Errorf("%w: %q", ErrSearchOperatorInjection, op)
		}
	}

	// Security: Pattern must be a simple filename, not a path
	if strings.ContainsAny(pattern, "/\\") {
		return ErrPatternContainsPath
	}

	return nil
}
