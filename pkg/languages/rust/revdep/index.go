/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package revdep

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// DefaultIndexURL is the crates.io sparse-index endpoint.
const DefaultIndexURL = "https://index.crates.io/"

// ErrNotFound indicates a crate is absent from the index, which for our purposes
// means it is a local/workspace (path) crate rather than a published one.
var ErrNotFound = errors.New("not found in index")

var (
	errUnexpectedStatus = errors.New("unexpected index status")
	errNoAcceptable     = errors.New("no acceptable published version exists")
	errNoDependency     = errors.New("no dependent version depends on the crate")
	errNoPermitting     = errors.New("no dependent version permits the target")
)

// Fetcher answers the version queries the walk needs. *Client is the live
// crates.io-backed implementation; tests inject a stub.
type Fetcher interface {
	MaxVersion(ctx context.Context, crate string, allowPre bool) (Version, bool, error)
	VersionsAtLeast(ctx context.Context, crate string, floor Version, allowPre bool) ([]Version, error)
	MinVersionRequiring(ctx context.Context, dependent string, floor Version, depCrate string, depFloor Version, acceptable []Version, allowPre bool) (Version, error)
}

// IndexDep mirrors a dependency entry in a crates.io sparse-index record.
type IndexDep struct {
	Name     string `json:"name"`     // the (possibly renamed) dependency name
	Req      string `json:"req"`      // Cargo version requirement
	Kind     string `json:"kind"`     // "normal", "build" or "dev" ("" == normal)
	Optional bool   `json:"optional"` //
	Package  string `json:"package"`  // real crate name when the dep is renamed
}

// crate returns the actual published crate name this dep resolves to.
func (d IndexDep) crate() string {
	if d.Package != "" {
		return d.Package
	}
	return d.Name
}

// IndexVersion mirrors a single line of a sparse-index file.
type IndexVersion struct {
	Name   string     `json:"name"`
	Vers   string     `json:"vers"`
	Deps   []IndexDep `json:"deps"`
	Yanked bool       `json:"yanked"`
}

// Client fetches and caches crate metadata from a crates.io-style sparse index.
type Client struct {
	base  string
	http  *http.Client
	cache map[string][]IndexVersion
}

// NewClient builds a sparse-index client rooted at base. A nil http client
// defaults to a 30s-timeout client.
func NewClient(base string, hc *http.Client) *Client {
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		base:  base,
		http:  hc,
		cache: map[string][]IndexVersion{},
	}
}

// indexPath returns the sparse-index sub-path for a crate name, following the
// standard cargo layout (1/2/3-char names live in dedicated prefixes).
func indexPath(name string) string {
	n := strings.ToLower(name)
	switch len(n) {
	case 0:
		return ""
	case 1:
		return "1/" + n
	case 2:
		return "2/" + n
	case 3:
		return "3/" + n[:1] + "/" + n
	default:
		return n[:2] + "/" + n[2:4] + "/" + n
	}
}

// Fetch returns all published versions of a crate, newest last, cached per name.
func (c *Client) Fetch(ctx context.Context, name string) ([]IndexVersion, error) {
	if v, ok := c.cache[name]; ok {
		return v, nil
	}
	url := c.base + indexPath(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "omnibump")
	resp, err := c.http.Do(req) // #nosec G704 - scheme and host are the constant crates.io index base; only the sharded crate-name path is dynamic
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("crate %q: %w", name, ErrNotFound)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching %s: %w %s", url, errUnexpectedStatus, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var out []IndexVersion
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 1024*1024), 8*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var iv IndexVersion
		if err := json.Unmarshal(line, &iv); err != nil {
			return nil, fmt.Errorf("parsing index line for %s: %w", name, err)
		}
		out = append(out, iv)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	c.cache[name] = out
	return out, nil
}

// MaxVersion returns the highest non-yanked published version of a crate.
// The bool is false when the crate has no usable versions.
func (c *Client) MaxVersion(ctx context.Context, crate string, allowPre bool) (Version, bool, error) {
	versions, err := c.Fetch(ctx, crate)
	if err != nil {
		return Version{}, false, err
	}
	var maxV Version
	found := false
	for _, iv := range versions {
		if iv.Yanked {
			continue
		}
		v, err := ParseVersion(iv.Vers)
		if err != nil {
			continue
		}
		if v.Pre != "" && !allowPre {
			continue
		}
		if !found || v.Compare(maxV) > 0 {
			maxV, found = v, true
		}
	}
	return maxV, found, nil
}

// candidate is a parsed, publishable version of a crate.
type candidate struct {
	ver  Version
	deps []IndexDep
}

// preAllowed reports whether pre-release versions should be considered as
// candidates: either the caller explicitly allowed them, or the floor is itself a
// pre-release. A pre-release floor means the project has already opted this crate
// into its pre-release line, so its pre-release versions are legitimate upgrade
// targets. This matters for families whose dependency link exists only in the
// pre-release line — e.g. rsa depends on crypto-primes only in its 0.10.0-rc.*
// versions, so excluding pre-releases makes the walk conclude, wrongly, that no rsa
// version depends on crypto-primes.
func preAllowed(allowPre bool, floor Version) bool {
	return allowPre || floor.Pre != ""
}

