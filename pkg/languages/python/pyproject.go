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

	"github.com/BurntSushi/toml"
)

// pyprojectDoc represents the dependency-relevant sections of pyproject.toml.
type pyprojectDoc struct {
	Project struct {
		Dependencies []string `toml:"dependencies"`
	} `toml:"project"`
	Tool struct {
		Poetry struct {
			Dependencies map[string]interface{} `toml:"dependencies"`
		} `toml:"poetry"`
	} `toml:"tool"`
}

// ParsePyprojectDeps parses dependencies from pyproject.toml content.
// For Poetry projects, it reads [tool.poetry.dependencies].
// For all others (PEP 621 — hatch, maturin, setuptools, scikit-build-core, pdm, uv),
// it reads [project].dependencies.
func ParsePyprojectDeps(data []byte, buildTool BuildTool) ([]VersionSpec, error) {
	var doc pyprojectDoc
	if err := toml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing pyproject.toml: %w", err)
	}

	if buildTool == BuildToolPoetry {
		return parsePoetryDeps(doc.Tool.Poetry.Dependencies), nil
	}
	return parsePEP621Deps(doc.Project.Dependencies), nil
}

// parsePEP621Deps parses the PEP 621 [project].dependencies list.
// Each entry is a PEP 508 dependency specifier string.
func parsePEP621Deps(deps []string) []VersionSpec {
	specs := make([]VersionSpec, 0, len(deps))
	for _, dep := range deps {
		spec := parsePEP508(strings.TrimSpace(dep))
		if spec != nil {
			specs = append(specs, *spec)
		}
	}
	return specs
}

// parsePoetryDeps parses [tool.poetry.dependencies].
// Values may be strings ("^1.0") or inline tables ({version = "^1.0", optional = true}).
func parsePoetryDeps(deps map[string]interface{}) []VersionSpec {
	specs := make([]VersionSpec, 0, len(deps))
	for name, val := range deps {
		if name == "python" {
			continue
		}
		var ver string
		switch v := val.(type) {
		case string:
			ver = v
		case map[string]interface{}:
			if s, ok := v["version"].(string); ok {
				ver = s
			}
		}
		if ver == "" {
			continue
		}
		full, op, version := splitSpecifier(ver)
		specs = append(specs, VersionSpec{
			Package:   normalizePkgName(name),
			Specifier: op,
			Version:   version,
			RawLine:   fmt.Sprintf("%s = %q", name, full),
		})
	}
	return specs
}

// pep508Re matches a PEP 508 dependency string: name [extras] [specifier] [; marker].
var pep508Re = regexp.MustCompile(`(?i)^([A-Z0-9]([A-Z0-9._-]*[A-Z0-9])?)\s*(?:\[[^\]]*\])?\s*([><=!~^][^;]*)?`)

// parsePEP508 parses a single PEP 508 specifier string.
func parsePEP508(s string) *VersionSpec {
	m := pep508Re.FindStringSubmatch(s)
	if m == nil {
		return nil
	}
	name := m[1]
	rawSpec := strings.TrimSpace(m[3])
	_, op, version := splitSpecifier(rawSpec)
	return &VersionSpec{
		Package:   normalizePkgName(name),
		Specifier: op,
		Version:   version,
		RawLine:   s,
	}
}

// splitSpecifier splits a version specifier like ">=2.28.0" into (">=", "2.28.0").
// For compound specifiers like ">=1.0,<2.0" it returns the full string as specifier.
func splitSpecifier(s string) (full, op, version string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", ""
	}
	// Compound specifier (contains comma)
	if strings.Contains(s, ",") {
		return s, s, ""
	}
	opRe := regexp.MustCompile(`^([><=!~^]+)(.+)$`)
	m := opRe.FindStringSubmatch(s)
	if m == nil {
		return s, "", s
	}
	return s, m[1], strings.TrimSpace(m[2])
}

// UpdatePyprojectDep updates the version of a named dependency in pyproject.toml.
// It preserves comments and formatting by operating on the raw file text.
// newVersion should be just the version number (e.g. "2.32.0"), not include an operator.
// The existing operator is preserved.
func UpdatePyprojectDep(path, packageName, newVersion string) error {
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

	// Detect the build tool to know which section to update.
	bt, _ := DetectBuildToolFromPyproject(path)

	updated, found := false, false
	if bt == BuildToolPoetry {
		updated, err = updatePoetryDep(data, path, packageName, newVersion)
		found = updated
	} else {
		updated, err = updatePEP621Dep(data, path, packageName, newVersion)
		found = updated
	}

	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: %s", ErrPackageNotFound, packageName)
	}
	return nil
}

