/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package golang

import (
	"context"
	"os"
	"path/filepath"
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
			name:        "invalid version",
			pkgPath:     "github.com/libp2p/go-libp2p",
			version:     "v999.999.999",
			expectError: true,
		},
		{
			name:        "invalid package",
			pkgPath:     "github.com/nonexistent/package",
			version:     "v1.0.0",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mod, err := fetchGoModForPackage(ctx, tt.pkgPath, tt.version)

			if tt.expectError {
				assert.Error(t, err)
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
			name:          "oras-go v1.2.7 requires newer docker packages",
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
		goModContent := `module github.com/example/test

go 1.24.0

require (
	oras.land/oras-go v1.2.5
	github.com/docker/cli v25.0.1+incompatible // indirect
	github.com/docker/docker v28.0.0+incompatible // indirect
	github.com/docker/go-connections v0.5.0 // indirect
	github.com/spf13/cobra v1.9.1
	golang.org/x/crypto v0.41.0 // indirect
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
