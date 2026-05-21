/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package ruby

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

var (
	// ErrInvalidGemfileLock is returned when the lockfile cannot be parsed.
	ErrInvalidGemfileLock = errors.New("invalid Gemfile.lock format")

	// specLineRe matches a gem spec line: "    gemname (version)" with exactly 4 spaces of indent.
	specLineRe = regexp.MustCompile(`^    (\S+) \(([^)]+)\)$`)

	// depLineRe matches a dependency constraint line with 6+ spaces of indent.
	depLineRe = regexp.MustCompile(`^      (\S+)`)
)

// ParseGemfileLock parses a Gemfile.lock file and returns the list of gem packages.
// It handles GEM, GIT, PATH, PLATFORMS, and BUNDLED WITH sections.
func ParseGemfileLock(r io.Reader) ([]GemPackage, error) {
	scanner := bufio.NewScanner(r)
	var packages []GemPackage

	var (
		inSpecs    bool
		currentPkg *GemPackage
		source     string
	)

	for scanner.Scan() {
		line := scanner.Text()

		// Detect top-level section headers (no leading whitespace)
		if len(line) > 0 && line[0] != ' ' {
			// Save any pending package
			if currentPkg != nil {
				packages = append(packages, *currentPkg)
				currentPkg = nil
			}
			inSpecs = false

			trimmed := strings.TrimSpace(line)
			switch trimmed {
			case "GEM":
				source = "rubygems"
			case "GIT":
				source = "git"
			case "PATH":
				source = "path"
			default:
				source = ""
			}
			continue
		}

		trimmed := strings.TrimSpace(line)

		// Handle remote: and specs: directives
		if strings.HasPrefix(trimmed, "remote:") {
			continue
		}
		if strings.HasPrefix(trimmed, "revision:") {
			continue
		}
		if strings.HasPrefix(trimmed, "ref:") {
			continue
		}
		if strings.HasPrefix(trimmed, "tag:") {
			continue
		}
		if strings.HasPrefix(trimmed, "branch:") {
			continue
		}
		if strings.HasPrefix(trimmed, "glob:") {
			continue
		}
		if trimmed == "specs:" {
			inSpecs = true
			continue
		}

		if !inSpecs {
			continue
		}

		// Check for gem spec line (4-space indent): "    gemname (version)"
		if matches := specLineRe.FindStringSubmatch(line); matches != nil {
			// Save previous package
			if currentPkg != nil {
				packages = append(packages, *currentPkg)
			}
			currentPkg = &GemPackage{
				Name:    matches[1],
				Version: matches[2],
				Source:  source,
			}
			continue
		}

		// Check for dependency constraint line (6+ space indent): "      depname (constraint)"
		if matches := depLineRe.FindStringSubmatch(line); matches != nil && currentPkg != nil {
			currentPkg.Dependencies = append(currentPkg.Dependencies, trimmed)
			continue
		}
	}

	// Save final package
	if currentPkg != nil {
		packages = append(packages, *currentPkg)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading Gemfile.lock: %w", err)
	}

	sortGemPackages(packages)

	return packages, nil
}

// sortGemPackages sorts gem packages by name, then by version for stable output.
func sortGemPackages(packages []GemPackage) {
	sort.Slice(packages, func(i, j int) bool {
		if packages[i].Name != packages[j].Name {
			return packages[i].Name < packages[j].Name
		}
		return packages[i].Version < packages[j].Version
	})
}
