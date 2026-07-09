/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package revdep computes, for a Rust crate and a target version, the minimum
// version each reverse dependency must be upgraded to so the target becomes
// reachable — terminating at the workspace crate whose Cargo.toml must be edited.
//
// It is a copy of the standalone rust-reverse-dependency-calculator, adapted into
// an importable package with a context-aware crates.io index client and a single
// Calculate entry point. The manifest-editing half of the original tool is
// intentionally omitted: omnibump's own appliers perform the Cargo.toml edits.
package revdep

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var (
	errEmptyVersion           = errors.New("empty version")
	errInvalidComponent       = errors.New("invalid version component")
	errUnsupportedRequirement = errors.New("unsupported requirement term")
)

// Version is a semantic version. Build metadata (after '+') is discarded because
// it does not participate in version comparison.
type Version struct {
	Major, Minor, Patch uint64
	Pre                 string // pre-release, without the leading '-'
}

func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// ParseVersion parses a concrete version such as "0.56.0" or "1.2.3-rc.1".
// A leading 'v' is tolerated. Missing minor/patch components default to 0.
func ParseVersion(s string) (Version, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	pre := ""
	if i := strings.IndexByte(s, '-'); i >= 0 {
		pre = s[i+1:]
		s = s[:i]
	}
	if s == "" {
		return Version{}, errEmptyVersion
	}
	parts := strings.Split(s, ".")
	var nums [3]uint64
	for i := 0; i < 3; i++ {
		if i < len(parts) && parts[i] != "" {
			n, err := strconv.ParseUint(parts[i], 10, 64)
			if err != nil {
				return Version{}, fmt.Errorf("invalid version %q: %w", s, err)
			}
			nums[i] = n
		}
	}
	return Version{nums[0], nums[1], nums[2], pre}, nil
}

// Compare returns -1, 0 or 1 following semver ordering (release > pre-release).
func (v Version) Compare(o Version) int {
	if c := cmpUint(v.Major, o.Major); c != 0 {
		return c
	}
	if c := cmpUint(v.Minor, o.Minor); c != 0 {
		return c
	}
	if c := cmpUint(v.Patch, o.Patch); c != 0 {
		return c
	}
	return comparePre(v.Pre, o.Pre)
}

func cmpUint(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func comparePre(a, b string) int {
	if a == b {
		return 0
	}
	// A version without a pre-release ranks higher than one with a pre-release.
	if a == "" {
		return 1
	}
	if b == "" {
		return -1
	}
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, aErr := strconv.ParseUint(as[i], 10, 64)
		bn, bErr := strconv.ParseUint(bs[i], 10, 64)
		switch {
		case aErr == nil && bErr == nil:
			if c := cmpUint(an, bn); c != 0 {
				return c
			}
		case aErr == nil: // numeric identifiers rank lower than alphanumeric
			return -1
		case bErr == nil:
			return 1
		default:
			if as[i] != bs[i] {
				if as[i] < bs[i] {
					return -1
				}
				return 1
			}
		}
	}
	return cmpUint(uint64(len(as)), uint64(len(bs)))
}

// comparator is a single primitive constraint, e.g. ">= 0.56.0".
type comparator struct {
	op string // one of ">=", ">", "<", "<=", "="
	v  Version
}

func (c comparator) matches(v Version) bool {
	cmp := v.Compare(c.v)
	switch c.op {
	case ">=":
		return cmp >= 0
	case ">":
		return cmp > 0
	case "<":
		return cmp < 0
	case "<=":
		return cmp <= 0
	case "=":
		return cmp == 0
	}
	return false
}

// Requirement is a Cargo version requirement: a set of comparators combined with
// AND. It implements the subset of Cargo's VersionReq grammar that appears in
// practice: caret (the default), tilde, wildcard, '=', and the inequality
// operators, joined by commas.
type Requirement struct {
	comps []comparator
}

