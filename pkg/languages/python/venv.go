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
	"regexp"
	"strings"

	pep440 "github.com/aquasecurity/go-pep440-version"
	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/languages"
)

// venvPkgNameRe validates PEP 503 normalized package names.
// Matches names like "requests", "python-dateutil", "my_pkg123" but rejects "-invalid", "pkg-" etc.
var venvPkgNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$`)

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
	toolHint, _ := cfg.Options["tool"].(string)

	installer := selectVenvInstaller(toolHint)

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

	// Snapshot installed versions once (avoid calling pip list per package)
	installed, err := listInstalledVersions(ctx, absVenv, installer)
	if err != nil {
		return err
	}

	// Check current versions and reject downgrades
	for _, spec := range specs {
		current := installed[normalizePkgName(spec.Name)]

		if current != "" {
			// Compare versions: reject if spec is lower than current
			if isVersionLower(spec.Version, current) {
				return fmt.Errorf("%w: %s from %s to %s", ErrVenvDowngrade, spec.Name, current, spec.Version)
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
	toolHint, _ := cfg.Options["tool"].(string)

	installer := selectVenvInstaller(toolHint)

	installed, err := listInstalledVersions(ctx, absVenv, installer)
	if err != nil {
		return err
	}

	// Validate each dependency
	for _, dep := range cfg.Dependencies {
		current := installed[normalizePkgName(dep.Name)]

		if current == "" {
			return fmt.Errorf("validation: package %s not found in venv: %w", dep.Name, ErrPackageNotFound)
		}

		if current != dep.Version {
			log.Warnf("validation: %s expected %s but found %s", dep.Name, dep.Version, current)
			return fmt.Errorf("validation failed: %s expected %s, got %s: %w", dep.Name, dep.Version, current, ErrInvalidVersion)
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

// parseAndValidateVenvSpecs validates that all dependencies use == pinning, have valid package names,
// and valid versions. Rejects package names that could be interpreted as command-line options.
func parseAndValidateVenvSpecs(deps []languages.Dependency) ([]venvSpecifier, error) {
	specs := make([]venvSpecifier, 0, len(deps))

	for _, dep := range deps {
		// Reject package names starting with '-' to prevent option injection
		if strings.HasPrefix(dep.Name, "-") {
			return nil, fmt.Errorf("%w: %q (cannot start with '-')", ErrVenvInvalidPackageName, dep.Name)
		}

		// Normalize and validate package name against PEP 503 rules
		normName := normalizePkgName(dep.Name)
		if !venvPkgNameRe.MatchString(normName) {
			return nil, fmt.Errorf("%w: %q (must match PEP 503 format)", ErrVenvInvalidPackageName, dep.Name)
		}

		version := dep.Version
		if version == "" {
			return nil, fmt.Errorf("%w: %s", ErrVenvEmptyVersion, dep.Name)
		}

		// Validate version format (basic check - no leading dashes or spaces)
		if strings.HasPrefix(version, "-") || strings.Contains(version, " ") {
			return nil, fmt.Errorf("%w: %q for package %s", ErrVenvInvalidVersionFormat, version, dep.Name)
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

// selectVenvInstaller returns the appropriate installer (uv or pip).
func selectVenvInstaller(toolHint string) *venvInstaller {
	// If tool is explicitly specified as uv, use uv
	if toolHint == UVCommand {
		return &venvInstaller{
			name:    UVCommand,
			install: installWithUV,
			check:   checkWithUV,
		}
	}

	// If tool is explicitly specified as pip, use venv's pip
	if toolHint == PipCommand {
		return &venvInstaller{
			name:    PipCommand,
			install: installWithPip,
			check:   checkWithPip,
		}
	}

	// Auto-detect: if uv is in PATH, use it; otherwise use venv's pip
	_, err := exec.LookPath(UVCommand)
	if err == nil {
		return &venvInstaller{
			name:    UVCommand,
			install: installWithUV,
			check:   checkWithUV,
		}
	}

	return &venvInstaller{
		name:    PipCommand,
		install: installWithPip,
		check:   checkWithPip,
	}
}

// installWithUV installs packages using uv pip install.
func installWithUV(ctx context.Context, venv string, specs []venvSpecifier) error {
	args := make([]string, 0, 6+len(specs))
	args = append(args, "pip", "install", "--upgrade", "--only-binary", ":all:", "--no-deps")
	for _, spec := range specs {
		args = append(args, fmt.Sprintf("%s==%s", spec.Name, spec.Version))
	}

	cmd := exec.CommandContext(ctx, UVCommand, args...) //nolint:gosec // args are constructed from validated package specs
	cmd.Env = append(os.Environ(), fmt.Sprintf("VIRTUAL_ENV=%s", venv))

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", ErrPipInstallFailed, stderr.String())
	}

	return nil
}

// installWithPip installs packages using the venv's pip.
func installWithPip(ctx context.Context, venv string, specs []venvSpecifier) error {
	pipBin := filepath.Join(venv, "bin", "pip")
	// Validate pip exists before executing
	if _, err := os.Stat(pipBin); err != nil {
		return fmt.Errorf("pip not found in venv: %w", err)
	}

	args := make([]string, 0, 5+len(specs))
	args = append(args, "install", "--upgrade", "--only-binary", ":all:", "--no-deps")
	for _, spec := range specs {
		args = append(args, fmt.Sprintf("%s==%s", spec.Name, spec.Version))
	}

	cmd := exec.CommandContext(ctx, pipBin, args...) //nolint:gosec // pipBin existence verified via os.Stat above
	cmd.Env = append(os.Environ(), fmt.Sprintf("VIRTUAL_ENV=%s", venv))

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", ErrPipInstallFailed, stderr.String())
	}

	return nil
}

// checkWithUV verifies environment consistency using uv pip check.
func checkWithUV(ctx context.Context, venv string) error {
	cmd := exec.CommandContext(ctx, UVCommand, "pip", "check")
	cmd.Env = append(os.Environ(), fmt.Sprintf("VIRTUAL_ENV=%s", venv))

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", ErrPipCheckFailed, stderr.String())
	}

	return nil
}

// checkWithPip verifies environment consistency using the venv's pip check.
func checkWithPip(ctx context.Context, venv string) error {
	pipBin := filepath.Join(venv, "bin", "pip")
	// Validate pip exists before executing
	if _, err := os.Stat(pipBin); err != nil {
		return fmt.Errorf("pip not found in venv: %w", err)
	}

	cmd := exec.CommandContext(ctx, pipBin, "check") //nolint:gosec // pipBin existence verified via os.Stat above
	cmd.Env = append(os.Environ(), fmt.Sprintf("VIRTUAL_ENV=%s", venv))

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", ErrPipCheckFailed, stderr.String())
	}

	return nil
}

// listInstalledVersions returns a map of normalized package name → version
// for every package installed in the venv. Calls pip/uv list once.
func listInstalledVersions(ctx context.Context, venv string, installer *venvInstaller) (map[string]string, error) {
	var cmd *exec.Cmd
	if installer.name == UVCommand {
		cmd = exec.CommandContext(ctx, UVCommand, "pip", "list", "--format", "json")
		cmd.Env = append(os.Environ(), fmt.Sprintf("VIRTUAL_ENV=%s", venv))
	} else {
		pipBin := filepath.Join(venv, "bin", "pip")
		if _, err := os.Stat(pipBin); err != nil {
			return nil, fmt.Errorf("pip not found in venv: %w", err)
		}
		cmd = exec.CommandContext(ctx, pipBin, "list", "--format", "json") //nolint:gosec // pipBin existence verified via os.Stat above
		cmd.Env = append(os.Environ(), fmt.Sprintf("VIRTUAL_ENV=%s", venv))
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to list installed packages: %w: %s", err, stderr.String())
	}

	var packages []struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &packages); err != nil {
		return nil, fmt.Errorf("failed to parse pip list output: %w", err)
	}

	m := make(map[string]string, len(packages))
	for _, pkg := range packages {
		m[normalizePkgName(pkg.Name)] = pkg.Version
	}
	return m, nil
}

// isVersionLower returns true if v1 < v2 according to PEP 440 ordering.
// Falls back to lexicographic comparison if either version cannot be parsed.
func isVersionLower(v1, v2 string) bool {
	pv1, err1 := pep440.Parse(v1)
	pv2, err2 := pep440.Parse(v2)
	if err1 != nil || err2 != nil {
		return v1 < v2
	}
	return pv1.LessThan(pv2)
}
