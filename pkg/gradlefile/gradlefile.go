/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradlefile

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// DSL identifies the Gradle script dialect of a file.
type DSL int

const (
	// Groovy is the classic Gradle DSL (*.gradle).
	Groovy DSL = iota
	// Kotlin is the Kotlin Gradle DSL (*.gradle.kts).
	Kotlin
)

var (
	// ErrInvalidVersion is returned when a version string contains characters
	// outside the safe allowlist. The allowlist prevents code injection into
	// Gradle scripts through crafted version strings.
	ErrInvalidVersion = errors.New("invalid version string")

	// ErrInvalidCoordinate is returned when a group or artifact identifier
	// contains characters outside the safe allowlist.
	ErrInvalidCoordinate = errors.New("invalid coordinate")

	// ErrConflictingEdit is returned when two edits target the same or
	// overlapping source spans with different replacement values.
	ErrConflictingEdit = errors.New("conflicting edit")

	// ErrNotEditable is returned when an edit is requested on a construct the
	// library cannot rewrite in place (for example setting the version of a
	// declaration whose version is a variable reference).
	ErrNotEditable = errors.New("construct is not editable")
)

var (
	// versionRegex is the allowlist for version strings embedded in Gradle
	// files: alphanumerics, dots, underscores, hyphens and plus signs.
	versionRegex = regexp.MustCompile(`^[a-zA-Z0-9._+-]+$`)

	// coordinateRegex is the allowlist for group and artifact identifiers.
	coordinateRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
)

// ValidateVersion reports whether version is safe to embed in a Gradle file.
func ValidateVersion(version string) error {
	if !versionRegex.MatchString(version) {
		return fmt.Errorf("%w: %q (allowed characters: a-zA-Z0-9._+-)", ErrInvalidVersion, version)
	}
	return nil
}

// ValidateCoordinate reports whether a group or artifact identifier is safe
// to embed in a Gradle file.
func ValidateCoordinate(coordinate string) error {
	if !coordinateRegex.MatchString(coordinate) {
		return fmt.Errorf("%w: %q (allowed characters: a-zA-Z0-9._-)", ErrInvalidCoordinate, coordinate)
	}
	return nil
}

// DSLFromPath returns the DSL dialect implied by a file path.
func DSLFromPath(path string) DSL {
	if strings.HasSuffix(filepath.Base(path), ".kts") {
		return Kotlin
	}
	return Groovy
}

// span is a half-open byte range [start, end) into a file's original content.
type span struct {
	start int
	end   int
}

func (s span) valid() bool { return s.start >= 0 && s.end >= s.start }

// pendingEdit is a queued replacement of one span.
type pendingEdit struct {
	span
	replacement string
}

// editBuffer accumulates span edits against immutable original content and
// applies them all at render time, so recorded spans never go stale.
type editBuffer struct {
	original []byte
	edits    []pendingEdit
}

// add queues a replacement for s. Re-adding the same span with the same
// replacement is a no-op; a different replacement or an overlapping span is
// an error.
func (b *editBuffer) add(s span, replacement string) error {
	if !s.valid() || s.end > len(b.original) {
		return fmt.Errorf("%w: span [%d,%d) out of range", ErrConflictingEdit, s.start, s.end)
	}
	for _, e := range b.edits {
		if e.start == s.start && e.end == s.end {
			if e.replacement == replacement {
				return nil
			}
			return fmt.Errorf("%w: span [%d,%d) already set to %q, requested %q",
				ErrConflictingEdit, s.start, s.end, e.replacement, replacement)
		}
		if s.start < e.end && e.start < s.end {
			return fmt.Errorf("%w: span [%d,%d) overlaps [%d,%d)",
				ErrConflictingEdit, s.start, s.end, e.start, e.end)
		}
	}
	b.edits = append(b.edits, pendingEdit{span: s, replacement: replacement})
	return nil
}

// changed reports whether rendering would differ from the original content.
func (b *editBuffer) changed() bool {
	return b.changeCount() > 0
}

// changeCount returns the number of edits that modify the original content.
func (b *editBuffer) changeCount() int {
	n := 0
	for _, e := range b.edits {
		if string(b.original[e.start:e.end]) != e.replacement {
			n++
		}
	}
	return n
}

// render applies all queued edits and returns the resulting content.
func (b *editBuffer) render() []byte {
	if len(b.edits) == 0 {
		out := make([]byte, len(b.original))
		copy(out, b.original)
		return out
	}
	edits := make([]pendingEdit, len(b.edits))
	copy(edits, b.edits)
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })

	out := make([]byte, len(b.original))
	copy(out, b.original)
	for _, e := range edits {
		out = append(out[:e.start], append([]byte(e.replacement), out[e.end:]...)...)
	}
	return out
}

// lineIsComment reports whether the line containing offset starts with a
// line-comment marker (// for scripts, # for properties files).
func lineIsComment(content []byte, offset int) bool {
	lineStart := offset
	for lineStart > 0 && content[lineStart-1] != '\n' {
		lineStart--
	}
	trimmed := strings.TrimLeft(string(content[lineStart:offset]), " \t")
	return strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "*")
}
