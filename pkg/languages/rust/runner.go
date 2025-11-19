/*
Copyright 2025 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package rust

import (
	"fmt"
	"os/exec"
	"strings"
)

// CargoUpdate runs 'cargo update' to refresh the Cargo.lock file.
// Ported from cargobump/pkg/run/cargo.go
func CargoUpdate(cargoRoot string) (string, error) {
	cmd := exec.Command("cargo", "update") //nolint:gosec
	cmd.Dir = cargoRoot
	if bytes, err := cmd.CombinedOutput(); err != nil {
		return strings.TrimSpace(string(bytes)), err
	}
	return "", nil
}

// CargoUpdatePackage updates a specific package to a precise version.
// Uses: cargo update --precise <newVersion> --package <name>@<oldVersion>
// Ported from cargobump/pkg/run/cargo.go
func CargoUpdatePackage(name, oldVersion, newVersion, cargoRoot string) (string, error) {
	cmd := exec.Command("cargo", "update", "--precise", newVersion, "--package", fmt.Sprintf("%s@%s", name, oldVersion)) //nolint:gosec
	cmd.Dir = cargoRoot
	if bytes, err := cmd.CombinedOutput(); err != nil {
		return strings.TrimSpace(string(bytes)), err
	}
	return "", nil
}
