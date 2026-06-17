/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradlefile

import (
	"regexp"
	"strings"
)

// Regex patterns shared by the build and settings script scanners. All
// version-bearing constructs are captured with submatch indexes so the
// scanners can record precise byte spans for later in-place edits.
var (
	// coordinateLiteralPattern matches a quoted "group:artifact:version"
	// string literal anywhere in a script: string-notation dependency
	// declarations with or without parentheses, classpath entries, and
	// coordinate values inside Groovy ext maps such as
	//   lz4: "org.lz4:lz4-java:$versions.lz4"
	// Group 1: group, group 2: artifact, group 3: version token (literal or
	// $var / ${var} interpolation).
	coordinateLiteralPattern = regexp.MustCompile(`["']([A-Za-z0-9._-]+):([A-Za-z0-9._-]+):([^"'\s]+)["']`)

	// kotlinMapNotationPattern matches Kotlin named-argument notation:
	//   group = "g", name = "a", version = "1.2.3"
	kotlinMapNotationPattern = regexp.MustCompile(`group\s*=\s*["']([A-Za-z0-9._-]+)["']\s*,\s*name\s*=\s*["']([A-Za-z0-9._-]+)["']\s*,\s*version\s*=\s*["']([^"']+)["']`)

	// groovyMapNotationPattern matches Groovy map notation:
	//   group: 'g', name: 'a', version: '1.2.3'
	//   group: 'g', name: 'a', version: versions.lz4
	// Group 3 is the quoted version (may be interpolated), group 4 a bare
	// variable path used as the version.
	groovyMapNotationPattern = regexp.MustCompile(`group\s*:\s*["']([A-Za-z0-9._-]+)["']\s*,\s*name\s*:\s*["']([A-Za-z0-9._-]+)["']\s*,\s*version\s*:\s*(?:["']([^"']+)["']|([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]+)*))`)

	// libraryFnPattern matches Spring Boot bom-plugin library declarations:
	//   library("Netty", "4.1.118.Final")
	// Group 1: library name (matched against artifact ids), group 2: version.
	libraryFnPattern = regexp.MustCompile(`library\s*\(\s*["']([A-Za-z0-9._\s-]+)["']\s*,\s*["']([^"']+)["']\s*\)`)

	// strictlyPattern matches rich-version strictly constraints attached to a
	// dependency declaration, in both DSLs:
	//   api(libs.commonsLang3) { version { strictly("3.18.0") } }
	//   api("org.foo:bar") { version { strictly '3.18.0' } }
	// Group 1: catalog alias path (libs.x.y), group 2: group, group 3:
	// artifact (for the quoted coordinate form), group 4: version literal.
	strictlyPattern = regexp.MustCompile(`\(\s*(?:(libs(?:\.[A-Za-z0-9_]+)+)|["']([A-Za-z0-9._-]+):([A-Za-z0-9._-]+)(?::[^"']*)?["'])\s*\)\s*\{[^{}]*version\s*\{\s*strictly\s*\(?\s*["']([^"']+)["']`)

	// catalogRefPattern matches version-catalog accessor references used as
	// dependency declarations, e.g. implementation(libs.netty.codec) or the
	// paren-less Groovy form. Group 1: alias path after "libs.".
	catalogRefPattern = regexp.MustCompile(`[A-Za-z][A-Za-z0-9]*\s*\(?\s*libs\.([A-Za-z0-9_.]+)`)

	// extMapHeaderPattern matches the start of a Groovy map assigned to a
	// variable, optionally additive and optionally via the ext namespace:
	//   versions = [        versions += [        ext.versions = [
	// Group 1: map name.
	extMapHeaderPattern = regexp.MustCompile(`(?m)^\s*(?:ext\.)?([A-Za-z_][A-Za-z0-9_]*)\s*\+?=\s*\[`)

	// extMapEntryPattern matches one entry inside a Groovy map literal:
	//   log4j2: "2.25.1",
	// Keys may be quoted. Group 1: key, group 2: value.
	extMapEntryPattern = regexp.MustCompile(`(?m)^\s*["']?([A-Za-z_][A-Za-z0-9_-]*)["']?\s*:\s*["']([^"']*)["']\s*,?\s*$`)

	// extFlatAssignPattern matches flat Groovy ext assignments:
	//   ext.nettyVersion = '4.1.0'
	// and assignments inside an ext { } block:
	//   nettyVersion = '4.1.0'
	// Group 1: optional "ext." prefix, group 2: name, group 3: value.
	extFlatAssignPattern = regexp.MustCompile(`(?m)^\s*(ext\.)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*["']([^"']*)["']\s*$`)

	// kotlinExtraIndexPattern matches Kotlin extra-properties assignments:
	//   extra["nettyVersion"] = "4.1.0"
	// Group 1: name, group 2: value.
	kotlinExtraIndexPattern = regexp.MustCompile(`extra\[["']([A-Za-z0-9._-]+)["']\]\s*=\s*["']([^"']*)["']`)

	// kotlinExtraDelegatePattern matches Kotlin extra property delegation:
	//   val nettyVersion by extra("4.1.0")
	// Group 1: name, group 2: value.
	kotlinExtraDelegatePattern = regexp.MustCompile(`val\s+([A-Za-z_][A-Za-z0-9_]*)\s+by\s+extra\s*\(\s*["']([^"']*)["']\s*\)`)

	// settingsVersionPattern matches inline version-catalog version
	// declarations in settings scripts: version("netty", "4.1.0").
	// Group 1: key, group 2: value.
	settingsVersionPattern = regexp.MustCompile(`version\s*\(\s*["']([A-Za-z0-9._-]+)["']\s*,\s*["']([^"']+)["']\s*\)`)

	// settingsLibraryRefPattern matches inline catalog library declarations
	// that reference a version key:
	//   library("netty-codec", "io.netty", "netty-codec").versionRef("netty")
	// Groups: alias, group, artifact, version key.
	settingsLibraryRefPattern = regexp.MustCompile(`library\s*\(\s*["']([A-Za-z0-9._-]+)["']\s*,\s*["']([A-Za-z0-9._-]+)["']\s*,\s*["']([A-Za-z0-9._-]+)["']\s*\)\s*\.\s*versionRef\s*\(\s*["']([A-Za-z0-9._-]+)["']\s*\)`)

	// propertyAccessorPattern matches version values read from project
	// properties: version = property("x") / version: project.property('x').
	propertyAccessorPattern = regexp.MustCompile(`^\$?\{?(?:project\.)?property\(["']([A-Za-z0-9._-]+)["']\)\}?$`)

	// dependencySetPattern matches the Spring dependency-management plugin's
	// grouped declarations, in both the Groovy map and coordinate forms:
	//   dependencySet(group: 'ch.qos.logback', version: '1.5.32') { ... }
	//   dependencySet("ch.qos.logback:1.5.32") { ... }
	// Groups: 1 map-form group, 2 map-form version, 3 coord-form group,
	// 4 coord-form version.
	dependencySetPattern = regexp.MustCompile(`dependencySet\s*\(\s*(?:group\s*:\s*["']([A-Za-z0-9._-]+)["']\s*,\s*version\s*:\s*["']([^"']+)["']|["']([A-Za-z0-9._-]+):([^"':]+)["'])\s*\)\s*\{`)

	// dependencySetEntryPattern matches one entry inside a dependencySet
	// block: entry 'logback-classic' / entry("logback-classic").
	dependencySetEntryPattern = regexp.MustCompile(`entry\s*\(?\s*["']([A-Za-z0-9._-]+)["']\s*\)?`)

	// useVersionPattern matches the version argument of a dependency resolve
	// rule, in both call styles:
	//   details.useVersion(libs.versions.netty.get())
	//   details.useVersion '1.0.49'
	// Group 1: parenthesized argument, group 2: paren-less Groovy string.
	useVersionPattern = regexp.MustCompile(`useVersion\s*(?:\(([^()]*(?:\([^()]*\))*[^()]*)\)|["']([^"']+)["'])`)

	// ruleIfPattern locates conditional blocks inside resolve rules; the
	// condition and body are bracket-matched from the match position.
	ruleIfPattern = regexp.MustCompile(`if\s*\(`)

	// ruleGroupCondPattern extracts a group equality condition from a resolve
	// rule, e.g. details.requested.group == "io.netty".
	ruleGroupCondPattern = regexp.MustCompile(`requested\.group\s*==\s*["']([A-Za-z0-9._-]+)["']`)

	// ruleNameCondPattern extracts a module-name equality condition from a
	// resolve rule, e.g. details.requested.name == 'signalfx-java'.
	ruleNameCondPattern = regexp.MustCompile(`requested\.name\s*==\s*["']([A-Za-z0-9._-]+)["']`)

	// catalogVersionAccessorPattern matches typed catalog version accessors
	// used as useVersion arguments: libs.versions.netty.get().
	catalogVersionAccessorPattern = regexp.MustCompile(`^libs\.versions\.([A-Za-z0-9_.]+)\.get\(\)$`)

	// quotedLiteralPattern extracts a quoted string and its offsets from a
	// useVersion argument.
	quotedLiteralPattern = regexp.MustCompile(`["']([^"']*)["']`)
)

