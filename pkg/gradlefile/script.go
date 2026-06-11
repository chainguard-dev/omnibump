/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradlefile

import (
	"fmt"
	"strings"
)

// scriptBlock is a minimal builder for generated Gradle script fragments.
// Generators describe nested blocks and statements; rendering owns brace
// placement and indentation, so emitting code never hand-manages either.
type scriptBlock struct {
	head  string
	nodes []scriptNode
}

// scriptNode is one entry of a block: either a plain statement line or a
// nested block.
type scriptNode struct {
	line  string
	block *scriptBlock
}

// newScriptBlock starts a top-level block, e.g. newScriptBlock("allprojects").
func newScriptBlock(head string) *scriptBlock {
	return &scriptBlock{head: head}
}

// child appends a nested block and returns it for further building.
func (b *scriptBlock) child(headFormat string, args ...any) *scriptBlock {
	nested := &scriptBlock{head: fmt.Sprintf(headFormat, args...)}
	b.nodes = append(b.nodes, scriptNode{block: nested})
	return nested
}

// stmt appends one statement line to the block.
func (b *scriptBlock) stmt(format string, args ...any) {
	b.nodes = append(b.nodes, scriptNode{line: fmt.Sprintf(format, args...)})
}

// render writes the block tree with four-space indentation per depth.
func (b *scriptBlock) render(w *strings.Builder, depth int) {
	indent := strings.Repeat("    ", depth)
	w.WriteString(indent + b.head + " {\n")
	for _, node := range b.nodes {
		if node.block != nil {
			node.block.render(w, depth+1)
			continue
		}
		w.WriteString(indent + "    " + node.line + "\n")
	}
	w.WriteString(indent + "}\n")
}

// str renders a string literal in the dialect's conventional quoting:
// single quotes for Groovy, double quotes for Kotlin.
func (d DSL) str(value string) string {
	if d == Kotlin {
		return fmt.Sprintf("%q", value)
	}
	return "'" + value + "'"
}
