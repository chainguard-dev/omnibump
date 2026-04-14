/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package composer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeVersionForSemver(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"1.2.3", "v1.2.3"},
		{"v1.2.3", "v1.2.3"},
		{"1.2.3.0", "v1.2.3"},
		{"v1.2.3.0", "v1.2.3"},
		{"6.4.0", "v6.4.0"},
		{"3.0.0.1", "v3.0.0"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalizeVersionForSemver(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFindMatchingPackages(t *testing.T) {
	pkgs := []LockPackage{
		{Name: "monolog/monolog", Version: "3.4.0"},
		{Name: "psr/log", Version: "3.0.0"},
		{Name: "symfony/console", Version: "6.4.0"},
	}

	// Find existing package
	matches := findMatchingPackages("monolog/monolog", pkgs)
	assert.Len(t, matches, 1)
	assert.Equal(t, "3.4.0", matches[0].Version)

	// Find non-existing package
	matches = findMatchingPackages("nonexistent/package", pkgs)
	assert.Len(t, matches, 0)
}

func TestOrderPackages(t *testing.T) {
	packages := map[string]*Package{
		"pkg-c": {Name: "pkg-c", Index: 2},
		"pkg-a": {Name: "pkg-a", Index: 0},
		"pkg-b": {Name: "pkg-b", Index: 1},
	}

	ordered := orderPackages(packages)

	assert.Equal(t, []string{"pkg-a", "pkg-b", "pkg-c"}, ordered)
}