// VersionsAtLeast returns all non-yanked published versions of crate that are
// >= floor, sorted ascending. Pre-releases are excluded unless allowPre is set or
// the floor is itself a pre-release (see preAllowed).
func (c *Client) VersionsAtLeast(ctx context.Context, crate string, floor Version, allowPre bool) ([]Version, error) {
	versions, err := c.Fetch(ctx, crate)
	if err != nil {
		return nil, err
	}
	allowPre = preAllowed(allowPre, floor)
	var out []Version
	for _, iv := range versions {
		if iv.Yanked {
			continue
		}
		v, err := ParseVersion(iv.Vers)
		if err != nil {
			continue
		}
		if v.Pre != "" && !allowPre {
			continue
		}
		if v.Compare(floor) < 0 {
			continue
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Compare(out[j]) < 0 })
	return out, nil
}

// MinVersionRequiring returns the lowest non-yanked version of dependent that is
// >= floor and whose requirement on depCrate can resolve to at least one of the
// acceptable depCrate versions.
//
// This is the core step: given that depCrate must resolve to some version in
// `acceptable` (i.e. >= the child's own minimum), it finds the minimal upgrade
// of the crate that depends on it. A parent qualifies as soon as it *permits* an
// acceptable child version, because Cargo resolves to the highest permitted one.
//
// depFloor is depCrate's target version (the line the bumped instance lives in). It
// disambiguates when a candidate depends on depCrate under multiple renames — see
// the grouping logic below.
func (c *Client) MinVersionRequiring(ctx context.Context, dependent string, floor Version, depCrate string, depFloor Version, acceptable []Version, allowPre bool) (Version, error) {
	versions, err := c.Fetch(ctx, dependent)
	if err != nil {
		return Version{}, err
	}
	if len(acceptable) == 0 {
		return Version{}, fmt.Errorf("%w: %s", errNoAcceptable, depCrate)
	}
	allowPre = preAllowed(allowPre, floor)

	var cands []candidate
	for _, iv := range versions {
		if iv.Yanked {
			continue
		}
		v, err := ParseVersion(iv.Vers)
		if err != nil {
			continue // skip unparseable versions rather than failing the run
		}
		if v.Pre != "" && !allowPre {
			continue
		}
		if v.Compare(floor) < 0 {
			continue
		}
		cands = append(cands, candidate{ver: v, deps: iv.Deps})
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].ver.Compare(cands[j].ver) < 0 })

	sawDep := false
	for _, cand := range cands {
		// Group requirements on depCrate by the dependency's local name. Cargo unifies
		// same-named entries (e.g. one dependency split across [target.'cfg(...)']
		// tables) into a single locked instance, so those must be satisfied together.
		// A renamed entry — a different name with the same package, e.g. combine's
		// `bytes` ^1 and `bytes_05` (package = bytes) ^0.5 — is an independent locked
		// instance and must not be conjoined with the others; only the group governing
		// the instance being bumped needs to permit an acceptable version.
		groups := map[string][]string{}
		var order []string
		for _, d := range cand.deps {
			if d.crate() != depCrate || d.Kind == "dev" { // dev-deps don't propagate downstream
				continue
			}
			if _, ok := groups[d.Name]; !ok {
				order = append(order, d.Name)
			}
			groups[d.Name] = append(groups[d.Name], d.Req)
		}
		if len(groups) == 0 {
			continue // this version doesn't depend on depCrate; not a valid link
		}
		sawDep = true

		// With more than one rename group, each governs an independent locked instance
		// on its own SemVer line, and only the group governing the instance being
		// bumped (depFloor's line) may qualify. Scope the acceptable set to depFloor's
		// line so an unrelated group can't yield a false positive — e.g. combine's
		// `bytes` ^1 must not "permit" the target when the governing group is the
		// renamed `bytes_05` ^0.5. A single group is unambiguous, so the full
		// acceptable set is used, preserving cross-line resolution.
		permit := acceptable
		if len(groups) > 1 {
			permit = versionsInLine(acceptable, depFloor)
		}
		for _, name := range order {
			if permitsAcceptable(groups[name], permit) {
				return cand.ver, nil
			}
		}
	}

	if !sawDep {
		return Version{}, fmt.Errorf("%w: no version of %s >= %s depends on %s", errNoDependency, dependent, floor, depCrate)
	}
	return Version{}, fmt.Errorf("%w: no version of %s >= %s permits an acceptable %s", errNoPermitting, dependent, floor, depCrate)
}

// versionsInLine returns the subset of vers that fall in the same Cargo caret line
// as ref (see sameCaretLine).
func versionsInLine(vers []Version, ref Version) []Version {
	var out []Version
	for _, v := range vers {
		if sameCaretLine(ref, v) {
			out = append(out, v)
		}
	}
	return out
}

// permitsAcceptable reports whether the requirements of a single dependency entry
// (one rename group — several comparators appear when a dep is split across target
// tables) are all satisfied by at least one common acceptable version.
func permitsAcceptable(reqs []string, acceptable []Version) bool {
	for _, av := range acceptable {
		all := true
		for _, rs := range reqs {
			req, err := ParseRequirement(rs)
			if err != nil || !req.Matches(av) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}