// Matches reports whether v satisfies the requirement, applying Cargo's rule
// that a pre-release version only matches when a comparator names the same
// major.minor.patch with its own pre-release tag.
func (r Requirement) Matches(v Version) bool {
	if v.Pre != "" {
		ok := false
		for _, c := range r.comps {
			if c.v.Pre != "" && c.v.Major == v.Major && c.v.Minor == v.Minor && c.v.Patch == v.Patch {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	for _, c := range r.comps {
		if !c.matches(v) {
			return false
		}
	}
	return true
}

// ParseRequirement parses a full Cargo requirement string (comma-separated).
func ParseRequirement(s string) (Requirement, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "*" {
		return Requirement{[]comparator{{">=", Version{}}}}, nil
	}
	var comps []comparator
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		cs, err := parseTerm(part)
		if err != nil {
			return Requirement{}, err
		}
		comps = append(comps, cs...)
	}
	if len(comps) == 0 {
		return Requirement{[]comparator{{">=", Version{}}}}, nil
	}
	return Requirement{comps}, nil
}

func parseTerm(s string) ([]comparator, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "*" {
		return []comparator{{">=", Version{}}}, nil
	}
	op := "^" // Cargo's default operator for a bare version
	switch {
	case strings.HasPrefix(s, ">="):
		op, s = ">=", s[2:]
	case strings.HasPrefix(s, "<="):
		op, s = "<=", s[2:]
	case strings.HasPrefix(s, "^"):
		op, s = "^", s[1:]
	case strings.HasPrefix(s, "~"):
		op, s = "~", s[1:]
	case strings.HasPrefix(s, ">"):
		op, s = ">", s[1:]
	case strings.HasPrefix(s, "<"):
		op, s = "<", s[1:]
	case strings.HasPrefix(s, "="):
		op, s = "=", s[1:]
	}
	s = strings.TrimSpace(s)
	if s == "" || s == "*" {
		return []comparator{{">=", Version{}}}, nil
	}

	major, m, p, count, wild, err := parsePartial(s)
	if err != nil {
		return nil, err
	}
	filled := Version{major, m, p, ""}

	// Wildcards ("1.*", "1.2.*") behave like caret-style ranges regardless of a
	// leading operator, so handle them first.
	if wild {
		switch count {
		case 0:
			return []comparator{{">=", Version{}}}, nil
		case 1:
			return []comparator{{">=", Version{major, 0, 0, ""}}, {"<", Version{major + 1, 0, 0, ""}}}, nil
		default: // count == 2
			return []comparator{{">=", Version{major, m, 0, ""}}, {"<", Version{major, m + 1, 0, ""}}}, nil
		}
	}

	switch op {
	case "^":
		lo, hi := caretRange(major, m, p, count)
		return []comparator{{">=", lo}, {"<", hi}}, nil
	case "~":
		var hi Version
		if count >= 2 {
			hi = Version{major, m + 1, 0, ""}
		} else {
			hi = Version{major + 1, 0, 0, ""}
		}
		return []comparator{{">=", filled}, {"<", hi}}, nil
	case "=":
		switch count {
		case 3:
			return []comparator{{"=", filled}}, nil
		case 2:
			return []comparator{{">=", Version{major, m, 0, ""}}, {"<", Version{major, m + 1, 0, ""}}}, nil
		default:
			return []comparator{{">=", Version{major, 0, 0, ""}}, {"<", Version{major + 1, 0, 0, ""}}}, nil
		}
	case ">=":
		return []comparator{{">=", filled}}, nil
	case "<":
		return []comparator{{"<", filled}}, nil
	case "<=":
		switch count {
		case 3:
			return []comparator{{"<=", filled}}, nil
		case 2:
			return []comparator{{"<", Version{major, m + 1, 0, ""}}}, nil
		default:
			return []comparator{{"<", Version{major + 1, 0, 0, ""}}}, nil
		}
	case ">":
		switch count {
		case 3:
			return []comparator{{">", filled}}, nil
		case 2:
			return []comparator{{">=", Version{major, m + 1, 0, ""}}}, nil
		default:
			return []comparator{{">=", Version{major + 1, 0, 0, ""}}}, nil
		}
	}
	return nil, fmt.Errorf("%w %q", errUnsupportedRequirement, s)
}

// caretRange implements Cargo's caret semantics, where the upper bound is set by
// incrementing the left-most non-zero (or last specified) component.
func caretRange(major, m, p uint64, count int) (lo, hi Version) {
	lo = Version{major, m, p, ""}
	switch {
	case major > 0:
		hi = Version{major + 1, 0, 0, ""}
	case count == 1: // ^0
		hi = Version{1, 0, 0, ""}
	case m > 0: // ^0.m[.p]
		hi = Version{0, m + 1, 0, ""}
	case count == 2: // ^0.0
		hi = Version{0, 1, 0, ""}
	case p > 0: // ^0.0.p
		hi = Version{0, 0, p + 1, ""}
	default: // ^0.0.0
		hi = Version{0, 0, 1, ""}
	}
	return lo, hi
}

// parsePartial parses a possibly-partial version such as "1", "1.2", "1.2.3" or
// a wildcard form "1.*". count is the number of concrete numeric components
// given (before any wildcard); wild reports whether a wildcard was present.
func parsePartial(s string) (major, minor, patch uint64, count int, wild bool, err error) {
	vals := [3]uint64{}
	for i, tok := range strings.Split(s, ".") {
		if i > 2 {
			break
		}
		tok = strings.TrimSpace(tok)
		if tok == "*" || tok == "x" || tok == "X" {
			wild = true
			break
		}
		n, e := strconv.ParseUint(tok, 10, 64)
		if e != nil {
			return 0, 0, 0, 0, false, fmt.Errorf("%w %q", errInvalidComponent, tok)
		}
		vals[i] = n
		count++
	}
	return vals[0], vals[1], vals[2], count, wild, nil
}
