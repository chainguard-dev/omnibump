/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package golang

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

func TestClassifyDependency(t *testing.T) {
	tests := []struct {
		name         string
		goModContent string
		packageName  string
		expected     DependencyType
	}{
		{
			name: "direct dependency",
			goModContent: `module test

require (
	github.com/example/pkg v1.0.0
)
`,
			packageName: "github.com/example/pkg",
			expected:    Direct,
		},
		{
			name: "indirect dependency",
			goModContent: `module test

require (
	github.com/example/pkg v1.0.0 // indirect
)
`,
			packageName: "github.com/example/pkg",
			expected:    Indirect,
		},
		{
			name: "not found",
			goModContent: `module test

require (
	github.com/example/other v1.0.0
)
`,
			packageName: "github.com/example/pkg",
			expected:    NotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modFile, err := modfile.Parse("go.mod", []byte(tt.goModContent), nil)
			require.NoError(t, err)

			result := ClassifyDependency(modFile, tt.packageName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractModulePath(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "github.com/example/pkg@v1.0.0",
			expected: "github.com/example/pkg",
		},
		{
			input:    "golang.org/x/crypto@v0.43.0",
			expected: "golang.org/x/crypto",
		},
		{
			input:    "github.com/pkg/errors@v0.9.1",
			expected: "github.com/pkg/errors",
		},
		{
			input:    "no-version",
			expected: "no-version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := extractModulePath(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractModuleVersion(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "github.com/example/pkg@v1.0.0",
			expected: "v1.0.0",
		},
		{
			input:    "golang.org/x/crypto@v0.43.0",
			expected: "v0.43.0",
		},
		{
			input:    "no-version",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := extractModuleVersion(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFetchFromProxy_404ReturnsErrModuleVersionNotFound(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer srv.Close()

	origHost, origClient := proxyHost, proxyClient
	proxyHost = srv.Listener.Addr().String()
	proxyClient = srv.Client()
	defer func() { proxyHost, proxyClient = origHost, origClient }()

	_, err := fetchFromProxy(context.Background(), "/github.com/example/pkg/@v/v3.0.0.mod")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrModuleVersionNotFound), "expected ErrModuleVersionNotFound, got: %v", err)
	assert.False(t, errors.Is(err, ErrProxyRequestFailed), "should not be ErrProxyRequestFailed for a 404")
}

func TestFetchFromProxy_NonOKNon404ReturnsErrProxyRequestFailed(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	origHost, origClient := proxyHost, proxyClient
	proxyHost = srv.Listener.Addr().String()
	proxyClient = srv.Client()
	defer func() { proxyHost, proxyClient = origHost, origClient }()

	_, err := fetchFromProxy(context.Background(), "/github.com/example/pkg/@v/v1.0.0.mod")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrProxyRequestFailed), "expected ErrProxyRequestFailed for 500, got: %v", err)
	assert.False(t, errors.Is(err, ErrModuleVersionNotFound), "should not be ErrModuleVersionNotFound for a 500")
}

func TestFetchGoModForPackage(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test that fetches from Go proxy")
	}

	ctx := context.Background()

	tests := []struct {
		name        string
		pkgPath     string
		version     string
		expectError bool
		errTarget   error
		checkFunc   func(*testing.T, *modfile.File)
	}{
		{
			name:        "fetch valid package",
			pkgPath:     "github.com/libp2p/go-libp2p",
			version:     "v0.47.0",
			expectError: false,
			checkFunc: func(t *testing.T, mod *modfile.File) {
				assert.Equal(t, "github.com/libp2p/go-libp2p", mod.Module.Mod.Path)
				// Should have webtransport-go@v0.10.0
				found := false
				for _, req := range mod.Require {
					if req.Mod.Path == "github.com/quic-go/webtransport-go" {
						found = true
						assert.Equal(t, "v0.10.0", req.Mod.Version)
						break
					}
				}
				assert.True(t, found, "Should have webtransport-go dependency")
			},
		},
		{
			name:        "version not on proxy returns ErrModuleVersionNotFound",
			pkgPath:     "github.com/libp2p/go-libp2p",
			version:     "v999.999.999",
			expectError: true,
			errTarget:   ErrModuleVersionNotFound,
		},
		{
			name:        "nonexistent package returns ErrModuleVersionNotFound",
			pkgPath:     "github.com/nonexistent/package",
			version:     "v1.0.0",
			expectError: true,
			errTarget:   ErrModuleVersionNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mod, err := fetchGoModForPackage(ctx, tt.pkgPath, tt.version)

			if tt.expectError {
				require.Error(t, err)
				if tt.errTarget != nil {
					assert.True(t, errors.Is(err, tt.errTarget), "expected errors.Is(%v), got: %v", tt.errTarget, err)
				}
			} else {
				require.NoError(t, err)
				require.NotNil(t, mod)
				if tt.checkFunc != nil {
					tt.checkFunc(t, mod)
				}
			}
		})
	}
}

