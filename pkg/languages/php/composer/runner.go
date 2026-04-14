/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package composer

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// RunUpdate runs 'composer update' to refresh the composer.lock file.
// Additional args can be passed (e.g., "--no-install" for testing).
func RunUpdate(ctx context.Context, composerRoot string, extraArgs ...string) (string, error) {
	args := make([]string, 0, 2+len(extraArgs))
	args = append(args, "update", "--no-interaction")
	args = append(args, extraArgs...)
	cmd := exec.CommandContext(ctx, "composer", args...) //nolint:gosec
	cmd.Dir = composerRoot
	if bytes, err := cmd.CombinedOutput(); err != nil {
		return strings.TrimSpace(string(bytes)), err
	}
	return "", nil
}

// RunRequire updates a specific package to a precise version.
// Uses: composer require <package>:<version> --no-interaction
// Additional args can be passed (e.g., "--no-install" for testing).
func RunRequire(ctx context.Context, name, version, composerRoot string, extraArgs ...string) (string, error) {
	packageSpec := fmt.Sprintf("%s:%s", name, version)
	args := make([]string, 0, 3+len(extraArgs))
	args = append(args, "require", packageSpec, "--no-interaction")
	args = append(args, extraArgs...)
	cmd := exec.CommandContext(ctx, "composer", args...) //nolint:gosec
	cmd.Dir = composerRoot
	if bytes, err := cmd.CombinedOutput(); err != nil {
		return strings.TrimSpace(string(bytes)), err
	}
	return "", nil
}

// RunRequireDev updates a specific dev package to a precise version.
// Uses: composer require --dev <package>:<version> --no-interaction
// Additional args can be passed (e.g., "--no-install" for testing).
func RunRequireDev(ctx context.Context, name, version, composerRoot string, extraArgs ...string) (string, error) {
	packageSpec := fmt.Sprintf("%s:%s", name, version)
	args := make([]string, 0, 4+len(extraArgs))
	args = append(args, "require", "--dev", packageSpec, "--no-interaction")
	args = append(args, extraArgs...)
	cmd := exec.CommandContext(ctx, "composer", args...) //nolint:gosec
	cmd.Dir = composerRoot
	if bytes, err := cmd.CombinedOutput(); err != nil {
		return strings.TrimSpace(string(bytes)), err
	}
	return "", nil
}