// parseVersionToken classifies the version part of a declaration. It returns
// the literal version, or the referenced variable path for interpolated
// tokens such as $nettyVersion, ${nettyVersion}, $versions.log4j2,
// ${versions.log4j2} and property("x") accessors.
func parseVersionToken(token string) (literal, varRef string) {
	if m := propertyAccessorPattern.FindStringSubmatch(token); m != nil {
		return "", m[1]
	}
	if strings.HasPrefix(token, "$") {
		ref := strings.TrimPrefix(token, "$")
		ref = strings.TrimPrefix(ref, "{")
		ref = strings.TrimSuffix(ref, "}")
		return "", ref
	}
	return token, ""
}

// containsDigit reports whether s contains an ASCII digit. It distinguishes a
// real version literal (which always carries a digit) from an alphabetic
// format-string segment such as the "ss" in "HH:mm:ss".
func containsDigit(s string) bool {
	for _, r := range s {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

// NormalizeAlias normalizes a catalog accessor path (netty.codec.http) to
// the catalog entry key form (netty-codec-http). Gradle generates accessors
// by splitting alias keys on '-', '_' and '.'; normalizing to dashes matches
// how keys are conventionally written in catalog files.
func NormalizeAlias(alias string) string {
	return strings.ReplaceAll(strings.ReplaceAll(alias, ".", "-"), "_", "-")
}
