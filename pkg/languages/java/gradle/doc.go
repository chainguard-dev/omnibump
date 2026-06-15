/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package gradle implements Gradle build tool support for Java projects.
//
// It mirrors the Maven build tool's behavior: omnibump finds the best place
// to apply each requested bump across the mechanisms Gradle projects use to
// define dependency versions — version catalogs (gradle/libs.versions.toml
// and settings-script inline catalogs), version variables (gradle.properties,
// version.properties files, Groovy ext properties and version maps), and
// direct declarations in build scripts. Dependencies not declared anywhere
// are pinned through an omnibump-managed resolutionStrategy force block, the
// Gradle analog of Maven's DependencyManagement fallback for transitive
// dependencies.
//
// File parsing and editing is delegated to pkg/gradlefile, which performs
// format-preserving edits on both Groovy and Kotlin DSL files.
package gradle
