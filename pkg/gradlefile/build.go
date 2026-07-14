/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradlefile

import (
	"fmt"
	"regexp"
	"strings"
)

// DeclKind identifies the syntactic form of a dependency declaration.
type DeclKind int

const (
	// StringNotation is implementation("group:artifact:version") or the
	// paren-less Groovy form, including coordinate values in ext maps.
	StringNotation DeclKind = iota
	// MapNotationKotlin is group = "g", name = "a", version = "v".
	MapNotationKotlin
	// MapNotationGroovy is group: 'g', name: 'a', version: 'v'.
	MapNotationGroovy
	// LibraryFn is the Spring Boot bom-plugin form library("name", "version").
	LibraryFn
	// StrictlyBlock is a rich-version constraint version { strictly("v") }.
	StrictlyBlock
	// CatalogRef is a version-catalog accessor such as api(libs.netty.codec);
	// it carries no version of its own.
	CatalogRef
	// DependencySet is one entry of a Spring dependency-management plugin
	// dependencySet(group: 'g', version: 'v') { entry 'a' } block. All
	// entries of a set share one version literal.
	DependencySet
)

// DependencyDecl is one dependency declaration found in a build script.
type DependencyDecl struct {
	// Group and Artifact are the Maven coordinates. Group is empty for
	// LibraryFn declarations (the library name is matched against artifact
	// ids) and for CatalogRef/StrictlyBlock declarations identified only by a
	// catalog alias.
	Group    string
	Artifact string

	// Version is the literal version, or empty when the version is a
	// variable reference or the declaration carries no version.
	Version string

	// VarRef is the referenced variable path when the version is
	// interpolated, e.g. "nettyVersion" or "versions.log4j2".
	VarRef string

	// CatalogAlias is the version-catalog accessor path (dots preserved,
	// without the "libs." prefix) for CatalogRef and alias-based
	// StrictlyBlock declarations.
	CatalogAlias string

	// Kind is the syntactic form of the declaration.
	Kind DeclKind

	versionSpan span
}

// VarDef is one version-variable definition found in a build script: a flat
// ext property, a Kotlin extra property, or an entry in a Groovy version map.
type VarDef struct {
	// Name is the variable or map-entry name, e.g. "nettyVersion" or
	// "log4j2".
	Name string

	// MapName is the enclosing Groovy map variable name (e.g. "versions")
	// or empty for flat definitions.
	MapName string

	// Value is the current value.
	Value string

	valueSpan span
}

// Path returns the variable's reference path as used in interpolations:
// "versions.log4j2" for map entries, "nettyVersion" for flat definitions.
func (v VarDef) Path() string {
	if v.MapName != "" {
		return v.MapName + "." + v.Name
	}
	return v.Name
}

// ResolutionRule is a dependency resolve rule found in a build script: a
// conditional inside resolutionStrategy.eachDependency that redirects a
// group (or one module) to a version source, e.g.
//
//	if (details.requested.group == "io.netty" && !details.requested.name.startsWith("netty-tcnative-")) {
//	    details.useVersion(libs.versions.netty.get())
//	}
//
// Rules link modules to the catalog key, variable or literal that governs
// their version even when no [libraries] entry or interpolation does.
type ResolutionRule struct {
	// Group is the dependency group the rule matches.
	Group string

	// Artifact narrows the rule to one module; empty matches the whole group.
	Artifact string

	// CatalogKey is the referenced [versions] key (normalized) when the rule
	// reads a catalog version accessor such as libs.versions.netty.get().
	CatalogKey string

	// VarRef is the referenced variable path when the rule interpolates one.
	VarRef string

	// Version is the literal version, or empty.
	Version string

	versionSpan span
}

// BuildFile is a parsed Gradle build script.
type BuildFile struct {
	path        string
	dsl         DSL
	buf         editBuffer
	deps        []DependencyDecl
	vars        []VarDef
	rules       []ResolutionRule
	shipConfigs []ShipConfigRef
}

