/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package python

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- DetectManifestWithHint ---

func TestDetectManifestWithHint_NoHint(t *testing.T) {
	// Without hint, should use default priority (pyproject.toml first)
	info, err := DetectManifestWithHint("testdata/hatch-pyproject", "")
	require.NoError(t, err)
	assert.Equal(t, "pyproject.toml", info.Type)
	assert.Equal(t, BuildToolHatch, info.BuildTool)
}

func TestDetectManifestWithHint_PipHint(t *testing.T) {
	// With pip hint, should prefer requirements.txt
	// testdata/pip-requirements has both requirements.txt and (implicitly) no pyproject
	info, err := DetectManifestWithHint("testdata/pip-requirements", "pip")
	require.NoError(t, err)
	assert.Equal(t, "requirements.txt", info.Type)
	assert.Equal(t, BuildToolPip, info.BuildTool)
}

func TestDetectManifestWithHint_PoetryHint(t *testing.T) {
	// With poetry hint, should prefer pyproject.toml (and detect Poetry)
	info, err := DetectManifestWithHint("testdata/poetry-pyproject", "poetry")
	require.NoError(t, err)
	assert.Equal(t, "pyproject.toml", info.Type)
	assert.Equal(t, BuildToolPoetry, info.BuildTool)
}

func TestDetectManifestWithHint_SetuptoolsHint(t *testing.T) {
	// With setuptools hint, should prefer setup.cfg
	info, err := DetectManifestWithHint("testdata/setup-cfg", "setuptools")
	require.NoError(t, err)
	assert.Equal(t, "setup.cfg", info.Type)
	assert.Equal(t, BuildToolSetuptools, info.BuildTool)
}

func TestDetectManifestWithHint_UnknownHint(t *testing.T) {
	// Unknown hint should fall back to default priority
	info, err := DetectManifestWithHint("testdata/poetry-pyproject", "unknown-tool")
	require.NoError(t, err)
	// Should still detect poetry since pyproject.toml exists
	assert.Equal(t, "pyproject.toml", info.Type)
	assert.Equal(t, BuildToolPoetry, info.BuildTool)
}

func TestDetectManifestWithHint_HatchHint(t *testing.T) {
	// With hatch hint, prefer pyproject.toml
	info, err := DetectManifestWithHint("testdata/hatch-pyproject", "hatch")
	require.NoError(t, err)
	assert.Equal(t, "pyproject.toml", info.Type)
	assert.Equal(t, BuildToolHatch, info.BuildTool)
}

func TestDetectManifestWithHint_MaturinHint(t *testing.T) {
	// Maturin hint → pyproject.toml
	info, err := DetectManifestWithHint("testdata/maturin-project", "maturin")
	require.NoError(t, err)
	assert.Equal(t, "pyproject.toml", info.Type)
	assert.Equal(t, BuildToolMaturin, info.BuildTool)
}

func TestDetectManifestWithHint_PDMHint(t *testing.T) {
	// PDM hint → pyproject.toml (though no specific PDM test data, pyproject should work)
	info, err := DetectManifestWithHint("testdata/hatch-pyproject", "pdm")
	require.NoError(t, err)
	assert.Equal(t, "pyproject.toml", info.Type)
}

func TestDetectManifestWithHint_PipenvHint(t *testing.T) {
	// Pipenv hint → Pipfile (if it exists; otherwise fall back)
	// Since testdata doesn't have Pipfile, should fall back to default priority
	info, err := DetectManifestWithHint("testdata/poetry-pyproject", "pipenv")
	require.NoError(t, err)
	// Should detect whatever is available (in this case, pyproject.toml)
	assert.Equal(t, "pyproject.toml", info.Type)
}

// --- reorderManifestPriority ---

func TestReorderManifestPriority_PipHint(t *testing.T) {
	base := []string{"pyproject.toml", "requirements.txt", "setup.cfg", "setup.py", "Pipfile"}
	reordered := reorderManifestPriority("pip", base)

	// requirements.txt should be first
	assert.Equal(t, "requirements.txt", reordered[0])
	// Rest should be in some order
	assert.Len(t, reordered, len(base))
}

