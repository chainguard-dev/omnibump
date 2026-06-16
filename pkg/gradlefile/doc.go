/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package gradlefile is a small parsing and editing library for the Gradle
// files omnibump needs to patch: build scripts (build.gradle, build.gradle.kts
// and other *.gradle(.kts) scripts such as gradle/dependencies.gradle),
// settings scripts with inline version catalogs, gradle.properties-style
// files, and gradle/libs.versions.toml version catalogs.
//
// It is intentionally not a general Gradle DSL parser. Files are scanned for
// the version-bearing constructs omnibump understands, each construct is
// recorded with its source span, and edits are format-preserving splices at
// those spans: everything outside the edited value is preserved byte for
// byte. Both the Groovy and Kotlin DSL syntaxes are supported, selected by
// file extension.
//
// The package performs no file I/O: callers pass content in and read content
// back via Content(). Path policy (size limits, symlink and root-boundary
// checks) belongs to the caller.
package gradlefile
