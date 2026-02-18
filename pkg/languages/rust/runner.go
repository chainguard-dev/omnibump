/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package rust

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

var (
	// ErrInvalidCrateName is returned when a crate name is invalid.
	ErrInvalidCrateName = errors.New("invalid crate name")

	// ErrInvalidVersion is returned when a version string is invalid.
	ErrInvalidVersion = errors.New("invalid version string")

	// crateNameRegex validates Rust crate names.
	// Rust crate names must be alphanumeric, hyphens, or underscores.
	// See: https://doc.rust-lang.org/cargo/reference/manifest.html#the-name-field
	crateNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

	// versionRegex validates semantic version strings.
	// Allows: 1.2.3, 1.2.3-alpha, 1.2.3+build, etc.
	versionRegex = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*(-[a-zA-Z0-9.-]+)?(\+[a-zA-Z0-9.-]+)?$`)
)

// validateCrateName validates a Rust crate name against the allowed character set.
// Crate names must be alphanumeric, hyphens, or underscores per Cargo spec.
func validateCrateName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: crate name cannot be empty", ErrInvalidCrateName)
	}
	if !crateNameRegex.MatchString(name) {
		return fmt.Errorf("%w: %q (allowed characters: a-zA-Z0-9_-)", ErrInvalidCrateName, name)
	}
	return nil
}

// validateVersion validates that a version string conforms to semantic versioning.
func validateVersion(version string) error {
	if version == "" {
		return fmt.Errorf("%w: version cannot be empty", ErrInvalidVersion)
	}
	if !versionRegex.MatchString(version) {
		return fmt.Errorf("%w: %q (must be valid semver)", ErrInvalidVersion, version)
	}
	return nil
}

// CargoUpdate runs 'cargo update' to refresh the Cargo.lock file.
// Ported from cargobump/pkg/run/cargo.go.
func CargoUpdate(ctx context.Context, cargoRoot string) (string, error) {
	cmd := exec.CommandContext(ctx, "cargo", "update")
	cmd.Dir = cargoRoot
	if bytes, err := cmd.CombinedOutput(); err != nil {
		return strings.TrimSpace(string(bytes)), err
	}
	return "", nil
}

// CargoUpdatePackage updates a specific package to a precise version.
// Uses: cargo update --precise <newVersion> --package <name>@<oldVersion>
// Ported from cargobump/pkg/run/cargo.go.
func CargoUpdatePackage(ctx context.Context, name, oldVersion, newVersion, cargoRoot string) (string, error) {
	// Validate crate name and versions before passing to command.
	if err := validateCrateName(name); err != nil {
		return "", err
	}
	if err := validateVersion(oldVersion); err != nil {
		return "", fmt.Errorf("invalid old version: %w", err)
	}
	if err := validateVersion(newVersion); err != nil {
		return "", fmt.Errorf("invalid new version: %w", err)
	}

	cmd := exec.CommandContext(ctx, "cargo", "update", "--precise", newVersion, "--package", fmt.Sprintf("%s@%s", name, oldVersion)) //nolint:gosec
	cmd.Dir = cargoRoot
	if bytes, err := cmd.CombinedOutput(); err != nil {
		return strings.TrimSpace(string(bytes)), err
	}
	return "", nil
}