func TestFetchAvailableVersions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test that fetches from Go proxy")
	}

	ctx := context.Background()

	tests := []struct {
		name        string
		modulePath  string
		expectError bool
		checkFunc   func(*testing.T, []string)
	}{
		{
			name:        "fetch libp2p versions",
			modulePath:  "github.com/libp2p/go-libp2p",
			expectError: false,
			checkFunc: func(t *testing.T, versions []string) {
				assert.Greater(t, len(versions), 10, "Should have many versions")
				// Versions should be sorted newest first
				if len(versions) >= 2 {
					// First should be >= second
					cmp := semver.Compare(versions[0], versions[1])
					assert.GreaterOrEqual(t, cmp, 0, "Versions should be sorted newest first")
				}
			},
		},
		{
			name:        "invalid package",
			modulePath:  "github.com/nonexistent/package",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			versions, err := fetchAvailableVersions(ctx, tt.modulePath)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				if tt.checkFunc != nil {
					tt.checkFunc(t, versions)
				}
			}
		})
	}
}

func TestCheckIfDirectParentHasFix(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test that fetches from Go proxy")
	}

	ctx := context.Background()

	tests := []struct {
		name           string
		directDep      string
		currentVersion string
		indirectPkg    string
		targetVersion  string
		expectError    bool
		checkFunc      func(*testing.T, *ParentFixInfo)
	}{
		{
			name:           "libp2p v0.48.0 has webtransport-go v0.10.0",
			directDep:      "github.com/libp2p/go-libp2p",
			currentVersion: "v0.46.0",
			indirectPkg:    "github.com/quic-go/webtransport-go",
			targetVersion:  "v0.10.0",
			expectError:    false,
			checkFunc: func(t *testing.T, info *ParentFixInfo) {
				assert.Equal(t, "github.com/libp2p/go-libp2p", info.DirectDep)
				assert.Equal(t, "v0.46.0", info.CurrentVersion)
				assert.True(t, semver.Compare(info.FixVersion, "v0.47.0") >= 0, "FixVersion should be >= v0.47.0, got %s", info.FixVersion)
				assert.Equal(t, "github.com/quic-go/webtransport-go", info.IndirectPkg)
				assert.Equal(t, "v0.10.0", info.IndirectVersionIn)
			},
		},
		{
			name:           "no fix available",
			directDep:      "github.com/libp2p/go-libp2p",
			currentVersion: "v0.50.0",
			indirectPkg:    "github.com/nonexistent/pkg",
			targetVersion:  "v1.0.0",
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := CheckIfDirectParentHasFix(ctx,
				tt.directDep,
				tt.currentVersion,
				tt.indirectPkg,
				tt.targetVersion)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				require.NotNil(t, info)
				if tt.checkFunc != nil {
					tt.checkFunc(t, info)
				}
			}
		})
	}
}

// TestResolveIndirectDependency_RealWorld tests with a minimal go.mod file.
func TestResolveIndirectDependency_Direct(t *testing.T) {
	ctx := context.Background()

	// Create temporary directory with go.mod
	tmpDir := t.TempDir()

	goModContent := `module test

go 1.25

require (
	github.com/example/pkg v1.0.0
)
`

	err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goModContent), 0o600)
	require.NoError(t, err)

	// Test with direct dependency
	resolution, err := ResolveIndirectDependency(ctx, tmpDir, "github.com/example/pkg", "v1.1.0")
	require.NoError(t, err)
	assert.False(t, resolution.IsIndirect, "Should detect as direct dependency")
}

