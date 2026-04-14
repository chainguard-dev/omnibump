/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

/*
Package composer implements Composer build tool support for PHP projects
within omnibump.

It handles parsing composer.lock files, performing dependency updates
via the composer CLI, and analyzing project dependencies.

# Parsing

Use [ParseLock] to parse a composer.lock file into a list of [LockPackage]
values representing all installed packages (both regular and dev).

# Updating

The [Composer] type implements the PHP BuildTool interface, providing
detection, update, and validation operations for Composer-based projects.
Updates are performed by modifying composer.json constraints and
optionally running composer update.

# Analysis

The [Analyzer] type implements the analyzer.Analyzer interface, providing
dependency analysis and update strategy recommendations for Composer
projects. All Composer updates use direct dependency updates (no property
indirection).
*/
package composer
