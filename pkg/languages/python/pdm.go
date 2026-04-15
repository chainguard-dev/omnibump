/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package python

// pdm projects declare dependencies in pyproject.toml [project].dependencies (PEP 621).
// omnibump updates pyproject.toml directly. The user must re-run `pdm lock` afterwards
// to regenerate pdm.lock from the updated constraints.
//
// pdm.lock detection is used to identify the build tool but the lockfile itself
// is not modified by omnibump.
