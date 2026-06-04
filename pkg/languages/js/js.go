/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package js

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/languages"
	"github.com/google/go-cmp/cmp"
)

// PackageJSON is the canonical manifest filename for JavaScript projects.
const PackageJSON = "package.json"

var (
	// ErrPackageJSONNotFound is returned when package.json is missing from
	// the project root.
	ErrPackageJSONNotFound = errors.New("package.json not found")

	// ErrNoDependencies is returned when no overrides were supplied to apply.
	ErrNoDependencies = errors.New("no dependencies to update")
)

// JS implements the Language interface for JavaScript projects.
type JS struct{}

func init() {
	languages.Register(&JS{})
}

// Name returns the language identifier.
func (j *JS) Name() string { return "js" }

// Detect checks if a JavaScript project exists in the directory.
func (j *JS) Detect(ctx context.Context, dir string) (bool, error) {
	log := clog.FromContext(ctx)

	_, err := os.Stat(filepath.Join(dir, PackageJSON))
	if err == nil {
		log.Debugf("Detected JS project at %s", dir)
		return true, nil
	}

	log.Debugf("No JS project detected at %s", dir)
	return false, nil
}

// GetManifestFiles returns JavaScript manifest and lock files.
func (j *JS) GetManifestFiles() []string {
	return []string{
		PackageJSON,
		PnpmLock,
		YarnLock,
		NpmLock,
		BunLock,
		BunLockBinary,
	}
}

// SupportsAnalysis returns true since JS has analysis capabilities.
func (j *JS) SupportsAnalysis() bool { return true }

// Update applies the configured dependency overrides to package.json.
func (j *JS) Update(ctx context.Context, cfg *languages.UpdateConfig) error {
	log := clog.FromContext(ctx)

	if len(cfg.Dependencies) == 0 {
		return ErrNoDependencies
	}

	pkgPath := j.manifestPath(cfg)
	if _, err := os.Stat(pkgPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w at %s", ErrPackageJSONNotFound, pkgPath)
		}
		return fmt.Errorf("stat package.json: %w", err)
	}

	managers, err := SelectManagers(ctx, cfg.RootDir, managersFromOptions(cfg.Options))
	if err != nil {
		return err
	}
	log.Infof("Using package manager(s): %v", managers)

	overrides := dependenciesToOverrides(cfg.Dependencies)

	log.Infof("Applying %d override(s) to %s", len(overrides), pkgPath)
	for _, ov := range overrides {
		msg := fmt.Sprintf("  %s -> %s", ov.Selector, ov.Version)
		if ov.Reason != "" {
			msg += fmt.Sprintf("  (%s)", ov.Reason)
		}
		log.Infof("%s", msg)
	}

	if cfg.DryRun {
		log.Infof("Dry run: not writing to %s", pkgPath)
		return nil
	}

	var originalContent []byte
	if cfg.ShowDiff {
		originalContent, _ = os.ReadFile(pkgPath) //nolint:gosec // pkgPath validated by os.Stat above
	}

	if err := ApplyOverrides(pkgPath, managers, overrides); err != nil {
		return fmt.Errorf("apply overrides: %w", err)
	}

	if cfg.ShowDiff && originalContent != nil {
		newContent, _ := os.ReadFile(pkgPath) //nolint:gosec // pkgPath validated by os.Stat above
		if diff := cmp.Diff(string(originalContent), string(newContent)); diff != "" {
			log.Infof("Diff for %s:\n%s", pkgPath, diff)
		}
	}

	log.Infof("Successfully applied overrides to %s", pkgPath)
	return nil
}

// Validate re-reads package.json after Update and confirms each override
// is present at the expected JSON path.
func (j *JS) Validate(ctx context.Context, cfg *languages.UpdateConfig) error {
	log := clog.FromContext(ctx)

	pkgPath := j.manifestPath(cfg)
	managers, err := SelectManagers(ctx, cfg.RootDir, managersFromOptions(cfg.Options))
	if err != nil {
		return err
	}

	overrides := dependenciesToOverrides(cfg.Dependencies)
	if err := VerifyOverrides(pkgPath, managers, overrides); err != nil {
		log.Warnf("Validation found discrepancies: %v", err)
		return err
	}

	log.Infof("Validation complete: all overrides present under %v", managers)
	return nil
}

// manifestPath returns the path to the package.json to mutate, honouring
// cfg.ManifestFile when set.
func (j *JS) manifestPath(cfg *languages.UpdateConfig) string {
	if cfg.ManifestFile != "" {
		return cfg.ManifestFile
	}
	return filepath.Join(cfg.RootDir, PackageJSON)
}

// managersFromOptions extracts an explicit manager-override list from
// cfg.Options. A nil result means "detect from disk".
func managersFromOptions(opts map[string]any) []Manager {
	v, _ := opts["manager"].([]Manager)
	return v
}

// dependenciesToOverrides projects the language-agnostic Dependency list
// into the manager-agnostic Override list used by the updater.
func dependenciesToOverrides(deps []languages.Dependency) []Override {
	out := make([]Override, 0, len(deps))
	for _, d := range deps {
		ov := Override{
			Selector: d.Name,
			Version:  d.Version,
		}
		if reason, ok := d.Metadata["reason"].(string); ok {
			ov.Reason = reason
		}
		out = append(out, ov)
	}
	return out
}
