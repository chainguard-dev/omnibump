/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package composer

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/chainguard-dev/omnibump/pkg/languages"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// composerAvailable checks if composer is installed and the Packagist
// registry is reachable (CI environments may block network access).
func composerAvailable() bool {
	if _, err := exec.LookPath("composer"); err != nil {
		return false
	}
	// Check if Packagist is reachable (CI may have egress restrictions)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, "https://repo.packagist.org", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

// setupTestProject creates a temporary PHP project with composer.json and composer.lock
// and returns the path to the temporary directory.
func setupTestProject(t *testing.T) string {
	t.Helper()

	tmpDir := t.TempDir()

	// Copy composer.json
	jsonContent, err := os.ReadFile("testdata/composer.json")
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(tmpDir, "composer.json"), jsonContent, 0o644)
	require.NoError(t, err)

	// Copy composer.lock.orig as composer.lock
	lockContent, err := os.ReadFile("testdata/composer.lock.orig")
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(tmpDir, "composer.lock"), lockContent, 0o644)
	require.NoError(t, err)

	return tmpDir
}

// getPackageVersion reads the composer.lock and returns the version for a given package.
func getPackageVersion(t *testing.T, projectDir, packageName string) string {
	t.Helper()

	lockFile, err := os.Open(filepath.Join(projectDir, "composer.lock"))
	require.NoError(t, err)
	defer func() { _ = lockFile.Close() }()

	pkgs, err := ParseLock(lockFile)
	require.NoError(t, err)

	for _, pkg := range pkgs {
		if pkg.Name == packageName {
			return pkg.Version
		}
	}
	return ""
}

// composerJSON represents the structure of composer.json for testing.
type composerJSON struct {
	Name    string            `json:"name"`
	Require map[string]string `json:"require"`
}

// getPackageConstraint reads the composer.json and returns the version constraint for a package.
func getPackageConstraint(t *testing.T, projectDir, packageName string) string { //nolint:unparam // packageName may vary in future tests
	t.Helper()

	data, err := os.ReadFile(filepath.Join(projectDir, "composer.json"))
	require.NoError(t, err)

	var cj composerJSON
	err = json.Unmarshal(data, &cj)
	require.NoError(t, err)

	return cj.Require[packageName]
}

// setupPinnedProject creates a temporary PHP project with an exact pinned version.
// It runs composer install to generate a valid composer.lock.
func setupPinnedProject(t *testing.T, packageName, pinnedVersion string) string {
	t.Helper()

	tmpDir := t.TempDir()

	// Create composer.json with exact pinned version
	cj := composerJSON{
		Name:    "test/pinned-test",
		Require: map[string]string{packageName: pinnedVersion},
	}
	data, err := json.MarshalIndent(cj, "", "    ")
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(tmpDir, "composer.json"), data, 0o644)
	require.NoError(t, err)

	// Run composer install to generate lock file
	ctx := context.Background()
	cmd := exec.CommandContext(ctx, "composer", "install", "--no-interaction")
	cmd.Dir = tmpDir
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "composer install failed: %s", string(output))

	return tmpDir
}

// TestComposerUpdateEndToEnd tests the full update flow using the BuildTool interface.
// This is the primary use case: user wants to bump a package to a specific version.
func TestComposerUpdateEndToEnd(t *testing.T) {
	if !composerAvailable() {
		t.Skip("composer not available, skipping integration test")
	}

	projectDir := setupTestProject(t)

	// Verify initial state
	initialVersion := getPackageVersion(t, projectDir, "monolog/monolog")
	require.Equal(t, "3.4.0", initialVersion, "Initial monolog version should be 3.4.0")

	// Create the Composer build tool handler
	c := &Composer{}
	ctx := context.Background()

	// Verify detection works
	detected, err := c.Detect(ctx, projectDir)
	require.NoError(t, err)
	require.True(t, detected, "Should detect composer project")

	// Perform the update using the BuildTool interface
	cfg := &languages.UpdateConfig{
		RootDir: projectDir,
		Dependencies: []languages.Dependency{
			{Name: "monolog/monolog", Version: "3.5.0"},
		},
		Options: map[string]any{
			"noInstall": true, // Skip package installation (we just want lock file update)
		},
	}

	err = c.Update(ctx, cfg)
	require.NoError(t, err)

	// Verify the update was applied
	updatedVersion := getPackageVersion(t, projectDir, "monolog/monolog")
	assert.Equal(t, "3.5.0", updatedVersion, "Monolog should be updated to 3.5.0")

	// Verify validation passes
	err = c.Validate(ctx, cfg)
	require.NoError(t, err)
}