func TestReorderManifestPriority_PyprojectHint(t *testing.T) {
	base := []string{"pyproject.toml", "requirements.txt", "setup.cfg", "setup.py", "Pipfile"}
	reordered := reorderManifestPriority("poetry", base)

	// pyproject.toml should be first (and was already first)
	assert.Equal(t, "pyproject.toml", reordered[0])
	assert.Len(t, reordered, len(base))
}

func TestReorderManifestPriority_SetuptoolsHint(t *testing.T) {
	base := []string{"pyproject.toml", "requirements.txt", "setup.cfg", "setup.py", "Pipfile"}
	reordered := reorderManifestPriority("setuptools", base)

	// setup.cfg should be first
	assert.Equal(t, "setup.cfg", reordered[0])
	assert.Len(t, reordered, len(base))
	// setup.py should be after setup.cfg (if present)
	assert.Contains(t, reordered, "setup.py")
}

func TestReorderManifestPriority_UnknownHint(t *testing.T) {
	base := []string{"pyproject.toml", "requirements.txt", "setup.cfg", "setup.py", "Pipfile"}
	reordered := reorderManifestPriority("xyz-tool", base)

	// Unknown hint should return base unchanged
	assert.Equal(t, base, reordered)
}

// --- Tool hints for various build tools ---

func TestDetectManifestWithHint_FlitHint(t *testing.T) {
	// With flit hint, prefer pyproject.toml
	info, err := DetectManifestWithHint("testdata/flit-pyproject", "flit")
	require.NoError(t, err)
	assert.Equal(t, "pyproject.toml", info.Type)
	assert.Equal(t, BuildToolFlit, info.BuildTool)
}

func TestDetectManifestWithHint_AllPyprojectTools(t *testing.T) {
	tools := []string{"poetry", "hatch", "uv", "pdm", "flit", "maturin", "scikit-build-core", "scikit-build"}

	for _, tool := range tools {
		t.Run(tool, func(t *testing.T) {
			info, err := DetectManifestWithHint("testdata/hatch-pyproject", tool)
			require.NoError(t, err)
			assert.Equal(t, "pyproject.toml", info.Type, "tool %s should prefer pyproject.toml", tool)
		})
	}
}

// --- Real-world scenario: tool detection fallback ---

func TestDetectManifestWithHint_ToolHintFallback(t *testing.T) {
	// Scenario: user specifies --tool pip, but only pyproject.toml exists
	// Should still work (find pyproject.toml via default priority)
	dir := t.TempDir()
	pyproject := filepath.Join(dir, "pyproject.toml")
	require.NoError(t, os.WriteFile(pyproject, []byte(`[project]
name = "test"
version = "0.0.1"
dependencies = ["requests==2.28.0"]
`), 0o600))

	// Even with pip hint, pyproject.toml should be detected
	info, err := DetectManifestWithHint(dir, "pip")
	require.NoError(t, err)
	assert.Equal(t, "pyproject.toml", info.Type)
}

// --- Manifest detection priority tests ---

func TestDetectManifest_PriorityOrder(t *testing.T) {
	// When multiple manifest files exist, pyproject.toml wins
	dir := t.TempDir()

	// Create both requirements.txt and setup.cfg
	reqs := filepath.Join(dir, "requirements.txt")
	setup := filepath.Join(dir, "setup.cfg")
	require.NoError(t, os.WriteFile(reqs, []byte("requests==2.28.0\n"), 0o600))
	require.NoError(t, os.WriteFile(setup, []byte("[metadata]\nname = test\n"), 0o600))

	info, err := DetectManifest(dir)
	require.NoError(t, err)
	// requirements.txt comes before setup.cfg in priority
	assert.Equal(t, "requirements.txt", info.Type)
}