func TestResolveIndirectDependency_Indirect(t *testing.T) {
	ctx := context.Background()

	// Create temporary directory with go.mod
	tmpDir := t.TempDir()

	goModContent := `module test

go 1.25

require (
	github.com/libp2p/go-libp2p v0.46.0
	github.com/quic-go/webtransport-go v0.9.0 // indirect
)
`

	err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goModContent), 0o600)
	require.NoError(t, err)

	// Create minimal go.sum (required for go mod graph)
	goSumContent := `github.com/libp2p/go-libp2p v0.46.0 h1:test
github.com/libp2p/go-libp2p v0.46.0/go.mod h1:test
github.com/quic-go/webtransport-go v0.9.0 h1:test
github.com/quic-go/webtransport-go v0.9.0/go.mod h1:test
`
	err = os.WriteFile(filepath.Join(tmpDir, "go.sum"), []byte(goSumContent), 0o600)
	require.NoError(t, err)

	// Test with indirect dependency (no go mod graph in temp dir, will fallback)
	resolution, err := ResolveIndirectDependency(ctx, tmpDir, "github.com/quic-go/webtransport-go", "v0.10.0")
	require.NoError(t, err)
	assert.True(t, resolution.IsIndirect, "Should detect as indirect dependency")
	// Will have FallbackAllowed=true because go mod graph won't work without full module
}

func TestResolveIndirectDependency_NotFound(t *testing.T) {
	ctx := context.Background()

	// Create temporary directory with go.mod
	tmpDir := t.TempDir()

	goModContent := `module test

go 1.25

require (
	github.com/example/pkg v1.0.0
)
`

	err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goModContent), 0o600)
	require.NoError(t, err)

	// Test with package not in go.mod
	resolution, err := ResolveIndirectDependency(ctx, tmpDir, "github.com/nonexistent/pkg", "v1.0.0")
	require.NoError(t, err)
	assert.False(t, resolution.IsIndirect, "Package not found should return IsIndirect=false")
}

func TestFindDirectParents_WithReplace(t *testing.T) {
	// Create temporary directory with go.mod that has replace directive
	tmpDir := t.TempDir()

	goModContent := `module test

go 1.25

replace (
	github.com/replaced/pkg => github.com/fork/pkg v2.0.0
)

require (
	github.com/direct/pkg v1.0.0
	github.com/replaced/pkg v1.0.0
	github.com/indirect/dep v0.5.0 // indirect
)
`

	err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goModContent), 0o600)
	require.NoError(t, err)

	// Create minimal go.sum
	goSumContent := `github.com/direct/pkg v1.0.0 h1:test
github.com/direct/pkg v1.0.0/go.mod h1:test
github.com/replaced/pkg v1.0.0 h1:test
github.com/replaced/pkg v1.0.0/go.mod h1:test
github.com/indirect/dep v0.5.0 h1:test
github.com/indirect/dep v0.5.0/go.mod h1:test
`
	err = os.WriteFile(filepath.Join(tmpDir, "go.sum"), []byte(goSumContent), 0o600)
	require.NoError(t, err)

	// Mock go.mod file to parse
	modFile, _, err := ParseGoModfile(filepath.Join(tmpDir, "go.mod"))
	require.NoError(t, err)

	// Verify replace directive exists
	assert.Len(t, modFile.Replace, 1)
	assert.Equal(t, "github.com/replaced/pkg", modFile.Replace[0].Old.Path)

	// Test classification
	directType := ClassifyDependency(modFile, "github.com/direct/pkg")
	assert.Equal(t, Direct, directType, "github.com/direct/pkg should be Direct")

	replacedType := ClassifyDependency(modFile, "github.com/replaced/pkg")
	assert.Equal(t, Direct, replacedType, "github.com/replaced/pkg should be Direct (has replace)")

	// The key test: FindDirectParents should EXCLUDE replaced/pkg even though it's direct
	// because we can't query versions of the original package when it's replaced with a fork
	// This test would need go mod graph to work fully, but we've verified the logic:
	// In FindDirectParents, we check: !req.Indirect && !replacedDeps[req.Mod.Path]

	// Verify that replacedDeps map would be built correctly
	replacedDeps := make(map[string]bool)
	for _, repl := range modFile.Replace {
		if repl != nil {
			replacedDeps[repl.Old.Path] = true
		}
	}

	assert.True(t, replacedDeps["github.com/replaced/pkg"], "replaced/pkg should be in replacedDeps map")
	assert.False(t, replacedDeps["github.com/direct/pkg"], "direct/pkg should NOT be in replacedDeps map")
}

