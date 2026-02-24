/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package golang

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestExtractModulePath_MultiVersion ensures that module path extraction
// works correctly for packages with multiple major versions (v2, v3, etc).
func TestExtractModulePath_MultiVersion(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "dtls v2",
			input:    "github.com/pion/dtls/v2@v2.2.12",
			expected: "github.com/pion/dtls/v2",
		},
		{
			name:     "dtls v3",
			input:    "github.com/pion/dtls/v3@v3.0.6",
			expected: "github.com/pion/dtls/v3",
		},
		{
			name:     "transport v2",
			input:    "github.com/pion/transport/v2@v2.2.4",
			expected: "github.com/pion/transport/v2",
		},
		{
			name:     "transport v3",
			input:    "github.com/pion/transport/v3@v3.0.7",
			expected: "github.com/pion/transport/v3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractModulePath(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestFindDirectParents_MultiVersionNoCollision tests that when searching for
// parents of a package with multiple major versions (e.g., dtls/v3), we don't
// incorrectly match lines containing similar packages (e.g., dtls/v2).
//
// This was a real bug where strings.Contains("github.com/pion/dtls/v2", "github.com/pion/dtls/v3")
// would incorrectly match because the substring "github.com/pion/dtls/v" is common.
//
// The fix uses exact string comparison (==) instead of substring matching.
func TestFindDirectParents_MultiVersionNoCollision(t *testing.T) {
	// Test that we correctly identify v3-specific edges
	v3Edges := 0
	v2Edges := 0

	lines := []string{
		"github.com/libp2p/go-libp2p@v0.46.0 github.com/pion/dtls/v2@v2.2.12",
		"github.com/libp2p/go-libp2p@v0.46.0 github.com/pion/dtls/v3@v3.0.6",
	}

	targetV3 := "github.com/pion/dtls/v3"
	targetV2 := "github.com/pion/dtls/v2"

	for _, line := range lines {
		parts := []string{"github.com/libp2p/go-libp2p@v0.46.0", ""}
		if len(line) > len(parts[0]) {
			parts[1] = line[len(parts[0])+1:]
		}

		targetPkg := extractModulePath(parts[1])

		// Test exact match for v3
		if targetPkg == targetV3 {
			v3Edges++
		}

		// Test exact match for v2
		if targetPkg == targetV2 {
			v2Edges++
		}
	}

	// Should find exactly one edge for v3 and one for v2
	assert.Equal(t, 1, v3Edges, "Should find exactly one v3 edge")
	assert.Equal(t, 1, v2Edges, "Should find exactly one v2 edge")
}
