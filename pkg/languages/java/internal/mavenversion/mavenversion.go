/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package mavenversion

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
)

// IsDowngrade reports whether replacing current with requested would lower
// the version. It is false whenever either side is not a single concrete
// version: an empty string, an unresolved ${property} reference, or a
// version range.
func IsDowngrade(current, requested string) bool {
	if !isConcrete(current) || !isConcrete(requested) {
		return false
	}
	return compareVersions(requested, current) < 0
}

// AtLeast reports whether actual is the same version as want or a newer
// one. Versions that are not concrete only match when the strings are equal.
func AtLeast(actual, want string) bool {
	if actual == want {
		return true
	}
	if !isConcrete(actual) || !isConcrete(want) {
		return false
	}
	return compareVersions(actual, want) >= 0
}

// Highest returns the newest concrete version in versions, or "" when there
// is none.
func Highest(versions []string) string {
	var highest string
	for _, v := range versions {
		if !isConcrete(v) {
			continue
		}
		if highest == "" || compareVersions(v, highest) > 0 {
			highest = v
		}
	}
	return highest
}

// compareVersions orders a against b: negative when a is older, zero when
// they are the same version, positive when a is newer.
func compareVersions(a, b string) int {
	return parse(a).compare(parse(b))
}

// isConcrete reports whether v names one version, rather than nothing, a
// property reference or a range.
func isConcrete(v string) bool {
	return v != "" && !strings.ContainsAny(v, "$[](),")
}

// itemKind identifies which of the three item shapes an item holds.
type itemKind int

const (
	intKind itemKind = iota
	stringKind
	listKind
)

// item is one parsed piece of a version: a number, a qualifier, or a nested
// list started by '-' or by a switch between digits and letters.
type item struct {
	kind itemKind
	// digits holds a number without leading zeros, so numbers of any length
	// compare without overflow.
	digits string
	// qualifier holds a lower-case qualifier with aliases applied.
	qualifier string
	items     []*item
}

// qualifiers lists the known qualifiers from lowest to highest. The empty
// qualifier is the release itself.
var qualifiers = []string{"alpha", "beta", "milestone", "rc", "snapshot", "", "sp"}

// qualifierAliases maps alternative spellings to their qualifier.
var qualifierAliases = map[string]string{"ga": "", "final": "", "release": "", "cr": "rc"}

func newInt(digits string) *item {
	digits = strings.TrimLeft(digits, "0")
	return &item{kind: intKind, digits: digits}
}

// newString builds a qualifier item. A single letter followed directly by a
// digit is a short form: a1 is alpha-1, b1 is beta-1 and m1 is milestone-1.
func newString(value string, followedByDigit bool) *item {
	if followedByDigit && len(value) == 1 {
		switch value {
		case "a":
			value = "alpha"
		case "b":
			value = "beta"
		case "m":
			value = "milestone"
		}
	}
	if alias, ok := qualifierAliases[value]; ok {
		value = alias
	}
	return &item{kind: stringKind, qualifier: value}
}

func parseItem(isDigit bool, s string) *item {
	if isDigit {
		return newInt(s)
	}
	return newString(s, false)
}

// parse splits a version into its items.
func parse(version string) *item {
	version = strings.ToLower(version)
	root := &item{kind: listKind}
	list := root
	stack := []*item{root}
	startNested := func() {
		nested := &item{kind: listKind}
		list.items = append(list.items, nested)
		list = nested
		stack = append(stack, nested)
	}

	isDigit := false
	start := 0
	for i := range len(version) {
		c := version[i]
		switch {
		case c == '.' || c == '-':
			if i == start {
				list.items = append(list.items, newInt("0"))
			} else {
				list.items = append(list.items, parseItem(isDigit, version[start:i]))
			}
			start = i + 1
			if c == '-' {
				startNested()
			}
		case c >= '0' && c <= '9':
			if !isDigit && i > start {
				list.items = append(list.items, newString(version[start:i], true))
				start = i
				startNested()
			}
			isDigit = true
		default:
			if isDigit && i > start {
				list.items = append(list.items, newInt(version[start:i]))
				start = i
				startNested()
			}
			isDigit = false
		}
	}
	if len(version) > start {
		// A trailing qualifier after '.' ranks as if it followed '-', so
		// 1.0.0.X1 sorts before 1.0.0-X2.
		if !isDigit && len(list.items) > 0 {
			startNested()
		}
		list.items = append(list.items, parseItem(isDigit, version[start:]))
	}

	for _, nested := range slices.Backward(stack) {
		nested.normalize()
	}
	return root
}