// TestComposerUpdateMultiplePackages tests updating multiple packages in one operation.
func TestComposerUpdateMultiplePackages(t *testing.T) {
	if !composerAvailable() {
		t.Skip("composer not available, skipping integration test")
	}

	projectDir := setupTestProject(t)

	c := &Composer{}
	ctx := context.Background()

	// Update multiple packages
	cfg := &languages.UpdateConfig{
		RootDir: projectDir,
		Dependencies: []languages.Dependency{
			{Name: "monolog/monolog", Version: "3.5.0"},
			{Name: "symfony/console", Version: "6.4.1"},
		},
		Options: map[string]any{
			"noInstall": true,
		},
	}

	err := c.Update(ctx, cfg)
	require.NoError(t, err)

	// Verify both updates were applied
	assert.Equal(t, "3.5.0", getPackageVersion(t, projectDir, "monolog/monolog"))
	assert.Equal(t, "6.4.1", getPackageVersion(t, projectDir, "symfony/console"))
}

// TestComposerUpdateSkipsDowngrade verifies that downgrades are skipped.
func TestComposerUpdateSkipsDowngrade(t *testing.T) {
	if !composerAvailable() {
		t.Skip("composer not available, skipping integration test")
	}

	projectDir := setupTestProject(t)

	c := &Composer{}
	ctx := context.Background()

	// Try to "update" to an older version
	cfg := &languages.UpdateConfig{
		RootDir: projectDir,
		Dependencies: []languages.Dependency{
			{Name: "monolog/monolog", Version: "3.3.0"}, // Older than 3.4.0
		},
		Options: map[string]any{
			"noInstall": true,
		},
	}

	err := c.Update(ctx, cfg)
	require.NoError(t, err) // Should succeed (skip with warning, not error)

	// Verify the version was NOT changed
	version := getPackageVersion(t, projectDir, "monolog/monolog")
	assert.Equal(t, "3.4.0", version, "Version should remain unchanged when downgrade requested")
}

// TestComposerUpdateSkipsCurrentVersion verifies no-op when already at target version.
func TestComposerUpdateSkipsCurrentVersion(t *testing.T) {
	if !composerAvailable() {
		t.Skip("composer not available, skipping integration test")
	}

	projectDir := setupTestProject(t)

	c := &Composer{}
	ctx := context.Background()

	// "Update" to the same version
	cfg := &languages.UpdateConfig{
		RootDir: projectDir,
		Dependencies: []languages.Dependency{
			{Name: "monolog/monolog", Version: "3.4.0"}, // Same as current
		},
		Options: map[string]any{
			"noInstall": true,
		},
	}

	err := c.Update(ctx, cfg)
	require.NoError(t, err)

	// Verify version is unchanged
	version := getPackageVersion(t, projectDir, "monolog/monolog")
	assert.Equal(t, "3.4.0", version)
}

// TestComposerUpdatePinnedVersion verifies that exact pinned versions in composer.json
// are updated along with composer.lock when bumping to a new version.
func TestComposerUpdatePinnedVersion(t *testing.T) {
	if !composerAvailable() {
		t.Skip("composer not available, skipping integration test")
	}

	// Create project with exact pinned version
	projectDir := setupPinnedProject(t, "monolog/monolog", "3.5.0")

	// Verify initial state
	initialConstraint := getPackageConstraint(t, projectDir, "monolog/monolog")
	require.Equal(t, "3.5.0", initialConstraint, "Initial constraint should be exact 3.5.0")

	initialVersion := getPackageVersion(t, projectDir, "monolog/monolog")
	require.Equal(t, "3.5.0", initialVersion, "Initial lock version should be 3.5.0")

	c := &Composer{}
	ctx := context.Background()

	// Update to a newer version
	cfg := &languages.UpdateConfig{
		RootDir: projectDir,
		Dependencies: []languages.Dependency{
			{Name: "monolog/monolog", Version: "3.6.0"},
		},
		Options: map[string]any{
			"noInstall": true,
		},
	}

	err := c.Update(ctx, cfg)
	require.NoError(t, err)

	// Verify composer.json constraint was updated
	updatedConstraint := getPackageConstraint(t, projectDir, "monolog/monolog")
	assert.Equal(t, "3.6.0", updatedConstraint, "composer.json constraint should be updated to 3.6.0")

	// Verify composer.lock version was updated
	updatedVersion := getPackageVersion(t, projectDir, "monolog/monolog")
	assert.Equal(t, "3.6.0", updatedVersion, "composer.lock version should be updated to 3.6.0")
}

