/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package golang

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/chainguard-dev/clog"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	versionutil "k8s.io/apimachinery/pkg/util/version"
)

// ErrEmptyModulePath is returned when a module path is empty.
var ErrEmptyModulePath = errors.New("module path cannot be empty")

// ErrEmptyVersionQuery is returned when a version query is empty.
var ErrEmptyVersionQuery = errors.New("version query cannot be empty")

// ErrInvalidVersionQueryChar is returned when a version query contains an invalid character.
var ErrInvalidVersionQueryChar = errors.New("invalid character in version query")

// ErrUnexpectedGoVersionOutput is returned when go version output has unexpected format.
var ErrUnexpectedGoVersionOutput = errors.New("unexpected go version output")

// validateModulePath validates a Go module path to prevent injection attacks.
// Uses module.CheckPath() from golang.org/x/mod/module to ensure the path is valid.
func validateModulePath(path string) error {
	if path == "" {
		return ErrEmptyModulePath
	}
	if err := module.CheckPath(path); err != nil {
		return fmt.Errorf("invalid module path %q: %w", path, err)
	}
	return nil
}

// validateVersionQuery validates a Go version query string before passing to commands.
// Version queries can be: version numbers (v1.2.3), branch names, commit hashes, or special
// queries like "latest", "upgrade", "patch". We validate the character set to prevent injection.
func validateVersionQuery(query string) error {
	if query == "" {
		return ErrEmptyVersionQuery
	}
	// Allow alphanumeric, dots, hyphens, underscores, slashes, plus signs, and tildes
	// This covers semantic versions, branch names, commit hashes, and Go version queries
	for _, r := range query {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') &&
			r != '.' && r != '-' && r != '_' && r != '/' && r != '+' && r != '~' && r != 'v' {
			return fmt.Errorf("%w: %q contains %c", ErrInvalidVersionQueryChar, query, r)
		}
	}
	return nil
}

// GoModTidy runs go mod tidy with optional compatibility settings.
// The go version is automatically determined from the project's go.mod file.
// Ported from gobump/pkg/run/gorun.go.
func GoModTidy(ctx context.Context, modroot, _ string, compat string) (string, error) {
	// Note: goVersion parameter (now _) is kept for API compatibility but is no longer used.
	// go mod tidy will automatically use the Go version specified in the project's go.mod file.
	args := []string{"mod", "tidy"}
	if compat != "" {
		args = append(args, "-compat", compat)
	}

	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = modroot
	if bytes, err := cmd.CombinedOutput(); err != nil {
		return strings.TrimSpace(string(bytes)), err
	}
	return "", nil
}

// GoModDownload runs go mod download to update go.sum.
func GoModDownload(ctx context.Context, modroot string) (string, error) {
	cmd := exec.CommandContext(ctx, "go", "mod", "download")
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
	log := clog.FromContext(ctx)

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

	// Get Go version from environment if not provided
	if goVersion == "" {
		cmd := exec.CommandContext(ctx, "go", "version")
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to get Go version: %w, output: %s", err, strings.TrimSpace(string(output)))
		}

		parts := strings.Fields(strings.TrimSpace(string(output)))
		if len(parts) < 3 || !strings.HasPrefix(parts[2], "go") {
			return fmt.Errorf("%w: %s", ErrUnexpectedGoVersionOutput, string(output))
		}

		goVersion = strings.TrimPrefix(parts[2], "go")
	}

	// Read current go.work version to log the change
	// workPath is from findGoWork which validates it's a real go.work file
	workContent, err := os.ReadFile(workPath) //nolint:gosec // G304: workPath is validated by findGoWork
	if err != nil {
		return fmt.Errorf("failed to read go.work file: %w", err)
	}

	workFile, err := modfile.ParseWork(workPath, workContent, nil)
	if err != nil {
		return fmt.Errorf("failed to parse go.work file: %w", err)
	}

	currentVersion := ""
	if workFile.Go != nil {
		currentVersion = workFile.Go.Version
	}

	// Compare versions and log the change
	if currentVersion != "" && currentVersion != goVersion {
		currentV := versionutil.MustParseGeneric(currentVersion)
		newV := versionutil.MustParseGeneric(goVersion)

		if newV.GreaterThan(currentV) {
			log.Infof("Upgrading go.work version from %s to %s (runtime version)", currentVersion, goVersion)
		} else if newV.LessThan(currentV) {
			log.Infof("Downgrading go.work version from %s to %s (runtime version)", currentVersion, goVersion)
		}
	} else if currentVersion == "" {
		log.Infof("Setting go.work version to %s (runtime version)", goVersion)
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
	// Validate module path before passing to command.
	if err := validateModulePath(name); err != nil {
		return "", err
	}
	// Validate version query before passing to command.
	if err := validateVersionQuery(version); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "go", "get", fmt.Sprintf("%s@%s", name, version)) //nolint:gosec
	cmd.Dir = modroot
	if bytes, err := cmd.CombinedOutput(); err != nil {
		return strings.TrimSpace(string(bytes)), err
	}
	return "", nil
}

// GoModEditReplaceModule edits go.mod to replace one module with another.
func GoModEditReplaceModule(ctx context.Context, nameOld, nameNew, version, modroot string) (string, error) {
	// Validate both module paths before passing to command.
	if err := validateModulePath(nameOld); err != nil {
		return "", fmt.Errorf("invalid old module path: %w", err)
	}
	if err := validateModulePath(nameNew); err != nil {
		return "", fmt.Errorf("invalid new module path: %w", err)
	}
	// Validate version before passing to command.
	if err := validateVersionQuery(version); err != nil {
		return "", fmt.Errorf("invalid version: %w", err)
	}

	cmd := exec.CommandContext(ctx, "go", "mod", "edit", "-dropreplace", nameOld) //nolint:gosec
	cmd.Dir = modroot
	if bytes, err := cmd.CombinedOutput(); err != nil {
		return strings.TrimSpace(string(bytes)), fmt.Errorf("error running go command to drop replace modules: %w", err)
	}

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
