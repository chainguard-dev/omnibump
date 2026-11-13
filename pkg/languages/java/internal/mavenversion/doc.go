/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package mavenversion orders Maven and Gradle version strings the way
// Maven's ComparableVersion does, so omnibump can tell whether a requested
// version would lower the one a build already declares.
//
// A version is split into items at '.', at '-', and wherever digits and
// letters meet. Numbers compare numerically. Qualifiers rank
// alpha < beta < milestone < rc < snapshot < release < sp, with "ga",
// "final" and "release" meaning the release itself, and any other qualifier
// ranking after sp in alphabetical order.
package mavenversion