// TestComposerUpdateFromRangeToExact verifies that updating from a range constraint
// to a specific newer version works correctly.
// Note: With a range like ^3.5.0, composer resolves to the latest matching version.
// This test creates a project with an older pinned version, then verifies the update
// changes both the constraint and the lock file.
func TestComposerUpdateFromRangeToExact(t *testing.T) {
	if !composerAvailable() {
		t.Skip("composer not available, skipping integration test")
	}

	// Create project with an older exact version first
	projectDir := setupPinnedProject(t, "monolog/monolog", "3.5.0")

	// Verify initial state
	initialConstraint := getPackageConstraint(t, projectDir, "monolog/monolog")
	require.Equal(t, "3.5.0", initialConstraint, "Initial constraint should be 3.5.0")

	initialVersion := getPackageVersion(t, projectDir, "monolog/monolog")
	require.Equal(t, "3.5.0", initialVersion, "Initial lock version should be 3.5.0")

	c := &Composer{}
	ctx := context.Background()

	// Update to a newer specific version
	cfg := &languages.UpdateConfig{
		RootDir: projectDir,
		Dependencies: []languages.Dependency{
			{Name: "monolog/monolog", Version: "3.7.0"},
		},
		Options: map[string]any{
			"noInstall": true,
		},
	}

	err := c.Update(ctx, cfg)
	require.NoError(t, err)

	// Verify composer.json constraint was updated to the new exact version
	updatedConstraint := getPackageConstraint(t, projectDir, "monolog/monolog")
	assert.Equal(t, "3.7.0", updatedConstraint, "composer.json constraint should be updated to 3.7.0")

	// Verify composer.lock version was updated
	updatedVersion := getPackageVersion(t, projectDir, "monolog/monolog")
	assert.Equal(t, "3.7.0", updatedVersion, "composer.lock version should be updated to 3.7.0")
}

// TestComposerUpdateVersionRangeSkipsDowngrade verifies that when the lock file has a
// version newer than requested, the update is skipped even with a range constraint.
func TestComposerUpdateVersionRangeSkipsDowngrade(t *testing.T) {
	if !composerAvailable() {
		t.Skip("composer not available, skipping integration test")
	}

	// Create project with version range that will resolve to latest (e.g., 3.10.0+)
	projectDir := setupPinnedProject(t, "monolog/monolog", "^3.5.0")

	// Get the resolved version from lock file
	initialVersion := getPackageVersion(t, projectDir, "monolog/monolog")
	require.NotEmpty(t, initialVersion, "Should have a resolved version")
	t.Logf("Initial resolved version: %s", initialVersion)

	c := &Composer{}
	ctx := context.Background()

	// Try to "update" to an older version than what's in the lock file
	cfg := &languages.UpdateConfig{
		RootDir: projectDir,
		Dependencies: []languages.Dependency{
			{Name: "monolog/monolog", Version: "3.7.0"}, // Older than resolved 3.10.0
		},
		Options: map[string]any{
			"noInstall": true,
		},
	}

	err := c.Update(ctx, cfg)
	require.NoError(t, err) // Should succeed (skip with warning, not error)

	// Verify the version was NOT changed in lock file
	finalVersion := getPackageVersion(t, projectDir, "monolog/monolog")
	assert.Equal(t, initialVersion, finalVersion, "Version should remain unchanged when downgrade requested")

	// Verify the constraint in composer.json was NOT changed
	finalConstraint := getPackageConstraint(t, projectDir, "monolog/monolog")
	assert.Equal(t, "^3.5.0", finalConstraint, "Constraint should remain unchanged when downgrade skipped")
}
