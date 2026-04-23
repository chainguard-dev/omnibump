/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package python

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// reqLineRe matches a pip requirement line with an optional version specifier.
// It captures: (package)(extras)(specifier)(version)(rest — markers etc.)
var reqLineRe = regexp.MustCompile(`(?i)^([A-Z0-9]([A-Z0-9._-]*[A-Z0-9])?)(\[[^\]]*\])?\s*([><=!~^][^;#\n]*)?(.*)$`)

// ParseRequirements parses a requirements.txt byte slice and returns dependency specs.
// Comment lines, blank lines, and option flags (-r, -c, -e, -i, --index-url) are skipped.
func ParseRequirements(data []byte) []VersionSpec {
	var specs []VersionSpec
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			continue
		}
		// Strip inline comment
		if idx := strings.Index(line, " #"); idx != -1 {
			line = strings.TrimSpace(line[:idx])
		}
		m := reqLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := m[1]
		rawSpec := strings.TrimSpace(m[4])
		op, version := splitReqSpecifier(rawSpec)
		specs = append(specs, VersionSpec{
			Package:   normalizePkgName(name),
			Specifier: op,
			Version:   version,
			RawLine:   raw,
		})
	}
	return specs
}

// splitReqSpecifier splits ">=2.28.0" into (">=", "2.28.0").
// For compound specifiers (">=1.0,<2.0") returns the full string as specifier.
func splitReqSpecifier(s string) (op, version string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	if strings.Contains(s, ",") {
		return s, ""
	}
	re := regexp.MustCompile(`^([><=!~^]+)(.+)$`)
	m := re.FindStringSubmatch(s)
	if m == nil {
		return "", s
	}
	return m[1], strings.TrimSpace(m[2])
}

// UpdateRequirement updates the version of the named package in a requirements.txt file.
// The existing version operator is preserved (==, >=, ~=, etc.).
// newVersion is the bare version number without an operator.
func UpdateRequirement(path, packageName, newVersion string) error {
	if err := validatePythonVersion(newVersion); err != nil {
		return err
	}
	if err := validateManifestPath(path); err != nil {
		return err
	}

	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	norm := normalizePkgName(packageName)
	lines := strings.Split(string(data), "\n")
	found := false

	// versionRe matches the operator(s) and version in a requirement line.
	versionRe := regexp.MustCompile(`([><=!~^]+)\s*([0-9][^\s,;#]*)`)

	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			continue
		}
		m := reqLineRe.FindStringSubmatch(line)
		if m == nil || normalizePkgName(m[1]) != norm {
			continue
		}
		if m[4] == "" {
			// No existing specifier — append ==newVersion
			lines[i] = strings.TrimRight(raw, " \t") + "==" + newVersion
		} else {
			// Replace first version in the specifier, preserving the operator.
			replaced := false
			newSpec := versionRe.ReplaceAllStringFunc(m[4], func(match string) string {
				if replaced {
					return match
				}
				replaced = true
				mm := versionRe.FindStringSubmatch(match)
				return mm[1] + newVersion
			})
			lines[i] = strings.Replace(raw, m[4], newSpec, 1)
		}
		found = true
	}

	if !found {
		return fmt.Errorf("%w: %s", ErrPackageNotFound, packageName)
	}
	return safeWriteFile(path, []byte(strings.Join(lines, "\n")))
}
