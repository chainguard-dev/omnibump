/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package pathutil provides path safety helpers for omnibump.
package pathutil

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrUnsafePath is returned when a file path resolves outside the allowed
// project root boundary (e.g. via path traversal or a symlink escape).
var ErrUnsafePath = errors.New("unsafe path")

// ValidatePathWithinRoot checks that path resolves to a location inside root,
// following symlinks on both sides so a symlink cannot be used to escape.
func ValidatePathWithinRoot(root, path string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("failed to resolve project root %s: %w", root, err)
	}
	rootAbs, err = filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return fmt.Errorf("failed to resolve project root symlinks %s: %w", rootAbs, err)
	}

	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("failed to resolve path %s: %w", path, err)
	}
	pathAbs, err = filepath.EvalSymlinks(pathAbs)
	if err != nil {
		return fmt.Errorf("failed to resolve path symlinks %s: %w", pathAbs, err)
	}

	inside, err := pathIsWithinRoot(rootAbs, pathAbs)
	if err != nil {
		return fmt.Errorf("%w: failed to compare %s to project root %s: %w", ErrUnsafePath, pathAbs, rootAbs, err)
	}
	if !inside {
		return fmt.Errorf("%w: %s escapes project root %s", ErrUnsafePath, pathAbs, rootAbs)
	}
	return nil
}

func pathIsWithinRoot(rootAbs, pathAbs string) (bool, error) {
	rel, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil {
		return false, err
	}
	if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, nil
	}
	return true, nil
}
