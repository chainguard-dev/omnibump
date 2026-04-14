/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package composer

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Lock represents the structure of a composer.lock file.
type Lock struct {
	Packages    []lockEntry `json:"packages"`
	PackagesDev []lockEntry `json:"packages-dev"`
}

// lockEntry represents a package entry in composer.lock.
type lockEntry struct {
	Name    string            `json:"name"`
	Version string            `json:"version"`
	Require map[string]string `json:"require,omitempty"`
}

// ParseLock parses a composer.lock file and returns the list of packages.
func ParseLock(r io.Reader) ([]LockPackage, error) {
	var lockfile Lock
	decoder := json.NewDecoder(r)
	if err := decoder.Decode(&lockfile); err != nil {
		return nil, fmt.Errorf("failed to decode composer.lock: %w", err)
	}

	// Combine regular packages and dev packages
	allPackages := make([]lockEntry, 0, len(lockfile.Packages)+len(lockfile.PackagesDev))
	allPackages = append(allPackages, lockfile.Packages...)
	allPackages = append(allPackages, lockfile.PackagesDev...)

	result := make([]LockPackage, 0, len(allPackages))
	for _, pkg := range allPackages {
		p := LockPackage{
			Name:    pkg.Name,
			Version: normalizeVersion(pkg.Version),
		}

		// Extract dependency names from require map
		if len(pkg.Require) > 0 {
			for depName := range pkg.Require {
				p.Require = append(p.Require, depName)
			}
			sort.Strings(p.Require)
		}

		result = append(result, p)
	}

	sortLockPackages(result)
	return result, nil
}

// normalizeVersion removes the "v" prefix if present for consistency.
func normalizeVersion(version string) string {
	return strings.TrimPrefix(version, "v")
}

// sortLockPackages sorts packages by name for consistent ordering.
func sortLockPackages(pkgs []LockPackage) {
	sort.Slice(pkgs, func(i, j int) bool {
		return strings.Compare(pkgs[i].Name, pkgs[j].Name) < 0
	})
}