// isNull reports whether the item adds nothing to the version: a zero, the
// release qualifier, or an empty list.
func (it *item) isNull() bool {
	switch it.kind {
	case intKind:
		return it.digits == ""
	case stringKind:
		return it.qualifier == ""
	case listKind:
		return len(it.items) == 0
	}
	return false
}

// normalize drops trailing items that add nothing, so 1.0.0 and 1 are the
// same version. Nested lists at the end are kept and looked past.
func (it *item) normalize() {
	for i, last := range slices.Backward(it.items) {
		if last.isNull() {
			it.items = slices.Delete(it.items, i, i+1)
		} else if last.kind != listKind {
			break
		}
	}
}

// compare orders it against other. A nil other stands for a missing item,
// which ranks like a zero or the release.
func (it *item) compare(other *item) int {
	switch it.kind {
	case intKind:
		return it.compareInt(other)
	case stringKind:
		return it.compareString(other)
	case listKind:
		return it.compareList(other)
	}
	return 0
}

// compareInt orders a number: above any qualifier or nested list, and above
// a missing item unless it is zero.
func (it *item) compareInt(other *item) int {
	if other == nil {
		if it.digits == "" {
			return 0
		}
		return 1
	}
	switch other.kind {
	case intKind:
		return compareDigits(it.digits, other.digits)
	case stringKind, listKind:
		return 1
	}
	return 0
}

// compareString orders a qualifier: below any number or nested list, and
// against a missing item as the release would compare.
func (it *item) compareString(other *item) int {
	if other == nil {
		return strings.Compare(qualifierRank(it.qualifier), qualifierRank(""))
	}
	switch other.kind {
	case stringKind:
		return strings.Compare(qualifierRank(it.qualifier), qualifierRank(other.qualifier))
	case intKind, listKind:
		return -1
	}
	return 0
}

// compareList orders a nested list: below a number, above a qualifier, and
// item by item against another list.
func (it *item) compareList(other *item) int {
	if other == nil {
		if len(it.items) == 0 {
			return 0
		}
		return it.items[0].compare(nil)
	}
	if other.kind == intKind {
		return -1
	}
	if other.kind == stringKind {
		return 1
	}
	for i := range max(len(it.items), len(other.items)) {
		var left, right *item
		if i < len(it.items) {
			left = it.items[i]
		}
		if i < len(other.items) {
			right = other.items[i]
		}
		var result int
		if left == nil {
			result = -right.compare(nil)
		} else {
			result = left.compare(right)
		}
		if result != 0 {
			return result
		}
	}
	return 0
}

// qualifierRank returns a string that sorts qualifiers in Maven's order:
// the index of a known qualifier, or for any other qualifier a value that
// sorts after every known one and then alphabetically.
func qualifierRank(q string) string {
	if i := slices.Index(qualifiers, q); i >= 0 {
		return strconv.Itoa(i)
	}
	return strconv.Itoa(len(qualifiers)) + "-" + q
}

// compareDigits orders two numbers written without leading zeros: the longer
// one is larger, and equal lengths compare digit by digit.
func compareDigits(a, b string) int {
	return cmp.Or(cmp.Compare(len(a), len(b)), strings.Compare(a, b))
}
