/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package revdep

import (
	"errors"
	"testing"
)

func TestRequirementMatches(t *testing.T) {
	cases := []struct {
		req  string
		ver  string
		want bool
	}{
		// caret on 0.x: minor is the breaking component
		{"^0.47.0", "0.47.0", true},
		{"^0.47.0", "0.47.9", true},
		{"^0.47.0", "0.56.0", false},
		{"^0.56", "0.56.0", true},
		{"^0.56", "0.56.5", true},
		{"^0.56", "0.57.0", false},
		{"^0.55", "0.56.0", false},
		// bare version == caret
		{"0.56.0", "0.56.1", true},
		{"0.56.0", "0.57.0", false},
		// caret on >=1.0
		{"^1.2.3", "1.9.0", true},
		{"^1.2.3", "2.0.0", false},
		{"^1", "1.5.0", true},
		{"^1", "2.0.0", false},
		// caret special zero cases
		{"^0.0.3", "0.0.3", true},
		{"^0.0.3", "0.0.4", false},
		{"^0", "0.9.0", true},
		{"^0", "1.0.0", false},
		// tilde
		{"~1.2.3", "1.2.9", true},
		{"~1.2.3", "1.3.0", false},
		{"~1.2", "1.2.5", true},
		{"~1.2", "1.3.0", false},
		// wildcard
		{"1.*", "1.9.0", true},
		{"1.*", "2.0.0", false},
		{"*", "9.9.9", true},
		// exact
		{"=1.2.3", "1.2.3", true},
		{"=1.2.3", "1.2.4", false},
		// comma AND
		{">=0.47, <0.60", "0.56.0", true},
		{">=0.47, <0.60", "0.60.0", false},
		// inequality
		{">=0.56.0", "0.56.0", true},
		{">=0.56.0", "0.55.0", false},
		// pre-release requirements (e.g. russh's `=0.10.0-rc.18` on rsa)
		{"=0.10.0-rc.18", "0.10.0-rc.18", true},
		{"=0.10.0-rc.18", "0.10.0-rc.16", false},
		{"^0.10.0-rc.10", "0.10.0-rc.18", true}, // rc.18 >= rc.10, same 0.10.0 core
		{"^0.10.0-rc.10", "0.10.0-rc.9", false}, // rc.9 < rc.10
		{"^0.10.0-rc.10", "0.10.0", true},       // stable release satisfies the caret
		{"^0.10.0-rc.10", "0.11.0-rc.1", false}, // different core, outside the caret
		{"^0.7.0-pre.9", "0.7.2", true},         // crypto-primes: stable satisfies the caret
		{">=0.10.0-rc.10", "0.10.0-rc.18", true},
	}
	for _, tc := range cases {
		req, err := ParseRequirement(tc.req)
		if err != nil {
			t.Fatalf("ParseRequirement(%q): %v", tc.req, err)
		}
		v, err := ParseVersion(tc.ver)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", tc.ver, err)
		}
		if got := req.Matches(v); got != tc.want {
			t.Errorf("%q matches %q = %v, want %v", tc.req, tc.ver, got, tc.want)
		}
	}
}

func TestParseTreeASCII(t *testing.T) {
	in := "gix-transport v0.47.0\n" +
		"`-- gix-protocol v0.50.1\n" +
		"    `-- gix v0.72.1\n"
	root, err := ParseTree(in)
	if err != nil {
		t.Fatal(err)
	}
	if root.Name != "gix-transport" || root.Version != "0.47.0" {
		t.Fatalf("root = %+v", root)
	}
	if len(root.Children) != 1 || root.Children[0].Name != "gix-protocol" {
		t.Fatalf("bad child: %+v", root.Children)
	}
	gix := root.Children[0].Children
	if len(gix) != 1 || gix[0].Name != "gix" || gix[0].Version != "0.72.1" {
		t.Fatalf("bad grandchild: %+v", gix)
	}
}

func TestParseTreeMultipleRoots(t *testing.T) {
	// cargo tree -i for a crate locked at two versions emits two depth-0 roots
	// that differ in version. This must be refused, not silently reduced to the
	// first tree.
	in := "0rand v0.7.3\n1foo v1.0.0\n0rand v0.8.5\n1bar v2.0.0\n"
	_, err := ParseTree(in)
	if !errors.Is(err, errMultipleRoots) {
		t.Fatalf("expected errMultipleRoots, got %v", err)
	}
}

