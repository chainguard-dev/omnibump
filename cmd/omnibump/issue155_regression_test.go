/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package omnibump

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// issue155Goldens are the crate versions omnibump v0.20.1 (the release
// immediately before #155) resolved for the bundled kdash fixture, captured
// 2026-08-07.
//
// Pre-#155, omnibump dragged the whole dependency graph up to the latest
// compatible version. #155 replaced indirect-dependency resolution with a
// reverse-dependency "floor" engine that advances each crate only the minimum
// needed to satisfy the requested CVE bump. The result is that transitive
// crates settle at lower versions than a baseline built with pre-#155 omnibump,
// which reads as a mass component downgrade.
//
// This test drives the same fixture through the current tree via the public CLI
// (New()), so it compiles and runs unchanged on both the v0.20.1 tree and main.
// The versions below are treated as a baseline lower bound: the test fails if a
// crate resolves BELOW them (a regression), and tolerates crates.io publishing
// newer releases in the meantime. On v0.20.1 (and any tree that resolves to
// latest) it PASSES; on a tree with #155's floor resolver it FAILS, printing
// every crate that regressed.
var issue155Goldens = map[string]string{
	"addr2line":      "0.25.1",
	"clap":           "4.5.60",
	"clap_builder":   "4.5.60",
	"color-eyre":     "0.6.5",
	"gimli":          "0.32.3",
	"human-panic":    "2.0.8",
	"libc":           "0.2.189",
	"object":         "0.37.3",
	"owo-colors":     "4.3.0",
	"proc-macro2":    "1.0.107",
	"rustc-demangle": "0.1.28",
	"serde":          "1.0.229",
	"serde_derive":   "1.0.229",
	"uuid":           "1.24.0",
}

// TestIssue155TransitiveDowngrade reproduces chainguard-dev/omnibump#155's
// transitive-downgrade regression end to end. It needs cargo on PATH and egress
// to crates.io (both available in the Go Tests CI job).
func TestIssue155TransitiveDowngrade(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join("testdata", "issue155")
	for _, name := range []string{"Cargo.toml", "Cargo.lock", "deps.yaml"} {
		b, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatalf("read fixture %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.rs"), []byte("fn main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := New()
	cmd.SetArgs([]string{
		"--language", "rust",
		"--deps", filepath.Join(dir, "deps.yaml"),
		"--dir", dir,
		"--log-level", "error",
	})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("omnibump run failed: %v", err)
	}

	got := lockCrateVersions(t, filepath.Join(dir, "Cargo.lock"))

	names := make([]string, 0, len(issue155Goldens))
	for n := range issue155Goldens {
		names = append(names, n)
	}
	sort.Strings(names)

	// Emit a self-contained comparison so the CI log alone proves the point.
	var b strings.Builder
	b.WriteString("\ncrate            pre-#155 (v0.20.1)   this build\n")
	b.WriteString("---------------- -------------------- --------------------\n")
	for _, n := range names {
		fmt.Fprintf(&b, "%-16s %-20s %s\n", n, issue155Goldens[n], strings.Join(got[n], ","))
	}
	t.Log(b.String())

	for _, n := range names {
		have := maxVersion(got[n])
		if !versionGE(have, issue155Goldens[n]) {
			t.Errorf("%s: resolved %q, below the pre-#155 baseline %s — transitive downgrade from #155's floor resolver",
				n, have, issue155Goldens[n])
		}
	}
}

// lockCrateVersions parses a Cargo.lock into crate name -> all locked versions.
// A crate may appear more than once when the graph keeps multiple major lines.
func lockCrateVersions(t *testing.T, path string) map[string][]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read Cargo.lock: %v", err)
	}
	out := map[string][]string{}
	var name string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "name = "):
			name = strings.Trim(strings.TrimPrefix(line, "name = "), `"`)
		case strings.HasPrefix(line, "version = ") && name != "":
			out[name] = append(out[name], strings.Trim(strings.TrimPrefix(line, "version = "), `"`))
			name = ""
		}
	}
	return out
}

// maxVersion returns the highest dotted-numeric version in vs; a Cargo.lock may
// pin several major lines of a crate. Returns "" for an empty slice.
func maxVersion(vs []string) string {
	best := ""
	for _, v := range vs {
		if best == "" || cmpVersion(v, best) > 0 {
			best = v
		}
	}
	return best
}

// versionGE reports whether version a >= b, comparing dotted numeric components.
// Pre-release/build suffixes are ignored: the crates under test use plain semver
// and the comparison only needs to detect a regression below the baseline.
func versionGE(a, b string) bool { return cmpVersion(a, b) >= 0 }

func cmpVersion(a, b string) int {
	as, bs := splitVer(a), splitVer(b)
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y int
		if i < len(as) {
			x = as[i]
		}
		if i < len(bs) {
			y = bs[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func splitVer(v string) []int {
	if i := strings.IndexAny(v, "+-"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
		}
		out = append(out, n)
	}
	return out
}
