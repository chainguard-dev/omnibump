/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package python

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/chainguard-dev/omnibump/pkg/languages"
)

// --- parseAndValidateVenvSpecs ---

func TestParseAndValidateVenvSpecs_Valid(t *testing.T) {
	deps := []languages.Dependency{
		{Name: "cryptography", Version: "==46.0.6"},
		{Name: "pyjwt", Version: "==2.12.0"},
		{Name: "requests", Version: "==2.33.0"},
	}

	specs, err := parseAndValidateVenvSpecs(deps)
	require.NoError(t, err)
	require.Len(t, specs, 3)

	assert.Equal(t, "cryptography", specs[0].Name)
	assert.Equal(t, "46.0.6", specs[0].Version)
	assert.Equal(t, "pyjwt", specs[1].Name)
	assert.Equal(t, "2.12.0", specs[1].Version)
	assert.Equal(t, "requests", specs[2].Name)
	assert.Equal(t, "2.33.0", specs[2].Version)
}

func TestParseAndValidateVenvSpecs_RejectsNonEqualsPin(t *testing.T) {
	deps := []languages.Dependency{
		{Name: "urllib3", Version: "~=2.6.0"},
	}

	_, err := parseAndValidateVenvSpecs(deps)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires == pinning")
}

func TestParseAndValidateVenvSpecs_RejectsGreaterThanPin(t *testing.T) {
	deps := []languages.Dependency{
		{Name: "authlib", Version: ">=1.3.1"},
	}

	_, err := parseAndValidateVenvSpecs(deps)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires == pinning")
}

func TestParseAndValidateVenvSpecs_RejectsCaretPin(t *testing.T) {
	deps := []languages.Dependency{
		{Name: "protobuf", Version: "^5.29.6"},
	}

	_, err := parseAndValidateVenvSpecs(deps)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires == pinning")
}

func TestParseAndValidateVenvSpecs_EmptyVersion(t *testing.T) {
	deps := []languages.Dependency{
		{Name: "requests", Version: "=="},
	}

	_, err := parseAndValidateVenvSpecs(deps)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty version")
}

func TestParseAndValidateVenvSpecs_EmptyDeps(t *testing.T) {
	specs, err := parseAndValidateVenvSpecs([]languages.Dependency{})
	require.NoError(t, err)
	assert.Len(t, specs, 0)
}

// --- isVersionLower ---

