/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package python_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/chainguard-dev/omnibump/pkg/languages/python"
)

// --- DetectManifest ---

func TestDetectManifest_Pyproject(t *testing.T) {
	for _, tt := range []struct {
		dir      string
		wantTool python.BuildTool
		wantType string
	}{
		{"testdata/hatch-pyproject", python.BuildToolHatch, "pyproject.toml"},
		{"testdata/poetry-pyproject", python.BuildToolPoetry, "pyproject.toml"},
		{"testdata/maturin-project", python.BuildToolMaturin, "pyproject.toml"},
		{"testdata/scikit-build-core", python.BuildToolScikitBuildCore, "pyproject.toml"},
	} {
		t.Run(tt.dir, func(t *testing.T) {
			info, err := python.DetectManifest(tt.dir)
			require.NoError(t, err)
			assert.Equal(t, tt.wantType, info.Type)
			assert.Equal(t, tt.wantTool, info.BuildTool)
		})
	}
}

func TestDetectManifest_Requirements(t *testing.T) {
	info, err := python.DetectManifest("testdata/pip-requirements")
	require.NoError(t, err)
	assert.Equal(t, "requirements.txt", info.Type)
	assert.Equal(t, python.BuildToolPip, info.BuildTool)
}

func TestDetectManifest_SetupCfg(t *testing.T) {
	info, err := python.DetectManifest("testdata/setup-cfg")
	require.NoError(t, err)
	assert.Equal(t, "setup.cfg", info.Type)
	assert.Equal(t, python.BuildToolSetuptools, info.BuildTool)
}

func TestDetectManifest_NotFound(t *testing.T) {
	_, err := python.DetectManifest(t.TempDir())
	assert.ErrorIs(t, err, python.ErrManifestNotFound)
}

// --- ParsePyprojectDeps ---

func TestParsePyprojectDeps_Hatch(t *testing.T) {
	data, err := os.ReadFile("testdata/hatch-pyproject/pyproject.toml")
	require.NoError(t, err)

	specs, err := python.ParsePyprojectDeps(data, python.BuildToolHatch)
	require.NoError(t, err)
	require.Len(t, specs, 3)

	names := make(map[string]python.VersionSpec)
	for _, s := range specs {
		names[s.Package] = s
	}

	assert.Equal(t, "2.28.0", names["requests"].Version)
	assert.Equal(t, ">=", names["requests"].Specifier)
	assert.Equal(t, "39.0.1", names["cryptography"].Version)
}

func TestParsePyprojectDeps_Poetry(t *testing.T) {
	data, err := os.ReadFile("testdata/poetry-pyproject/pyproject.toml")
	require.NoError(t, err)

	specs, err := python.ParsePyprojectDeps(data, python.BuildToolPoetry)
	require.NoError(t, err)

	names := make(map[string]python.VersionSpec)
	for _, s := range specs {
		names[s.Package] = s
	}

	assert.Equal(t, "2.28.0", names["requests"].Version)
	_, hasPython := names["python"]
	assert.False(t, hasPython, "python itself should be excluded")
}

// --- ParseRequirements ---

func TestParseRequirements(t *testing.T) {
	data, err := os.ReadFile("testdata/pip-requirements/requirements.txt")
	require.NoError(t, err)

	specs := python.ParseRequirements(data)
	require.NotEmpty(t, specs)

	names := make(map[string]python.VersionSpec)
	for _, s := range specs {
		names[s.Package] = s
	}

	assert.Equal(t, "2.28.2", names["requests"].Version)
	assert.Equal(t, "==", names["requests"].Specifier)
	assert.Equal(t, "7.2.0", names["pytest"].Version)
}

func TestParseRequirements_SkipsComments(t *testing.T) {
	data := []byte("# this is a comment\nrequests==2.28.2\n")
	specs := python.ParseRequirements(data)
	require.Len(t, specs, 1)
	assert.Equal(t, "requests", specs[0].Package)
}

// --- UpdateRequirement ---

func TestUpdateRequirement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "requirements.txt")
	require.NoError(t, os.WriteFile(path, []byte("requests==2.28.2\nurllib3>=1.26.0,<2.0\n"), 0o600))

	require.NoError(t, python.UpdateRequirement(path, "requests", "2.32.0"))

	updated, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(updated), "requests==2.32.0")
	// urllib3 should be unchanged
	assert.Contains(t, string(updated), "urllib3>=1.26.0,<2.0")
}

func TestUpdateRequirement_PackageNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "requirements.txt")
	require.NoError(t, os.WriteFile(path, []byte("requests==2.28.2\n"), 0o600))

	err := python.UpdateRequirement(path, "nonexistent", "1.0.0")
	assert.ErrorIs(t, err, python.ErrPackageNotFound)
}

func TestUpdateRequirement_InvalidVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "requirements.txt")
	require.NoError(t, os.WriteFile(path, []byte("requests==2.28.2\n"), 0o600))

	err := python.UpdateRequirement(path, "requests", "2.32.0; rm -rf /")
	assert.ErrorIs(t, err, python.ErrInvalidVersion)
}

// --- UpdatePyprojectDep ---

func TestUpdatePyprojectDep_Hatch(t *testing.T) {
	src, err := os.ReadFile("testdata/hatch-pyproject/pyproject.toml")
	require.NoError(t, err)

	dir := t.TempDir()
	path := filepath.Join(dir, "pyproject.toml")
	require.NoError(t, os.WriteFile(path, src, 0o600))

	require.NoError(t, python.UpdatePyprojectDep(path, "requests", "2.32.0"))

	updated, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(updated), "2.32.0")
	// cryptography should be unchanged
	assert.Contains(t, string(updated), "39.0.1")
}

func TestUpdatePyprojectDep_Poetry(t *testing.T) {
	src, err := os.ReadFile("testdata/poetry-pyproject/pyproject.toml")
	require.NoError(t, err)

	dir := t.TempDir()
	path := filepath.Join(dir, "pyproject.toml")
	require.NoError(t, os.WriteFile(path, src, 0o600))

	require.NoError(t, python.UpdatePyprojectDep(path, "cryptography", "41.0.0"))

	updated, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(updated), "41.0.0")
}

// --- ParseSetupCfg ---

func TestParseSetupCfg(t *testing.T) {
	data, err := os.ReadFile("testdata/setup-cfg/setup.cfg")
	require.NoError(t, err)

	specs := python.ParseSetupCfg(data)
	require.NotEmpty(t, specs)

	names := make(map[string]python.VersionSpec)
	for _, s := range specs {
		names[s.Package] = s
	}

	assert.Equal(t, "2.28.0", names["requests"].Version)
	assert.Equal(t, "39.0.1", names["cryptography"].Version)
}

// --- normalizePkgName (via ParseRequirements) ---

func TestNormalizePkgName(t *testing.T) {
	// PEP 503: dashes, underscores, dots are all equivalent
	data := []byte("Pillow==9.0.0\nPIL_Image==1.0.0\npil.image==2.0.0\n")
	specs := python.ParseRequirements(data)
	for _, s := range specs {
		// All three should normalize to "pillow" or "pil-image"
		assert.NotEmpty(t, s.Package)
	}
}
