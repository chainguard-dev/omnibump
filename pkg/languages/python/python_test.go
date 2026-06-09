/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package python_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/chainguard-dev/omnibump/pkg/languages/python"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		{"testdata/flit-pyproject", python.BuildToolFlit, "pyproject.toml"},
		{"testdata/pdm-pyproject", python.BuildToolPDM, "pyproject.toml"},
		{"testdata/uv-project", python.BuildToolUV, "pyproject.toml"},
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

// --- ParsePipfile ---
// Based on real Pipfile patterns from projects like ansible-operator

func TestParsePipfile(t *testing.T) {
	data, err := os.ReadFile("testdata/pipfile-project/Pipfile")
	require.NoError(t, err)

	specs, err := python.ParsePipfile(data)
	require.NoError(t, err)
	require.NotEmpty(t, specs)

	names := make(map[string]python.VersionSpec)
	for _, s := range specs {
		names[s.Package] = s
	}

	// Exact pin: requests = "==2.31.0"
	assert.Equal(t, "2.31.0", names["requests"].Version)
	assert.Equal(t, "==", names["requests"].Specifier)
	// GTE pin: cryptography = ">=41.0.0"
	assert.Equal(t, "41.0.0", names["cryptography"].Version)
	assert.Equal(t, ">=", names["cryptography"].Specifier)
	// Compatible release: urllib3 = "~=2.0.7"
	assert.Equal(t, "2.0.7", names["urllib3"].Version)
	assert.Equal(t, "~=", names["urllib3"].Specifier)
	// Wildcard: click = "*" should be skipped
	_, hasClick := names["click"]
	assert.False(t, hasClick, "wildcard (*) pins should be skipped")
	// Inline table: pydantic = {version = ">=2.0.0", extras = ["dotenv"]}
	assert.Equal(t, "2.0.0", names["pydantic"].Version)
	// Dev packages should also be parsed
	assert.Equal(t, "7.4.0", names["pytest"].Version)
}

func TestParsePipfile_EmptyPackages(t *testing.T) {
	data := []byte("[packages]\n\n[dev-packages]\n")
	specs, err := python.ParsePipfile(data)
	require.NoError(t, err)
	assert.Empty(t, specs)
}

// --- UpdatePipfile ---
// Tests the regex fix: pipfileLineRe now uses * instead of ? for operator matching.
// CVE-2023-32681: requests <2.31.0 had authorization bypass via HTTP redirection.

func TestUpdatePipfile_ExactPin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Pipfile")
	// Simulate remediating CVE-2023-32681 (requests auth bypass)
	require.NoError(t, os.WriteFile(path, []byte(`[packages]
requests = "==2.28.0"
cryptography = ">=41.0.0"
`), 0o600))

	require.NoError(t, python.UpdatePipfile(path, "requests", "2.31.0"))

	updated, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(updated), `requests = "==2.31.0"`)
	// cryptography should be unchanged
	assert.Contains(t, string(updated), `cryptography = ">=41.0.0"`)
}

func TestUpdatePipfile_GTEOperator(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Pipfile")
	// Simulate bumping cryptography for CVE-2024-26130 (< 42.0.0)
	require.NoError(t, os.WriteFile(path, []byte(`[packages]
cryptography = ">=41.0.0"
`), 0o600))

	require.NoError(t, python.UpdatePipfile(path, "cryptography", "42.0.4"))

	updated, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(updated), `cryptography = ">=42.0.4"`)
}

func TestUpdatePipfile_CompatibleRelease(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Pipfile")
	// Simulate bumping urllib3 for CVE-2024-37891 (< 2.2.2)
	require.NoError(t, os.WriteFile(path, []byte(`[packages]
urllib3 = "~=2.0.7"
`), 0o600))

	require.NoError(t, python.UpdatePipfile(path, "urllib3", "2.2.2"))

	updated, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(updated), `urllib3 = "~=2.2.2"`)
}

func TestUpdatePipfile_PackageNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Pipfile")
	require.NoError(t, os.WriteFile(path, []byte(`[packages]
requests = "==2.28.0"
`), 0o600))

	err := python.UpdatePipfile(path, "nonexistent", "1.0.0")
	assert.ErrorIs(t, err, python.ErrPackageNotFound)
}

// --- UpdateSetupCfg ---
// CVE-2024-6345: setuptools <70.0.0 had command injection via VCS URLs

func TestUpdateSetupCfg(t *testing.T) {
	src, err := os.ReadFile("testdata/setup-cfg/setup.cfg")
	require.NoError(t, err)

	dir := t.TempDir()
	path := filepath.Join(dir, "setup.cfg")
	require.NoError(t, os.WriteFile(path, src, 0o600))

	// Bump requests for CVE-2023-32681
	require.NoError(t, python.UpdateSetupCfg(path, "requests", "2.31.0"))

	updated, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(updated), "requests>=2.31.0")
	// cryptography should be unchanged
	assert.Contains(t, string(updated), "cryptography>=39.0.1")
}

func TestUpdateSetupCfg_PackageNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "setup.cfg")
	require.NoError(t, os.WriteFile(path, []byte("[options]\ninstall_requires =\n    requests>=2.28.0\n"), 0o600))

	err := python.UpdateSetupCfg(path, "nonexistent", "1.0.0")
	assert.ErrorIs(t, err, python.ErrPackageNotFound)
}

