/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradle

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/gradlefile"
)

// projectModel is a one-pass scan of every Gradle file in a project that can
// define a dependency version, indexed so the updater can answer "where is
// the best place to bump group:artifact" the way Maven answers it with
// properties and dependencyManagement.
type projectModel struct {
	rootDir string

	builds   map[string]*gradlefile.BuildFile
	settings map[string]*gradlefile.SettingsFile
	props    map[string]*gradlefile.PropertiesFile
	catalogs map[string]*gradlefile.CatalogFile

	// sortedFiles holds every parsed file path in deterministic order.
	sortedFiles []string

	// catalogVersionSites indexes version-catalog version keys (TOML
	// [versions] entries and settings inline version() declarations).
	catalogVersionSites map[string][]catalogVersionSite

	// catalogLibrarySites indexes version-catalog library entries by
	// "group:artifact" module coordinates.
	catalogLibrarySites map[string][]catalogLibrarySite

	// aliasModules maps normalized catalog aliases to module coordinates,
	// used to resolve libs.x.y accessor references.
	aliasModules map[string]string

	// variableSites indexes version variables by reference path: flat names
	// ("nettyVersion") from gradle.properties / version.properties / ext
	// definitions, and map paths ("versions.log4j2") from Groovy ext maps.
	variableSites map[string][]variableSite

	// declarationSites indexes build-script dependency declarations by
	// "group:artifact".
	declarationSites map[string][]declarationSite

	// libraryFnSites indexes Spring Boot library("name", "version")
	// declarations by name, matched against artifact ids.
	libraryFnSites map[string][]declarationSite

	// resolutionRuleSites indexes dependency resolve rules by group; rules
	// link modules to the catalog key, variable or literal governing their
	// version when nothing else does (e.g. kafbat's group-wide
	// useVersion(libs.versions.netty.get()) rule).
	resolutionRuleSites map[string][]resolutionRuleSite

	// forcedSites indexes the managed force-block pins of every build
	// script by "group:artifact", collected once at scan time.
	forcedSites map[string][]string
}

// resolutionRuleSite is one resolve rule in a build script.
type resolutionRuleSite struct {
	build *gradlefile.BuildFile
	rule  gradlefile.ResolutionRule
}

// catalogVersionSite is one definition site of a catalog version key; exactly
// one of catalog/settings is set.
type catalogVersionSite struct {
	catalog  *gradlefile.CatalogFile
	settings *gradlefile.SettingsFile
	version  gradlefile.CatalogVersion
}

func (s catalogVersionSite) path() string {
	if s.catalog != nil {
		return s.catalog.Path()
	}
	return s.settings.Path()
}

// set queues the version edit at this site.
func (s catalogVersionSite) set(value string) error {
	if s.catalog != nil {
		return s.catalog.SetVersion(s.version.Key, value)
	}
	return s.settings.SetCatalogVersion(s.version, value)
}

// catalogLibrarySite is one catalog library entry; exactly one of
// catalog/settings is set.
type catalogLibrarySite struct {
	catalog  *gradlefile.CatalogFile
	settings *gradlefile.SettingsFile
	library  gradlefile.CatalogLibrary
}

func (s catalogLibrarySite) path() string {
	if s.catalog != nil {
		return s.catalog.Path()
	}
	return s.settings.Path()
}

// variableSite is one definition site of a version variable; exactly one of
// build/props is set.
type variableSite struct {
	build  *gradlefile.BuildFile
	varDef gradlefile.VarDef
	props  *gradlefile.PropertiesFile
	key    string
}

func (s variableSite) path() string {
	if s.build != nil {
		return s.build.Path()
	}
	return s.props.Path()
}

func (s variableSite) value() string {
	if s.build != nil {
		return s.varDef.Value
	}
	v, _ := s.props.Get(s.key)
	return v
}

// set queues the variable edit at this site.
func (s variableSite) set(value string) error {
	if s.build != nil {
		return s.build.SetVariable(s.varDef, value)
	}
	return s.props.Set(s.key, value)
}

// declarationSite is one dependency declaration in a build script.
type declarationSite struct {
	build *gradlefile.BuildFile
	decl  gradlefile.DependencyDecl
}

// buildProjectModel scans rootDir and parses every Gradle file into the
// model.
func buildProjectModel(ctx context.Context, rootDir string) (*projectModel, error) {
	log := clog.FromContext(ctx)

	files, err := findBuildFiles(rootDir)
	if err != nil {
		return nil, fmt.Errorf("failed to find build files: %w", err)
	}

	m := &projectModel{
		rootDir:             rootDir,
		builds:              make(map[string]*gradlefile.BuildFile),
		settings:            make(map[string]*gradlefile.SettingsFile),
		props:               make(map[string]*gradlefile.PropertiesFile),
		catalogs:            make(map[string]*gradlefile.CatalogFile),
		catalogVersionSites: make(map[string][]catalogVersionSite),
		catalogLibrarySites: make(map[string][]catalogLibrarySite),
		aliasModules:        make(map[string]string),
		variableSites:       make(map[string][]variableSite),
		declarationSites:    make(map[string][]declarationSite),
		libraryFnSites:      make(map[string][]declarationSite),
		resolutionRuleSites: make(map[string][]resolutionRuleSite),
		forcedSites:         make(map[string][]string),
	}

	for _, path := range files {
		if err := m.parseFile(path); err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", path, err)
		}
	}
	sort.Strings(m.sortedFiles)

	m.indexCatalogs()
	m.indexVariables()
	m.indexDeclarations()

	log.Debugf("Gradle model: %d files, %d catalog modules, %d variables, %d declared modules",
		len(m.sortedFiles), len(m.catalogLibrarySites), len(m.variableSites), len(m.declarationSites))
	return m, nil
}

