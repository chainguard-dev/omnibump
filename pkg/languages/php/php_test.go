/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package php

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/chainguard-dev/omnibump/pkg/languages"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPHPRegistered(t *testing.T) {
	// Verify that PHP is registered with the languages registry
	lang, err := languages.Get("php")
	require.NoError(t, err)
	assert.Equal(t, "php", lang.Name())
}

func TestPHPName(t *testing.T) {
	p := &PHP{}
	assert.Equal(t, "php", p.Name())
}

func TestPHPDetect(t *testing.T) {
	p := &PHP{}
	ctx := context.Background()

	// Create a temp directory with composer.lock
	tmpDir := t.TempDir()
	err := os.WriteFile(filepath.Join(tmpDir, "composer.lock"), []byte("{}"), 0o644)
	require.NoError(t, err)

	// Should detect PHP project
	detected, err := p.Detect(ctx, tmpDir)
	require.NoError(t, err)
	assert.True(t, detected)

	// Test with directory that doesn't have any PHP build tool files
	emptyDir := t.TempDir()
	detected, err = p.Detect(ctx, emptyDir)
	require.NoError(t, err)
	assert.False(t, detected)
}

func TestPHPGetManifestFiles(t *testing.T) {
	p := &PHP{}
	files := p.GetManifestFiles()
	assert.Contains(t, files, "composer.json")
	assert.Contains(t, files, "composer.lock")
}

func TestPHPSupportsAnalysis(t *testing.T) {
	p := &PHP{}
	assert.True(t, p.SupportsAnalysis())
}

func TestPHPGetBuildTool(t *testing.T) {
	p := &PHP{}
	ctx := context.Background()

	// Create a temp directory with composer.lock
	tmpDir := t.TempDir()
	err := os.WriteFile(filepath.Join(tmpDir, "composer.lock"), []byte("{}"), 0o644)
	require.NoError(t, err)

	buildTool, err := p.GetBuildTool(ctx, tmpDir)
	require.NoError(t, err)
	assert.Equal(t, "composer", buildTool.Name())
}

func TestPHPGetBuildToolNotFound(t *testing.T) {
	p := &PHP{}
	ctx := context.Background()

	// Test with directory that doesn't have any PHP build tool files
	emptyDir := t.TempDir()
	_, err := p.GetBuildTool(ctx, emptyDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no supported PHP build tool found")
}