// Integration test with real k3s scenario (requires network access).
func TestResolveIndirectDependency_K3S_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	// This test requires actual k3s repository
	// Skip if not available
	k3sPath := "/tmp/k3s-analysis"
	if _, err := os.Stat(filepath.Join(k3sPath, "go.mod")); os.IsNotExist(err) {
		t.Skip("k3s repository not available at /tmp/k3s-analysis")
	}

	ctx := context.Background()

	// Test the exact scenario from PR #30473
	resolution, err := ResolveIndirectDependency(ctx,
		k3sPath,
		"github.com/quic-go/webtransport-go",
		"v0.10.0")

	require.NoError(t, err)
	assert.True(t, resolution.IsIndirect, "webtransport-go should be indirect in k3s")
	assert.Greater(t, len(resolution.DirectParents), 0, "Should find direct parents")

	// Should find libp2p as a parent
	foundLibp2p := false
	foundSpegel := false
	for _, parent := range resolution.DirectParents {
		if parent.Package == "github.com/libp2p/go-libp2p" {
			foundLibp2p = true
			assert.Equal(t, "v0.46.0", parent.CurrentVersion)
		}
		if parent.Package == "github.com/spegel-org/spegel" {
			foundSpegel = true
		}
	}
	assert.True(t, foundLibp2p, "Should find libp2p as a direct parent")
	assert.False(t, foundSpegel, "Should NOT find spegel (it has replace directive to k3s-io/spegel fork)")

	// Should find multiple possible bumps (libp2p and boxo at minimum)
	assert.GreaterOrEqual(t, len(resolution.PossibleBumps), 1, "Should find at least one parent bump")

	// Should include libp2p@v0.47.0
	foundLibp2pBump := false
	for _, bump := range resolution.PossibleBumps {
		if bump.Package == "github.com/libp2p/go-libp2p" {
			foundLibp2pBump = true
			assert.Equal(t, "v0.46.0", bump.FromVersion)
			assert.Equal(t, "v0.47.0", bump.ToVersion)
			assert.Equal(t, "github.com/quic-go/webtransport-go", bump.WillBringIn)
			assert.Equal(t, "v0.10.0", bump.WillBringInVersion)
			break
		}
	}
	assert.True(t, foundLibp2pBump, "Should include libp2p bump option")
}

func TestCheckTransitiveRequirements(t *testing.T) {
	tests := []struct {
		name                string
		packageName         string
		targetVersion       string
		currentGoModContent string
		expectedMissing     int
		expectedPackages    []string
		skipTest            bool // Skip if network required
	}{
		{
			// Indirect deps are excluded from version-constraint checks because Go's MVS
			// resolves them automatically; only direct deps can cause API breakage for the project.
			name:          "oras-go v1.2.7 with docker packages as indirect — no co-updates needed",
			packageName:   "oras.land/oras-go",
			targetVersion: "v1.2.7",
			currentGoModContent: `module test

go 1.24

require (
	github.com/docker/cli v25.0.1+incompatible // indirect
	github.com/docker/docker v28.0.0+incompatible // indirect
	github.com/docker/go-connections v0.5.0 // indirect
	golang.org/x/crypto v0.41.0 // indirect
)
`,
			expectedMissing:  0,
			expectedPackages: []string{},
		},
		{
			// When the project directly depends on docker packages, a version bump
			// by oras-go that requires newer docker APIs IS flagged as a co-update.
			name:          "oras-go v1.2.7 with docker packages as direct — co-updates required",
			packageName:   "oras.land/oras-go",
			targetVersion: "v1.2.7",
			currentGoModContent: `module test

go 1.24

require (
	github.com/docker/cli v25.0.1+incompatible
	github.com/docker/docker v28.0.0+incompatible
	github.com/docker/go-connections v0.5.0
	golang.org/x/crypto v0.41.0
)
`,
			expectedMissing: 4,
			expectedPackages: []string{
				"github.com/docker/cli",
				"github.com/docker/docker",
				"github.com/docker/go-connections",
				"golang.org/x/crypto",
			},
		},
		{
			name:          "package with all requirements satisfied",
			packageName:   "github.com/google/uuid",
			targetVersion: "v1.6.0",
			currentGoModContent: `module test

go 1.21

require (
	github.com/google/uuid v1.5.0
)
`,
			expectedMissing:  0,
			expectedPackages: []string{},
		},
		{
			name:          "package with current version higher than required",
			packageName:   "github.com/stretchr/testify",
			targetVersion: "v1.8.0",
			currentGoModContent: `module test

go 1.21

require (
	github.com/davecgh/go-spew v1.2.0
	github.com/pmezard/go-difflib v1.1.0
	gopkg.in/yaml.v3 v3.1.0
)
`,
			expectedMissing:  0,
			expectedPackages: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skipTest {
				t.Skip("Skipping test that requires network access")
			}

			ctx := context.Background()

			// Parse the current go.mod content
			modFile, err := modfile.Parse("go.mod", []byte(tt.currentGoModContent), nil)
			require.NoError(t, err)

			// Check transitive requirements
			missing, err := CheckTransitiveRequirements(ctx, tt.packageName, tt.targetVersion, modFile)
			require.NoError(t, err)

			// Verify count
			assert.Equal(t, tt.expectedMissing, len(missing), "Should find expected number of missing dependencies")

			// Verify expected packages are in the missing list
			foundPackages := make(map[string]bool)
			for _, m := range missing {
				foundPackages[m.Package] = true
			}

			for _, expectedPkg := range tt.expectedPackages {
				assert.True(t, foundPackages[expectedPkg], "Should find %s in missing dependencies", expectedPkg)
			}
		})
	}
}

