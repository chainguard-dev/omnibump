/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package revdep

import (
	"errors"
	"fmt"
	"strings"
)

var (
	errNoRoot        = errors.New("could not find a root crate in the tree output")
	errMultipleRoots = errors.New("multiple inverted-tree roots (crate locked at several versions)")
)

// TreeNode is a node in an inverted dependency tree. In `cargo tree -i` output
// the root is the queried crate and each child is a crate that depends on it.
type TreeNode struct {
	Name     string
	Version  string
	Path     string // local crate directory, when cargo annotates one (path deps)
	Children []*TreeNode
}

// ParseTree parses `cargo tree` output. It supports both the "--prefix depth"
// format (each line prefixed with a numeric depth) and the ASCII box-drawing
// format, so a hand-pasted tree can be fed in as well.
func ParseTree(text string) (*TreeNode, error) {
	var root *TreeNode
	stack := []*TreeNode{}

	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		pl, ok := parseTreeLine(line)
		if !ok {
			continue
		}
		depth := pl.depth
		node := &TreeNode{Name: pl.name, Version: pl.version, Path: pl.path}

		if depth == 0 {
			// A second depth-0 line means cargo emitted more than one inverted tree
			// (the crate is locked at several versions). Refuse it rather than
			// silently keeping only the first; callers must scope the query to a
			// single version.
			if root != nil {
				return nil, fmt.Errorf("%w: %s", errMultipleRoots, node.Name)
			}
			root = node
			stack = []*TreeNode{node}
			continue
		}
		if depth-1 >= len(stack) || stack[depth-1] == nil {
			// Malformed indentation; attach to the nearest known parent.
			if len(stack) == 0 {
				continue
			}
			parent := stack[len(stack)-1]
			parent.Children = append(parent.Children, node)
			continue
		}
		parent := stack[depth-1]
		parent.Children = append(parent.Children, node)
		if depth < len(stack) {
			stack = stack[:depth]
		}
		stack = append(stack, node)
	}

	if root == nil {
		return nil, errNoRoot
	}
	return root, nil
}

// parsedLine is one parsed tree row.
type parsedLine struct {
	depth   int
	name    string
	version string
	path    string
}

// parseTreeLine extracts a parsedLine from a single tree line, handling both the
// numeric-depth prefix and ASCII connectors.
func parseTreeLine(line string) (parsedLine, bool) {
	// "--prefix depth": leading run of digits is the depth.
	if len(line) > 0 && line[0] >= '0' && line[0] <= '9' {
		i := 0
		for i < len(line) && line[i] >= '0' && line[i] <= '9' {
			i++
		}
		pl, ok := parseNameVersion(line[i:])
		if ok {
			pl.depth += parseIntPrefix(line[:i])
		}
		return pl, ok
	}

	// ASCII box-drawing: consume 4-char connector groups.
	depth := 0
	for len(line) >= 4 {
		p := line[:4]
		if p == "|-- " || p == "`-- " || p == "|   " || p == "    " || p == "+-- " {
			depth++
			line = line[4:]
			continue
		}
		break
	}
	pl, ok := parseNameVersion(line)
	if ok {
		pl.depth += depth
	}
	return pl, ok
}

// parseNameVersion pulls "name vX.Y.Z (/path)" from the remainder of a line. The
// optional trailing "(...)" is a local path only when it looks like a filesystem
// path (cargo also prints "(proc-macro)", git URLs, etc., which are ignored).
func parseNameVersion(s string) (parsedLine, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return parsedLine{}, false
	}
	fields := strings.Fields(s)
	if len(fields) < 2 {
		return parsedLine{}, false
	}
	ver := fields[1]
	if !strings.HasPrefix(ver, "v") || len(ver) < 2 || ver[1] < '0' || ver[1] > '9' {
		return parsedLine{}, false
	}
	pl := parsedLine{name: fields[0], version: strings.TrimPrefix(ver, "v")}
	if open := strings.IndexByte(s, '('); open >= 0 {
		if closeIdx := strings.LastIndexByte(s, ')'); closeIdx > open {
			inner := s[open+1 : closeIdx]
			if strings.HasPrefix(inner, "/") || strings.HasPrefix(inner, "./") || strings.HasPrefix(inner, "../") {
				pl.path = inner
			}
		}
	}
	return pl, true
}

func parseIntPrefix(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}
