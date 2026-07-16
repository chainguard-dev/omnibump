/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package revdep

import (
	"context"
	"errors"
	"testing"
)

// preload seeds a Client's cache so MinVersionRequiring runs offline.
func preload(versions ...IndexVersion) *Client {
	c := NewClient(DefaultIndexURL, nil)
	if len(versions) > 0 {
		c.cache[versions[0].Name] = versions
	}
	return c
}

// Test_MinVersionRequiring_preReleaseFloor guards the yazi regression: rsa depends
// on crypto-primes only in its pre-release (0.10.0-rc.*) versions; the stable
// 0.9.10 does not. With the crate locked at an rc (a pre-release floor), those rc
// candidates must be considered, or the walk wrongly concludes no rsa version
// depends on crypto-primes. crypto-primes has stable 0.7.x, which rc.18's `^0.7`
// requirement permits.
func Test_MinVersionRequiring_preReleaseFloor(t *testing.T) {
	c := preload(
		IndexVersion{Name: "rsa", Vers: "0.9.10"}, // stable, no crypto-primes dep
		IndexVersion{Name: "rsa", Vers: "0.10.0-rc.16", Deps: []IndexDep{{Name: "crypto-primes", Req: "^0.7.0-pre.9", Kind: "normal"}}},
		IndexVersion{Name: "rsa", Vers: "0.10.0-rc.18", Deps: []IndexDep{{Name: "crypto-primes", Req: "^0.7", Kind: "normal"}}},
	)

	acceptable := []Version{{Major: 0, Minor: 7, Patch: 0}, {Major: 0, Minor: 7, Patch: 2}}
	// allowPre is false, but the floor is a pre-release, so rc candidates must count.
	got, err := c.MinVersionRequiring(context.Background(), "rsa", Version{Major: 0, Minor: 10, Patch: 0, Pre: "rc.18"}, "crypto-primes", acceptable, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.String() != "0.10.0-rc.18" {
		t.Errorf("got %s, want 0.10.0-rc.18", got)
	}
}

// Test_MinVersionRequiring_preReleaseRequirement guards the yazi russh regression:
// russh 0.61.2 requires rsa `=0.10.0-rc.18` (a pre-release version requirement).
// The requirement parser must handle the pre-release tag, or the requirement is
// treated as unsatisfiable and the walk aborts with "no version of russh permits
// an acceptable rsa".
func Test_MinVersionRequiring_preReleaseRequirement(t *testing.T) {
	c := preload(
		IndexVersion{Name: "russh", Vers: "0.60.2", Deps: []IndexDep{{Name: "rsa", Req: "=0.10.0-rc.16", Kind: "normal", Optional: true}}},
		IndexVersion{Name: "russh", Vers: "0.61.2", Deps: []IndexDep{{Name: "rsa", Req: "=0.10.0-rc.18", Kind: "normal", Optional: true}}},
	)

	acceptable := []Version{{Major: 0, Minor: 10, Patch: 0, Pre: "rc.18"}}
	got, err := c.MinVersionRequiring(context.Background(), "russh", Version{Major: 0, Minor: 61, Patch: 2}, "rsa", acceptable, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.String() != "0.61.2" {
		t.Errorf("got %s, want 0.61.2 (russh 0.61.2's =0.10.0-rc.18 permits rsa rc.18)", got)
	}
}

// Test_MinVersionRequiring_renamedDependency guards the sccache regression: combine
// 4.6.6 declares two dependencies that both resolve to crate bytes — `bytes` (^1)
// and the renamed `bytes_05` (package = bytes, ^0.5). They are independent locked
// instances, so bumping the 1.x bytes must be judged against the ^1 entry alone.
// Conjoining ^1 and ^0.5 (no single version satisfies both) wrongly reports that no
// combine version permits the target.
func Test_MinVersionRequiring_renamedDependency(t *testing.T) {
	c := preload(IndexVersion{
		Name: "combine", Vers: "4.6.6",
		Deps: []IndexDep{
			{Name: "bytes", Package: "bytes", Req: "^1", Kind: "normal", Optional: true},
			{Name: "bytes", Package: "bytes", Req: "^1", Kind: "dev"},
			{Name: "bytes_05", Package: "bytes", Req: "^0.5", Kind: "normal", Optional: true},
			{Name: "bytes_05", Package: "bytes", Req: "^0.5", Kind: "dev"},
		},
	})

	acceptable := []Version{{Major: 1, Minor: 11, Patch: 1}, {Major: 1, Minor: 12, Patch: 1}}
	got, err := c.MinVersionRequiring(context.Background(), "combine", Version{Major: 4, Minor: 6, Patch: 6}, "bytes", acceptable, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.String() != "4.6.6" {
		t.Errorf("got %s, want 4.6.6 (combine 4.6.6 permits bytes 1.x via its ^1 entry)", got)
	}
}

// Test_MinVersionRequiring_sameNameConjoined guards that same-named entries (one
// dependency split across target tables) are still ANDed: a version that satisfies
// only one of them must not qualify.
func Test_MinVersionRequiring_sameNameConjoined(t *testing.T) {
	c := preload(IndexVersion{
		Name: "dependent", Vers: "1.0.0",
		Deps: []IndexDep{
			{Name: "child", Req: ">=1.1", Kind: "normal"},
			{Name: "child", Req: "<1.2", Kind: "normal"},
		},
	})

	// 1.5.0 satisfies >=1.1 but not <1.2, so the conjoined per-target requirement is
	// unsatisfiable by the only acceptable version.
	_, err := c.MinVersionRequiring(context.Background(), "dependent", Version{Major: 1}, "child", []Version{{Major: 1, Minor: 5}}, false)
	if !errors.Is(err, errNoPermitting) {
		t.Fatalf("expected errNoPermitting for unsatisfiable conjoined per-target reqs, got %v", err)
	}
}