// ParseBuild parses a Gradle build script (build.gradle, build.gradle.kts or
// any other *.gradle(.kts) script). The DSL dialect is derived from the path.
func ParseBuild(path string, content []byte) (*BuildFile, error) {
	f := &BuildFile{
		path: path,
		dsl:  DSLFromPath(path),
		buf:  editBuffer{original: content},
	}
	f.scanDependencies()
	f.scanVariables()
	f.scanResolutionRules()
	f.scanShipConfigs(f.buf.original)
	return f, nil
}

// Path returns the file path the script was parsed from.
func (f *BuildFile) Path() string { return f.path }

// DSL returns the script's DSL dialect.
func (f *BuildFile) DSL() DSL { return f.dsl }

// Dependencies returns all dependency declarations found in the script.
func (f *BuildFile) Dependencies() []DependencyDecl { return f.deps }

// Variables returns all version-variable definitions found in the script.
func (f *BuildFile) Variables() []VarDef { return f.vars }

// ResolutionRules returns all dependency resolve rules found in the script.
func (f *BuildFile) ResolutionRules() []ResolutionRule { return f.rules }

// SetResolutionRuleVersion queues an in-place rewrite of r's version
// literal. Rules whose version comes from a catalog key or variable cannot
// be edited here; update the referenced source instead.
func (f *BuildFile) SetResolutionRuleVersion(r ResolutionRule, version string) error {
	if err := ValidateVersion(version); err != nil {
		return err
	}
	if r.Version == "" || !r.versionSpan.valid() {
		return fmt.Errorf("%w: resolution rule for %s has no literal version in %s", ErrNotEditable, r.Group, f.path)
	}
	return f.buf.add(r.versionSpan, version)
}

// Content renders the script with all queued edits applied.
func (f *BuildFile) Content() []byte { return f.buf.render() }

// Changed reports whether any queued edit modifies the original content.
func (f *BuildFile) Changed() bool { return f.buf.changed() }

// ChangeCount returns the number of queued edits that modify the content.
func (f *BuildFile) ChangeCount() int { return f.buf.changeCount() }

// SetDependencyVersion queues an in-place rewrite of d's version literal.
// Declarations whose version is a variable reference cannot be edited here;
// update the variable definition instead.
func (f *BuildFile) SetDependencyVersion(d DependencyDecl, version string) error {
	if err := ValidateVersion(version); err != nil {
		return err
	}
	if d.VarRef != "" || !d.versionSpan.valid() || d.Kind == CatalogRef {
		return fmt.Errorf("%w: %s:%s has no literal version in %s", ErrNotEditable, d.Group, d.Artifact, f.path)
	}
	return f.buf.add(d.versionSpan, version)
}

// SetVariable queues an in-place rewrite of v's value.
func (f *BuildFile) SetVariable(v VarDef, value string) error {
	if err := ValidateVersion(value); err != nil {
		return err
	}
	if !v.valueSpan.valid() {
		return fmt.Errorf("%w: variable %s in %s", ErrNotEditable, v.Path(), f.path)
	}
	return f.buf.add(v.valueSpan, value)
}

// scanDependencies finds all dependency declarations in the script.
func (f *BuildFile) scanDependencies() {
	content := f.buf.original
	f.scanStrictlyBlocks(content)
	f.scanCoordinateLiterals(content)
	f.scanMapNotations(content)
	f.scanLibraryFns(content)
	f.scanCatalogRefs(content)
	f.scanDependencySets(content)
}

