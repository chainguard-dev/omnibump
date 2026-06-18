/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradlefile

import (
	"bytes"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// Substitution is a coordinate swap: every request for OldModule
// ("group:artifact") is redirected to NewModule ("group:artifact") at
// Version. It is the resolution-rule analog of a version bump for the case
// where the dependency's coordinate itself changes (e.g. a fork), and it
// applies to both declared and transitive requests.
type Substitution struct {
	OldModule string
	NewModule string
	Version   string
}

// managedConfigurations matches the configurations the managed pins apply to:
// the java ecosystem's compile and runtime classpaths (including per-source-set
// variants such as testRuntimeClasspath), which are what ends up in the built
// artifact. Build-tooling resolution contexts (formatters, linters) are not
// matched: their dependencies never ship.
const managedConfigurations = `.*([Cc]ompileClasspath|[Rr]untimeClasspath)`

// managedBlockOpen opens the settings lifecycle hook. The Groovy form appends an
// explicit closure parameter; Kotlin uses the implicit receiver.
const managedBlockOpen = "gradle.beforeProject {"

var (
	// managedForcePattern matches a version-pin line in the managed block,
	// either DSL:
	//   Groovy: configuration.resolutionStrategy.force 'group:artifact:version'
	//   Kotlin: resolutionStrategy.force("group:artifact:version")
	managedForcePattern = regexp.MustCompile(
		`resolutionStrategy\.force\s*\(?\s*["']([A-Za-z0-9._-]+:[A-Za-z0-9._-]+):([A-Za-z0-9._+-]+)["']`)

	// managedSubstitutionPattern matches a substitution line in either DSL:
	//   Groovy: substitute module('old:mod') using module('new:mod:version')
	//   Kotlin: substitute(module("old:mod")).using(module("new:mod:version"))
	managedSubstitutionPattern = regexp.MustCompile(
		`substitute\s*\(?\s*module\(\s*["']([A-Za-z0-9._-]+:[A-Za-z0-9._-]+)["']\s*\)[^\n]*?module\(\s*["']([A-Za-z0-9._-]+:[A-Za-z0-9._-]+):([A-Za-z0-9._+-]+)["']\s*\)`)
)

// validateManaged checks every coordinate and version that will be embedded
// into the synthesized script against the safe allowlists.
func validateManaged(constraints map[string]string, subs []Substitution) error {
	for module, version := range constraints {
		if err := validateModule(module); err != nil {
			return err
		}
		if err := ValidateVersion(version); err != nil {
			return err
		}
	}
	for _, s := range subs {
		if err := validateModule(s.OldModule); err != nil {
			return err
		}
		if err := validateModule(s.NewModule); err != nil {
			return err
		}
		if err := ValidateVersion(s.Version); err != nil {
			return err
		}
	}
	return nil
}

func validateModule(module string) error {
	group, artifact, ok := strings.Cut(module, ":")
	if !ok {
		return fmt.Errorf("%w: %q is not in group:artifact form", ErrInvalidCoordinate, module)
	}
	if err := ValidateCoordinate(group); err != nil {
		return err
	}
	return ValidateCoordinate(artifact)
}

// renderManagedBlock renders the omnibump-managed block for a settings script.
//
// The block runs from gradle.beforeProject, so every rule is registered before
// the project's build script evaluates — and therefore before any plugin
// resolves a configuration. This is what lets the pins apply to projects that
// resolve a classpath at configuration time (e.g. a plugin that calls
// evaluationDependsOnChildren() or resolves runtimeClasspath eagerly), which a
// build-script-appended resolutionStrategy block cannot.
//
// Two rule kinds are emitted:
//   - Version bumps become resolutionStrategy.force pins on the compile/runtime
//     classpaths. force is used rather than a dependency constraint because it
//     is exempt from failOnVersionConflict() — which OpenSearch plugins enable
//     and which rejects the divergent version a `require` constraint would add
//     to the graph. force collapses all requests for the module to the pinned
//     version (so it can, in principle, pin below an already-higher selection).
//   - Coordinate swaps become dependencySubstitution rules, which redirect a
//     module (direct or transitive) to a new module at a fixed version.
//
// constraints maps "group:artifact" -> version. subs are coordinate swaps.
func renderManagedBlock(constraints map[string]string, subs []Substitution, dsl DSL) string {
	modules := slices.Sorted(maps.Keys(constraints))
	sortedSubs := slices.SortedFunc(slices.Values(subs), func(a, b Substitution) int {
		return strings.Compare(a.OldModule, b.OldModule)
	})

	// The block is emitted in the project's DSL. Groovy uses single quotes, an
	// explicit closure parameter, a qualified receiver, and command syntax;
	// Kotlin uses double quotes, an implicit receiver, and method-call syntax.
	q := "'"
	beforeProject := managedBlockOpen + " project ->"
	recv := "project."
	eachParam := " configuration ->"
	eachRecv := "configuration."
	matchExpr := "configuration.name ==~ /" + managedConfigurations + "/"
	force := func(module, version string) string {
		return eachRecv + "resolutionStrategy.force " + q + module + ":" + version + q
	}
	substitute := func(s Substitution) string {
		return "substitute module('" + s.OldModule + "') using module('" +
			s.NewModule + ":" + s.Version + "') because 'omnibump coordinate swap'"
	}
	if dsl == Kotlin {
		q = "\""
		beforeProject = managedBlockOpen
		recv = ""
		eachParam = ""
		eachRecv = ""
		matchExpr = "name.matches(Regex(" + q + managedConfigurations + q + "))"
		force = func(module, version string) string {
			return eachRecv + "resolutionStrategy.force(" + q + module + ":" + version + q + ")"
		}
		substitute = func(s Substitution) string {
			return "substitute(module(" + q + s.OldModule + q + ")).using(module(" +
				q + s.NewModule + ":" + s.Version + q + ")).because(" + q + "omnibump coordinate swap" + q + ")"
		}
	}

	var b strings.Builder
	b.WriteString(ForceBlockBegin + "\n")
	b.WriteString(beforeProject + "\n")
	// Version pins use resolutionStrategy.force (not dependency constraints):
	// force is exempt from failOnVersionConflict(), which OpenSearch plugins
	// enable and which rejects the divergent version a constraint introduces.
	// Coordinate swaps use dependencySubstitution. Both are installed via
	// configureEach under gradle.beforeProject, so they register before any
	// configuration resolves.
	if len(modules) > 0 || len(sortedSubs) > 0 {
		b.WriteString("    " + recv + "configurations.configureEach {" + eachParam + "\n")
		b.WriteString("        if (" + matchExpr + ") {\n")
		for _, module := range modules {
			b.WriteString("            " + force(module, constraints[module]) + "\n")
		}
		if len(sortedSubs) > 0 {
			b.WriteString("            " + eachRecv + "resolutionStrategy.dependencySubstitution {\n")
			for _, s := range sortedSubs {
				b.WriteString("                " + substitute(s) + "\n")
			}
			b.WriteString("            }\n")
		}
		b.WriteString("        }\n")
		b.WriteString("    }\n")
	}
	b.WriteString("}\n")
	b.WriteString(ForceBlockEnd)
	return b.String()
}

// parseManagedBlock extracts the constraints ("group:artifact" -> version) and
// substitutions of an existing managed block in content. Used to merge on
// re-runs (idempotency) and to surface the managed pins to validation.
func parseManagedBlock(content []byte) (map[string]string, []Substitution) {
	constraints := make(map[string]string)
	var subs []Substitution
	sp, ok := managedBlockSpan(content)
	if !ok {
		return constraints, subs
	}
	block := content[sp.start:sp.end]
	for _, m := range managedForcePattern.FindAllSubmatch(block, -1) {
		constraints[string(m[1])] = string(m[2])
	}
	for _, m := range managedSubstitutionPattern.FindAllSubmatch(block, -1) {
		subs = append(subs, Substitution{
			OldModule: string(m[1]),
			NewModule: string(m[2]),
			Version:   string(m[3]),
		})
	}
	return constraints, subs
}

// managedBlockSpan returns the span of the existing managed block, from the
// begin marker through the end marker.
func managedBlockSpan(content []byte) (span, bool) {
	begin := bytes.Index(content, []byte(ForceBlockBegin))
	if begin < 0 {
		return span{-1, -1}, false
	}
	end := bytes.Index(content[begin:], []byte(ForceBlockEnd))
	if end < 0 {
		return span{-1, -1}, false
	}
	return span{begin, begin + end + len(ForceBlockEnd)}, true
}

// EnsureManagedBlock queues an edit that guarantees the settings file's
// managed block pins every constraint and applies every substitution. An
// existing block is merged (new versions/targets win, entries deduplicated and
// sorted); otherwise the block is appended. The operation is idempotent.
func (f *SettingsFile) EnsureManagedBlock(constraints map[string]string, subs []Substitution) error {
	if len(constraints) == 0 && len(subs) == 0 {
		return nil
	}
	if err := validateManaged(constraints, subs); err != nil {
		return err
	}

	mergedConstraints, mergedSubs := parseManagedBlock(f.buf.original)
	maps.Copy(mergedConstraints, constraints)
	subByOld := make(map[string]Substitution, len(mergedSubs)+len(subs))
	for _, s := range mergedSubs {
		subByOld[s.OldModule] = s
	}
	for _, s := range subs {
		subByOld[s.OldModule] = s
	}
	mergedSubs = slices.Collect(maps.Values(subByOld))

	block := renderManagedBlock(mergedConstraints, mergedSubs, f.dsl)
	if sp, ok := managedBlockSpan(f.buf.original); ok {
		return f.buf.add(sp, block)
	}
	suffix := "\n" + block + "\n"
	if len(f.buf.original) > 0 && f.buf.original[len(f.buf.original)-1] != '\n' {
		suffix = "\n" + suffix
	}
	end := len(f.buf.original)
	return f.buf.add(span{end, end}, suffix)
}

// ManagedCoordinates returns the effective "group:artifact" -> version pins of
// the file's managed block: each constraint, plus each substitution's new
// module at its target version. This lets the project model surface the
// settings-managed pins to dependency validation.
func (f *SettingsFile) ManagedCoordinates() map[string]string {
	constraints, subs := parseManagedBlock(f.buf.original)
	out := make(map[string]string, len(constraints)+len(subs))
	maps.Copy(out, constraints)
	for _, s := range subs {
		out[s.NewModule] = s.Version
	}
	return out
}

// NewSettingsFileContent returns the content for a brand-new root settings
// script that exists only to host the managed block.
func NewSettingsFileContent(dsl DSL, constraints map[string]string, subs []Substitution) (string, error) {
	if err := validateManaged(constraints, subs); err != nil {
		return "", err
	}
	return strings.TrimLeft(renderManagedBlock(constraints, subs, dsl), "\n") + "\n", nil
}