func TestIsVersionLower_SimpleComparison(t *testing.T) {
	tests := []struct {
		v1, v2 string
		want   bool
	}{
		// v1 < v2 → true
		{"1.0.0", "2.0.0", true},
		{"2.0.0", "2.1.0", true},
		{"2.1.0", "2.1.1", true},
		{"40.0.0", "46.0.6", true},
		// v1 >= v2 → false
		{"2.0.0", "1.0.0", false},
		{"2.1.0", "2.0.0", false},
		{"2.1.1", "2.1.0", false},
		// v1 == v2 → false
		{"1.0.0", "1.0.0", false},
		{"46.0.6", "46.0.6", false},
		// Version with different segment counts
		{"1.0", "1.0.1", true},
		{"1.0.0", "1.0.1", true},
		{"1", "2", true},
		{"1.2", "1.2.0", false}, // effectively equal after padding
	}

	for _, tt := range tests {
		t.Run(tt.v1+"-vs-"+tt.v2, func(t *testing.T) {
			got := isVersionLower(tt.v1, tt.v2)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsVersionLower_DowngradeDetection(t *testing.T) {
	// Real CVE remediation scenario: current version vs target
	// If target < current, it's a downgrade
	tests := []struct {
		current, target string
		isDowngrade    bool
	}{
		{"46.0.6", "44.0.0", true},  // downgrade
		{"46.0.6", "46.0.6", false}, // same version
		{"46.0.6", "47.0.0", false}, // upgrade
		{"2.12.0", "2.11.0", true},  // downgrade
		{"2.12.0", "2.12.0", false}, // same
		{"2.12.0", "2.13.0", false}, // upgrade
	}

	for _, tt := range tests {
		t.Run(tt.current+"-to-"+tt.target, func(t *testing.T) {
			// isVersionLower(target, current) == true means downgrade
			isDowngrade := isVersionLower(tt.target, tt.current)
			assert.Equal(t, tt.isDowngrade, isDowngrade)
		})
	}
}

// --- selectVenvInstaller ---

func TestSelectVenvInstaller_ExplicitUV(t *testing.T) {
	installer, err := selectVenvInstaller("/tmp/venv", "uv")
	require.NoError(t, err)
	assert.Equal(t, "uv", installer.name)
}

func TestSelectVenvInstaller_ExplicitPip(t *testing.T) {
	installer, err := selectVenvInstaller("/tmp/venv", "pip")
	require.NoError(t, err)
	assert.Equal(t, "pip", installer.name)
}

func TestSelectVenvInstaller_AutoDetect(t *testing.T) {
	// Auto-detect with empty hint
	// Will return uv if in PATH, else pip
	installer, err := selectVenvInstaller("/tmp/venv", "")
	require.NoError(t, err)
	// Should be one of these
	assert.True(t, installer.name == "uv" || installer.name == "pip")
}

// --- Spec parsing integration ---

func TestParseAndValidateVenvSpecs_AirflowPattern(t *testing.T) {
	// Real airflow-3 CVE remediation pattern
	deps := []languages.Dependency{
		{Name: "cryptography", Version: "==46.0.6"},   // GHSA-r6ph-v2qm-q3c2
		{Name: "pyjwt", Version: "==2.12.0"},          // CVE-2026-32597
		{Name: "pyopenssl", Version: "==26.0.0"},      // CVE-2026-27459
		{Name: "pygments", Version: "==2.20.0"},       // GHSA-5239-wwwm-4pmq
		{Name: "requests", Version: "==2.33.0"},       // GHSA-gc5v-m9x4-r6x2
	}

	specs, err := parseAndValidateVenvSpecs(deps)
	require.NoError(t, err)
	require.Len(t, specs, 5)

	// All should be validated and parsed
	for i, spec := range specs {
		assert.NotEmpty(t, spec.Name, "spec %d has empty name", i)
		assert.NotEmpty(t, spec.Version, "spec %d has empty version", i)
	}
}

func TestParseAndValidateVenvSpecs_MixedValid(t *testing.T) {
	// Multiple dependencies with various version formats
	deps := []languages.Dependency{
		{Name: "aiohttp", Version: "==3.13.4"},
		{Name: "litellm", Version: "==1.83.0"},
		{Name: "ecdsa", Version: "==0.19.2"},
	}

	specs, err := parseAndValidateVenvSpecs(deps)
	require.NoError(t, err)
	require.Len(t, specs, 3)

	pkgMap := make(map[string]venvSpecifier)
	for _, s := range specs {
		pkgMap[s.Name] = s
	}

	assert.Equal(t, "3.13.4", pkgMap["aiohttp"].Version)
	assert.Equal(t, "1.83.0", pkgMap["litellm"].Version)
	assert.Equal(t, "0.19.2", pkgMap["ecdsa"].Version)
}

// --- parseVersionNumber ---

func TestParseVersionNumber(t *testing.T) {
	tests := []struct {
		input string
		want  int
		err   bool
	}{
		{"0", 0, false},
		{"1", 1, false},
		{"10", 10, false},
		{"123", 123, false},
		{"46", 46, false},
		{"46rc1", 46, false},      // pre-release suffix ignored
		{"2a1", 2, false},         // alpha suffix ignored
		{"0beta", 0, false},       // beta suffix ignored
		{"", 0, true},             // empty string is error
		{"a1", 0, true},           // no leading digits is error
		{"rc1", 0, true},          // no leading digits is error
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseVersionNumber(tt.input)
			if tt.err {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

// --- Version comparison edge cases ---

func TestIsVersionLower_EdgeCases(t *testing.T) {
	tests := []struct {
		v1, v2 string
		want   bool
		desc   string
	}{
		{"0.0.1", "0.0.2", true, "patch bump"},
		{"0.1.0", "0.2.0", true, "minor bump"},
		{"1.0.0", "2.0.0", true, "major bump"},
		{"2.0", "2.0.0", false, "equivalent after padding"},
		{"2", "2.0", false, "equivalent after padding"},
		{"1.0", "1.0.0", false, "equivalent (major.minor vs major.minor.patch)"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got := isVersionLower(tt.v1, tt.v2)
			assert.Equal(t, tt.want, got)
		})
	}
}

// --- Validation error messages ---

func TestParseAndValidateVenvSpecs_ErrorMessages(t *testing.T) {
	tests := []struct {
		dep  languages.Dependency
		desc string
	}{
		{
			dep:  languages.Dependency{Name: "pkg", Version: "~=1.0.0"},
			desc: "compatible-release pin",
		},
		{
			dep:  languages.Dependency{Name: "pkg", Version: ">1.0.0"},
			desc: "greater-than pin",
		},
		{
			dep:  languages.Dependency{Name: "pkg", Version: "<1.0.0"},
			desc: "less-than pin",
		},
		{
			dep:  languages.Dependency{Name: "pkg", Version: "1.0.0"},
			desc: "no specifier",
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			_, err := parseAndValidateVenvSpecs([]languages.Dependency{tt.dep})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "requires ==")
		})
	}
}
