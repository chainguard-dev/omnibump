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
)

// TestResolveIndirectDependency_WithReplaceDirective tests that indirect resolution
// is skipped when the dependency has a replace directive.
func TestResolveIndirectDependency_WithReplaceDirective(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// Create go.mod with replace directive for indirect dependency
	goModContent := `module test

go 1.21

require (
	github.com/example/parent v1.0.0
)

require (
	github.com/example/indirect v1.0.0 // indirect
)

replace github.com/example/indirect => github.com/example/indirect v1.0.0
`

	err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goModContent), 0o600)
	require.NoError(t, err)

	// Try to resolve the indirect dependency that has a replace directive
	resolution, err := ResolveIndirectDependency(ctx, tmpDir, "github.com/example/indirect", "v1.2.0")
	require.NoError(t, err)

	// Should detect it's indirect but NOT allow resolution
	assert.True(t, resolution.IsIndirect)
	assert.False(t, resolution.FallbackAllowed, "Should not allow fallback when replace directive exists")
	assert.Empty(t, resolution.PossibleBumps, "Should not find parent bumps when replace directive exists")
}

// TestHasReplaceDirective tests the hasReplaceDirective helper function.
func TestHasReplaceDirective(t *testing.T) {
	tests := []struct {
		name         string
		goModContent string
		packageName  string
		expected     bool
	}{
		{
			name: "has replace directive",
			goModContent: `module test

replace github.com/example/pkg => github.com/example/pkg v1.0.0
`,
			packageName: "github.com/example/pkg",
			expected:    true,
		},
		{
			name: "no replace directive",
			goModContent: `module test

require github.com/example/pkg v1.0.0
`,
			packageName: "github.com/example/pkg",
			expected:    false,
		},
		{
			name: "replace different package",
			goModContent: `module test

replace github.com/example/other => github.com/example/other v1.0.0
`,
			packageName: "github.com/example/pkg",
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modFile, err := modfile.Parse("go.mod", []byte(tt.goModContent), nil)
			require.NoError(t, err)

			result := hasReplaceDirective(modFile, tt.packageName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestHasReplaceConflicts tests the hasReplaceConflicts helper function.
func TestHasReplaceConflicts(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name             string
		parentGoMod      string
		userReplaceMap   map[string]string
		expectedConflict bool
	}{
		{
			name: "no conflict - parent requires same or older version",
			parentGoMod: `module parent

require (
	k8s.io/api v0.32.0
)
`,
			userReplaceMap: map[string]string{
				"k8s.io/api": "v0.32.11",
			},
			expectedConflict: false,
		},
		{
			name: "conflict - parent requires newer version than replace",
			parentGoMod: `module parent

require (
	k8s.io/api v0.35.2
)
`,
			userReplaceMap: map[string]string{
				"k8s.io/api": "v0.32.11",
			},
			expectedConflict: true,
		},
		{
			name: "no conflict - no overlapping dependencies",
			parentGoMod: `module parent

require (
	github.com/example/pkg v1.0.0
)
`,
			userReplaceMap: map[string]string{
				"k8s.io/api": "v0.32.11",
			},
			expectedConflict: false,
		},
		{
			name: "multiple conflicts",
			parentGoMod: `module parent

require (
	k8s.io/api v0.35.2
	k8s.io/apimachinery v0.35.2
)
`,
			userReplaceMap: map[string]string{
				"k8s.io/api":          "v0.32.11",
				"k8s.io/apimachinery": "v0.32.11",
			},
			expectedConflict: true,
		},
		{
			name: "conflict - parent uses v0.0.0 placeholder (k8s.io/kubernetes case)",
			parentGoMod: `module k8s.io/kubernetes

require (
	k8s.io/api v0.0.0
	k8s.io/apimachinery v0.0.0
	k8s.io/client-go v0.0.0
)

replace (
	k8s.io/api => ./staging/src/k8s.io/api
	k8s.io/apimachinery => ./staging/src/k8s.io/apimachinery
	k8s.io/client-go => ./staging/src/k8s.io/client-go
)
`,
			userReplaceMap: map[string]string{
				"k8s.io/api":          "v0.32.11",
				"k8s.io/apimachinery": "v0.32.11",
				"k8s.io/client-go":    "v0.32.11",
			},
			expectedConflict: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parentModFile, err := modfile.Parse("go.mod", []byte(tt.parentGoMod), nil)
			require.NoError(t, err)

			result := hasReplaceConflicts(ctx, parentModFile, tt.userReplaceMap)
			assert.Equal(t, tt.expectedConflict, result)
		})
	}
}

// TestResolveIndirectDependency_SkipsParentsWithReplaceConflicts tests that
// parent versions with replace conflicts are skipped.
func TestResolveIndirectDependency_SkipsParentsWithReplaceConflicts(t *testing.T) {
	t.Skip("Integration test - requires real packages from proxy")

	ctx := context.Background()
	tmpDir := t.TempDir()

	// Simulate calico's go.mod with k8s replace directives
	goModContent := `module github.com/projectcalico/calico

go 1.21

require (
	k8s.io/kubernetes v1.32.11
)

require (
	go.opentelemetry.io/otel/sdk v1.34.0 // indirect
)

// Pin k8s deps to v0.32.11
replace k8s.io/api => k8s.io/api v0.32.11
replace k8s.io/apimachinery => k8s.io/apimachinery v0.32.11
replace k8s.io/client-go => k8s.io/client-go v0.32.11
`

	err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goModContent), 0o600)
	require.NoError(t, err)

	// Try to resolve otel/sdk@v1.40.0
	// Should find that k8s.io/kubernetes v1.36.0-alpha.2 has it
	// But should skip it due to replace conflicts
	resolution, err := ResolveIndirectDependency(ctx, tmpDir, "go.opentelemetry.io/otel/sdk", "v1.40.0")
	require.NoError(t, err)

	// Should NOT recommend k8s.io/kubernetes bump due to replace conflicts
	assert.True(t, resolution.IsIndirect)
	// Either no bumps found, or the bumps don't include problematic versions
	if len(resolution.PossibleBumps) > 0 {
		// Verify none of the bumps would conflict
		for _, bump := range resolution.PossibleBumps {
			// Would need to fetch and verify - this is just a structure test
			t.Logf("Found bump: %s %s -> %s", bump.Package, bump.FromVersion, bump.ToVersion)
		}
	}
}
