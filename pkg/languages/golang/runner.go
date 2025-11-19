/*
Copyright 2025 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package golang

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	versionutil "k8s.io/apimachinery/pkg/util/version"
)

// GoModTidy runs go mod tidy with the specified go version and compatibility settings.
// Ported from gobump/pkg/run/gorun.go
func GoModTidy(modroot, goVersion, compat string) (string, error) {
	if goVersion == "" {
		goVersion = strings.TrimPrefix(runtime.Version(), "go")
		v := versionutil.MustParseGeneric(goVersion)
		goVersion = fmt.Sprintf("%d.%d", v.Major(), v.Minor())
	}

	args := []string{"mod", "tidy", "-go", goVersion}
	if compat != "" {
		args = append(args, "-compat", compat)
	}

	cmd := exec.Command("go", args...) //nolint:gosec
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
func UpdateGoWorkVersion(modroot string, forceWork bool, goVersion string) error {
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
	cmd := exec.Command("go", "work", "edit", "-go", goVersion)
	cmd.Dir = dir
	if bytes, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to update go.work version: %w, output: %s", err, strings.TrimSpace(string(bytes)))
	}

	return nil
}

// GoVendor runs go mod vendor or go work vendor depending on workspace configuration.
func GoVendor(dir string, forceWork bool) (string, error) {
	workPath := findGoWork(dir)
	if forceWork || workPath != "" {
		cmd := exec.Command("go", "work", "vendor")
		if bytes, err := cmd.CombinedOutput(); err != nil {
			return strings.TrimSpace(string(bytes)), err
		}
	} else {
		cmd := exec.Command("go", "mod", "vendor")
		if bytes, err := cmd.CombinedOutput(); err != nil {
			return strings.TrimSpace(string(bytes)), err
		}
	}

	return "", nil
}

// GoGetModule runs go get for a specific module and version.
func GoGetModule(name, version, modroot string) (string, error) {
	cmd := exec.Command("go", "get", fmt.Sprintf("%s@%s", name, version)) //nolint:gosec
	cmd.Dir = modroot
	if bytes, err := cmd.CombinedOutput(); err != nil {
		return strings.TrimSpace(string(bytes)), err
	}
	return "", nil
}

// GoModEditReplaceModule edits go.mod to replace one module with another.
func GoModEditReplaceModule(nameOld, nameNew, version, modroot string) (string, error) {
	cmd := exec.Command("go", "mod", "edit", "-dropreplace", nameOld) //nolint:gosec
	cmd.Dir = modroot
	if bytes, err := cmd.CombinedOutput(); err != nil {
		return strings.TrimSpace(string(bytes)), fmt.Errorf("error running go command to drop replace modules: %w", err)
	}

	cmd = exec.Command("go", "mod", "edit", "-replace", fmt.Sprintf("%s=%s@%s", nameOld, nameNew, version)) //nolint:gosec
	cmd.Dir = modroot
	if bytes, err := cmd.CombinedOutput(); err != nil {
		return strings.TrimSpace(string(bytes)), fmt.Errorf("error running go command to replace modules: %w", err)
	}
	return "", nil
}

// GoModEditDropRequireModule drops a require directive from go.mod.
func GoModEditDropRequireModule(name, modroot string) (string, error) {
	cmd := exec.Command("go", "mod", "edit", "-droprequire", name) //nolint:gosec
	cmd.Dir = modroot
	if bytes, err := cmd.CombinedOutput(); err != nil {
		return strings.TrimSpace(string(bytes)), err
	}

	return "", nil
}