func TestParseTreeMultipleRootsSameVersion(t *testing.T) {
	// `cargo tree -i -e normal,build` prints one inverted tree per
	// dependency-kind graph, so a crate reachable both as a normal dependency
	// and through a proc-macro / build-dependency edge yields several depth-0
	// roots for a single version. Modeled on rand@0.8.5 in garage v2.3.0, which
	// is reachable normally and via phf_generator -> phf_macros (proc-macro).
	// These roots must merge into one, not raise errMultipleRoots.
	in := "0rand v0.8.5\n" +
		"1getrandom v0.2.15\n" +
		"0rand v0.8.5\n" +
		"1phf_generator v0.11.3\n" +
		"2phf_macros v0.11.3\n"
	root, err := ParseTree(in)
	if err != nil {
		t.Fatalf("expected merged root, got error: %v", err)
	}
	if root.Name != "rand" || root.Version != "0.8.5" {
		t.Fatalf("root = %+v", root)
	}
	if len(root.Children) != 2 {
		t.Fatalf("expected 2 merged children, got %d: %+v", len(root.Children), root.Children)
	}
	if root.Children[0].Name != "getrandom" || root.Children[1].Name != "phf_generator" {
		t.Fatalf("bad merged children: %+v", root.Children)
	}
	if len(root.Children[1].Children) != 1 || root.Children[1].Children[0].Name != "phf_macros" {
		t.Fatalf("proc-macro subtree not preserved: %+v", root.Children[1])
	}
}

func TestParseTreeMultipleRootsDedupesChildren(t *testing.T) {
	// When the same top-level dependent appears under more than one root (same
	// name and version), the merge keeps a single copy rather than making the
	// calculator walk an identical dependent twice.
	in := "0rand v0.8.5\n" +
		"1shared v1.0.0\n" +
		"0rand v0.8.5\n" +
		"1shared v1.0.0\n" +
		"1unique v2.0.0\n"
	root, err := ParseTree(in)
	if err != nil {
		t.Fatalf("expected merged root, got error: %v", err)
	}
	if len(root.Children) != 2 {
		t.Fatalf("expected 2 deduped children, got %d: %+v", len(root.Children), root.Children)
	}
	if root.Children[0].Name != "shared" || root.Children[1].Name != "unique" {
		t.Fatalf("bad deduped children: %+v", root.Children)
	}
}

func TestParseTreeMultipleRootsMergesSubtrees(t *testing.T) {
	// The same top-level dependent (shared v1.0.0) appears under both roots, but
	// its own descendants differ between the per-edge-kind trees: depA under the
	// first root, depB under the second. Deduping shared to a single copy must
	// union those subtrees, keeping both depA and depB, rather than discarding the
	// second copy's subtree and hiding depB from the calculator's walk.
	in := "0rand v0.8.5\n" +
		"1shared v1.0.0\n" +
		"2depA v1.0.0\n" +
		"0rand v0.8.5\n" +
		"1shared v1.0.0\n" +
		"2depB v2.0.0\n"
	root, err := ParseTree(in)
	if err != nil {
		t.Fatalf("expected merged root, got error: %v", err)
	}
	if len(root.Children) != 1 || root.Children[0].Name != "shared" {
		t.Fatalf("expected single merged child shared, got %+v", root.Children)
	}
	shared := root.Children[0]
	if len(shared.Children) != 2 {
		t.Fatalf("expected 2 unioned grandchildren, got %d: %+v", len(shared.Children), shared.Children)
	}
	if shared.Children[0].Name != "depA" || shared.Children[1].Name != "depB" {
		t.Fatalf("subtrees not unioned: %+v", shared.Children)
	}
}

func TestParseTreeDepthPrefix(t *testing.T) {
	// This is the format `cargo tree -i --prefix depth` actually emits.
	in := "0gix-transport v0.47.0\n" +
		"1gix-protocol v0.50.1\n" +
		"2gix v0.72.1\n"
	root, err := ParseTree(in)
	if err != nil {
		t.Fatal(err)
	}
	if root.Name != "gix-transport" {
		t.Fatalf("root = %+v", root)
	}
	if len(root.Children) != 1 || root.Children[0].Name != "gix-protocol" {
		t.Fatalf("bad child: %+v", root.Children)
	}
	gc := root.Children[0].Children
	if len(gc) != 1 || gc[0].Name != "gix" {
		t.Fatalf("bad grandchild: %+v", gc)
	}
}
