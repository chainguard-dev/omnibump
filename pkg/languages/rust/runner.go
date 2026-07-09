/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package rust

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/chainguard-dev/clog"
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

// cargoToolchainEnv names the environment variable that overrides the rustup
// toolchain applied to cargo invocations.
const cargoToolchainEnv = "OMNIBUMP_CARGO_TOOLCHAIN"

// cargoToolchain returns the rustup toolchain to pin cargo to, as the bare
// toolchain name (the "+" prefix is added by cargoCommand). Some projects pin an
// old nightly toolchain that lacks features omnibump relies on (notably
// `cargo add`), which fails unless cargo is run against a known-good toolchain.
// The default is "stable"; operators can select a different toolchain, or disable
// the override entirely with an empty value, via OMNIBUMP_CARGO_TOOLCHAIN.
func cargoToolchain() string {
	if tc, ok := os.LookupEnv(cargoToolchainEnv); ok {
		return tc
	}
	return "stable"
}

// cargoCommand builds an *exec.Cmd for `cargo [+toolchain] args...` rooted at dir.
// The toolchain override (see cargoToolchain) is inserted before the subcommand,
// where rustup expects it. All cargo invocations in this package go through here
// so the toolchain is applied consistently.
func cargoCommand(ctx context.Context, dir string, args ...string) *exec.Cmd {
	log := clog.FromContext(ctx)

	if tc := cargoToolchain(); tc != "" {
		args = append([]string{"+" + tc}, args...)
	}

	log.Debugf("Running: cargo %s in %s", strings.Join(args, " "), dir)
	cmd := exec.CommandContext(ctx, "cargo", args...) //nolint:gosec // fixed "cargo" binary; args are cargo specs/flags derived from the lockfile and manifest
	cmd.Dir = dir
	return cmd
}

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

// CargoCheck runs `cargo check --workspace` to verify the project still compiles.
// It returns the combined output (so compiler errors can be surfaced) and an error
// if the check fails. Used to gate SemVer-breaking upgrades, which can leave the
// project unbuildable.
func CargoCheck(ctx context.Context, cargoRoot string) (string, error) {
	cmd := cargoCommand(ctx, cargoRoot, "check", "--workspace")
	if bytes, err := cmd.CombinedOutput(); err != nil {
		return strings.TrimSpace(string(bytes)), err
	}
	return "", nil
}

// CargoUpdate runs 'cargo update' to refresh the Cargo.lock file.
// Ported from cargobump/pkg/run/cargo.go.
func CargoUpdate(ctx context.Context, cargoRoot string) (string, error) {
	cmd := cargoCommand(ctx, cargoRoot, "update")
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

	cmd := cargoCommand(ctx, cargoRoot, "update", "--precise", newVersion, "--package", fmt.Sprintf("%s@%s", name, oldVersion))
	if bytes, err := cmd.CombinedOutput(); err != nil {
		return strings.TrimSpace(string(bytes)), err
	}
	return "", nil
}
