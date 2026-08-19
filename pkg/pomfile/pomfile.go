/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package pomfile

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"sort"
)

var (
	// ErrPropertyNotFound is returned when a property is not declared in the
	// POM's top-level <properties> block.
	ErrPropertyNotFound = errors.New("property not declared in pom properties")

	// ErrConflictingEdit is returned when two edits target overlapping ranges
	// with different replacements.
	ErrConflictingEdit = errors.New("conflicting edit")
)

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

// Property is one entry of a POM's top-level <properties> block.
type Property struct {
	// Name is the property element name, e.g. "logback.version".
	Name string

	// Value is the current text content of the element.
	Value string

	valueSpan span
}

// File is a parsed Maven POM whose top-level properties can be rewritten in
// place. Everything outside the edited value ranges is preserved byte for byte.
type File struct {
	path     string
	original []byte
	edits    []pendingEdit
	props    []Property
	index    map[string]int // property name -> first entry index
}

// Parse reads the top-level <properties> block of a POM. path is retained for
// error messages only; content is the POM's raw bytes.
//
// Properties declared inside a <profile> are deliberately not collected: they
// apply only when that profile is active, so rewriting one would silently
// change a conditional value while leaving the default in place.
func Parse(path string, content []byte) (*File, error) {
	f := &File{
		path:     path,
		original: content,
		index:    make(map[string]int),
	}

	dec := xml.NewDecoder(bytes.NewReader(content))
	// Maven POMs in the wild carry non-UTF-8 encodings and stray entities;
	// keep the tokenizer permissive so a quirky file degrades to "no
	// properties found" rather than a hard failure.
	dec.Strict = false
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }

	var stack []string
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parsing POM %s: %w", path, err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			stack = append(stack, t.Name.Local)
			if !isProjectProperty(stack) {
				continue
			}
			// InputOffset is now positioned just past the element's '>'.
			start := int(dec.InputOffset())
			end, err := skipToElementEnd(dec, content)
			if err != nil {
				return nil, fmt.Errorf("parsing POM %s: %w", path, err)
			}
			// The tokenizer consumed the matching EndElement, so drop the entry
			// this loop pushed for it.
			stack = stack[:len(stack)-1]
			if end < start {
				// A self-closing <foo/> has no value range to rewrite.
				continue
			}
			f.addProperty(t.Name.Local, span{start, end})
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}

	return f, nil
}

// addProperty records one property and its value range, keeping the first
// declaration as the one Get reports.
func (f *File) addProperty(name string, valueSpan span) {
	f.props = append(f.props, Property{
		Name:      name,
		Value:     string(f.original[valueSpan.start:valueSpan.end]),
		valueSpan: valueSpan,
	})
	if _, seen := f.index[name]; !seen {
		f.index[name] = len(f.props) - 1
	}
}

// isProjectProperty reports whether the element stack points at a direct child
// of the project's top-level <properties> block.
func isProjectProperty(stack []string) bool {
	return len(stack) == 3 && stack[0] == "project" && stack[1] == "properties"
}

// skipToElementEnd consumes tokens until the currently-open element closes and
// returns the offset at which its closing tag begins — the end of its value.
//
// The tokenizer only reports the offset *after* a token, so the closing tag's
// start is recovered by scanning back to the last '<' before that offset.
func skipToElementEnd(dec *xml.Decoder, content []byte) (int, error) {
	depth := 1
	for {
		tok, err := dec.Token()
		if err != nil {
			return 0, err
		}
		switch tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
			if depth == 0 {
				afterClose := min(int(dec.InputOffset()), len(content))
				return bytes.LastIndexByte(content[:afterClose], '<'), nil
			}
		}
	}
}

// Path returns the file path the POM was parsed from.
func (f *File) Path() string { return f.path }

// Properties returns the top-level properties in document order.
func (f *File) Properties() []Property { return f.props }

// Get returns the value of a top-level property and whether it is declared.
func (f *File) Get(name string) (string, bool) {
	i, ok := f.index[name]
	if !ok {
		return "", false
	}
	return f.props[i].Value, true
}

// Has reports whether the POM declares a top-level property.
func (f *File) Has(name string) bool {
	_, ok := f.index[name]
	return ok
}

// SetProperty queues an in-place rewrite of a property's value. Only the text
// between the element's tags is replaced; the tags, surrounding whitespace and
// any comments stay untouched. A property declared more than once is updated at
// every occurrence, matching how PatchProject treats a repeated key.
//
// A property the POM does not declare is an error rather than an insertion: the
// caller resolved this POM as the property's owner, so a miss means that
// resolution was wrong, and adding the entry here would shadow the real
// declaration in a parent POM.
func (f *File) SetProperty(name, value string) error {
	if _, ok := f.index[name]; !ok {
		return fmt.Errorf("%w: %s in %s", ErrPropertyNotFound, name, f.path)
	}
	for _, p := range f.props {
		if p.Name != name {
			continue
		}
		if err := f.addEdit(p.valueSpan, value); err != nil {
			return err
		}
	}
	return nil
}

// addEdit queues a replacement for s, rejecting overlapping or contradictory
// edits so a later change can never silently clobber an earlier one.
func (f *File) addEdit(s span, replacement string) error {
	if !s.valid() || s.end > len(f.original) {
		return fmt.Errorf("%w: span [%d,%d) out of range", ErrConflictingEdit, s.start, s.end)
	}
	for _, e := range f.edits {
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
	f.edits = append(f.edits, pendingEdit{span: s, replacement: replacement})
	return nil
}

// Changed reports whether any queued edit differs from the original content.
func (f *File) Changed() bool {
	for _, e := range f.edits {
		if string(f.original[e.start:e.end]) != e.replacement {
			return true
		}
	}
	return false
}

// Content renders the POM with all queued edits applied. Edits are applied from
// the end of the file backwards so earlier offsets stay valid.
func (f *File) Content() []byte {
	out := make([]byte, len(f.original))
	copy(out, f.original)
	if len(f.edits) == 0 {
		return out
	}

	edits := make([]pendingEdit, len(f.edits))
	copy(edits, f.edits)
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })

	for _, e := range edits {
		out = append(out[:e.start], append([]byte(e.replacement), out[e.end:]...)...)
	}
	return out
}