// --- DetectBuildToolFromBytes ---

func TestDetectBuildToolFromBytes(t *testing.T) {
	tests := []struct {
		desc    string
		data    string
		want    BuildTool
		wantErr bool
	}{
		{
			desc: "hatch backend",
			data: `[build-system]
build-backend = "hatchling.build"
`,
			want: BuildToolHatch,
		},
		{
			desc: "poetry backend",
			data: `[build-system]
build-backend = "poetry.core.masonry.api"
`,
			want: BuildToolPoetry,
		},
		{
			desc: "setuptools backend",
			data: `[build-system]
build-backend = "setuptools.build_meta"
`,
			want: BuildToolSetuptools,
		},
		{
			desc: "setuptools legacy backend",
			data: `[build-system]
build-backend = "setuptools.build_meta:__legacy__"
`,
			want: BuildToolSetuptools,
		},
		{
			desc: "flit_core backend",
			data: `[build-system]
build-backend = "flit_core.buildapi"
`,
			want: BuildToolFlit,
		},
		{
			desc: "flit legacy backend",
			data: `[build-system]
build-backend = "flit.buildapi"
`,
			want: BuildToolFlit,
		},
		{
			desc: "pdm backend",
			data: `[build-system]
build-backend = "pdm.backend"
`,
			want: BuildToolPDM,
		},
		{
			desc: "maturin backend",
			data: `[build-system]
build-backend = "maturin"
`,
			want: BuildToolMaturin,
		},
		{
			desc: "scikit-build-core backend",
			data: `[build-system]
build-backend = "scikit_build_core.build"
`,
			want: BuildToolScikitBuildCore,
		},
		{
			desc: "scikit-build legacy backend",
			data: `[build-system]
build-backend = "scikit_build.build"
`,
			want: BuildToolScikitBuild,
		},
		{
			desc: "no build-system table",
			data: `[project]
name = "test"
`,
			want: BuildToolUnknown,
		},
		{
			desc:    "invalid TOML",
			data:    `not valid toml {{{`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got, err := DetectBuildToolFromBytes([]byte(tt.data))
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDetectManifest_UVLockOverride(t *testing.T) {
	// When uv.lock exists alongside a hatch pyproject.toml, detect as uv
	info, err := DetectManifest("testdata/uv-project")
	require.NoError(t, err)
	assert.Equal(t, "pyproject.toml", info.Type)
	assert.Equal(t, BuildToolUV, info.BuildTool)
}

func TestDetectManifest_Flit(t *testing.T) {
	info, err := DetectManifest("testdata/flit-pyproject")
	require.NoError(t, err)
	assert.Equal(t, "pyproject.toml", info.Type)
	assert.Equal(t, BuildToolFlit, info.BuildTool)
}

func TestDetectManifest_PDM(t *testing.T) {
	info, err := DetectManifest("testdata/pdm-pyproject")
	require.NoError(t, err)
	assert.Equal(t, "pyproject.toml", info.Type)
	assert.Equal(t, BuildToolPDM, info.BuildTool)
}

func TestDetectManifest_Pipfile(t *testing.T) {
	info, err := DetectManifest("testdata/pipfile-project")
	require.NoError(t, err)
	assert.Equal(t, "Pipfile", info.Type)
	assert.Equal(t, BuildToolPip, info.BuildTool)
}

func TestDetectManifest_FallbackChain(t *testing.T) {
	// When pyproject.toml doesn't exist, should check requirements.txt, setup.cfg, etc.
	dir := t.TempDir()

	// Create only setup.cfg
	setup := filepath.Join(dir, "setup.cfg")
	require.NoError(t, os.WriteFile(setup, []byte("[metadata]\nname = test\n"), 0o600))

	info, err := DetectManifest(dir)
	require.NoError(t, err)
	assert.Equal(t, "setup.cfg", info.Type)
	assert.Equal(t, BuildToolSetuptools, info.BuildTool)
}
