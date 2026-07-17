/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package rust

import (
	"context"
	"sort"
	"strings"

	"github.com/chainguard-dev/clog"
)

// crateFamilies groups crates that are released and versioned together but have no
// machine-readable link in the crate ecosystem, so the relationships are curated
// here. Bumping any member refreshes the other present members within their current
// SemVer line (see refreshFamilies). Each entry is a set — membership, not a
// parent/child hierarchy — so requesting any crate in the set triggers the refresh.
//
// This is a deliberate special case: unlike the resolver-level fixes (rename-group
// handling, pre-release floors) it does not generalize. A newly lockstep-released
// family (a new rustls satellite, rand_distr, …) needs an entry added here — until
// then it simply isn't coordinated (no error, no warning). A future config/env
// override could make this operator-extensible without a code change; the set-based
// lookup below is structured to accept that. Tracked as a follow-up.
var crateFamilies = [][]string{
	{"rand", "rand_core", "rand_chacha"},
	{"rustls", "rustls-webpki", "webpki-roots", "rustls-pemfile"},
}

// familyOf returns the curated family containing crate, or nil if it belongs to
// none. A crate appears in at most one family.
func familyOf(crate string) []string {
	for _, fam := range crateFamilies {
		for _, m := range fam {
			if m == crate {
				return fam
			}
		}
	}
	return nil
}

// refreshFamilies advances the sibling crates of every requested family member to
// the latest version within each sibling's current SemVer line. It coordinates
// crate families (e.g. rand -> rand_core, rand_chacha) that must move together but
// have no ecosystem-defined link.
//
// Only present, curated siblings are touched, and members that are themselves
// explicit targets are skipped so an explicit pin is never overridden.
//
// It cannot break SemVer, so it needs no cargo check: a bare `cargo update -p
// sib@version` (no --precise) only advances a crate within the range its manifest
// constraints already permit — cargo will not cross a caret boundary. Cross-line
// moves still happen solely when cargo's own resolution requires them.
//
// The refresh is best-effort: it is a proactive convenience, not a guarantee. A
// cargo failure is logged, not fatal (a sibling sync must not abort the run), and a
// sibling that cargo leaves in place — e.g. pinned tighter by another dependent —
// is reported per-sibling below rather than silently ignored.
func refreshFamilies(ctx context.Context, cargoRoot string, requested map[string]bool) error {
	log := clog.FromContext(ctx)

	// Which families were touched? Collect their members, skipping explicit targets.
	members := map[string]bool{}
	for name := range requested {
		for _, m := range familyOf(name) {
			if !requested[m] {
				members[m] = true
			}
		}
	}
	if len(members) == 0 {
		return nil
	}

	// Read the lock once (no cargo invocation) to find which members are present and
	// at which versions.
	pkgs, err := GetCurrentPackages(ctx, cargoRoot)
	if err != nil {
		return err
	}
	present := map[string][]string{}
	for _, p := range pkgs {
		if members[p.Name] {
			present[p.Name] = append(present[p.Name], p.Version)
		}
	}

	// Build one spec per locked instance so a crate pinned at multiple lines has each
	// line advanced to its own latest.
	var specs []string
	for name := range members {
		for _, v := range present[name] {
			specs = append(specs, name+"@"+v)
		}
	}
	if len(specs) == 0 {
		return nil
	}
	sort.Strings(specs)

	log.Infof("Refreshing crate families in place: %s", strings.Join(specs, ", "))
	if err := runCargoUpdate(ctx, cargoRoot, specs); err != nil {
		log.Warnf("crate-family refresh failed (%v); leaving siblings unchanged", err)
		return nil
	}

	// Re-read the lock and report each sibling's outcome, so one that cargo could not
	// advance (already latest in its line, or constrained tighter by another
	// dependent) is visible rather than silently left behind.
	after, err := GetCurrentPackages(ctx, cargoRoot)
	if err != nil {
		return err
	}
	post := map[string][]string{}
	for _, p := range after {
		if members[p.Name] {
			post[p.Name] = append(post[p.Name], p.Version)
		}
	}
	for name := range members {
		before, now := sortedJoin(present[name]), sortedJoin(post[name])
		switch before {
		case "": // not present; nothing was refreshed
		case now:
			log.Infof("family sibling %s unchanged at %s (already latest in-line, or constrained by another dependent)", name, now)
		default:
			log.Infof("family sibling %s advanced: %s -> %s", name, before, now)
		}
	}
	return nil
}

// sortedJoin joins versions in a stable order for logging and comparison.
func sortedJoin(vers []string) string {
	s := append([]string(nil), vers...)
	sort.Strings(s)
	return strings.Join(s, ", ")
}