// parseFile reads and parses one discovered file into the model.
func (m *projectModel) parseFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("failed to stat: %w", err)
	}
	if info.Size() > MaxManifestSize {
		return fmt.Errorf("%w: %s is %d bytes (max: %d)", ErrManifestTooLarge, path, info.Size(), MaxManifestSize)
	}
	content, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("failed to read: %w", err)
	}

	switch classifyFile(path) {
	case fileKindSettings:
		f, err := gradlefile.ParseSettings(path, content)
		if err != nil {
			return err
		}
		m.settings[path] = f
	case fileKindBuild:
		f, err := gradlefile.ParseBuild(path, content)
		if err != nil {
			return err
		}
		m.builds[path] = f
	case fileKindProperties:
		f, err := gradlefile.ParseProperties(path, content)
		if err != nil {
			return err
		}
		m.props[path] = f
	case fileKindCatalog:
		f, err := gradlefile.ParseCatalog(path, content)
		if err != nil {
			return err
		}
		m.catalogs[path] = f
	case fileKindUnknown:
		return fmt.Errorf("%w: %s", ErrUnknownFileType, filepath.Base(path))
	}
	m.sortedFiles = append(m.sortedFiles, path)
	return nil
}

// fileKind classifies a discovered file for parser dispatch.
type fileKind int

const (
	fileKindUnknown fileKind = iota
	fileKindBuild
	fileKindSettings
	fileKindProperties
	fileKindCatalog
)

// classifyFile maps a path to the parser that handles it.
func classifyFile(path string) fileKind {
	base := filepath.Base(path)
	switch {
	case base == settingsGradleFile || base == settingsGradleKtsFile:
		return fileKindSettings
	case strings.HasSuffix(base, ".versions.toml"):
		return fileKindCatalog
	case base == gradlePropertiesFile || base == "version.properties" || base == "versions.properties":
		return fileKindProperties
	case strings.HasSuffix(base, ".gradle") || strings.HasSuffix(base, ".gradle.kts"):
		return fileKindBuild
	default:
		return fileKindUnknown
	}
}

// indexCatalogs indexes catalog version keys and library entries from TOML
// catalogs and settings inline catalogs.
func (m *projectModel) indexCatalogs() {
	for _, path := range m.sortedFiles {
		if catalog, ok := m.catalogs[path]; ok {
			for _, version := range catalog.Versions() {
				m.catalogVersionSites[version.Key] = append(m.catalogVersionSites[version.Key],
					catalogVersionSite{catalog: catalog, version: version})
			}
			for _, library := range catalog.Libraries() {
				m.indexLibrary(catalogLibrarySite{catalog: catalog, library: library})
			}
		}
		if settings, ok := m.settings[path]; ok {
			for _, version := range settings.CatalogVersions() {
				m.catalogVersionSites[version.Key] = append(m.catalogVersionSites[version.Key],
					catalogVersionSite{settings: settings, version: version})
			}
			for _, library := range settings.CatalogLibraries() {
				m.indexLibrary(catalogLibrarySite{settings: settings, library: library})
			}
		}
	}
}

func (m *projectModel) indexLibrary(site catalogLibrarySite) {
	module := site.library.Module()
	m.catalogLibrarySites[module] = append(m.catalogLibrarySites[module], site)
	m.aliasModules[gradlefile.NormalizeAlias(site.library.Alias)] = module
}

// indexVariables indexes version variables from properties files and build
// scripts.
func (m *projectModel) indexVariables() {
	for _, path := range m.sortedFiles {
		if props, ok := m.props[path]; ok {
			// Keys() repeats duplicated keys; one site per key suffices
			// because PropertiesFile.Set updates every occurrence.
			seen := make(map[string]struct{}, len(props.Keys()))
			for _, key := range props.Keys() {
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = struct{}{}
				m.variableSites[key] = append(m.variableSites[key],
					variableSite{props: props, key: key})
			}
		}
		if build, ok := m.builds[path]; ok {
			for _, varDef := range build.Variables() {
				m.variableSites[varDef.Path()] = append(m.variableSites[varDef.Path()],
					variableSite{build: build, varDef: varDef})
			}
		}
	}
}

