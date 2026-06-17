/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package rust implements omnibump support for Rust projects.
// Ported from cargobump with enhancements for the unified omnibump architecture.
package rust

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chainguard-dev/clog"
)

// GetCargoLockPath returns the path to Cargo.lock in the given cargo root directory.
func GetCargoLockPath(cargoRoot string) (string, error) {
	cargoLockPath := filepath.Join(cargoRoot, "Cargo.lock")
	if _, err := os.Stat(cargoLockPath); os.IsNotExist(err) {
		return "", fmt.Errorf("%w in: %s", ErrCargoLockNotFound, cargoRoot)
	}

	return cargoLockPath, nil
}

// GetCurrentPackages parses Cargo.lock to get the current packages.
func GetCurrentPackages(ctx context.Context, cargoRoot string) ([]CargoPackage, error) {
	log := clog.FromContext(ctx)

	// Find Cargo.lock
	cargoLockPath, err := GetCargoLockPath(cargoRoot)
	if err != nil {
		return nil, err
	}

	// Parse Cargo.lock to get current packages
	file, err := os.Open(filepath.Clean(cargoLockPath))
	if err != nil {
		return nil, fmt.Errorf("failed to open Cargo.lock: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			log.Warnf("failed to close Cargo.lock: %v", closeErr)
		}
	}()

	cargoPackages, err := ParseCargoLock(file)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Cargo.lock: %w", err)
	}

	return cargoPackages, nil
}
