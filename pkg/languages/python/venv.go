/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package python

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/languages"
)

// updateVenv upgrades packages in a staged Python venv using uv pip install or the venv's pip.
// Validates that all versions use == pinning, rejects downgrades, and verifies the environment
// after upgrade using pip check.
func updateVenv(ctx context.Context, cfg *languages.UpdateConfig, venvPath string) error {
	log := clog.FromContext(ctx)

	// Ensure venv path is absolute
	absVenv, err := filepath.Abs(venvPath)
	if err != nil {
		return fmt.Errorf("invalid venv path: %w", err)
	}

	// Verify venv exists
	if _, err := os.Stat(absVenv); err != nil {
		return fmt.Errorf("venv not found at %s: %w", absVenv, err)
	}

	log.Infof("Using Python venv at: %s", absVenv)

	// Determine the tool to use (uv or pip)
	var toolHint string
	if t, ok := cfg.Options["tool"]; ok {
		toolHint = t.(string)
	}

	installer, err := selectVenvInstaller(absVenv, toolHint)
	if err != nil {
		return err
	}

	log.Infof("Venv installer: %s", installer.name)

	// Validate and parse package specs
	specs, err := parseAndValidateVenvSpecs(cfg.Dependencies)
	if err != nil {
		return err
	}

	if len(specs) == 0 {
		log.Infof("No packages to update")
		return nil
	}

	// Check current versions and reject downgrades
	for _, spec := range specs {
		current, err := getInstalledVersion(ctx, absVenv, installer, spec.Name)
		if err != nil {
			return err
		}

		if current != "" {
			// Compare versions: reject if spec is lower than current
			if isVersionLower(spec.Version, current) {
				return fmt.Errorf("downgrade rejected: %s would be downgraded from %s to %s", spec.Name, current, spec.Version)
			}

			if current == spec.Version {
				log.Infof("%s: already at %s", spec.Name, current)
			} else {
				log.Infof("%s: %s -> %s", spec.Name, current, spec.Version)
			}
		} else {
			log.Infof("%s: (new) -> %s", spec.Name, spec.Version)
		}
	}

	if cfg.DryRun {
		log.Infof("[dry-run] would install: %v", specs)
		return nil
	}

	// Install the packages using the selected installer
	if err := installer.install(ctx, absVenv, specs); err != nil {
		return fmt.Errorf("pip install failed: %w", err)
	}

	// Verify environment consistency
	if err := installer.check(ctx, absVenv); err != nil {
		return fmt.Errorf("pip check failed: %w", err)
	}

	log.Infof("Updated packages and environment validation passed")

	return nil
}

// validateVenv verifies that all packages in cfg.Dependencies are installed at the expected version.
func validateVenv(ctx context.Context, cfg *languages.UpdateConfig, venvPath string) error {
	log := clog.FromContext(ctx)

	// Ensure venv path is absolute
	absVenv, err := filepath.Abs(venvPath)
	if err != nil {
		return fmt.Errorf("invalid venv path: %w", err)
	}

	// Verify venv exists
	if _, err := os.Stat(absVenv); err != nil {
		return fmt.Errorf("venv not found at %s: %w", absVenv, err)
	}

	// Determine the tool to use
	var toolHint string
	if t, ok := cfg.Options["tool"]; ok {
		toolHint = t.(string)
	}

	installer, err := selectVenvInstaller(absVenv, toolHint)
	if err != nil {
		return err
	}

	// Validate each dependency
	for _, dep := range cfg.Dependencies {
		current, err := getInstalledVersion(ctx, absVenv, installer, dep.Name)
		if err != nil {
			return err
		}

		if current == "" {
			return fmt.Errorf("validation: package %s not found in venv", dep.Name)
		}

		if current != dep.Version {
			log.Warnf("validation: %s expected %s but found %s", dep.Name, dep.Version, current)
			return fmt.Errorf("validation failed for %s: expected %s, got %s", dep.Name, dep.Version, current)
		}

		log.Debugf("validation ok: %s == %s", dep.Name, current)
	}

	return nil
}

// venvSpecifier represents a package==version pair for venv installation.
type venvSpecifier struct {
	Name    string
	Version string
}

// parseAndValidateVenvSpecs validates that all dependencies use == pinning and returns parsed specs.
func parseAndValidateVenvSpecs(deps []languages.Dependency) ([]venvSpecifier, error) {
	specs := make([]venvSpecifier, 0, len(deps))

	for _, dep := range deps {
		// Validate == pinning
		if !strings.HasPrefix(dep.Version, "==") {
			return nil, fmt.Errorf("venv mode requires == pinning, got %s@%s (use 'pkg==X.Y.Z')", dep.Name, dep.Version)
		}

		version := strings.TrimPrefix(dep.Version, "==")
		if version == "" {
			return nil, fmt.Errorf("empty version for %s", dep.Name)
		}

		specs = append(specs, venvSpecifier{
			Name:    dep.Name,
			Version: version,
		})
	}

	return specs, nil
}

// venvInstaller abstracts over uv and pip for installing in a venv.
type venvInstaller struct {
	name    string
	install func(ctx context.Context, venv string, specs []venvSpecifier) error
	check   func(ctx context.Context, venv string) error
}

