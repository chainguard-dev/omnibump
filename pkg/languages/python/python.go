/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package python

import (
	"context"
	"errors"
	"fmt"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/languages"
)

func init() {
	languages.Register(&Python{})
}

// Python implements the Language interface for Python projects.
// It auto-detects the build tool and delegates updates to the appropriate handler.
type Python struct{}

// Name returns the language identifier.
func (p *Python) Name() string {
	return "python"
}

// Detect checks whether a Python manifest file exists in dir.
func (p *Python) Detect(_ context.Context, dir string) (bool, error) {
	_, err := DetectManifest(dir)
	if errors.Is(err, ErrManifestNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// GetManifestFiles returns all Python manifest filenames omnibump can process.
func (p *Python) GetManifestFiles() []string {
	return []string{
		"pyproject.toml",
		"requirements.txt",
		"setup.cfg",
		"setup.py",
		"Pipfile",
	}
}

// SupportsAnalysis returns true since Python has full analysis capabilities.
func (p *Python) SupportsAnalysis() bool {
	return true
}

// Update performs dependency version updates on a Python project.
// For each dependency in cfg.Dependencies, it locates the highest-priority
// manifest and updates the version in-place.
// If Options["venv"] is set, uses venv mode (uv/pip install into a staged venv).
// Otherwise uses manifest mode (edit pyproject.toml, requirements.txt, etc. in-place).
func (p *Python) Update(ctx context.Context, cfg *languages.UpdateConfig) error {
	log := clog.FromContext(ctx)

	// Check for venv mode
	var venvPath string
	if v, ok := cfg.Options["venv"]; ok {
		venvPath = v.(string)
	}

	// If venv mode is specified, use venv bumping instead of manifest editing
	if venvPath != "" {
		return updateVenv(ctx, cfg, venvPath)
	}

	// Otherwise, use manifest mode
	var toolHint string
	if t, ok := cfg.Options["tool"]; ok {
		toolHint = t.(string)
	}

	manifest, err := DetectManifestWithHint(cfg.RootDir, toolHint)
	if err != nil {
		return fmt.Errorf("detecting Python manifest in %s: %w", cfg.RootDir, err)
	}

	log.Infof("Detected Python build tool: %s (manifest: %s)", manifest.BuildTool, manifest.Type)

	for _, dep := range cfg.Dependencies {
		if cfg.DryRun {
			log.Infof("[dry-run] would update %s to %s in %s", dep.Name, dep.Version, manifest.Path)
			continue
		}

		if err := updateDepInManifest(manifest, dep.Name, dep.Version); err != nil {
			return fmt.Errorf("updating %s to %s: %w", dep.Name, dep.Version, err)
		}
		log.Infof("Updated %s to %s in %s", dep.Name, dep.Version, manifest.Path)
	}

	if manifest.BuildTool == BuildToolUV {
		log.Warnf("uv project: re-run 'uv lock' to regenerate uv.lock after updating pyproject.toml")
	}
	if manifest.BuildTool == BuildToolPDM {
		log.Warnf("pdm project: re-run 'pdm lock' to regenerate pdm.lock after updating pyproject.toml")
	}

	return nil
}

// Validate checks that each dependency was updated to the expected version.
// If Options["venv"] is set, validates versions in the venv.
// Otherwise validates versions in the manifest file.
func (p *Python) Validate(ctx context.Context, cfg *languages.UpdateConfig) error {
	log := clog.FromContext(ctx)

	// Check for venv mode
	var venvPath string
	if v, ok := cfg.Options["venv"]; ok {
		venvPath = v.(string)
	}

	// If venv mode is specified, validate venv instead of manifest
	if venvPath != "" {
		return validateVenv(ctx, cfg, venvPath)
	}

	// Otherwise, use manifest mode
	var toolHint string
	if t, ok := cfg.Options["tool"]; ok {
		toolHint = t.(string)
	}

	manifest, err := DetectManifestWithHint(cfg.RootDir, toolHint)
	if err != nil {
		return fmt.Errorf("detecting Python manifest in %s: %w", cfg.RootDir, err)
	}

	specs, err := readSpecsFromManifest(manifest)
	if err != nil {
		return err
	}

	// Build a lookup map of package → current version
	current := make(map[string]string, len(specs))
	for _, s := range specs {
		current[s.Package] = s.Version
	}

	for _, dep := range cfg.Dependencies {
		norm := normalizePkgName(dep.Name)
		got, ok := current[norm]
		if !ok {
			return fmt.Errorf("validation: %w: %s", ErrPackageNotFound, dep.Name)
		}
		if got != dep.Version {
			log.Warnf("validation: %s expected %s but found %s", dep.Name, dep.Version, got)
			return fmt.Errorf("validation failed: %s expected %s, got %s: %w", dep.Name, dep.Version, got, ErrInvalidVersion)
		}
		log.Debugf("validation ok: %s == %s", dep.Name, dep.Version)
	}

	return nil
}

// updateDepInManifest routes the update to the correct file handler.
func updateDepInManifest(manifest *ManifestInfo, pkg, version string) error {
	switch manifest.Type {
	case "pyproject.toml":
		return UpdatePyprojectDep(manifest.Path, pkg, version)
	case "requirements.txt":
		return UpdateRequirement(manifest.Path, pkg, version)
	case "setup.cfg":
		return UpdateSetupCfg(manifest.Path, pkg, version)
	case "Pipfile":
		return UpdatePipfile(manifest.Path, pkg, version)
	case ManifestSetupPy:
		return ErrSetupPyReadOnly
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedManifestType, manifest.Type)
	}
}