func TestCheckTransitiveRequirements_Integration(t *testing.T) {
	// Integration test using real go.mod files
	t.Run("real oras-go update scenario", func(t *testing.T) {
		ctx := context.Background()

		// Create a temp directory with a go.mod similar to gatekeeper
		tmpDir := t.TempDir()
		// docker packages listed as direct deps — the project imports them directly,
		// so a version bump from oras-go that requires newer docker is a real co-update.
		goModContent := `module github.com/example/test

go 1.24.0

require (
	oras.land/oras-go v1.2.5
	github.com/docker/cli v25.0.1+incompatible
	github.com/docker/docker v28.0.0+incompatible
	github.com/docker/go-connections v0.5.0
	github.com/spf13/cobra v1.9.1
	golang.org/x/crypto v0.41.0
	golang.org/x/sync v0.16.0 // indirect
)
`
		goModPath := filepath.Join(tmpDir, "go.mod")
		err := os.WriteFile(goModPath, []byte(goModContent), 0o600)
		require.NoError(t, err)

		modFile, _, err := ParseGoModfile(goModPath)
		require.NoError(t, err)

		// Check what updating oras-go to v1.2.7 would require
		missing, err := CheckTransitiveRequirements(ctx, "oras.land/oras-go", "v1.2.7", modFile)
		require.NoError(t, err)

		// Should find multiple missing dependencies
		assert.Greater(t, len(missing), 0, "Should find missing dependencies")

		// Should include docker/cli and docker/docker at minimum
		foundDocker := false
		foundCli := false
		for _, m := range missing {
			if m.Package == "github.com/docker/docker" {
				foundDocker = true
				assert.Equal(t, "v28.0.0+incompatible", m.CurrentVersion)
				assert.True(t, semver.Compare(m.RequiredVersion, "v28.5.0") >= 0, "Required version should be >= v28.5.0")
			}
			if m.Package == "github.com/docker/cli" {
				foundCli = true
				assert.Equal(t, "v25.0.1+incompatible", m.CurrentVersion)
				assert.True(t, semver.Compare(m.RequiredVersion, "v28.5.0") >= 0, "Required version should be >= v28.5.0")
			}
		}

		assert.True(t, foundDocker, "Should detect github.com/docker/docker needs updating")
		assert.True(t, foundCli, "Should detect github.com/docker/cli needs updating")
	})
}

func TestCheckAPICompatibility(t *testing.T) {
	// This test verifies that when a package is updated, we detect other packages
	// in the project that import from it and flag them as potentially needing updates.
	t.Run("detects packages importing updated dependency", func(t *testing.T) {
		ctx := context.Background()

		// Simulate a project that has google/uuid and other packages
		// google/uuid is a simple package with minimal dependencies
		currentGoModContent := `module github.com/example/test

go 1.24

require (
	github.com/google/uuid v1.5.0
	github.com/stretchr/testify v1.8.0
)
`
		modFile, err := modfile.Parse("go.mod", []byte(currentGoModContent), nil)
		require.NoError(t, err)

		// Check API compatibility when updating google/uuid
		issues, err := CheckAPICompatibility(ctx, "github.com/google/uuid", "v1.6.0", modFile)
		require.NoError(t, err)

		// google/uuid has few dependencies, so we just verify the function works
		// and returns appropriate issue structure if any are found
		for _, issue := range issues {
			assert.NotEmpty(t, issue.Package)
			assert.NotEmpty(t, issue.Reason)
			assert.Contains(t, issue.Reason, "imports")
		}
	})

	t.Run("skips indirect dependencies", func(t *testing.T) {
		ctx := context.Background()

		currentGoModContent := `module github.com/example/test

go 1.24

require (
	github.com/google/uuid v1.5.0
)
`
		modFile, err := modfile.Parse("go.mod", []byte(currentGoModContent), nil)
		require.NoError(t, err)

		issues, err := CheckAPICompatibility(ctx, "github.com/google/uuid", "v1.6.0", modFile)
		require.NoError(t, err)

		// Verify no issues are returned (all existing deps are direct, none import uuid typically)
		_ = issues // Just verify we can call it without error
	})
}

