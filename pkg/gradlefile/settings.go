/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradlefile

import "fmt"

// CatalogVersion is one version entry of a version catalog: a [versions] key
// in a TOML catalog or a version("key", "value") declaration in a settings
// script's inline catalog.
type CatalogVersion struct {
	// Key is the version key, e.g. "netty".
	Key string

	// Value is the current version string.
	Value string

	valueSpan span
}

// CatalogLibrary is one library entry of a version catalog, mapping an alias
// to Maven coordinates and a version (either by reference or inline).
type CatalogLibrary struct {
	// Alias is the catalog entry key, e.g. "netty-codec".
	Alias string

	// Group and Artifact are the Maven coordinates.
	Group    string
	Artifact string

	// VersionRef is the referenced [versions] key, or empty.
	VersionRef string

	// Version is the inline version literal, or empty.
	Version string

	versionSpan span
}

// Module returns the library's "group:artifact" module coordinates.
func (l CatalogLibrary) Module() string {
	return l.Group + ":" + l.Artifact
}

// SettingsFile is a parsed settings.gradle(.kts) script. Only the inline
// version-catalog declarations are modeled.
type SettingsFile struct {
	path      string
	dsl       DSL
	buf       editBuffer
	versions  []CatalogVersion
	libraries []CatalogLibrary
}

// ParseSettings parses a settings.gradle(.kts) script.
func ParseSettings(path string, content []byte) (*SettingsFile, error) {
	f := &SettingsFile{
		path: path,
		dsl:  DSLFromPath(path),
		buf:  editBuffer{original: content},
	}

	for _, m := range settingsVersionPattern.FindAllSubmatchIndex(content, -1) {
		if lineIsComment(content, m[0]) {
			continue
		}
		f.versions = append(f.versions, CatalogVersion{
			Key:       string(content[m[2]:m[3]]),
			Value:     string(content[m[4]:m[5]]),
			valueSpan: span{m[4], m[5]},
		})
	}

	for _, m := range settingsLibraryRefPattern.FindAllSubmatchIndex(content, -1) {
		if lineIsComment(content, m[0]) {
			continue
		}
		f.libraries = append(f.libraries, CatalogLibrary{
			Alias:       string(content[m[2]:m[3]]),
			Group:       string(content[m[4]:m[5]]),
			Artifact:    string(content[m[6]:m[7]]),
			VersionRef:  string(content[m[8]:m[9]]),
			versionSpan: span{-1, -1},
		})
	}

	return f, nil
}

// Path returns the file path the script was parsed from.
func (f *SettingsFile) Path() string { return f.path }

// DSL returns the script's DSL dialect.
func (f *SettingsFile) DSL() DSL { return f.dsl }

// CatalogVersions returns the inline catalog version declarations.
func (f *SettingsFile) CatalogVersions() []CatalogVersion { return f.versions }

// CatalogLibraries returns the inline catalog library declarations that
// reference a version key.
func (f *SettingsFile) CatalogLibraries() []CatalogLibrary { return f.libraries }

// SetCatalogVersion queues an in-place rewrite of c's value.
func (f *SettingsFile) SetCatalogVersion(c CatalogVersion, value string) error {
	if err := ValidateVersion(value); err != nil {
		return err
	}
	if !c.valueSpan.valid() {
		return fmt.Errorf("%w: catalog version %s in %s", ErrNotEditable, c.Key, f.path)
	}
	return f.buf.add(c.valueSpan, value)
}

// Content renders the script with all queued edits applied.
func (f *SettingsFile) Content() []byte { return f.buf.render() }

// Changed reports whether any queued edit modifies the original content.
func (f *SettingsFile) Changed() bool { return f.buf.changed() }

// ChangeCount returns the number of queued edits that modify the content.
func (f *SettingsFile) ChangeCount() int { return f.buf.changeCount() }