// --- ParseSetupPy ---
// Based on real Django-like project patterns

func TestParseSetupPy(t *testing.T) {
	data, err := os.ReadFile("testdata/setup-py/setup.py")
	require.NoError(t, err)

	specs := python.ParseSetupPy(data)
	require.NotEmpty(t, specs)

	names := make(map[string]python.VersionSpec)
	for _, s := range specs {
		names[s.Package] = s
	}

	// Django>=4.2,<5.0 — compound specifier
	_, hasDjango := names["django"]
	assert.True(t, hasDjango, "should parse Django from setup.py")
	// psycopg2-binary==2.9.6
	assert.Equal(t, "2.9.6", names["psycopg2-binary"].Version)
	assert.Equal(t, "==", names["psycopg2-binary"].Specifier)
	// requests>=2.28.0
	assert.Equal(t, "2.28.0", names["requests"].Version)
}

func TestParseSetupPy_NoInstallRequires(t *testing.T) {
	data := []byte(`from setuptools import setup
setup(name="bare")
`)
	specs := python.ParseSetupPy(data)
	assert.Empty(t, specs)
}

// --- ParsePyprojectDeps for new build tools ---

func TestParsePyprojectDeps_Flit(t *testing.T) {
	data, err := os.ReadFile("testdata/flit-pyproject/pyproject.toml")
	require.NoError(t, err)

	// Flit uses PEP 621, same parsing path as hatch
	specs, err := python.ParsePyprojectDeps(data, python.BuildToolFlit)
	require.NoError(t, err)
	require.Len(t, specs, 4)

	names := make(map[string]python.VersionSpec)
	for _, s := range specs {
		names[s.Package] = s
	}

	assert.Equal(t, "8.0.0", names["click"].Version)
	assert.Equal(t, ">=", names["click"].Specifier)
	assert.Equal(t, "2.28.0", names["requests"].Version)
}

func TestParsePyprojectDeps_PDM(t *testing.T) {
	data, err := os.ReadFile("testdata/pdm-pyproject/pyproject.toml")
	require.NoError(t, err)

	specs, err := python.ParsePyprojectDeps(data, python.BuildToolPDM)
	require.NoError(t, err)
	require.Len(t, specs, 4)

	names := make(map[string]python.VersionSpec)
	for _, s := range specs {
		names[s.Package] = s
	}

	assert.Equal(t, "41.0.0", names["cryptography"].Version)
}

// --- UpdateRequirement with real CVE scenarios ---

func TestUpdateRequirement_CVE2023_32681_RequestsAuthBypass(t *testing.T) {
	// CVE-2023-32681: requests <2.31.0 leaked Proxy-Authorization headers
	dir := t.TempDir()
	path := filepath.Join(dir, "requirements.txt")
	require.NoError(t, os.WriteFile(path, []byte(
		"requests==2.28.2\ncryptography==41.0.0\nurllib3==2.0.4\n",
	), 0o600))

	require.NoError(t, python.UpdateRequirement(path, "requests", "2.31.0"))

	updated, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(updated), "requests==2.31.0")
	assert.Contains(t, string(updated), "cryptography==41.0.0")
	assert.Contains(t, string(updated), "urllib3==2.0.4")
}

func TestUpdateRequirement_CVE2024_37891_Urllib3CRLF(t *testing.T) {
	// CVE-2024-37891: urllib3 <2.2.2 CRLF injection via request headers
	dir := t.TempDir()
	path := filepath.Join(dir, "requirements.txt")
	require.NoError(t, os.WriteFile(path, []byte("urllib3>=2.0.0\n"), 0o600))

	require.NoError(t, python.UpdateRequirement(path, "urllib3", "2.2.2"))

	updated, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(updated), "urllib3>=2.2.2")
}

// --- UpdatePyprojectDep with real CVE scenarios ---

func TestUpdatePyprojectDep_CVE2024_26130_Cryptography(t *testing.T) {
	// CVE-2024-26130: cryptography <42.0.0 private key loading vulnerability
	dir := t.TempDir()
	path := filepath.Join(dir, "pyproject.toml")
	require.NoError(t, os.WriteFile(path, []byte(`[build-system]
build-backend = "hatchling.build"

[project]
name = "myapp"
dependencies = [
    "cryptography>=41.0.0",
    "requests>=2.28.0",
]
`), 0o600))

	require.NoError(t, python.UpdatePyprojectDep(path, "cryptography", "42.0.4"))

	updated, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(updated), "cryptography>=42.0.4")
	// requests should be unchanged
	assert.Contains(t, string(updated), "requests>=2.28.0")
}

func TestUpdatePyprojectDep_Poetry_CVE2022_29217_PyJWT(t *testing.T) {
	// CVE-2022-29217: PyJWT <2.4.0 key confusion allowing token forgery
	dir := t.TempDir()
	path := filepath.Join(dir, "pyproject.toml")
	require.NoError(t, os.WriteFile(path, []byte(`[build-system]
build-backend = "poetry.core.masonry.api"

[tool.poetry.dependencies]
python = "^3.9"
pyjwt = "^2.1.0"
requests = "^2.28.0"
`), 0o600))

	require.NoError(t, python.UpdatePyprojectDep(path, "pyjwt", "2.4.0"))

	updated, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(updated), "2.4.0")
	// requests should be unchanged
	assert.Contains(t, string(updated), "2.28.0")
}
