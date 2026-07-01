/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradlefile

import (
	"regexp"
	"strings"
)

// ShipConfigRef is a reference to a Gradle configuration whose resolved
// contents are bundled into a shipped artifact: a fat/uber jar (shadow plugin
// or a hand-rolled Jar task), a capsule, a distribution, or a war. omnibump
// must force its managed version pins on these configurations too, because a
// configuration whose name is not a compile or runtime classpath escapes
// managedConfigurations yet still ends up inside the published artifact (e.g.
// nextflow's shadowJar bundles a custom 'lineageImplementation' configuration).
type ShipConfigRef struct {
	// Name is the resolved configuration name (e.g. "lineageImplementation"),
	// or empty when the bundling site references a configuration in a form
	// omnibump cannot resolve to a literal name.
	Name string

	// Source labels how the reference was found (e.g. "configurations =",
	// "from", "classpath", "embedConfiguration"), for diagnostics.
	Source string

	// Raw is a trimmed source snippet, surfaced in warnings for unresolved
	// references.
	Raw string

	// Resolved reports whether Name was extracted. False marks a bundling site
	// whose configuration could not be statically determined, so the operator
	// must pin it explicitly.
	Resolved bool
}

var (
	// shipConfigAssignPattern matches a shadow-style assignment of the set of
	// configurations a packaging task bundles:
	//   configurations = [project.configurations.runtimeClasspath, project.configurations.lineageImplementation]
	//   configurations = project.configurations.named("x").map { listOf(it) }
	// Group 1 is the right-hand side (a list literal or a single expression),
	// parsed for configuration references.
	shipConfigAssignPattern = regexp.MustCompile(`(?s)\bconfigurations\s*=\s*(\[[^\]]*\]|[^\n]*)`)

	// shipEmbedPattern matches the gradle-capsule-plugin's embedConfiguration:
	//   embedConfiguration = configurations.getByName("runtimeClasspath")
	//   embedConfiguration.set(configurations.getByName("runtimeClasspath"))
	shipEmbedPattern = regexp.MustCompile(`\bembedConfiguration\b[^\n]*`)

	// shipFromPattern matches a packaging task pulling a configuration's files
	// into its output — the generic (no-plugin) fat-jar idiom and the
	// bootJar/war classpath form:
	//   from configurations.myBundle
	//   from { configurations.myBundle.collect { it.isDirectory() ? it : zipTree(it) } }
	//   classpath configurations.foo
	// Group 1 is the keyword, group 2 the expression following it (a closure or
	// the rest of the line), parsed for configuration references.
	//
	// The `\{[^{}]*\}` arm matches only a single-level closure. A multiline
	// `from {` whose configuration reference sits in a nested inner closure on a
	// later line is not matched: the closure arm cannot cross the inner brace and
	// the fallback `[^\n]*` captures only the opening `{`, so no ShipConfigRef
	// (neither resolved nor unresolved) is recorded and no warning is emitted.
	// GradleForceConfigurations is the intended escape hatch for that form.
	shipFromPattern = regexp.MustCompile(`(?s)\b(from|classpath)\b\s*(\{[^{}]*\}|[^\n]*)`)

	// configRefQuotedPattern matches configuration references whose name is a
	// string literal: configurations.getByName("x"), configurations.named('x'),
	// configurations['x'], configurations.findByName("x"),
	// configurations.maybeCreate("x").
	configRefQuotedPattern = regexp.MustCompile(`configurations\s*(?:\.\s*(?:getByName|named|findByName|maybeCreate)\s*\(\s*|\[\s*)["']([A-Za-z][A-Za-z0-9_]*)["']`)

	// configRefPropertyPattern matches property-access configuration references:
	//   configurations.lineageImplementation / project.configurations.runtimeClasspath
	// The captured identifier may be a container member (getByName, all, ...);
	// those are filtered out via configContainerMethods.
	configRefPropertyPattern = regexp.MustCompile(`configurations\s*\.\s*([A-Za-z][A-Za-z0-9_]*)`)

	// configRefUnresolvedPattern matches a configuration lookup whose argument
	// is not a string literal (a variable or expression), which omnibump cannot
	// resolve to a name: configurations.getByName(someVar) / configurations[someVar].
	configRefUnresolvedPattern = regexp.MustCompile(`configurations\s*(?:\.\s*(?:getByName|named|findByName)\s*\(\s*|\[\s*)[A-Za-z_$]`)
)

// nonShippingExact and nonShippingSubstrings identify configurations used only
// by documentation, linting, static-analysis or annotation-processing tooling.
// Such configurations are frequently pulled into javadoc/sources Jar tasks via
// `from configurations.X`, but their dependencies never ship in the runtime
// artifact, and force-pinning them risks breaking tool resolution — the very
// reason the default managed match is limited to compile/runtime classpaths.
// They are dropped from the auto-detected ship set; an operator can still force
// one explicitly via GradleForceConfigurations. Substrings are only the
// unambiguous long tokens that cannot appear inside a real shipping
// configuration name.
// nonShippingExact lists short, ambiguous tooling configuration names matched
// exactly (substring matching would risk hitting real shipping names). Names
// already covered by nonShippingSubstrings are intentionally not repeated here.
var nonShippingExact = map[string]struct{}{
	"kdoc": {}, "zinc": {}, "codenarc": {}, "pmd": {},
	"spotbugs": {}, "findbugs": {}, "ktlint": {}, "detekt": {},
}