// scanDependencySets records the entries of Spring dependency-management
// dependencySet blocks. Every entry shares the set's single version literal,
// so bumping any entry rewrites the whole set's version — the same effect
// the upstream sed-based patches have.
func (f *BuildFile) scanDependencySets(content []byte) {
	for _, m := range dependencySetPattern.FindAllSubmatchIndex(content, -1) {
		if lineIsComment(content, m[0]) {
			continue
		}
		var group string
		var verSpan span
		switch {
		case m[2] >= 0: // map form: group: 'g', version: 'v'
			group = string(content[m[2]:m[3]])
			verSpan = span{m[4], m[5]}
		case m[6] >= 0: // coordinate form: "g:v"
			group = string(content[m[6]:m[7]])
			verSpan = span{m[8], m[9]}
		default:
			continue
		}
		literal, varRef := parseVersionToken(string(content[verSpan.start:verSpan.end]))

		body, ok := bracketSpan(content, m[1]-1, '{', '}')
		if !ok {
			continue
		}
		for _, e := range dependencySetEntryPattern.FindAllSubmatchIndex(content[body.start:body.end], -1) {
			f.deps = append(f.deps, DependencyDecl{
				Kind:        DependencySet,
				Group:       group,
				Artifact:    string(content[body.start+e[2] : body.start+e[3]]),
				Version:     literal,
				VarRef:      varRef,
				versionSpan: verSpan,
			})
		}
	}
}

// scanStrictlyBlocks records rich-version strictly constraints. They are
// scanned first so the coordinate-literal scan can skip version spans that
// belong to a strictly block's coordinate.
func (f *BuildFile) scanStrictlyBlocks(content []byte) {
	for _, m := range strictlyPattern.FindAllSubmatchIndex(content, -1) {
		if lineIsComment(content, m[0]) {
			continue
		}
		d := DependencyDecl{
			Kind:        StrictlyBlock,
			Version:     string(content[m[8]:m[9]]),
			versionSpan: span{m[8], m[9]},
		}
		if m[2] >= 0 { // libs.alias form
			d.CatalogAlias = strings.TrimPrefix(string(content[m[2]:m[3]]), "libs.")
		} else { // quoted "group:artifact" form
			d.Group = string(content[m[4]:m[5]])
			d.Artifact = string(content[m[6]:m[7]])
		}
		f.deps = append(f.deps, d)
	}
}

// scanCoordinateLiterals records quoted "group:artifact:version" literals.
func (f *BuildFile) scanCoordinateLiterals(content []byte) {
	for _, m := range coordinateLiteralPattern.FindAllSubmatchIndex(content, -1) {
		if lineIsComment(content, m[0]) {
			continue
		}
		token := string(content[m[6]:m[7]])
		literal, varRef := parseVersionToken(token)
		// A bare "group:artifact:version" literal is ambiguous: time and date
		// format strings like "HH:mm:ss" match the same shape. Treat it as a
		// dependency only when the version is a variable reference or contains
		// a digit, which every real version does and a format string does not.
		if varRef == "" && !containsDigit(literal) {
			continue
		}
		f.deps = append(f.deps, DependencyDecl{
			Kind:        StringNotation,
			Group:       string(content[m[2]:m[3]]),
			Artifact:    string(content[m[4]:m[5]]),
			Version:     literal,
			VarRef:      varRef,
			versionSpan: span{m[6], m[7]},
		})
	}
}

// scanMapNotations records Kotlin named-argument and Groovy map notations.
func (f *BuildFile) scanMapNotations(content []byte) {
	for _, m := range kotlinMapNotationPattern.FindAllSubmatchIndex(content, -1) {
		if lineIsComment(content, m[0]) {
			continue
		}
		token := string(content[m[6]:m[7]])
		literal, varRef := parseVersionToken(token)
		f.deps = append(f.deps, DependencyDecl{
			Kind:        MapNotationKotlin,
			Group:       string(content[m[2]:m[3]]),
			Artifact:    string(content[m[4]:m[5]]),
			Version:     literal,
			VarRef:      varRef,
			versionSpan: span{m[6], m[7]},
		})
	}
	for _, m := range groovyMapNotationPattern.FindAllSubmatchIndex(content, -1) {
		if lineIsComment(content, m[0]) {
			continue
		}
		d := DependencyDecl{
			Kind:     MapNotationGroovy,
			Group:    string(content[m[2]:m[3]]),
			Artifact: string(content[m[4]:m[5]]),
		}
		switch {
		case m[6] >= 0: // quoted version
			literal, varRef := parseVersionToken(string(content[m[6]:m[7]]))
			d.Version = literal
			d.VarRef = varRef
			d.versionSpan = span{m[6], m[7]}
		case m[8] >= 0: // bare variable path
			d.VarRef = string(content[m[8]:m[9]])
			d.versionSpan = span{-1, -1}
		}
		f.deps = append(f.deps, d)
	}
}

