/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package python implements omnibump support for Python projects.
//
// It supports both manifest mode (editing pyproject.toml, requirements.txt,
// setup.cfg, or Pipfile in-place) and venv mode (upgrading packages directly
// in a staged Python virtualenv).
//
// Manifest mode auto-detects build tools (pip, uv, poetry, hatch, pdm, maturin,
// scikit-build-core, setuptools) and can be overridden with the tool hint.
// It handles multiple manifest file formats: PEP 621 pyproject.toml (with Poetry,
// Hatch, and other backends), requirements.txt, setup.cfg, and Pipfile.
//
// Venv mode is designed for application/leaf packages that bundle dependencies
// in a staged virtualenv. It validates strict == pinning, rejects downgrades,
// and verifies environment consistency with pip check. It supports both uv pip
// and standard pip installers.
//
// Both modes integrate with omnibump's language interface for unified
// dependency version bumping across multiple ecosystems.
package python
