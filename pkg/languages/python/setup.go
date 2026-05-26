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

// Package-level compiled regexes for setup.py and setup.cfg.
var (
	// setupPyBlockRe matches install_requires=[...] blocks in setup.py.
	setupPyBlockRe = regexp.MustCompile(`(?s)install_requires\s*=\s*\[([^\]]*)\]`)

	// setupPyQuotedRe extracts quoted strings from setup.py blocks.
	setupPyQuotedRe = regexp.MustCompile(`["']([^"']+)["']`)

	// setupCfgVersionRe matches operator + version in setup.cfg lines.
	setupCfgVersionRe = regexp.MustCompile(`([><=!~^]+)\s*([0-9][^\s,;#]*)`)
)

// ParseSetupPy extracts install_requires entries from setup.py using regex.
// This handles the common pattern: install_requires=["pkg>=1.0", ...] or
// install_requires=[ ... ] across multiple lines.
func ParseSetupPy(data []byte) []VersionSpec {
	var specs []VersionSpec
	m := setupPyBlockRe.FindSubmatch(data)
	if m == nil {
		return specs
	}
	block := string(m[1])
	for _, match := range setupPyQuotedRe.FindAllStringSubmatch(block, -1) {
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

	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	norm := normalizePkgName(packageName)
	lines := strings.Split(string(data), "\n")
	found := false

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
			newLine := setupCfgVersionRe.ReplaceAllStringFunc(raw, func(match string) string {
				if replaced {
					return match
				}
				replaced = true
				mm := setupCfgVersionRe.FindStringSubmatch(match)
				return mm[1] + newVersion
			})
			lines[i] = newLine
		}
		found = true
	}

	if !found {
		return fmt.Errorf("%w: %s", ErrPackageNotFound, packageName)
	}
	return safeWriteFile(path, []byte(strings.Join(lines, "\n")))
}

// inlineInstallRequiresRe matches "install_requires = <value>" (case-insensitive).
var inlineInstallRequiresRe = regexp.MustCompile(`(?i)^\s*install_requires\s*=\s*(.*)$`)

// parseInlineInstallRequires handles inline install_requires = pkg>=1.0 lines.
// Returns true if the line was parsed as an inline install_requires.
func parseInlineInstallRequires(raw string, specs *[]VersionSpec) bool {
	m := inlineInstallRequiresRe.FindStringSubmatch(raw)
	if m == nil {
		return false
	}
	val := strings.TrimSpace(m[1])
	if val != "" {
		if spec := parsePEP508(val); spec != nil {
			*specs = append(*specs, *spec)
		}
	}
	return true
}
