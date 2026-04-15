/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package python

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// pipfileDoc represents the dependency-relevant sections of a Pipfile.
type pipfileDoc struct {
	Packages    map[string]interface{} `toml:"packages"`
	DevPackages map[string]interface{} `toml:"dev-packages"`
}

// ParsePipfile parses [packages] and [dev-packages] from a Pipfile.
func ParsePipfile(data []byte) ([]VersionSpec, error) {
	var doc pipfileDoc
	if err := toml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing Pipfile: %w", err)
	}

	var specs []VersionSpec
	for section, deps := range map[string]map[string]interface{}{
		"packages":     doc.Packages,
		"dev-packages": doc.DevPackages,
	} {
		_ = section
		for name, val := range deps {
			var ver string
			switch v := val.(type) {
			case string:
				ver = v
			case map[string]interface{}:
				if s, ok := v["version"].(string); ok {
					ver = s
				}
			}
			if ver == "" || ver == "*" {
				continue
			}
			_, op, version := splitSpecifier(ver)
			specs = append(specs, VersionSpec{
				Package:   normalizePkgName(name),
				Specifier: op,
				Version:   version,
				RawLine:   fmt.Sprintf("%s = %q", name, ver),
			})
		}
	}
	return specs, nil
}

// UpdatePipfile updates the version of a named package in a Pipfile.
// The existing operator is preserved. newVersion is a bare version number.
func UpdatePipfile(path, packageName, newVersion string) error {
	if err := validatePythonVersion(newVersion); err != nil {
		return err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	norm := normalizePkgName(packageName)
	lines := strings.Split(string(data), "\n")
	found := false

	// Match: pkg = "specifier" in Pipfile (TOML style)
	lineRe := regexp.MustCompile(`(?i)^(\s*)([A-Z0-9][A-Z0-9._-]*)(\s*=\s*")([><=!~^]?)([0-9][^"]*)(".*$)`)

	for i, raw := range lines {
		m := lineRe.FindStringSubmatchIndex(raw)
		if m == nil {
			continue
		}
		name := raw[m[4]:m[5]]
		if normalizePkgName(name) != norm {
			continue
		}
		// Replace the version digits (m[10]:m[11])
		lines[i] = raw[:m[10]] + newVersion + raw[m[11]:]
		found = true
	}

	if !found {
		return fmt.Errorf("%w: %s", ErrPackageNotFound, packageName)
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600)
}