// scanLibraryFns records Spring Boot bom-plugin library("name", "version")
// declarations.
func (f *BuildFile) scanLibraryFns(content []byte) {
	for _, m := range libraryFnPattern.FindAllSubmatchIndex(content, -1) {
		if lineIsComment(content, m[0]) {
			continue
		}
		f.deps = append(f.deps, DependencyDecl{
			Kind:        LibraryFn,
			Artifact:    string(content[m[2]:m[3]]),
			Version:     string(content[m[4]:m[5]]),
			versionSpan: span{m[4], m[5]},
		})
	}
}

// scanCatalogRefs records version-catalog accessor references; they carry no
// version but identify which catalog entries are in use.
func (f *BuildFile) scanCatalogRefs(content []byte) {
	for _, m := range catalogRefPattern.FindAllSubmatchIndex(content, -1) {
		if lineIsComment(content, m[0]) {
			continue
		}
		f.deps = append(f.deps, DependencyDecl{
			Kind:         CatalogRef,
			CatalogAlias: string(content[m[2]:m[3]]),
			versionSpan:  span{-1, -1},
		})
	}
}

var (
	// groovyTypedAssignPattern matches Groovy local version declarations in
	// def, var, and typed String forms, with optional leading modifiers:
	//   def x = '...'          String x = "..."
	//   final String x = '...' var x = "..."
	// Group 1: name, group 2: value.
	groovyTypedAssignPattern = regexp.MustCompile(`(?m)^\s*(?:(?:final|static|public|private|protected)\s+)*(?:def|var|String)\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*["']([^"']*)["']\s*$`)

	// kotlinValAssignPattern matches Kotlin val version declarations, with an
	// optional String type annotation and optional leading modifiers:
	//   val x = "..."   val x: String = "..."   const val x = "..."
	// Group 1: name, group 2: value.
	kotlinValAssignPattern = regexp.MustCompile(`(?m)^\s*(?:(?:const|private|internal|public|protected)\s+)*val\s+([A-Za-z_][A-Za-z0-9_]*)\s*(?::\s*String\s*)?=\s*"([^"]*)"\s*$`)

	extBlockPattern = regexp.MustCompile(`(?m)^\s*ext\s*\{`)
)

// reservedVarNames are bare assignment names that describe the project
// itself, not dependency versions; rerouting a bump to them would corrupt the
// build (mirrors Maven skipping ${project.version}).
var reservedVarNames = map[string]struct{}{
	"version":          {},
	"group":            {},
	"description":      {},
	"archivesBaseName": {},
}

// scanVariables finds version-variable definitions: Groovy ext maps and flat
// ext properties, def assignments, and Kotlin extra properties.
func (f *BuildFile) scanVariables() {
	content := f.buf.original
	f.scanExtMaps(content)
	f.scanExtBlocks(content)
	f.scanFlatAssignments(content)
	f.scanKotlinExtras(content)
}

