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
	// cargo tree -i for a crate locked at two versions emits two depth-0 roots.
	// This must be refused, not silently reduced to the first tree.
	in := "0rand v0.7.3\n1foo v1.0.0\n0rand v0.8.5\n1bar v2.0.0\n"
	_, err := ParseTree(in)
	if !errors.Is(err, errMultipleRoots) {
		t.Fatalf("expected errMultipleRoots, got %v", err)
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
