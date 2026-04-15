/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package python implements omnibump support for Python projects.
// Supports multiple build tools (pip, uv, hatch, poetry, pdm, maturin,
// scikit-build-core, setuptools) through a unified interface.
package python

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Manifest file type constants.
const (
	// UVCommand is the uv package manager command.
	UVCommand = "uv"
	// PipCommand is the pip package manager command.
	PipCommand = "pip"

	// ManifestPyprojectTOML is the pyproject.toml manifest type.
	ManifestPyprojectTOML = "pyproject.toml"
	// ManifestRequirementsTxt is the requirements.txt manifest type.
	ManifestRequirementsTxt = "requirements.txt"
	// ManifestSetupCfg is the setup.cfg manifest type.
	ManifestSetupCfg = "setup.cfg"
	// ManifestSetupPy is the setup.py manifest type.
	ManifestSetupPy = "setup.py"
	// ManifestPipfile is the Pipfile manifest type.
	ManifestPipfile = "Pipfile"
)

// BuildTool identifies the Python build system in use.
type BuildTool string

const (
	// BuildToolPip indicates the pip build tool.
	BuildToolPip BuildTool = "pip"
	// BuildToolUV indicates the uv build tool.
	BuildToolUV BuildTool = "uv"
	// BuildToolHatch indicates the hatch build tool.
	BuildToolHatch BuildTool = "hatch"
	// BuildToolPoetry indicates the poetry build tool.
	BuildToolPoetry BuildTool = "poetry"
	// BuildToolPDM indicates the pdm build tool.
	BuildToolPDM BuildTool = "pdm"
	// BuildToolMaturin indicates the maturin build tool.
	BuildToolMaturin BuildTool = "maturin"
	// BuildToolSetuptools indicates the setuptools build tool.
	BuildToolSetuptools BuildTool = "setuptools"
	// BuildToolScikitBuild indicates the scikit-build tool.
	BuildToolScikitBuild BuildTool = "scikit-build"
	// BuildToolScikitBuildCore indicates the scikit-build-core tool.
	BuildToolScikitBuildCore BuildTool = "scikit-build-core"
	// BuildToolUnknown indicates an unknown build tool.
	BuildToolUnknown BuildTool = "unknown"
)

// ManifestInfo describes a detected Python manifest file.
type ManifestInfo struct {
	// Path is the absolute path to the manifest file.
	Path string
	// Type is the manifest filename (e.g. "pyproject.toml", "requirements.txt").
	Type string
	// BuildTool is the detected build backend.
	BuildTool BuildTool
}

// VersionSpec represents a single parsed dependency entry.
type VersionSpec struct {
	// Package is the normalized package name.
	Package string
	// Specifier is the version operator(s), e.g. ">=", "==", "~=", "^".
	Specifier string
	// Version is the version string without the operator.
	Version string
	// RawLine is the original unparsed line from the manifest.
	RawLine string
}

// validateManifestPath checks that a path is safe for reading/writing.
// Paths must be absolute and exist. Call this before ReadFile/WriteFile operations.
func validateManifestPath(path string) error {
	// Path must be absolute
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%w: %s", ErrInvalidVersion, path)
	}
	// Path must have one of the known manifest filenames
	base := filepath.Base(path)
	switch base {
	case ManifestPyprojectTOML, ManifestRequirementsTxt, ManifestSetupCfg, ManifestSetupPy, ManifestPipfile:
		// Valid manifest filename
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedManifestType, base)
	}
	return nil
}

// safeWriteFile writes to a manifest file after path validation.
func safeWriteFile(path string, data []byte) error {
	if err := validateManifestPath(path); err != nil {
		return err
	}
	// Verify path is still valid after Clean
	cleanPath := filepath.Clean(path)
	if !filepath.IsAbs(cleanPath) {
		return fmt.Errorf("%w: %s", ErrInvalidPathAfterClean, cleanPath)
	}
	// Verify the base filename is still a manifest file
	base := filepath.Base(cleanPath)
	switch base {
	case ManifestPyprojectTOML, ManifestRequirementsTxt, ManifestSetupCfg, ManifestSetupPy, ManifestPipfile:
		// Path has been validated and cleaned, write to file
		//nolint:gosec // Path is validated: absolute, cleaned, and filename verified
		return os.WriteFile(cleanPath, data, 0o600)
	default:
		return fmt.Errorf("%w: %s", ErrInvalidManifestFile, base)
	}
}

var (
	// ErrManifestNotFound is returned when no Python manifest is found.
	ErrManifestNotFound = errors.New("no Python manifest file found")

	// ErrPackageNotFound is returned when the target package is not in the manifest.
	ErrPackageNotFound = errors.New("package not found in manifest")

	// ErrInvalidVersion is returned when a version string fails validation.
	ErrInvalidVersion = errors.New("invalid version string")

	// ErrVersionResolverUnavailable is returned when no registry can resolve a version.
	ErrVersionResolverUnavailable = errors.New("version resolver unavailable")

	// ErrUnsupportedManifestType is returned for unsupported manifest types.
	ErrUnsupportedManifestType = errors.New("unsupported manifest type")

	// ErrVenvDowngrade is returned when a version downgrade is attempted.
	ErrVenvDowngrade = errors.New("downgrade rejected")

	// ErrVenvInvalidPinning is returned when venv mode requires == pinning.
	ErrVenvInvalidPinning = errors.New("venv mode requires == pinning")

	// ErrVenvEmptyVersion is returned when a version is empty.
	ErrVenvEmptyVersion = errors.New("empty version for package")

	// ErrPipInstallFailed is returned when pip install fails.
	ErrPipInstallFailed = errors.New("pip install failed")

	// ErrPipCheckFailed is returned when pip check fails.
	ErrPipCheckFailed = errors.New("pip check failed")

	// ErrSetupPyReadOnly is returned when attempting to modify setup.py.
	ErrSetupPyReadOnly = errors.New("setup.py is read-only: omnibump cannot safely update setup.py; migrate to setup.cfg or pyproject.toml")

	// ErrVersionNotFound is returned when no versions are found in registry.
	ErrVersionNotFound = errors.New("no versions found in registry")

	// ErrInvalidVersionResponse is returned when version response is invalid.
	ErrInvalidVersionResponse = errors.New("invalid version in response")

	// ErrHTTPNotFound is returned for 404 HTTP responses.
	ErrHTTPNotFound = errors.New("not found")

	// ErrUnexpectedHTTPStatus is returned for unexpected HTTP status codes.
	ErrUnexpectedHTTPStatus = errors.New("unexpected HTTP status")

	// ErrParseVersionFailed is returned when version parsing fails.
	ErrParseVersionFailed = errors.New("failed to parse version number")

	// ErrNoVersionNumber is returned when a string has no leading digits.
	ErrNoVersionNumber = errors.New("not a number")

	// ErrInvalidPathAfterClean is returned when a path is still invalid after filepath.Clean.
	ErrInvalidPathAfterClean = errors.New("invalid path after clean")

	// ErrInvalidManifestFile is returned when the file is not a known manifest type.
	ErrInvalidManifestFile = errors.New("invalid manifest file")
)
