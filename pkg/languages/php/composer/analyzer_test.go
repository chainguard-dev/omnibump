/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package composer

import (
	"context"
	"testing"

	"github.com/chainguard-dev/omnibump/pkg/analyzer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyzerAnalyze(t *testing.T) {
	ca := &Analyzer{}
	ctx := context.Background()

	result, err := ca.Analyze(ctx, "testdata")
	require.NoError(t, err)

	assert.Equal(t, "php", result.Language)
	assert.Len(t, result.Dependencies, 5)

	// Check specific dependency
	monolog, ok := result.Dependencies["monolog/monolog"]
	require.True(t, ok)
	assert.Equal(t, "monolog/monolog", monolog.Name)
	assert.Equal(t, "3.4.0", monolog.Version)
	assert.False(t, monolog.UsesProperty)
	assert.Equal(t, "direct", monolog.UpdateStrategy)

	// Check metadata
	totalPkgs, ok := result.Metadata["totalPackages"].(int)
	require.True(t, ok)
	assert.Equal(t, 5, totalPkgs)
}

func TestAnalyzerAnalyzeFile(t *testing.T) {
	ca := &Analyzer{}
	ctx := context.Background()

	// Test with direct file path
	result, err := ca.Analyze(ctx, "testdata/composer.lock")
	require.NoError(t, err)
	assert.Len(t, result.Dependencies, 5)
}

func TestAnalyzerRecommendStrategy(t *testing.T) {
	ca := &Analyzer{}
	ctx := context.Background()

	// First analyze
	analysis, err := ca.Analyze(ctx, "testdata")
	require.NoError(t, err)

	deps := []analyzer.Dependency{
		{Name: "monolog/monolog", Version: "3.5.0"},
		{Name: "nonexistent/package", Version: "1.0.0"},
	}

	strategy, err := ca.RecommendStrategy(ctx, analysis, deps)
	require.NoError(t, err)

	// monolog should be in direct updates
	assert.Len(t, strategy.DirectUpdates, 1)
	assert.Equal(t, "monolog/monolog", strategy.DirectUpdates[0].Name)

	// nonexistent package should generate a warning
	assert.Len(t, strategy.Warnings, 1)
	assert.Contains(t, strategy.Warnings[0], "nonexistent/package")
}