// nonShippingSubstrings are the unambiguous long tokens that cannot appear
// inside a real shipping configuration name, matched as substrings so variants
// (testAnnotationProcessor, kotlinCompilerPluginClasspath, ...) are covered.
var nonShippingSubstrings = []string{
	"javadoc", "groovydoc", "spotless", "checkstyle", "jacoco",
	"errorprone", "annotationprocessor", "kotlincompiler", "scalacompiler", "dokka",
}

// IsNonShippingConfigName reports whether name denotes a documentation, lint,
// static-analysis or annotation-processing configuration whose dependencies do
// not ship at runtime, so it should be excluded from auto-detected ship
// configurations.
func IsNonShippingConfigName(name string) bool {
	l := strings.ToLower(name)
	if _, ok := nonShippingExact[l]; ok {
		return true
	}
	for _, s := range nonShippingSubstrings {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}

// configContainerMethods are members of the configurations container that the
// property-access pattern can capture but which are not configuration names.
var configContainerMethods = map[string]struct{}{
	"getByName": {}, "named": {}, "findByName": {}, "maybeCreate": {},
	"create": {}, "register": {}, "all": {}, "configureEach": {},
	"matching": {}, "withType": {}, "each": {}, "collect": {},
	"findAll": {}, "asMap": {}, "names": {}, "getAt": {}, "forEach": {},
	"removeAll": {}, "addAll": {}, "add": {},
}

// scanShipConfigs records configurations whose resolved contents are bundled
// into a shipped artifact by a packaging task (shadow/capsule/war, or a generic
// Jar/Copy task), so the managed block can force its pins on them in addition
// to the compile/runtime classpaths. See ShipConfigRef.
//
// Detection is intentionally file-wide, not scoped to packaging-task blocks: a
// `configurations = [...]` or `from configurations.x` outside a packaging
// context produces a conservative false-positive ship config (an extra, harmless
// force pin) rather than a missed one. This tradeoff is deliberate — a false
// positive costs a redundant pin, a false negative costs a shipped CVE — so do
// not tighten the scope to packaging blocks only, which would trade harmless
// over-pinning for silent under-pinning.
func (f *BuildFile) scanShipConfigs(content []byte) {
	record := func(source string, expr []byte) {
		names := extractConfigRefs(expr)
		if len(names) == 0 {
			if configRefUnresolvedPattern.Match(expr) {
				f.shipConfigs = append(f.shipConfigs, ShipConfigRef{
					Source: source, Raw: snippet(expr), Resolved: false,
				})
			}
			return
		}
		for _, n := range names {
			f.shipConfigs = append(f.shipConfigs, ShipConfigRef{
				Name: n, Source: source, Raw: snippet(expr), Resolved: true,
			})
		}
	}

	for _, m := range shipConfigAssignPattern.FindAllSubmatchIndex(content, -1) {
		if lineIsComment(content, m[0]) {
			continue
		}
		record("configurations =", content[m[2]:m[3]])
	}
	for _, m := range shipEmbedPattern.FindAllSubmatchIndex(content, -1) {
		if lineIsComment(content, m[0]) {
			continue
		}
		record("embedConfiguration", content[m[0]:m[1]])
	}
	for _, m := range shipFromPattern.FindAllSubmatchIndex(content, -1) {
		if lineIsComment(content, m[0]) {
			continue
		}
		record(string(content[m[2]:m[3]]), content[m[4]:m[5]])
	}
}

// extractConfigRefs returns the configuration names referenced in a Gradle
// expression, across the property-access (configurations.x), string-lookup
// (configurations.getByName("x"), configurations["x"]) and named() forms.
// Container members (getByName, all, configureEach, ...) captured by the
// property form are filtered out. Results are deduplicated, order preserved.
func extractConfigRefs(expr []byte) []string {
	seen := make(map[string]struct{})
	var out []string
	add := func(n string) {
		if n == "" {
			return
		}
		if _, ok := seen[n]; ok {
			return
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	for _, m := range configRefQuotedPattern.FindAllSubmatch(expr, -1) {
		add(string(m[1]))
	}
	for _, m := range configRefPropertyPattern.FindAllSubmatch(expr, -1) {
		name := string(m[1])
		if _, ok := configContainerMethods[name]; ok {
			continue
		}
		add(name)
	}
	return out
}

// snippet trims and collapses whitespace in a source fragment and truncates it
// for log output.
func snippet(b []byte) string {
	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) > 80 {
		s = s[:80] + "…"
	}
	return s
}

// ShipConfigs returns the configuration references bundled into shipped
// artifacts by packaging tasks in this build script.
func (f *BuildFile) ShipConfigs() []ShipConfigRef { return f.shipConfigs }
