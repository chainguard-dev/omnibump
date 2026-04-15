/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package python

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	// envRegistryURL is the env var for overriding the Chainguard Python registry base URL.
	envRegistryURL = "CHAINGUARD_PYTHON_REGISTRY_URL"
	// envToken is the env var for the Chainguard auth token.
	envToken = "CHAINGUARD_TOKEN"
	// pypiBaseURL is the PyPI JSON API base.
	pypiBaseURL = "https://pypi.org/pypi"
	// defaultTimeout for HTTP requests.
	defaultTimeout = 15 * time.Second
)

// VersionResolver resolves Python package versions.
// It checks the Chainguard GAR-backed registry first, then falls back to PyPI.
type VersionResolver struct {
	registryBaseURL string
	token           string
	httpClient      *http.Client
}

// NewVersionResolver creates a VersionResolver from environment variables.
func NewVersionResolver() *VersionResolver {
	return &VersionResolver{
		registryBaseURL: os.Getenv(envRegistryURL),
		token:           os.Getenv(envToken),
		httpClient:      &http.Client{Timeout: defaultTimeout},
	}
}

// GetLatestVersion returns the latest available version of a Python package.
// Checks the Chainguard registry first; falls back to PyPI.
func (r *VersionResolver) GetLatestVersion(ctx context.Context, pkg string) (string, error) {
	norm := normalizePkgName(pkg)

	if r.registryBaseURL != "" {
		ver, err := r.latestFromRegistry(ctx, norm)
		if err == nil && ver != "" {
			return ver, nil
		}
	}

	return r.latestFromPyPI(ctx, norm)
}

// VersionExists checks whether a specific version of a package is available.
func (r *VersionResolver) VersionExists(ctx context.Context, pkg, version string) (bool, error) {
	norm := normalizePkgName(pkg)

	if r.registryBaseURL != "" {
		exists, err := r.versionExistsInRegistry(ctx, norm, version)
		if err == nil {
			return exists, nil
		}
	}

	return r.versionExistsInPyPI(ctx, norm, version)
}

// latestFromRegistry queries the Chainguard Python Simple Index for the latest version.
// The Simple Index (PEP 503) returns an HTML page with links whose filenames encode versions.
func (r *VersionResolver) latestFromRegistry(ctx context.Context, pkg string) (string, error) {
	url := fmt.Sprintf("%s/simple/%s/", strings.TrimRight(r.registryBaseURL, "/"), pkg)
	body, err := r.get(ctx, url)
	if err != nil {
		return "", err
	}
	versions := extractVersionsFromSimpleIndex(body)
	if len(versions) == 0 {
		return "", fmt.Errorf("%w: %s", ErrVersionNotFound, pkg)
	}
	return latestVersion(versions), nil
}

// versionExistsInRegistry checks the Simple Index for a specific version.
func (r *VersionResolver) versionExistsInRegistry(ctx context.Context, pkg, version string) (bool, error) {
	url := fmt.Sprintf("%s/simple/%s/", strings.TrimRight(r.registryBaseURL, "/"), pkg)
	body, err := r.get(ctx, url)
	if err != nil {
		return false, err
	}
	versions := extractVersionsFromSimpleIndex(body)
	for _, v := range versions {
		if v == version {
			return true, nil
		}
	}
	return false, nil
}

// pypiJSON is the minimal structure we need from the PyPI JSON API response.
type pypiJSON struct {
	Info struct {
		Version string `json:"version"`
	} `json:"info"`
	Releases map[string]interface{} `json:"releases"`
}

// latestFromPyPI queries https://pypi.org/pypi/{pkg}/json for the latest version.
func (r *VersionResolver) latestFromPyPI(ctx context.Context, pkg string) (string, error) {
	url := fmt.Sprintf("%s/%s/json", pypiBaseURL, pkg)
	body, err := r.get(ctx, url)
	if err != nil {
		return "", fmt.Errorf("PyPI query for %s: %w", pkg, err)
	}

	var result pypiJSON
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parsing PyPI response for %s: %w", pkg, err)
	}
	if result.Info.Version == "" {
		return "", fmt.Errorf("%w: %s", ErrInvalidVersionResponse, pkg)
	}
	return result.Info.Version, nil
}

// versionExistsInPyPI checks if a version exists in the PyPI releases map.
func (r *VersionResolver) versionExistsInPyPI(ctx context.Context, pkg, version string) (bool, error) {
	url := fmt.Sprintf("%s/%s/json", pypiBaseURL, pkg)
	body, err := r.get(ctx, url)
	if err != nil {
		return false, fmt.Errorf("PyPI query for %s: %w", pkg, err)
	}

	var result pypiJSON
	if err := json.Unmarshal(body, &result); err != nil {
		return false, fmt.Errorf("parsing PyPI response for %s: %w", pkg, err)
	}
	_, exists := result.Releases[version]
	return exists, nil
}

// get performs a GET request, adding the Bearer token if configured.
func (r *VersionResolver) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: %s", ErrHTTPNotFound, url)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d for %s", ErrUnexpectedHTTPStatus, resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

// wheelFilenameRe extracts the version from a PEP 425 wheel filename or sdist tarball.
// Examples: requests-2.28.0-py3-none-any.whl, requests-2.28.0.tar.gz.
var wheelFilenameRe = regexp.MustCompile(`-([0-9][^-/!]+?)(?:-py|\.tar\.gz|\.zip|\.whl)`)

// extractVersionsFromSimpleIndex parses wheel/sdist filenames from a PEP 503 Simple Index HTML page.
func extractVersionsFromSimpleIndex(html []byte) []string {
	seen := make(map[string]bool)
	var versions []string
	for _, m := range wheelFilenameRe.FindAllSubmatch(html, -1) {
		v := string(m[1])
		if !seen[v] {
			seen[v] = true
			versions = append(versions, v)
		}
	}
	return versions
}

// latestVersion returns the lexicographically largest version string.
// This is a simple approximation — for proper semver comparison use a semver library.
func latestVersion(versions []string) string {
	if len(versions) == 0 {
		return ""
	}
	sort.Strings(versions)
	return versions[len(versions)-1]
}
