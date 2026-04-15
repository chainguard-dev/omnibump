/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package python

// uv projects declare dependencies in pyproject.toml (PEP 621 [project].dependencies).
// omnibump updates pyproject.toml directly. The user must re-run `uv lock` afterwards
// to regenerate the uv.lock file from the updated constraints.
//
// uv.lock detection is used to identify the build tool but the lockfile itself
// is not modified by omnibump.
