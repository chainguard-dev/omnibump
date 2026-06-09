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
// # Build tool detection
//
// Manifest mode auto-detects build tools from the [build-system].build-backend
// field in pyproject.toml and can be overridden with the --tool flag.
// Supported build tools:
//
//   - pip / uv — requirements.txt or PEP 621 pyproject.toml
//   - poetry — [tool.poetry.dependencies] in pyproject.toml
//   - hatch — PEP 621 [project].dependencies via hatchling backend
//   - pdm — PEP 621 [project].dependencies; detected by pdm.lock
//   - flit — PEP 621 [project].dependencies via flit_core backend
//   - setuptools — PEP 621 pyproject.toml, setup.cfg, or setup.py
//   - maturin — PEP 621 [project].dependencies for Rust+Python (PyO3) projects;
//     used by polars, pydantic-core, orjson, ruff, and other performance-critical packages
//   - scikit-build-core — PEP 621 [project].dependencies for C/C++ extension modules
//
// All PEP 621 backends (hatch, flit, pdm, maturin, scikit-build-core, setuptools)
// share the same parsing path since they use the standard [project].dependencies
// array. Only Poetry requires a separate parser for its [tool.poetry.dependencies]
// section.
//
// # Manifest files
//
// Supported manifest file formats, checked in priority order:
// pyproject.toml, requirements.txt, setup.cfg, setup.py (read-only), Pipfile.
//
// # Lockfile handling
//
// For uv and pdm projects, omnibump updates pyproject.toml directly. The user
// must re-run `uv lock` or `pdm lock` afterwards to regenerate the lockfile from
// the updated constraints. Lockfile detection (uv.lock, pdm.lock) is used to
// identify the build tool, but lockfiles themselves are not modified.
//
// # Venv mode
//
// Venv mode is designed for application/leaf packages that bundle dependencies
// in a staged virtualenv. It validates strict == pinning, rejects downgrades,
// and verifies environment consistency with pip check. It supports both uv pip
// and standard pip installers.
//
// Both modes integrate with omnibump's language interface for unified
// dependency version bumping across multiple ecosystems.
package python
