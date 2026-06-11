/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradlefile

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// CatalogFile is a parsed gradle/libs.versions.toml version catalog. The
// TOML structure is read with a real TOML parser; edits are line-targeted
// splices on the raw text so formatting, comments and key alignment survive.
type CatalogFile struct {
	path      string
	buf       editBuffer
	versions  []CatalogVersion
	libraries []CatalogLibrary
}

// ParseCatalog parses a libs.versions.toml version catalog.
func ParseCatalog(path string, content []byte) (*CatalogFile, error) {
	var catalog struct {
		Versions  map[string]any `toml:"versions"`
		Libraries map[string]any `toml:"libraries"`
	}
	if err := toml.Unmarshal(content, &catalog); err != nil {
		return nil, fmt.Errorf("failed to parse TOML catalog %s: %w", path, err)
	}

	f := &CatalogFile{
		path: path,
		buf:  editBuffer{original: content},
	}
	f.collectVersions(catalog.Versions)
	f.collectLibraries(catalog.Libraries)
	return f, nil
}

// Path returns the file path the catalog was parsed from.
func (f *CatalogFile) Path() string { return f.path }

// Versions returns the [versions] entries with string values.
func (f *CatalogFile) Versions() []CatalogVersion { return f.versions }

// Libraries returns the [libraries] entries with resolvable coordinates.
func (f *CatalogFile) Libraries() []CatalogLibrary { return f.libraries }

// SetVersion queues a rewrite of the [versions] entry named key.
func (f *CatalogFile) SetVersion(key, value string) error {
	if err := ValidateVersion(value); err != nil {
		return err
	}
	for _, v := range f.versions {
		if v.Key != key {
			continue
		}
		if !v.valueSpan.valid() {
			return fmt.Errorf("%w: catalog version %s in %s", ErrNotEditable, key, f.path)
		}
		return f.buf.add(v.valueSpan, value)
	}
	return fmt.Errorf("%w: catalog version %s in %s", ErrNotEditable, key, f.path)
}

// SetLibraryVersion queues a rewrite of lib's inline version literal.
// Libraries that use version.ref are updated through SetVersion on the
// referenced key instead.
func (f *CatalogFile) SetLibraryVersion(lib CatalogLibrary, value string) error {
	if err := ValidateVersion(value); err != nil {
		return err
	}
	if lib.VersionRef != "" || !lib.versionSpan.valid() {
		return fmt.Errorf("%w: catalog library %s in %s has no inline version", ErrNotEditable, lib.Alias, f.path)
	}
	return f.buf.add(lib.versionSpan, value)
}

// Content renders the catalog with all queued edits applied.
func (f *CatalogFile) Content() []byte { return f.buf.render() }

// Changed reports whether any queued edit modifies the original content.
func (f *CatalogFile) Changed() bool { return f.buf.changed() }

// ChangeCount returns the number of queued edits that modify the content.
func (f *CatalogFile) ChangeCount() int { return f.buf.changeCount() }

// collectVersions records [versions] entries with string values, locating
// each entry's value span in the raw text.
func (f *CatalogFile) collectVersions(versions map[string]any) {
	section, ok := tomlSectionSpan(f.buf.original, "versions")
	if !ok {
		return
	}
	entries := indexSectionEntries(f.buf.original, section)
	for _, key := range slices.Sorted(maps.Keys(versions)) {
		value, isString := versions[key].(string)
		if !isString {
			// Rich versions ({ strictly = "..." }) are recorded without an
			// editable span; resolution can still route through other tiers.
			continue
		}
		f.versions = append(f.versions, CatalogVersion{
			Key:       key,
			Value:     value,
			valueSpan: valueSpanIn(f.buf.original, entries[key], value),
		})
	}
}

// collectLibraries records [libraries] entries that declare coordinates via
// module = "g:a" or group/name pairs, with either version.ref or an inline
// version string.
func (f *CatalogFile) collectLibraries(libraries map[string]any) {
	section, _ := tomlSectionSpan(f.buf.original, "libraries")
	entries := indexSectionEntries(f.buf.original, section)
	for _, alias := range slices.Sorted(maps.Keys(libraries)) {
		entry, isTable := libraries[alias].(map[string]any)
		if !isTable {
			continue
		}
		lib, ok := parseLibraryEntry(alias, entry)
		if !ok {
			continue
		}
		if lib.Version != "" {
			lib.versionSpan = inlineVersionSpanIn(f.buf.original, entries[alias], lib.Version)
		}
		f.libraries = append(f.libraries, lib)
	}
}