// updatePEP621Dep updates a dep in [project].dependencies list.
// Lines look like: "requests>=2.28.0".
func updatePEP621Dep(data []byte, path, pkg, newVersion string) (bool, error) {
	lines := strings.Split(string(data), "\n")
	norm := normalizePkgName(pkg)
	found := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Strip surrounding quotes and trailing comma for matching
		inner := strings.Trim(trimmed, `"',`)
		spec := parsePEP508(inner)
		if spec == nil || normalizePkgName(spec.Package) != norm {
			continue
		}
		// Rebuild the specifier with the new version
		newInner := rebuildPEP508(inner, spec, newVersion)
		lines[i] = strings.Replace(line, inner, newInner, 1)
		found = true
	}

	if !found {
		return false, nil
	}
	return true, safeWriteFile(path, []byte(strings.Join(lines, "\n")))
}

// updatePoetryDep updates a dep in [tool.poetry.dependencies].
// Lines look like: requests = "^2.28.0".
func updatePoetryDep(data []byte, path, pkg, newVersion string) (bool, error) {
	lines := strings.Split(string(data), "\n")
	norm := normalizePkgName(pkg)
	found := false

	// Match: pkg = "specifier" or pkg = {version = "specifier", ...}
	simpleRe := regexp.MustCompile(`(?i)^(\s*)([A-Z0-9][A-Z0-9._-]*)(\s*=\s*")([><=!~^]*)([0-9][^"]*)(".*$)`)
	tableRe := regexp.MustCompile(`(?i)^(\s*)([A-Z0-9][A-Z0-9._-]*)(\s*=\s*\{[^}]*version\s*=\s*")([><=!~^]*)([0-9][^"]*)(".*$)`)

	for i, line := range lines {
		for _, re := range []*regexp.Regexp{simpleRe, tableRe} {
			m := re.FindStringSubmatchIndex(line)
			if m == nil {
				continue
			}
			// m[4] = start of name, m[5] = end of name
			name := line[m[4]:m[5]]
			if normalizePkgName(name) != norm {
				continue
			}
			// m[10] = start of version digits, m[11] = end
			lines[i] = line[:m[10]] + newVersion + line[m[11]:]
			found = true
			break
		}
	}

	if !found {
		return false, nil
	}
	return true, safeWriteFile(path, []byte(strings.Join(lines, "\n")))
}

// rebuildPEP508 reconstructs a PEP 508 specifier with a new version.
// The existing operator is preserved; compound specifiers are replaced wholesale.
func rebuildPEP508(original string, spec *VersionSpec, newVersion string) string {
	if spec.Specifier == "" {
		return original // no version specifier — leave unchanged
	}
	// For compound specifiers, replace first version occurrence only
	if strings.Contains(spec.Specifier, ",") {
		// Replace the first version number in the specifier
		vRe := regexp.MustCompile(`([><=!~^]+)\s*([0-9][^\s,;"]*)`)
		replaced := false
		result := vRe.ReplaceAllStringFunc(original, func(match string) string {
			if replaced {
				return match
			}
			replaced = true
			m := vRe.FindStringSubmatch(match)
			return m[1] + newVersion
		})
		return result
	}
	// Simple specifier: replace version after the operator
	vRe := regexp.MustCompile(`([><=!~^]+)\s*([0-9][^\s,;"]*)`)
	return vRe.ReplaceAllStringFunc(original, func(match string) string {
		m := vRe.FindStringSubmatch(match)
		return m[1] + newVersion
	})
}

// normalizePkgName normalises a Python package name per PEP 503:
// lowercase, with runs of [-_.] replaced by a single hyphen.
func normalizePkgName(name string) string {
	name = strings.ToLower(name)
	re := regexp.MustCompile(`[-_.]+`)
	return re.ReplaceAllString(name, "-")
}

// validatePythonVersion checks that a version string is safe to write into a manifest file.
// Allows: digits, dots, letters, hyphens, underscores, plus signs.
func validatePythonVersion(v string) error {
	re := regexp.MustCompile(`^[a-zA-Z0-9._+\-]+$`)
	if !re.MatchString(v) {
		return fmt.Errorf("%w: %q", ErrInvalidVersion, v)
	}
	return nil
}
