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
)

// ParseSetupCfg parses install_requires from a setup.cfg file.
func ParseSetupCfg(data []byte) []VersionSpec {
	var specs []VersionSpec
	inInstallRequires := false

	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)

		if strings.HasPrefix(line, "[") {
			// New section — leave install_requires mode
			if inInstallRequires && !strings.HasPrefix(line, "[options") {
				inInstallRequires = false
			}
		}

		if strings.ToLower(line) == "install_requires =" ||
			strings.ToLower(line) == "install_requires=" {
			inInstallRequires = true
			continue
		}

		// Also handle inline: install_requires = pkg>=1.0
		if parseInlineInstallRequires(raw, &specs) {
			inInstallRequires = true
			continue
		}

		if inInstallRequires {
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			// A new key (not indented in original) ends the section
			if raw != "" && raw[0] != ' ' && raw[0] != '\t' && !strings.HasPrefix(line, "#") {
				inInstallRequires = false
				continue
			}
			if spec := parsePEP508(line); spec != nil {
				specs = append(specs, *spec)
			}
		}
	}
	return specs
}

// ParseSetupPy extracts install_requires entries from setup.py using regex.
// This handles the common pattern: install_requires=["pkg>=1.0", ...] or
// install_requires=[ ... ] across multiple lines.
func ParseSetupPy(data []byte) []VersionSpec {
	var specs []VersionSpec
	// Match contents of install_requires=[...] (greedy, handles multi-line)
	blockRe := regexp.MustCompile(`(?s)install_requires\s*=\s*\[([^\]]*)\]`)
	m := blockRe.FindSubmatch(data)
	if m == nil {
		return specs
	}
	block := string(m[1])
	// Extract quoted strings from the block
	quotedRe := regexp.MustCompile(`["']([^"']+)["']`)
	for _, match := range quotedRe.FindAllStringSubmatch(block, -1) {
		if spec := parsePEP508(strings.TrimSpace(match[1])); spec != nil {
			specs = append(specs, *spec)
		}
	}
	return specs
}

// UpdateSetupCfg updates the version of a package in the install_requires section
// of a setup.cfg file. The existing operator is preserved.
func UpdateSetupCfg(path, packageName, newVersion string) error {
	if err := validatePythonVersion(newVersion); err != nil {
		return err
	}
	if err := validateManifestPath(path); err != nil {
		return err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	norm := normalizePkgName(packageName)
	lines := strings.Split(string(data), "\n")
	found := false
	versionRe := regexp.MustCompile(`([><=!~^]+)\s*([0-9][^\s,;#]*)`)

	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		spec := parsePEP508(line)
		if spec == nil || normalizePkgName(spec.Package) != norm {
			continue
		}
		if spec.Specifier == "" {
			lines[i] = strings.TrimRight(raw, " \t") + "==" + newVersion
		} else {
			replaced := false
			newLine := versionRe.ReplaceAllStringFunc(raw, func(match string) string {
				if replaced {
					return match
				}
				replaced = true
				mm := versionRe.FindStringSubmatch(match)
				return mm[1] + newVersion
			})
			lines[i] = newLine
		}
		found = true
	}

	if !found {
		return fmt.Errorf("%w: %s", ErrPackageNotFound, packageName)
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600)
}

// parseInlineInstallRequires handles inline install_requires = pkg>=1.0 lines.
// Returns true if the line was parsed as an inline install_requires.
func parseInlineInstallRequires(raw string, specs *[]VersionSpec) bool {
	if !strings.Contains(strings.ToLower(raw), "install_requires") || !strings.Contains(raw, "=") {
		return false
	}
	parts := strings.SplitN(raw, "=", 2)
	if len(parts) != 2 || strings.TrimSpace(strings.ToLower(parts[0])) != "install_requires" {
		return false
	}
	val := strings.TrimSpace(parts[1])
	if val != "" {
		if spec := parsePEP508(val); spec != nil {
			*specs = append(*specs, *spec)
		}
	}
	return true
}
