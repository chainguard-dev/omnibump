/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package python implements omnibump support for Python projects.
// Supports multiple build tools (pip, uv, hatch, poetry, pdm, maturin,
// scikit-build-core, setuptools) through a unified interface.
package python

import "errors"

// BuildTool identifies the Python build system in use.
type BuildTool string

const (
	BuildToolPip            BuildTool = "pip"
	BuildToolUV             BuildTool = "uv"
	BuildToolHatch          BuildTool = "hatch"
	BuildToolPoetry         BuildTool = "poetry"
	BuildToolPDM            BuildTool = "pdm"
	BuildToolMaturin        BuildTool = "maturin"
	BuildToolSetuptools     BuildTool = "setuptools"
	BuildToolScikitBuild    BuildTool = "scikit-build"
	BuildToolScikitBuildCore BuildTool = "scikit-build-core"
	BuildToolUnknown        BuildTool = "unknown"
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

var (
	// ErrManifestNotFound is returned when no Python manifest is found.
	ErrManifestNotFound = errors.New("no Python manifest file found")

	// ErrPackageNotFound is returned when the target package is not in the manifest.
	ErrPackageNotFound = errors.New("package not found in manifest")

	// ErrInvalidVersion is returned when a version string fails validation.
	ErrInvalidVersion = errors.New("invalid version string")

	// ErrVersionResolverUnavailable is returned when no registry can resolve a version.
	ErrVersionResolverUnavailable = errors.New("version resolver unavailable")
)
