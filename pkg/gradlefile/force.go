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

// Marker comments delimiting the omnibump-managed resolution-strategy block.
// The block pins transitive dependency versions the way Maven pins them via
// DependencyManagement; re-running omnibump merges into the existing block
// instead of appending duplicates.
const (
	// ForceBlockBegin opens the managed block.
	ForceBlockBegin = "// omnibump:resolutionStrategy:begin"
	// ForceBlockEnd closes the managed block.
	ForceBlockEnd = "// omnibump:resolutionStrategy:end"
)

// forceLinePattern extracts the coordinates of one force entry inside the
// managed block, covering both DSL render styles.
var forceLinePattern = regexp.MustCompile(`force\s*\(?\s*["']([A-Za-z0-9._-]+):([A-Za-z0-9._-]+):([A-Za-z0-9._+-]+)["']\s*\)?`)

// ForcedCoordinates returns the "group:artifact" -> version entries of the
// file's managed force block, or an empty map when no block exists.
func (f *BuildFile) ForcedCoordinates() map[string]string {
	coords := make(map[string]string)
	blockSpan, ok := f.forceBlockSpan()
	if !ok {
		return coords
	}
	block := f.buf.original[blockSpan.start:blockSpan.end]
	for _, m := range forceLinePattern.FindAllSubmatch(block, -1) {
		coords[string(m[1])+":"+string(m[2])] = string(m[3])
	}
	return coords
}

// EnsureForceBlock queues an edit that guarantees the file's managed force
// block pins every "group:artifact" in coords at the given version. An
// existing block is merged (new versions win, entries are deduplicated and
// sorted); otherwise the block is appended to the end of the file. The
// operation is idempotent.
func (f *BuildFile) EnsureForceBlock(coords map[string]string) error {
	if len(coords) == 0 {
		return nil
	}
	merged := f.ForcedCoordinates()
	for module, version := range coords {
		group, artifact, ok := strings.Cut(module, ":")
		if !ok {
			return fmt.Errorf("%w: %q is not in group:artifact form", ErrInvalidCoordinate, module)
		}
		// The force block is the one place omnibump synthesizes script code,
		// so every embedded component is validated against the allowlists.
		if err := ValidateCoordinate(group); err != nil {
			return err
		}
		if err := ValidateCoordinate(artifact); err != nil {
			return err
		}
		if err := ValidateVersion(version); err != nil {
			return err
		}
		merged[module] = version
	}

	block := renderForceBlock(merged, f.dsl)
	if blockSpan, ok := f.forceBlockSpan(); ok {
		return f.buf.add(blockSpan, block)
	}
	suffix := "\n" + block + "\n"
	if len(f.buf.original) > 0 && f.buf.original[len(f.buf.original)-1] != '\n' {
		suffix = "\n" + suffix
	}
	end := len(f.buf.original)
	return f.buf.add(span{end, end}, suffix)
}

// forceBlockSpan returns the span of the existing managed block, from the
// begin marker through the end marker.
func (f *BuildFile) forceBlockSpan() (span, bool) {
	begin := bytes.Index(f.buf.original, []byte(ForceBlockBegin))
	if begin < 0 {
		return span{-1, -1}, false
	}
	end := bytes.Index(f.buf.original[begin:], []byte(ForceBlockEnd))
	if end < 0 {
		return span{-1, -1}, false
	}
	return span{begin, begin + end + len(ForceBlockEnd)}, true
}

// forceIncludedConfigurations matches the configurations the force block
// applies to: the java ecosystem's compile and runtime classpaths (including
// per-source-set variants such as testRuntimeClasspath), which are what ends
// up in the built artifact. Resolution contexts created by build tooling
// (formatters, linters, code generators) are deliberately not matched: their
// dependencies never ship, so pinning there has no security value — and
// their bare resolution contexts lack the JVM attributes needed to
// disambiguate multi-variant modules (e.g. forcing guava 32.x into
// Spotless's configuration fails variant matching on Gradle 7.x).
const forceIncludedConfigurations = `.*([Cc]ompileClasspath|[Rr]untimeClasspath)`

// renderForceBlock renders the managed block with one force entry and one
// eachDependency rule per module, sorted for determinism.
//
// Both mechanisms are emitted because they defeat different version sources:
// force wins over transitive requests and platform()/BOM constraints, while
// the eachDependency rules win over plugins that manage versions through
// their own resolve rules (notably io.spring.dependency-management, which
// silently overrides force). The block is wrapped in afterEvaluate so the
// eachDependency rules are registered after any such plugin registers its
// own — for resolve rules, the last registered rule decides.
func renderForceBlock(coords map[string]string, dsl DSL) string {
	modules := slices.Sorted(maps.Keys(coords))

	root := newScriptBlock("allprojects")
	deferred := root.child("afterEvaluate")

	var matched *scriptBlock
	if dsl == Kotlin {
		matched = deferred.child("configurations.matching { it.name.matches(Regex(%q)) }.all", forceIncludedConfigurations)
	} else {
		matched = deferred.child("configurations.matching { it.name ==~ /%s/ }.all", forceIncludedConfigurations)
	}

	strategy := matched.child("resolutionStrategy")
	for _, module := range modules {
		if dsl == Kotlin {
			strategy.stmt("force(%s)", dsl.str(module+":"+coords[module]))
		} else {
			strategy.stmt("force %s", dsl.str(module+":"+coords[module]))
		}
	}

	rules := strategy.child("eachDependency")
	for _, module := range modules {
		group, artifact, _ := strings.Cut(module, ":")
		if dsl == Kotlin {
			rules.stmt("if (requested.group == %s && requested.name == %s) { useVersion(%s) }",
				dsl.str(group), dsl.str(artifact), dsl.str(coords[module]))
		} else {
			rules.stmt("if (it.requested.group == %s && it.requested.name == %s) { it.useVersion(%s) }",
				dsl.str(group), dsl.str(artifact), dsl.str(coords[module]))
		}
	}

	var b strings.Builder
	b.WriteString(ForceBlockBegin + "\n")
	root.render(&b, 0)
	b.WriteString(ForceBlockEnd)
	return b.String()
}

// NewBuildFileContent returns the content for a brand-new root build script
// that exists only to host the managed force block.
func NewBuildFileContent(dsl DSL, coords map[string]string) (string, error) {
	f := &BuildFile{dsl: dsl, buf: editBuffer{original: nil}}
	if err := f.EnsureForceBlock(coords); err != nil {
		return "", err
	}
	return strings.TrimLeft(string(f.Content()), "\n"), nil
}