// parseLibraryEntry extracts coordinates and version information from one
// [libraries] table entry.
func parseLibraryEntry(alias string, entry map[string]any) (CatalogLibrary, bool) {
	lib := CatalogLibrary{Alias: alias, versionSpan: span{-1, -1}}

	if module, ok := entry["module"].(string); ok {
		parts := strings.SplitN(module, ":", 2)
		if len(parts) != 2 {
			return lib, false
		}
		lib.Group, lib.Artifact = parts[0], parts[1]
	} else {
		group, hasGroup := entry["group"].(string)
		name, hasName := entry["name"].(string)
		if !hasGroup || !hasName {
			return lib, false
		}
		lib.Group, lib.Artifact = group, name
	}

	switch version := entry["version"].(type) {
	case string:
		lib.Version = version
	case map[string]any:
		if ref, ok := version["ref"].(string); ok {
			lib.VersionRef = ref
		}
	}
	return lib, true
}

// tomlSectionHeaderPattern matches a top-level TOML section header line and
// captures its name.
var tomlSectionHeaderPattern = regexp.MustCompile(`(?m)^\[([A-Za-z0-9_.-]+)\]\s*$`)

// tomlEntryLinePattern matches one `key = ...` line of a TOML section.
// Group 1: key (quotes stripped), group 2: the value remainder.
var tomlEntryLinePattern = regexp.MustCompile(`(?m)^[ \t]*["']?([A-Za-z0-9_.-]+)["']?[ \t]*=[ \t]*([^\n]*)$`)

// tomlInlineVersionPattern matches a quoted inline version assignment inside
// an inline table remainder.
var tomlInlineVersionPattern = regexp.MustCompile(`version\s*=\s*["']([^"']*)["']`)

// tomlSectionSpan returns the span of a top-level TOML section body (from
// the line after the [name] header to the next section header or EOF).
func tomlSectionSpan(content []byte, name string) (span, bool) {
	for _, m := range tomlSectionHeaderPattern.FindAllSubmatchIndex(content, -1) {
		if string(content[m[2]:m[3]]) != name {
			continue
		}
		rest := content[m[1]:]
		if n := tomlSectionHeaderPattern.FindIndex(rest); n != nil {
			return span{m[1], m[1] + n[0]}, true
		}
		return span{m[1], len(content)}, true
	}
	return span{-1, -1}, false
}

// indexSectionEntries locates every `key = ...` line of a section in one
// pass, mapping each key to the span of its value remainder.
func indexSectionEntries(content []byte, section span) map[string]span {
	entries := make(map[string]span)
	if !section.valid() {
		return entries
	}
	for _, m := range tomlEntryLinePattern.FindAllSubmatchIndex(content[section.start:section.end], -1) {
		key := string(content[section.start+m[2] : section.start+m[3]])
		if _, seen := entries[key]; !seen {
			entries[key] = span{section.start + m[4], section.start + m[5]}
		}
	}
	return entries
}

// valueSpanIn locates the quoted value literal within an entry's value
// remainder.
func valueSpanIn(content []byte, rest span, value string) span {
	if !rest.valid() {
		return span{-1, -1}
	}
	offset := strings.Index(string(content[rest.start:rest.end]), value)
	if offset < 0 {
		return span{-1, -1}
	}
	return span{rest.start + offset, rest.start + offset + len(value)}
}

// inlineVersionSpanIn locates the inline `version = "..."` literal within a
// [libraries] entry's inline-table remainder, e.g.
//
//	okio = { module = "com.squareup.okio:okio", version = "3.4.0" }
func inlineVersionSpanIn(content []byte, rest span, version string) span {
	if !rest.valid() {
		return span{-1, -1}
	}
	m := tomlInlineVersionPattern.FindSubmatchIndex(content[rest.start:rest.end])
	if m == nil || string(content[rest.start+m[2]:rest.start+m[3]]) != version {
		return span{-1, -1}
	}
	return span{rest.start + m[2], rest.start + m[3]}
}
