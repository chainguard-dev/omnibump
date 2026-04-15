/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package python

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// manifestPriority defines the manifest files checked in order.
// pyproject.toml covers the widest range of modern build tools and is checked first.
var manifestPriority = []string{
	"pyproject.toml",
	"requirements.txt",
	"setup.cfg",
	"setup.py",
	"Pipfile",
}

// DetectManifestWithHint returns a manifest file with optional tool preference.
// If toolHint is non-empty, it reorders the priority to check the preferred
// manifest for that tool first, then falls back to the standard order.
// toolHint examples: "pip" → requirements.txt, "poetry" → pyproject.toml, etc.
func DetectManifestWithHint(dir, toolHint string) (*ManifestInfo, error) {
	priority := manifestPriority
	if toolHint != "" {
		priority = reorderManifestPriority(toolHint, manifestPriority)
	}
	return detectManifestWithPriority(dir, priority)
}

// DetectManifest returns the highest-priority manifest file found in dir.
// The returned ManifestInfo includes the build tool inferred from the file.
func DetectManifest(dir string) (*ManifestInfo, error) {
	return detectManifestWithPriority(dir, manifestPriority)
}

// detectManifestWithPriority returns the highest-priority manifest file found in dir.
// The returned ManifestInfo includes the build tool inferred from the file.
func detectManifestWithPriority(dir string, priority []string) (*ManifestInfo, error) {
	for _, name := range priority {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}

		info := &ManifestInfo{Path: path, Type: name}
		info.BuildTool = detectBuildTool(name, dir, path)
		return info, nil
	}
	return nil, ErrManifestNotFound
}

// detectBuildTool determines the build tool for a manifest file.
func detectBuildTool(name, dir, path string) BuildTool {
	if name != ManifestPyprojectTOML {
		return toolForManifest(name)
	}

	bt, err := DetectBuildToolFromPyproject(path)
	if err != nil {
		bt = BuildToolUnknown
	}
	// Prefer uv if a uv.lock exists alongside pyproject.toml.
	if bt == BuildToolUnknown || bt == BuildToolHatch || bt == BuildToolSetuptools {
		if HasUVLock(dir) {
			bt = BuildToolUV
		}
	}
	if HasPDMLock(dir) && bt == BuildToolUnknown {
		bt = BuildToolPDM
	}
	return bt
}

// toolForManifest returns the build tool associated with a non-pyproject manifest.
func toolForManifest(name string) BuildTool {
	switch name {
	case ManifestRequirementsTxt:
		return BuildToolPip
	case ManifestSetupCfg, ManifestSetupPy:
		return BuildToolSetuptools
	case ManifestPipfile:
		return BuildToolPip
	default:
		return BuildToolUnknown
	}
}

// pyprojectBuildSystem is used only to read the [build-system] table.
type pyprojectBuildSystem struct {
	BuildSystem struct {
		BuildBackend string `toml:"build-backend"`
	} `toml:"build-system"`
}

// DetectBuildToolFromPyproject reads [build-system].build-backend from a pyproject.toml.
func DetectBuildToolFromPyproject(path string) (BuildTool, error) {
	var doc pyprojectBuildSystem
	if _, err := toml.DecodeFile(path, &doc); err != nil {
		return BuildToolUnknown, err
	}

	switch doc.BuildSystem.BuildBackend {
	case "hatchling.build":
		return BuildToolHatch, nil
	case "poetry.core.masonry.api":
		return BuildToolPoetry, nil
	case "pdm.backend":
		return BuildToolPDM, nil
	case "maturin":
		return BuildToolMaturin, nil
	case "scikit_build_core.build":
		return BuildToolScikitBuildCore, nil
	case "scikit_build.build":
		return BuildToolScikitBuild, nil
	case "setuptools.build_meta", "setuptools.build_meta:__legacy__":
		return BuildToolSetuptools, nil
	case "flit_core.buildapi", "flit.buildapi":
		return BuildToolHatch, nil // flit uses PEP 621 [project].dependencies
	default:
		if doc.BuildSystem.BuildBackend == "" {
			return BuildToolUnknown, nil
		}
		// Unknown backend — still treat [project].dependencies as PEP 621
		return BuildToolUnknown, nil
	}
}

// HasUVLock returns true when a uv.lock file exists in dir.
func HasUVLock(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "uv.lock"))
	return err == nil
}

// HasPDMLock returns true when a pdm.lock file exists in dir.
func HasPDMLock(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "pdm.lock"))
	return err == nil
}

// reorderManifestPriority returns a reordered manifest priority list with the
// preferred manifest for the given tool hint moved to the front.
func reorderManifestPriority(toolHint string, basePriority []string) []string {
	var preferred string
	switch toolHint {
	case "pip", "pipenv":
		preferred = "requirements.txt"
	case "uv", "poetry", "hatch", "pdm", "maturin", "scikit-build-core", "scikit-build":
		preferred = "pyproject.toml"
	case "setuptools":
		preferred = "setup.cfg"
	default:
		// Unknown tool hint, use default priority
		return basePriority
	}

	// Find the preferred manifest in the base priority
	var found bool
	var newPriority []string
	for _, name := range basePriority {
		if name == preferred {
			found = true
			newPriority = append([]string{preferred}, newPriority...)
		} else {
			newPriority = append(newPriority, name)
		}
	}

	if !found {
		// Preferred manifest not in base priority, return base as-is
		return basePriority
	}

	return newPriority
}