// selectVenvInstaller returns the appropriate installer (uv or pip) for the venv.
func selectVenvInstaller(venv, toolHint string) (*venvInstaller, error) {
	// If tool is explicitly specified as uv, use uv
	if toolHint == "uv" {
		return &venvInstaller{
			name:    "uv",
			install: installWithUV,
			check:   checkWithUV,
		}, nil
	}

	// If tool is explicitly specified as pip, use venv's pip
	if toolHint == "pip" {
		return &venvInstaller{
			name:    "pip",
			install: installWithPip,
			check:   checkWithPip,
		}, nil
	}

	// Auto-detect: if uv is in PATH, use it; otherwise use venv's pip
	_, err := exec.LookPath("uv")
	if err == nil {
		return &venvInstaller{
			name:    "uv",
			install: installWithUV,
			check:   checkWithUV,
		}, nil
	}

	return &venvInstaller{
		name:    "pip",
		install: installWithPip,
		check:   checkWithPip,
	}, nil
}

// installWithUV installs packages using uv pip install.
func installWithUV(ctx context.Context, venv string, specs []venvSpecifier) error {
	args := []string{"pip", "install", "--upgrade", "--only-binary", ":all:", "--no-deps"}
	for _, spec := range specs {
		args = append(args, fmt.Sprintf("%s==%s", spec.Name, spec.Version))
	}

	cmd := exec.CommandContext(ctx, "uv", args...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("VIRTUAL_ENV=%s", venv))

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("uv pip install failed: %s", stderr.String())
	}

	return nil
}

// installWithPip installs packages using the venv's pip.
func installWithPip(ctx context.Context, venv string, specs []venvSpecifier) error {
	pipBin := filepath.Join(venv, "bin", "pip")

	args := []string{"install", "--upgrade", "--no-deps"}
	for _, spec := range specs {
		args = append(args, fmt.Sprintf("%s==%s", spec.Name, spec.Version))
	}

	cmd := exec.CommandContext(ctx, pipBin, args...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("VIRTUAL_ENV=%s", venv))

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pip install failed: %s", stderr.String())
	}

	return nil
}

// checkWithUV verifies environment consistency using uv pip check.
func checkWithUV(ctx context.Context, venv string) error {
	cmd := exec.CommandContext(ctx, "uv", "pip", "check")
	cmd.Env = append(os.Environ(), fmt.Sprintf("VIRTUAL_ENV=%s", venv))

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("uv pip check failed: %s", stderr.String())
	}

	return nil
}

// checkWithPip verifies environment consistency using the venv's pip check.
func checkWithPip(ctx context.Context, venv string) error {
	pipBin := filepath.Join(venv, "bin", "pip")

	cmd := exec.CommandContext(ctx, pipBin, "check")
	cmd.Env = append(os.Environ(), fmt.Sprintf("VIRTUAL_ENV=%s", venv))

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pip check failed: %s", stderr.String())
	}

	return nil
}

// getInstalledVersion returns the version of a package currently installed in the venv,
// or an empty string if the package is not installed.
func getInstalledVersion(ctx context.Context, venv string, installer *venvInstaller, pkgName string) (string, error) {
	// Use pip list --format json to get installed packages
	var cmd *exec.Cmd
	if installer.name == "uv" {
		cmd = exec.CommandContext(ctx, "uv", "pip", "list", "--format", "json")
		cmd.Env = append(os.Environ(), fmt.Sprintf("VIRTUAL_ENV=%s", venv))
	} else {
		pipBin := filepath.Join(venv, "bin", "pip")
		cmd = exec.CommandContext(ctx, pipBin, "list", "--format", "json")
		cmd.Env = append(os.Environ(), fmt.Sprintf("VIRTUAL_ENV=%s", venv))
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to list installed packages: %s", stderr.String())
	}

	// Parse JSON output
	var packages []struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &packages); err != nil {
		return "", fmt.Errorf("failed to parse pip list output: %w", err)
	}

	// Normalize package name for comparison (lowercase, replace hyphens/underscores)
	normName := normalizePkgName(pkgName)

	for _, pkg := range packages {
		if normalizePkgName(pkg.Name) == normName {
			return pkg.Version, nil
		}
	}

	return "", nil
}

// isVersionLower returns true if v1 < v2 by simple tuple comparison.
// Handles versions like "1.0.0", "2.3.4", etc.
// This is a simple comparison and doesn't handle pre-releases, post-releases, etc.
func isVersionLower(v1, v2 string) bool {
	parts1 := strings.Split(v1, ".")
	parts2 := strings.Split(v2, ".")

	// Pad shorter version with zeros
	maxLen := len(parts1)
	if len(parts2) > maxLen {
		maxLen = len(parts2)
	}

	for i := 0; i < maxLen; i++ {
		p1 := "0"
		if i < len(parts1) {
			p1 = parts1[i]
		}
		p2 := "0"
		if i < len(parts2) {
			p2 = parts2[i]
		}

		// Simple numeric comparison (ignores pre-release suffixes)
		n1, e1 := parseVersionNumber(p1)
		n2, e2 := parseVersionNumber(p2)

		if e1 != nil || e2 != nil {
			// Fall back to string comparison if parsing fails
			if p1 < p2 {
				return true
			} else if p1 > p2 {
				return false
			}
			continue
		}

		if n1 < n2 {
			return true
		} else if n1 > n2 {
			return false
		}
	}

	return false
}

// parseVersionNumber extracts the leading numeric part of a version component.
func parseVersionNumber(s string) (int, error) {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			if i == 0 {
				return 0, fmt.Errorf("not a number")
			}
			var num int
			_, _ = fmt.Sscanf(s[:i], "%d", &num)
			return num, nil
		}
	}
	var num int
	_, err := fmt.Sscanf(s, "%d", &num)
	return num, err
}