// scanExtMaps records entries of Groovy maps assigned to variables, e.g. the
// versions map in gradle/dependencies.gradle:
//
//	versions += [
//	    log4j2: "2.25.1",
//	]
//
// Entries whose value embeds Maven coordinates (contains ':') are skipped
// here; the coordinate-literal scan records those as declarations.
func (f *BuildFile) scanExtMaps(content []byte) {
	for _, m := range extMapHeaderPattern.FindAllSubmatchIndex(content, -1) {
		if lineIsComment(content, m[0]) {
			continue
		}
		mapName := string(content[m[2]:m[3]])
		body, ok := bracketSpan(content, m[1]-1, '[', ']')
		if !ok {
			continue
		}
		for _, e := range extMapEntryPattern.FindAllSubmatchIndex(content[body.start:body.end], -1) {
			value := string(content[body.start+e[4] : body.start+e[5]])
			if strings.Contains(value, ":") {
				continue
			}
			f.vars = append(f.vars, VarDef{
				Name:      string(content[body.start+e[2] : body.start+e[3]]),
				MapName:   mapName,
				Value:     value,
				valueSpan: span{body.start + e[4], body.start + e[5]},
			})
		}
	}
}

// scanExtBlocks records flat assignments inside ext { } blocks.
func (f *BuildFile) scanExtBlocks(content []byte) {
	for _, m := range extBlockPattern.FindAllIndex(content, -1) {
		if lineIsComment(content, m[0]) {
			continue
		}
		body, ok := bracketSpan(content, m[1]-1, '{', '}')
		if !ok {
			continue
		}
		for _, e := range extFlatAssignPattern.FindAllSubmatchIndex(content[body.start:body.end], -1) {
			name := string(content[body.start+e[4] : body.start+e[5]])
			if _, reserved := reservedVarNames[name]; reserved {
				continue
			}
			f.addVar(VarDef{
				Name:      name,
				Value:     string(content[body.start+e[6] : body.start+e[7]]),
				valueSpan: span{body.start + e[6], body.start + e[7]},
			})
		}
	}
}

// scanFlatAssignments records ext.name = 'value' assignments and local
// version declarations (Groovy def/var/String, Kotlin val) anywhere in the
// script.
func (f *BuildFile) scanFlatAssignments(content []byte) {
	for _, m := range extFlatAssignPattern.FindAllSubmatchIndex(content, -1) {
		if m[2] < 0 { // bare assignment outside an ext block: too ambiguous
			continue
		}
		if lineIsComment(content, m[0]) {
			continue
		}
		name := string(content[m[4]:m[5]])
		if _, reserved := reservedVarNames[name]; reserved {
			continue
		}
		f.addVar(VarDef{
			Name:      name,
			Value:     string(content[m[6]:m[7]]),
			valueSpan: span{m[6], m[7]},
		})
	}
	for _, pattern := range []*regexp.Regexp{groovyTypedAssignPattern, kotlinValAssignPattern} {
		for _, m := range pattern.FindAllSubmatchIndex(content, -1) {
			if lineIsComment(content, m[0]) {
				continue
			}
			name := string(content[m[2]:m[3]])
			if _, reserved := reservedVarNames[name]; reserved {
				continue
			}
			f.addVar(VarDef{
				Name:      name,
				Value:     string(content[m[4]:m[5]]),
				valueSpan: span{m[4], m[5]},
			})
		}
	}
}

// scanKotlinExtras records Kotlin extra-property definitions.
func (f *BuildFile) scanKotlinExtras(content []byte) {
	for _, pattern := range []*regexp.Regexp{kotlinExtraIndexPattern, kotlinExtraDelegatePattern} {
		for _, m := range pattern.FindAllSubmatchIndex(content, -1) {
			if lineIsComment(content, m[0]) {
				continue
			}
			f.addVar(VarDef{
				Name:      string(content[m[2]:m[3]]),
				Value:     string(content[m[4]:m[5]]),
				valueSpan: span{m[4], m[5]},
			})
		}
	}
}

// conditionalBlock is one if-condition and its brace-delimited body.
type conditionalBlock struct {
	condition span
	body      span
}