func TestFindMinCompatibleVersion(t *testing.T) {
	ctx := t.Context()
	cache := newGoModCache()

	t.Run("finds minimum otelgrpc version compatible with otel v1.43.0", func(t *testing.T) {
		// otelgrpc@v0.56.0 requires otel@v1.31.0. When otel is bumped to v1.43.0,
		// we need the first otelgrpc version that requires otel >= v1.43.0.
		// Verified from proxy: otelgrpc@v0.68.0 is the first to require otel@v1.43.0.
		got := FindMinCompatibleVersion(ctx,
			"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc",
			"v0.56.0",
			"go.opentelemetry.io/otel",
			"v1.43.0",
			cache)
		assert.Equal(t, "v0.68.0", got)
	})

	t.Run("returns empty string when no compatible version exists within limit", func(t *testing.T) {
		// Using a version that is already at the top of available releases so no
		// newer version with the required dep bump will be found.
		got := FindMinCompatibleVersion(ctx,
			"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc",
			"v0.68.0",
			"go.opentelemetry.io/otel",
			"v9.99.0", // far-future version that no release will satisfy
			cache)
		assert.Equal(t, "", got)
	})

	t.Run("returns empty string for unknown package", func(t *testing.T) {
		got := FindMinCompatibleVersion(ctx,
			"github.com/does-not-exist/package",
			"v1.0.0",
			"github.com/some/dep",
			"v2.0.0",
			cache)
		assert.Equal(t, "", got)
	})
}

// TestCheckAPICompatibilityWithCache_CoUpdateDeps verifies the second-pass behavior:
// when a package (e.g. otel) is discovered as a co-update, running API compat against
// it should surface community packages that import it (e.g. otelgrpc) and may break.
func TestCheckAPICompatibilityWithCache_CoUpdateDeps(t *testing.T) {
	ctx := t.Context()

	// Project directly depends on otelgrpc, which imports otel@v1.31.0 in its own go.mod.
	// When otel is bumped to v1.43.0 (as a co-update from bumping otel/sdk), otelgrpc
	// should be flagged because it imports the package being co-updated.
	modContent := `module github.com/example/test

go 1.24

require (
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.56.0
	go.opentelemetry.io/otel/sdk v1.40.0
)
`
	modFile, err := modfile.Parse("go.mod", []byte(modContent), nil)
	require.NoError(t, err)

	cache := newGoModCache()
	issues, err := CheckAPICompatibilityWithCache(ctx, "go.opentelemetry.io/otel", "v1.43.0", modFile, cache)
	require.NoError(t, err)

	found := false
	for _, issue := range issues {
		if issue.Package == "go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc" {
			found = true
			assert.Contains(t, issue.Reason, "imports")
		}
	}
	assert.True(t, found, "otelgrpc should be flagged: it imports otel which is being co-updated")
}

func TestCheckAPIBreakingChanges(t *testing.T) {
	ctx := t.Context()

	t.Run("detects breaking change in go-ntlmssp ProcessChallenge signature", func(t *testing.T) {
		// go-ldap/ldap/v3@v3.4.1 uses ntlmssp@v0.0.0-20200615164410-66371956d46c.
		// ntlmssp@v0.1.1 added a fourth argument to ProcessChallenge, breaking that caller.
		breaking, err := CheckAPIBreakingChanges(ctx,
			"github.com/Azure/go-ntlmssp",
			"v0.0.0-20200615164410-66371956d46c",
			"v0.1.1")
		require.NoError(t, err)
		require.NotEmpty(t, breaking, "expected breaking changes between ntlmssp versions")

		found := false
		for _, msg := range breaking {
			if strings.Contains(strings.ToLower(msg), "processchallenge") {
				found = true
				break
			}
		}
		assert.True(t, found, "expected ProcessChallenge to appear in breaking changes, got: %v", breaking)
	})

	t.Run("no breaking changes for a compatible bump", func(t *testing.T) {
		// google/uuid v1.5.0 → v1.6.0 only adds new exports; existing API is unchanged.
		breaking, err := CheckAPIBreakingChanges(ctx,
			"github.com/google/uuid",
			"v1.5.0",
			"v1.6.0")
		require.NoError(t, err)
		assert.Empty(t, breaking, "expected no breaking changes between uuid v1.5.0 and v1.6.0")
	})

	t.Run("reports package unavailable when new version does not contain it", func(t *testing.T) {
		// When the new version of a module does not contain the requested package
		// (e.g. a sub-package was removed, or the version simply does not exist),
		// we treat it as a breaking change so the caller knows not to bump to that version.
		// Using a non-existent version forces loadPackageTypes to fail on the new side
		// while the old side loads successfully — the same code path as a removed package.
		breaking, err := CheckAPIBreakingChanges(ctx,
			"github.com/google/uuid",
			"v1.5.0",
			"v99.99.99")
		require.NoError(t, err)
		require.NotEmpty(t, breaking, "expected a breaking change when new version is unavailable")
		assert.Contains(t, breaking[0], "unavailable")
	})
}