// indexDeclarations indexes build-script dependency declarations and
// resolve rules.
func (m *projectModel) indexDeclarations() {
	for _, path := range m.sortedFiles {
		build, ok := m.builds[path]
		if !ok {
			continue
		}
		for _, rule := range build.ResolutionRules() {
			m.resolutionRuleSites[rule.Group] = append(m.resolutionRuleSites[rule.Group],
				resolutionRuleSite{build: build, rule: rule})
		}
		for _, decl := range build.Dependencies() {
			site := declarationSite{build: build, decl: decl}
			switch {
			case decl.Kind == gradlefile.LibraryFn:
				m.libraryFnSites[decl.Artifact] = append(m.libraryFnSites[decl.Artifact], site)
			case decl.CatalogAlias != "":
				// Strictly constraints identified by a catalog alias resolve
				// to module coordinates through the catalog, so the regular
				// declaration tier edits them alongside the catalog key.
				if decl.Kind != gradlefile.StrictlyBlock {
					continue
				}
				if module, ok := m.aliasModules[gradlefile.NormalizeAlias(decl.CatalogAlias)]; ok {
					m.declarationSites[module] = append(m.declarationSites[module], site)
				}
			case decl.Group != "" && decl.Artifact != "":
				module := decl.Group + ":" + decl.Artifact
				m.declarationSites[module] = append(m.declarationSites[module], site)
			}
		}
		for module, version := range build.ForcedCoordinates() {
			m.forcedSites[module] = append(m.forcedSites[module], version)
		}
	}
}

// catalogKeyForVarPath maps a variable reference path shaped like a version
// catalog accessor to an existing catalog version key. Projects like
// OpenSearch bridge the catalog into build scripts (ext.versions =
// libs.versions), so "${versions.log4j}" resolves to the catalog key
// "log4j" even though no ext map defines it.
func (m *projectModel) catalogKeyForVarPath(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, "libs.versions.")
	if !ok {
		rest, ok = strings.CutPrefix(path, "versions.")
	}
	if !ok || rest == "" {
		return "", false
	}
	if len(m.catalogVersionSites[rest]) > 0 {
		return rest, true
	}
	// Catalog accessors split keys on dashes/underscores: versions.commons.lang3
	// addresses the key "commons-lang3".
	if normalized := gradlefile.NormalizeAlias(rest); len(m.catalogVersionSites[normalized]) > 0 {
		return normalized, true
	}
	return "", false
}

// variableSitesFor returns the definition sites of a property name. An exact
// reference-path match wins; otherwise Groovy map entries are matched by bare
// entry name, so "log4j2" finds the versions-map entry addressed internally
// as "versions.log4j2" (the name users know from the build file).
func (m *projectModel) variableSitesFor(name string) []variableSite {
	if sites := m.variableSites[name]; len(sites) > 0 {
		return sites
	}
	var sites []variableSite
	for _, path := range slices.Sorted(maps.Keys(m.variableSites)) {
		for _, site := range m.variableSites[path] {
			if site.build != nil && site.varDef.MapName != "" && site.varDef.Name == name {
				sites = append(sites, site)
			}
		}
	}
	return sites
}

// resolveCatalogValue returns the effective version of a catalog library:
// the referenced version key's value, or the inline version.
func (m *projectModel) resolveCatalogValue(library gradlefile.CatalogLibrary) string {
	if library.VersionRef != "" {
		if sites := m.catalogVersionSites[library.VersionRef]; len(sites) > 0 {
			return sites[0].version.Value
		}
		return ""
	}
	return library.Version
}

// rootBuildFile picks the build script that should host the managed force
// block: the build file adjacent to the root settings file, else the
// shallowest build file. Returns nil when the project has no build script.
func (m *projectModel) rootBuildFile() *gradlefile.BuildFile {
	for _, settingsPath := range m.sortedFiles {
		if _, ok := m.settings[settingsPath]; !ok {
			continue
		}
		dir := filepath.Dir(settingsPath)
		for _, name := range []string{buildGradleFile, buildGradleKtsFile} {
			if build, ok := m.builds[filepath.Join(dir, name)]; ok {
				return build
			}
		}
	}

	var best *gradlefile.BuildFile
	bestDepth := -1
	for _, path := range m.sortedFiles {
		build, ok := m.builds[path]
		if !ok {
			continue
		}
		base := filepath.Base(path)
		if base != buildGradleFile && base != buildGradleKtsFile {
			continue
		}
		depth := strings.Count(path, string(filepath.Separator))
		if bestDepth == -1 || depth < bestDepth {
			best = build
			bestDepth = depth
		}
	}
	return best
}

// rootSettingsDSL returns the DSL of the root settings file, used to pick the
// dialect of a newly created root build script. Defaults to Groovy.
func (m *projectModel) rootSettingsDSL() gradlefile.DSL {
	for _, path := range m.sortedFiles {
		if settings, ok := m.settings[path]; ok {
			return settings.DSL()
		}
	}
	return gradlefile.Groovy
}
