/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package composer

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLock(t *testing.T) {
	f, err := os.Open("testdata/composer.lock")
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	pkgs, err := ParseLock(f)
	require.NoError(t, err)

	// Verify we got all packages (4 regular + 1 dev)
	assert.Len(t, pkgs, 5)

	// Create a map for easier lookup
	pkgMap := make(map[string]LockPackage)
	for _, pkg := range pkgs {
		pkgMap[pkg.Name] = pkg
	}

	// Check monolog/monolog
	monolog, ok := pkgMap["monolog/monolog"]
	require.True(t, ok)
	assert.Equal(t, "3.4.0", monolog.Version)
	assert.Contains(t, monolog.Require, "psr/log")

	// Check symfony/console (version has v prefix which should be stripped)
	console, ok := pkgMap["symfony/console"]
	require.True(t, ok)
	assert.Equal(t, "6.4.0", console.Version)

	// Check dev dependency
	phpunit, ok := pkgMap["phpunit/phpunit"]
	require.True(t, ok)
	assert.Equal(t, "10.5.0", phpunit.Version)
}

func TestParseLockInvalid(t *testing.T) {
	r := strings.NewReader("not valid json")
	_, err := ParseLock(r)
	assert.Error(t, err)
}

func TestParseLockEmpty(t *testing.T) {
	r := strings.NewReader(`{"packages": [], "packages-dev": []}`)
	pkgs, err := ParseLock(r)
	require.NoError(t, err)
	assert.Len(t, pkgs, 0)
}

func TestNormalizeVersion(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"1.2.3", "1.2.3"},
		{"v1.2.3", "1.2.3"},
		{"v6.4.0", "6.4.0"},
		{"3.0.0", "3.0.0"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalizeVersion(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