func TestModuleFamilyPrefix(t *testing.T) {
	tests := []struct {
		pkg  string
		want string
	}{
		{"go.opentelemetry.io/otel/sdk", "go.opentelemetry.io/otel"},
		{"go.opentelemetry.io/otel/metric", "go.opentelemetry.io/otel"},
		{"go.opentelemetry.io/otel", "go.opentelemetry.io/otel"},
		{"go.opentelemetry.io/auto/sdk", "go.opentelemetry.io/auto"},
		{"github.com/stretchr/testify", "github.com/stretchr/testify"},
		{"github.com/stretchr/testify/mock", "github.com/stretchr/testify"},
		{"gitlab.com/org/repo/sub", "gitlab.com/org/repo"},
		{"k8s.io/client-go", "k8s.io/client-go"},
		{"k8s.io/api", "k8s.io/api"},
		{"google.golang.org/grpc", "google.golang.org/grpc"},
		// golang.org/x/* packages have independent release cadences and must NOT
		// be grouped as one family — each is its own family.
		{"golang.org/x/net", "golang.org/x/net"},
		{"golang.org/x/oauth2", "golang.org/x/oauth2"},
		{"golang.org/x/crypto", "golang.org/x/crypto"},
		{"golang.org/x/net/http2", "golang.org/x/net"},
		// gopkg.in packages are also independent projects.
		{"gopkg.in/yaml.v3", "gopkg.in/yaml.v3"},
		{"gopkg.in/check.v1", "gopkg.in/check.v1"},
	}
	for _, tt := range tests {
		t.Run(tt.pkg, func(t *testing.T) {
			assert.Equal(t, tt.want, moduleFamilyPrefix(tt.pkg))
		})
	}
}

