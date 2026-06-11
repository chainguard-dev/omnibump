/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradlefile

import (
	"fmt"
	"regexp"
)

// propertyLinePattern matches one key/value line of a Java-properties-style
// file, covering the forms found in gradle.properties and version.properties
// files: key=value, key = value and key: value. Group 1: key, group 2:
// separator, group 3: value.
var propertyLinePattern = regexp.MustCompile(`(?m)^[ \t]*([A-Za-z0-9._-]+)[ \t]*([=:])[ \t]*([^\r\n]*?)[ \t]*$`)

// PropertyEntry is one key/value pair of a properties file.
type PropertyEntry struct {
	// Key is the property name.
	Key string

	// Value is the current value.
	Value string

	valueSpan span
}

// PropertiesFile is a parsed gradle.properties or version.properties-style
// file. Order, comments and unrelated lines are preserved on edit.
type PropertiesFile struct {
	path    string
	buf     editBuffer
	entries []PropertyEntry
	index   map[string]int // key -> first entry index
}

// ParseProperties parses a properties file.
func ParseProperties(path string, content []byte) (*PropertiesFile, error) {
	f := &PropertiesFile{
		path:  path,
		buf:   editBuffer{original: content},
		index: make(map[string]int),
	}
	// Comment lines (# or !) can never match the pattern: the key character
	// class excludes both markers, so no explicit comment filtering is needed.
	for _, m := range propertyLinePattern.FindAllSubmatchIndex(content, -1) {
		entry := PropertyEntry{
			Key:       string(content[m[2]:m[3]]),
			Value:     string(content[m[6]:m[7]]),
			valueSpan: span{m[6], m[7]},
		}
		f.entries = append(f.entries, entry)
		if _, seen := f.index[entry.Key]; !seen {
			f.index[entry.Key] = len(f.entries) - 1
		}
	}
	return f, nil
}

// Path returns the file path the properties were parsed from.
func (f *PropertiesFile) Path() string { return f.path }

// Entries returns all key/value pairs in file order.
func (f *PropertiesFile) Entries() []PropertyEntry { return f.entries }

// Get returns the value of key and whether it is defined.
func (f *PropertiesFile) Get(key string) (string, bool) {
	i, ok := f.index[key]
	if !ok {
		return "", false
	}
	return f.entries[i].Value, true
}

// Keys returns all defined property names in file order.
func (f *PropertiesFile) Keys() []string {
	keys := make([]string, 0, len(f.entries))
	for _, e := range f.entries {
		keys = append(keys, e.Key)
	}
	return keys
}

// Set queues an in-place rewrite of key's value. Only the value segment of
// the line is replaced; separator style and surrounding content are
// preserved. All occurrences of the key are updated.
func (f *PropertiesFile) Set(key, value string) error {
	if err := ValidateVersion(value); err != nil {
		return err
	}
	found := false
	for _, e := range f.entries {
		if e.Key != key {
			continue
		}
		found = true
		if err := f.buf.add(e.valueSpan, value); err != nil {
			return err
		}
	}
	if !found {
		return fmt.Errorf("%w: property %s in %s", ErrNotEditable, key, f.path)
	}
	return nil
}

// Content renders the file with all queued edits applied.
func (f *PropertiesFile) Content() []byte { return f.buf.render() }

// Changed reports whether any queued edit modifies the original content.
func (f *PropertiesFile) Changed() bool { return f.buf.changed() }

// ChangeCount returns the number of queued edits that modify the content.
func (f *PropertiesFile) ChangeCount() int { return f.buf.changeCount() }
