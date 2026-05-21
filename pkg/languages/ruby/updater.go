/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package ruby

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/chainguard-dev/clog"
	"golang.org/x/mod/semver"
)

// DoUpdate performs the actual update of gem versions in a Gemfile.lock.
// It does direct text replacement — no Ruby/Bundler CLI needed.
func DoUpdate(ctx context.Context, packages map[string]*Package, gemfileLockPath string) error {
	log := clog.FromContext(ctx)

	content, err := os.ReadFile(filepath.Clean(gemfileLockPath))
	if err != nil {
		return fmt.Errorf("failed to read Gemfile.lock: %w", err)
	}

	text := string(content)

	// Order packages by index for consistent updates
	orderedPackages := orderPackages(packages)

	for _, pkgName := range orderedPackages {
		pkg := packages[pkgName]
		if pkg == nil {
			log.Warnf("Package %s has nil entry in packages map, skipping", pkgName)
			continue
		}

		updated, newText := updateGemVersion(log, text, pkg)
		if updated {
			text = newText
		}
	}

	if err := os.WriteFile(gemfileLockPath, []byte(text), 0o600); err != nil {
		return fmt.Errorf("failed to write Gemfile.lock: %w", err)
	}

	return nil
}

// updateGemVersion replaces a gem's version in the lockfile text.
// It looks for lines matching "    gemname (oldversion)" and replaces with "    gemname (newversion)".
// Returns whether a replacement was made and the new text.
func updateGemVersion(log interface {
	Warnf(string, ...any)
	Infof(string, ...any)
}, text string, pkg *Package,
) (bool, string) {
	// Build a regex to find the spec line for this gem.
	// Match "    gemname (version)" exactly with 4-space indent.
	pattern := fmt.Sprintf(`^(    %s \()([^)]+)(\))`, regexp.QuoteMeta(pkg.Name))
	re := regexp.MustCompile("(?m)" + pattern)

	matches := re.FindStringSubmatch(text)
	if matches == nil {
		log.Warnf("Gem %s not found in Gemfile.lock", pkg.Name)
		return false, text
	}

	currentVersion := matches[2]

	// Check if already at target version
	if currentVersion == pkg.Version {
		log.Infof("Gem %s is already at version %s, skipping", pkg.Name, pkg.Version)
		return false, text
	}

	// Check for downgrade (if both are valid semver)
	if semver.IsValid("v"+currentVersion) && semver.IsValid("v"+pkg.Version) {
		if semver.Compare("v"+currentVersion, "v"+pkg.Version) > 0 {
			log.Warnf("Gem %s: current version %s is newer than requested %s, skipping",
				pkg.Name, currentVersion, pkg.Version)
			return false, text
		}
	}

	log.Infof("Updating gem %s from version %s to %s", pkg.Name, currentVersion, pkg.Version)

	// Replace the version
	replacement := fmt.Sprintf("${1}%s${3}", pkg.Version)
	newText := re.ReplaceAllString(text, replacement)

	return true, newText
}

// orderPackages returns package names sorted by their index.
func orderPackages(packages map[string]*Package) []string {
	names := make([]string, 0, len(packages))
	for name := range packages {
		names = append(names, name)
	}

	sort.SliceStable(names, func(i, j int) bool {
		return packages[names[i]].Index < packages[names[j]].Index
	})

	return names
}