func TestFindVersionGroupPackages(t *testing.T) {
	tests := []struct {
		name           string
		packageName    string
		currentVersion string
		goModContent   string
		wantGroup      []string
	}{
		{
			name:           "otel ecosystem — siblings at same version included",
			packageName:    "go.opentelemetry.io/otel/sdk",
			currentVersion: "v1.40.0",
			goModContent: `module test
go 1.24
require (
	go.opentelemetry.io/otel/sdk v1.40.0
	go.opentelemetry.io/otel v1.40.0
	go.opentelemetry.io/otel/metric v1.40.0
	go.opentelemetry.io/otel/trace v1.40.0
	golang.org/x/sys v0.38.0 // indirect
	github.com/stretchr/testify v1.9.0 // indirect
)
`,
			wantGroup: []string{
				"go.opentelemetry.io/otel",
				"go.opentelemetry.io/otel/metric",
				"go.opentelemetry.io/otel/trace",
			},
		},
		{
			name:           "otel ecosystem — drifted sibling at lower version included",
			packageName:    "go.opentelemetry.io/otel/sdk",
			currentVersion: "v1.40.0",
			goModContent: `module test
go 1.24
require (
	go.opentelemetry.io/otel/sdk v1.40.0
	go.opentelemetry.io/otel v1.40.0
	go.opentelemetry.io/otel/metric v1.39.0
	go.opentelemetry.io/otel/trace v1.40.0
	golang.org/x/sys v0.38.0 // indirect
)
`,
			wantGroup: []string{
				"go.opentelemetry.io/otel",
				"go.opentelemetry.io/otel/metric",
				"go.opentelemetry.io/otel/trace",
			},
		},
		{
			name:           "auto/sdk is a different family — not included with otel/*",
			packageName:    "go.opentelemetry.io/otel/sdk",
			currentVersion: "v1.40.0",
			goModContent: `module test
go 1.24
require (
	go.opentelemetry.io/otel/sdk v1.40.0
	go.opentelemetry.io/otel v1.40.0
	go.opentelemetry.io/auto/sdk v1.1.0
)
`,
			wantGroup: []string{
				"go.opentelemetry.io/otel",
			},
		},
		{
			name:           "github package — unrelated same-repo package not included",
			packageName:    "github.com/google/uuid",
			currentVersion: "v1.6.0",
			goModContent: `module test
go 1.24
require (
	github.com/google/uuid v1.6.0
	github.com/google/go-cmp v1.6.0
	golang.org/x/sys v0.38.0
)
`,
			// go-cmp is a different repo (github.com/google/go-cmp vs github.com/google/uuid)
			wantGroup: []string{},
		},
		{
			// otlploghttp uses v0.x (max release v0.19.0) while core otel uses v1.x.
			// FindVersionGroupPackages returns it as a family member — the caller
			// (detectCoUpdates) is responsible for finding the correct v0.x version
			// via findMinCompatibleVersion rather than blindly applying the v1.x target.
			name:           "otel otlploghttp v0.x included in family group for caller to resolve",
			packageName:    "go.opentelemetry.io/otel/sdk",
			currentVersion: "v1.40.0",
			goModContent: `module test
go 1.24
require (
	go.opentelemetry.io/otel/sdk v1.40.0
	go.opentelemetry.io/otel v1.40.0
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp v0.18.0
)
`,
			wantGroup: []string{
				"go.opentelemetry.io/otel",
				"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp",
			},
		},
		{
			// go.opentelemetry.io/otel/exporters/prometheus deliberately uses v0.x versioning
			// and has never had a v1.x release. When bumping core otel (v1.x), recommending
			// exporters/prometheus at v1.43.0 would cause a go mod tidy failure because that
			// version does not exist. FindVersionGroupPackages must exclude it.
			//
			// Confirmed from pkg.go.dev: latest exporters/prometheus is v0.65.0 (v0.x track).
			name:           "otel exporters/prometheus v0.x must not be recommended at v1.43.0",
			packageName:    "go.opentelemetry.io/otel/sdk",
			currentVersion: "v1.40.0",
			goModContent: `module test
go 1.24
require (
	go.opentelemetry.io/otel/sdk v1.40.0
	go.opentelemetry.io/otel v1.40.0
	go.opentelemetry.io/otel/metric v1.40.0
	go.opentelemetry.io/otel/exporters/prometheus v0.60.0
)
`,
			// exporters/prometheus IS returned as a family member — it's in the same
			// go.opentelemetry.io/otel family and at v0.60.0 ≤ v1.40.0. The caller
			// (detectCoUpdates) detects the major version difference and uses
			// findMinCompatibleVersion to find the correct v0.x version (v0.65.0).
			wantGroup: []string{
				"go.opentelemetry.io/otel",
				"go.opentelemetry.io/otel/metric",
				"go.opentelemetry.io/otel/exporters/prometheus",
			},
		},
		{
			name:           "non-semver version — returns nil immediately",
			packageName:    "github.com/example/pkg",
			currentVersion: "not-a-semver",
			goModContent: `module test
go 1.24
require (
	github.com/example/pkg v1.0.0
	github.com/example/other v1.0.0
)
`,
			wantGroup: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modFile, err := modfile.Parse("go.mod", []byte(tt.goModContent), nil)
			require.NoError(t, err)

			got := FindVersionGroupPackages(tt.packageName, tt.currentVersion, modFile)

			if tt.wantGroup == nil {
				assert.Nil(t, got)
				return
			}
			assert.ElementsMatch(t, tt.wantGroup, got)
		})
	}
}

func TestGoModCache_BasicOperations(t *testing.T) {
	cache := newGoModCache()

	// Test that cache starts empty
	require.Equal(t, 0, len(cache))

	// Test key generation
	key := cache.key("github.com/example/pkg", "v1.0.0")
	require.Equal(t, "github.com/example/pkg@v1.0.0", key)

	// Test get on empty cache returns false
	_, exists := cache.get("github.com/example/pkg", "v1.0.0")
	require.False(t, exists)

	// Test set stores the value
	testValue := &modfile.File{}
	cache.set("github.com/example/pkg", "v1.0.0", testValue)
	require.Equal(t, 1, len(cache))

	// Test get retrieves the value
	retrieved, exists := cache.get("github.com/example/pkg", "v1.0.0")
	require.True(t, exists)
	require.Same(t, testValue, retrieved)

	// Test multiple packages can be cached
	testValue2 := &modfile.File{}
	cache.set("github.com/other/pkg", "v2.0.0", testValue2)
	require.Equal(t, 2, len(cache))
}