// scanResolutionRules records eachDependency resolve rules: each useVersion
// call is associated with the group/name equality conditions of every
// enclosing if block (kayenta-style rules nest a name condition inside a
// group condition; kafbat-style rules put both in one condition). Extra
// conditions such as negated startsWith calls are deliberately ignored: the
// rule still identifies which version source governs the group, and bumping
// that source preserves the rule's exact applicability.
func (f *BuildFile) scanResolutionRules() {
	content := f.buf.original
	matches := useVersionPattern.FindAllSubmatchIndex(content, -1)
	if len(matches) == 0 {
		return
	}
	conditionals := scanConditionalBlocks(content)

	for _, m := range matches {
		if lineIsComment(content, m[0]) {
			continue
		}

		var group, artifact string
		for _, c := range conditionals {
			if m[0] < c.body.start || m[0] >= c.body.end {
				continue
			}
			condition := content[c.condition.start:c.condition.end]
			if g := ruleGroupCondPattern.FindSubmatch(condition); g != nil {
				group = string(g[1])
			}
			if n := ruleNameCondPattern.FindSubmatch(condition); n != nil {
				artifact = string(n[1])
			}
		}
		if group == "" {
			continue
		}

		rule := ResolutionRule{Group: group, Artifact: artifact, versionSpan: span{-1, -1}}
		switch {
		case m[2] >= 0: // parenthesized argument
			arg := strings.TrimSpace(string(content[m[2]:m[3]]))
			if accessor := catalogVersionAccessorPattern.FindStringSubmatch(arg); accessor != nil {
				rule.CatalogKey = NormalizeAlias(accessor[1])
			} else if quoted := quotedLiteralPattern.FindSubmatchIndex(content[m[2]:m[3]]); quoted != nil {
				token := string(content[m[2]+quoted[2] : m[2]+quoted[3]])
				literal, varRef := parseVersionToken(token)
				rule.Version = literal
				rule.VarRef = varRef
				if literal != "" {
					rule.versionSpan = span{m[2] + quoted[2], m[2] + quoted[3]}
				}
			} else {
				_, rule.VarRef = parseVersionToken("$" + arg)
			}
		case m[4] >= 0: // paren-less Groovy string argument
			literal, varRef := parseVersionToken(string(content[m[4]:m[5]]))
			rule.Version = literal
			rule.VarRef = varRef
			if literal != "" {
				rule.versionSpan = span{m[4], m[5]}
			}
		}
		f.rules = append(f.rules, rule)
	}
}

// scanConditionalBlocks collects every if block with its bracket-matched
// condition and body spans.
func scanConditionalBlocks(content []byte) []conditionalBlock {
	var blocks []conditionalBlock
	for _, m := range ruleIfPattern.FindAllIndex(content, -1) {
		condition, ok := bracketSpan(content, m[1]-1, '(', ')')
		if !ok {
			continue
		}
		// The body opens at the first brace after the condition closes.
		braceOpen := condition.end + 1
		for braceOpen < len(content) && (content[braceOpen] == ' ' || content[braceOpen] == '\t' || content[braceOpen] == '\n' || content[braceOpen] == '\r') {
			braceOpen++
		}
		body, ok := bracketSpan(content, braceOpen, '{', '}')
		if !ok {
			continue
		}
		blocks = append(blocks, conditionalBlock{condition: condition, body: body})
	}
	return blocks
}

// addVar appends v unless the same value span was already recorded (the ext
// block and flat assignment scans can both match ext.x = 'v' lines).
func (f *BuildFile) addVar(v VarDef) {
	for _, existing := range f.vars {
		if existing.valueSpan == v.valueSpan {
			return
		}
	}
	f.vars = append(f.vars, v)
}

// bracketSpan returns the span of the bracketed body starting at the opening
// bracket at offset open (exclusive of the brackets themselves).
func bracketSpan(content []byte, open int, openCh, closeCh byte) (span, bool) {
	if open < 0 || open >= len(content) || content[open] != openCh {
		return span{-1, -1}, false
	}
	depth := 0
	for i := open; i < len(content); i++ {
		switch content[i] {
		case openCh:
			depth++
		case closeCh:
			depth--
			if depth == 0 {
				return span{open + 1, i}, true
			}
		}
	}
	return span{-1, -1}, false
}
