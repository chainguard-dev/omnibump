/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package golang

// Package represents a Go module package to be updated or replaced.
// Ported from gobump/pkg/types/types.go.
type Package struct {
	OldName string
	Name    string
	Version string
	Replace bool
	Require bool
	Index   int
	// Force allows downgrading a package to a version older than the current one.
	// By default, downgrade attempts are skipped with a warning.
	Force bool
}

// UpdateConfig contains configuration options for the Go update process.
type UpdateConfig struct {
	Modroot         string
	GoVersion       string
	ShowDiff        bool
	Tidy            bool
	TidyCompat      string
	SkipInitialTidy bool
	ForceWork       bool
}

// PackageList is used to marshal from yaml/json file to get the list of packages.
type PackageList struct {
	Packages []Package `json:"packages" yaml:"packages"`
}
