/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package rust

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/chainguard-dev/clog"
	"github.com/ghodss/yaml"
	"github.com/samber/lo"
)

// ErrInvalidPackageSpec is returned when a package spec is missing required fields.
var ErrInvalidPackageSpec = errors.New("invalid package spec")

// cargoLockPackage represents a package entry in Cargo.lock.
type cargoLockPackage struct {
	Name         string   `toml:"name"`
	Version      string   `toml:"version"`
	Dependencies []string `toml:"dependencies,omitempty"`
}

// Lockfile represents a Cargo.lock file structure.
// Based on https://doc.rust-lang.org/cargo/reference/manifest.html
type Lockfile struct {
	Packages []cargoLockPackage `toml:"package"`
}

// ParseCargoLock parses a Cargo.lock file and returns the list of packages.
func ParseCargoLock(r io.Reader) ([]CargoPackage, error) {
	var lockfile Lockfile
	decoder := toml.NewDecoder(r)
	if _, err := decoder.Decode(&lockfile); err != nil {
		return nil, fmt.Errorf("failed to decode Cargo.lock: %w", err)
	}

	// Create map of package names to versions for dependency resolution
	// Needed for lockfile v3 where dependencies may not include versions
	pkgs := lo.SliceToMap(lockfile.Packages, func(pkg cargoLockPackage) (string, cargoLockPackage) {
		return pkg.Name, pkg
	})

	result := make([]CargoPackage, 0, len(lockfile.Packages))
	for _, pkg := range lockfile.Packages {
		p := CargoPackage{
			Name:    pkg.Name,
			Version: pkg.Version,
		}

		deps := parseDependencies(pkg, pkgs)
		if len(deps) > 0 {
			p.Dependencies = append(p.Dependencies, deps...)
		}

		result = append(result, p)
	}

	sortCargoPackages(result)
	return result, nil
}

// sortCargoPackages sorts packages by name@version identifier.
func sortCargoPackages(pkgs []CargoPackage) {
	sort.Slice(pkgs, func(i, j int) bool {
		return strings.Compare(
			packageID(pkgs[i].Name, pkgs[i].Version),
			packageID(pkgs[j].Name, pkgs[j].Version),
		) < 0
	})
}

// parseDependencies extracts dependency information from a package entry.
func parseDependencies(pkg cargoLockPackage, pkgs map[string]cargoLockPackage) []string {
	var dependOn []string

	for _, pkgDep := range pkg.Dependencies {
		/*
			Dependency entries look like:
			- "any-package" - if lock file contains only 1 version of dependency
			- "any-package 0.1.2" - if lock file contains more than 1 version of dependency
		*/
		fields := strings.Fields(pkgDep)
		switch len(fields) {
		case 1:
			// Single version dependency - look up version from packages map
			name := fields[0]
			version, ok := pkgs[name]
			if !ok {
				clog.Warnf("can't find version for dependency %s", name)
				continue
			}
			dependOn = append(dependOn, packageID(name, version.Version))
		case 2, 3:
			// Multiple version dependency (2 fields in new format, 3 in old format)
			dependOn = append(dependOn, packageID(fields[0], fields[1]))
		default:
			clog.Warnf("unexpected dependency format: %s", pkgDep)
			continue
		}
	}

	return dependOn
}

// packageID creates a unique identifier for a package.
func packageID(name, version string) string {
	return fmt.Sprintf("%s@%s", name, version)
}

// PackageList is used to marshal from yaml/json file to get the list of packages.
type PackageList struct {
	Packages []Package `json:"packages" yaml:"packages"`
}

// ParseBumpFile parses a YAML file containing package update specifications.
// Ported from cargobump/pkg/parser/parser.go:ParseBumpFile.
func ParseBumpFile(r io.Reader) (map[string]*Package, error) {
	bytes, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}
	var patches map[string]*Package
	var packageList PackageList

	if err := yaml.Unmarshal(bytes, &packageList); err != nil {
		return patches, fmt.Errorf("unmarshaling file: %w", err)
	}

	for i, p := range packageList.Packages {
		if p.Name == "" {
			return patches, fmt.Errorf("%w at [%d]: missing name", ErrInvalidPackageSpec, i)
		}

		if p.Version == "" {
			return patches, fmt.Errorf("%w at [%d]: missing version", ErrInvalidPackageSpec, i)
		}

		if patches == nil {
			patches = make(map[string]*Package, 1)
		}

		patches[p.Name] = &packageList.Packages[i]
	}

	return patches, nil
}
