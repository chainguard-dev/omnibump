/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package composer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/chainguard-dev/omnibump/pkg/languages"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComposerDetect(t *testing.T) {
	c := &Composer{}
	ctx := context.Background()

	// Test with testdata directory that has composer.lock
	detected, err := c.Detect(ctx, "testdata")
	require.NoError(t, err)
	assert.True(t, detected)

	// Test with directory that doesn't have composer.lock
	tmpDir := t.TempDir()
	detected, err = c.Detect(ctx, tmpDir)
	require.NoError(t, err)
	assert.False(t, detected)
}

func TestComposerName(t *testing.T) {
	c := &Composer{}
	assert.Equal(t, "composer", c.Name())
}

func TestComposerGetManifestFiles(t *testing.T) {
	c := &Composer{}
	files := c.GetManifestFiles()
	assert.Contains(t, files, "composer.json")
	assert.Contains(t, files, "composer.lock")
}

func TestComposerGetAnalyzer(t *testing.T) {
	c := &Composer{}
	a := c.GetAnalyzer()
	assert.NotNil(t, a)
	_, ok := a.(*Analyzer)
	assert.True(t, ok)
}

func TestComposerUpdateDryRun(t *testing.T) {
	c := &Composer{}
	ctx := context.Background()

	tmpDir := t.TempDir()

	// Copy testdata/composer.lock to temp dir
	lockContent, err := os.ReadFile("testdata/composer.lock")
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(tmpDir, "composer.lock"), lockContent, 0o644)
	require.NoError(t, err)

	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "monolog/monolog", Version: "3.5.0"},
		},
		DryRun:  true,
		Options: map[string]any{},
	}

	// Dry run should succeed without actually running composer
	err = c.Update(ctx, cfg)
	require.NoError(t, err)
}

func TestComposerUpdateMissingLockFile(t *testing.T) {
	c := &Composer{}
	ctx := context.Background()

	tmpDir := t.TempDir()

	cfg := &languages.UpdateConfig{
		RootDir: tmpDir,
		Dependencies: []languages.Dependency{
			{Name: "monolog/monolog", Version: "3.5.0"},
		},
		Options: map[string]any{},
	}

	err := c.Update(ctx, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "composer.lock not found")
}
