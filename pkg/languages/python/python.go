/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package python

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/languages"
	"github.com/google/go-cmp/cmp"
)

func init() {
	languages.Register(&Python{})
}

// Python implements the Language interface for Python projects.
// It auto-detects the build tool and delegates updates to the appropriate handler.
type Python struct{}

// Verify Python implements the languages.Language interface at compile time.
var _ languages.Language = (*Python)(nil)

// Name returns the language identifier.
func (p *Python) Name() string {
	return LanguagePython
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
		ManifestPyprojectTOML,
		ManifestRequirementsTxt,
		ManifestSetupCfg,
		ManifestSetupPy,
		ManifestPipfile,
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
	venvPath, _ := cfg.Options["venv"].(string)

	// If venv mode is specified, use venv bumping instead of manifest editing
	if venvPath != "" {
		return updateVenv(ctx, cfg, venvPath)
	}

	// Otherwise, use manifest mode
	manifest, err := resolveManifest(cfg)
	if err != nil {
		return err
	}

	log.Infof("Using Python build tool: %s (manifest: %s)", manifest.BuildTool, manifest.Type)

	// Snapshot the manifest before updates for --show-diff.
	var originalContent []byte
	if cfg.ShowDiff {
		originalContent, _ = os.ReadFile(manifest.Path)
	}

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

	if cfg.ShowDiff && !cfg.DryRun && originalContent != nil {
		newContent, _ := os.ReadFile(manifest.Path)
		if diff := cmp.Diff(string(originalContent), string(newContent)); diff != "" {
			log.Infof("Diff for %s:\n%s", manifest.Path, diff)
		}
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
	venvPath, _ := cfg.Options["venv"].(string)

	// If venv mode is specified, validate venv instead of manifest
	if venvPath != "" {
		return validateVenv(ctx, cfg, venvPath)
	}

	// Otherwise, use manifest mode
	manifest, err := resolveManifest(cfg)
	if err != nil {
		return err
	}

	specs, err := readSpecsFromManifest(manifest)
	if err != nil {
		return err
	}

	// Build a lookup map of package → spec
	currentSpecs := make(map[string]VersionSpec, len(specs))
	for _, s := range specs {
		currentSpecs[s.Package] = s
	}

	for _, dep := range cfg.Dependencies {
		norm := normalizePkgName(dep.Name)
		spec, ok := currentSpecs[norm]
		if !ok {
			return fmt.Errorf("validation: %w: %s", ErrPackageNotFound, dep.Name)
		}
		// For simple specifiers, compare the parsed version directly.
		// For compound specifiers (e.g. ">=46.0.5, <47.0.0"), the Version
		// field is empty — check that the expected version appears in the
		// raw specifier string instead.
		if spec.Version != "" {
			if spec.Version != dep.Version {
				log.Warnf("validation: %s expected %s but found %s", dep.Name, dep.Version, spec.Version)
				return fmt.Errorf("validation failed: %s expected %s, got %s: %w", dep.Name, dep.Version, spec.Version, ErrInvalidVersion)
			}
		} else if spec.Specifier != "" {
			if !strings.Contains(spec.Specifier, dep.Version) {
				log.Warnf("validation: %s expected %s but specifier is %s", dep.Name, dep.Version, spec.Specifier)
				return fmt.Errorf("validation failed: %s expected %s in specifier %s: %w", dep.Name, dep.Version, spec.Specifier, ErrInvalidVersion)
			}
		}
		log.Debugf("validation ok: %s == %s", dep.Name, dep.Version)
	}

	return nil
}

// resolveManifest returns the manifest to operate on. If ManifestFile is set,
// it uses that path directly; otherwise it auto-detects using the tool hint.
func resolveManifest(cfg *languages.UpdateConfig) (*ManifestInfo, error) {
	if cfg.ManifestFile != "" {
		absPath, err := filepath.Abs(cfg.ManifestFile)
		if err != nil {
			return nil, fmt.Errorf("resolving manifest path: %w", err)
		}
		name := filepath.Base(absPath)
		dir := filepath.Dir(absPath)
		info := &ManifestInfo{Path: absPath, Type: name}
		info.BuildTool = detectBuildTool(name, dir, absPath)
		return info, nil
	}

	toolHint, _ := cfg.Options["tool"].(string)
	manifest, err := DetectManifestWithHint(cfg.RootDir, toolHint)
	if err != nil {
		return nil, fmt.Errorf("detecting Python manifest in %s: %w", cfg.RootDir, err)
	}
	return manifest, nil
}

// updateDepInManifest routes the update to the correct file handler.
func updateDepInManifest(manifest *ManifestInfo, pkg, version string) error {
	switch manifest.Type {
	case ManifestPyprojectTOML:
		return UpdatePyprojectDep(manifest.Path, pkg, version)
	case ManifestRequirementsTxt, ManifestRequirementsPinnedTxt:
		return UpdateRequirement(manifest.Path, pkg, version)
	case ManifestSetupCfg:
		return UpdateSetupCfg(manifest.Path, pkg, version)
	case ManifestPipfile:
		return UpdatePipfile(manifest.Path, pkg, version)
	case ManifestSetupPy:
		return ErrSetupPyReadOnly
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedManifestType, manifest.Type)
	}
}
