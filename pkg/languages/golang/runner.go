/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package golang

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/mod/module"
	versionutil "k8s.io/apimachinery/pkg/util/version"
)

// validateModulePath validates a Go module path to prevent injection attacks.
// SECURITY: This prevents malicious inputs like "--flag-injection" or "name; rm -rf /"
// from being passed to exec.Command as arguments.
// Uses module.CheckPath() from golang.org/x/mod/module to ensure the path is valid.
func validateModulePath(path string) error {
	if path == "" {
		return fmt.Errorf("module path cannot be empty")
	}
	if err := module.CheckPath(path); err != nil {
		return fmt.Errorf("invalid module path %q: %w", path, err)
	}
	return nil
}

// GoModTidy runs go mod tidy with the specified go version and compatibility settings.
// Ported from gobump/pkg/run/gorun.go.
func GoModTidy(ctx context.Context, modroot, goVersion, compat string) (string, error) {
	if goVersion == "" {
		goVersion = strings.TrimPrefix(runtime.Version(), "go")
		v := versionutil.MustParseGeneric(goVersion)
		goVersion = fmt.Sprintf("%d.%d", v.Major(), v.Minor())
	}

	args := []string{"mod", "tidy", "-go", goVersion}
	if compat != "" {
		args = append(args, "-compat", compat)
	}

	cmd := exec.CommandContext(ctx, "go", args...) //nolint:gosec
	cmd.Dir = modroot
	if bytes, err := cmd.CombinedOutput(); err != nil {
		return strings.TrimSpace(string(bytes)), err
	}
	return "", nil
}

func findWorkspaceFile(dir string) (root string) {
	dir = filepath.Clean(dir)
	for {
		f := filepath.Join(dir, "go.work")
		if fi, err := os.Stat(f); err == nil && !fi.IsDir() {
			return f
		}
		d := filepath.Dir(dir)
		if d == dir {
			break
		}
		dir = d
	}
	return ""
}

func findGoWork(modroot string) string {
	switch gowork := os.Getenv("GOWORK"); gowork {
	case "off":
		return ""
	case "", "auto":
		return findWorkspaceFile(modroot)
	default:
		return gowork
	}
}

// UpdateGoWorkVersion updates the go.work version if we're using workspaces.
func UpdateGoWorkVersion(ctx context.Context, modroot string, forceWork bool, goVersion string) error {
	workPath := findGoWork(modroot)
	if !forceWork && workPath == "" {
		return nil
	}

	if workPath == "" && forceWork {
		workPath = findGoWork(".")
	}

	if workPath == "" {
		return nil
	}

	// Auto-detect Go version if not provided
	if goVersion == "" {
		goVersion = strings.TrimPrefix(runtime.Version(), "go")
		v := versionutil.MustParseGeneric(goVersion)
		goVersion = fmt.Sprintf("%d.%d", v.Major(), v.Minor())
	}

	dir := filepath.Dir(workPath)
	// Safe: goVersion is either auto-detected from runtime.Version() or user-provided version string (e.g., "1.21")
	cmd := exec.CommandContext(ctx, "go", "work", "edit", "-go", goVersion) //nolint:gosec // G204: goVersion is a version string, not user-controlled path
	cmd.Dir = dir
	if bytes, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to update go.work version: %w, output: %s", err, strings.TrimSpace(string(bytes)))
	}

	return nil
}

// GoVendor runs go mod vendor or go work vendor depending on workspace configuration.
func GoVendor(ctx context.Context, dir string, forceWork bool) (string, error) {
	workPath := findGoWork(dir)
	if forceWork || workPath != "" {
		cmd := exec.CommandContext(ctx, "go", "work", "vendor")
		cmd.Dir = dir
		if bytes, err := cmd.CombinedOutput(); err != nil {
			return strings.TrimSpace(string(bytes)), err
		}
	} else {
		cmd := exec.CommandContext(ctx, "go", "mod", "vendor")
		cmd.Dir = dir
		if bytes, err := cmd.CombinedOutput(); err != nil {
			return strings.TrimSpace(string(bytes)), err
		}
	}

	return "", nil
}

// GoGetModule runs go get for a specific module and version.
func GoGetModule(ctx context.Context, name, version, modroot string) (string, error) {
	// SECURITY: Validate module path before exec.Command to prevent argument injection
	// (e.g., names starting with "-" could be interpreted as flags)
	if err := validateModulePath(name); err != nil {
		return "", err
	}
	// Safe: module path validated above
	cmd := exec.CommandContext(ctx, "go", "get", fmt.Sprintf("%s@%s", name, version)) //nolint:gosec
	cmd.Dir = modroot
	if bytes, err := cmd.CombinedOutput(); err != nil {
		return strings.TrimSpace(string(bytes)), err
	}
	return "", nil
}

// GoModEditReplaceModule edits go.mod to replace one module with another.
func GoModEditReplaceModule(ctx context.Context, nameOld, nameNew, version, modroot string) (string, error) {
	// SECURITY: Validate both module paths before exec.Command to prevent argument injection
	if err := validateModulePath(nameOld); err != nil {
		return "", fmt.Errorf("invalid old module path: %w", err)
	}
	if err := validateModulePath(nameNew); err != nil {
		return "", fmt.Errorf("invalid new module path: %w", err)
	}

	// Safe: both module paths validated above
	cmd := exec.CommandContext(ctx, "go", "mod", "edit", "-dropreplace", nameOld) //nolint:gosec
	cmd.Dir = modroot
	if bytes, err := cmd.CombinedOutput(); err != nil {
		return strings.TrimSpace(string(bytes)), fmt.Errorf("error running go command to drop replace modules: %w", err)
	}

	// Safe: both module paths validated above
	cmd = exec.CommandContext(ctx, "go", "mod", "edit", "-replace", fmt.Sprintf("%s=%s@%s", nameOld, nameNew, version)) //nolint:gosec
	cmd.Dir = modroot
	if bytes, err := cmd.CombinedOutput(); err != nil {
		return strings.TrimSpace(string(bytes)), fmt.Errorf("error running go command to replace modules: %w", err)
	}
	return "", nil
}

// GoModEditDropRequireModule drops a require directive from go.mod.
func GoModEditDropRequireModule(ctx context.Context, name, modroot string) (string, error) {
	// SECURITY: Validate module path before exec.Command to prevent argument injection
	if err := validateModulePath(name); err != nil {
		return "", err
	}
	// Safe: module path validated above
	cmd := exec.CommandContext(ctx, "go", "mod", "edit", "-droprequire", name) //nolint:gosec
	cmd.Dir = modroot
	if bytes, err := cmd.CombinedOutput(); err != nil {
		return strings.TrimSpace(string(bytes)), err
	}

	return "", nil
}
