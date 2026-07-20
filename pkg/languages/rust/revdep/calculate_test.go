/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package revdep

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// stubFetcher is a programmable Fetcher for offline Calculate tests.
type stubFetcher struct {
	max      map[string]Version   // crate -> highest published version
	atLeast  map[string][]Version // crate -> versions >= floor
	minReq   map[string]Version   // dependent -> min version requiring the parent
	notFound map[string]bool      // crates that 404 in the index (local/private)
}

func (s *stubFetcher) MaxVersion(_ context.Context, crate string, _ bool) (Version, bool, error) {
	if s.notFound[crate] {
		return Version{}, false, fmt.Errorf("crate %q: %w", crate, ErrNotFound)
	}
	v, ok := s.max[crate]
	return v, ok, nil
}

func (s *stubFetcher) VersionsAtLeast(_ context.Context, crate string, _ Version, _ bool) ([]Version, error) {
	if s.notFound[crate] {
		return nil, fmt.Errorf("crate %q: %w", crate, ErrNotFound)
	}
	return s.atLeast[crate], nil
}

func (s *stubFetcher) MinVersionRequiring(_ context.Context, dependent string, _ Version, _ string, _ Version, _ []Version, _ bool) (Version, error) {
	if s.notFound[dependent] {
		return Version{}, fmt.Errorf("crate %q: %w", dependent, ErrNotFound)
	}
	v, ok := s.minReq[dependent]
	if !ok {
		return Version{}, fmt.Errorf("%w: %s", errNoDependency, dependent)
	}
	return v, nil
}

func mustVer(t *testing.T, s string) Version {
	t.Helper()
	v, err := ParseVersion(s)
	require.NoError(t, err)
	return v
}

func treeOf(text string) TreeProvider {
	return func(_ context.Context, _ string) (string, error) { return text, nil }
}

func TestCalculate_DirectTarget(t *testing.T) {
	// Inverted tree for a direct dependency: rand <- demo (workspace member).
	tree := "0rand v0.8.5\n1demo v0.1.0 (/work/demo)\n"
	fetch := &stubFetcher{
		max:      map[string]Version{"rand": mustVer(t, "0.9.4")},
		atLeast:  map[string][]Version{"rand": {mustVer(t, "0.9.0")}},
		notFound: map[string]bool{"demo": true},
	}

	plan, err := Calculate(context.Background(), "rand", "0.9.0", Options{
		Tree:             treeOf(tree),
		Index:            fetch,
		WorkspaceMembers: map[string]bool{"demo": true},
	})
	require.NoError(t, err)
	require.False(t, plan.Empty())
	require.Equal(t, []DirectEdit{{Member: "demo", Dependency: "rand", MinVersion: "0.9.0"}}, plan.Edits)
	require.Equal(t, []Boundary{{Crate: "rand", From: "0.8.5", To: "0.9.0"}}, plan.Boundaries)
}

func TestCalculate_IndirectTargetResolvesViaDirectDep(t *testing.T) {
	// rand_core (indirect CVE target) <- rand (direct dep) <- demo (member).
	// Reaching rand_core 0.9 requires bumping rand to 0.9.
	tree := "0rand_core v0.6.4\n1rand v0.8.5\n2demo v0.1.0 (/work/demo)\n"
	fetch := &stubFetcher{
		max: map[string]Version{"rand_core": mustVer(t, "0.9.3")},
		atLeast: map[string][]Version{
			"rand_core": {mustVer(t, "0.9.0")},
			"rand":      {mustVer(t, "0.9.0")},
		},
		minReq:   map[string]Version{"rand": mustVer(t, "0.9.0")},
		notFound: map[string]bool{"demo": true},
	}

	plan, err := Calculate(context.Background(), "rand_core", "0.9.0", Options{
		Tree:             treeOf(tree),
		Index:            fetch,
		WorkspaceMembers: map[string]bool{"demo": true},
	})
	require.NoError(t, err)
	// The direct dependency to edit is `rand` in member `demo`, bumped to 0.9.
	require.Equal(t, []DirectEdit{{Member: "demo", Dependency: "rand", MinVersion: "0.9.0"}}, plan.Edits)
	require.Equal(t, []Boundary{{Crate: "rand", From: "0.8.5", To: "0.9.0"}}, plan.Boundaries)
}

func TestCalculate_AlreadySatisfiesFloor(t *testing.T) {
	tree := "0rand_core v0.9.5\n1rand v0.9.0\n2demo v0.1.0 (/work/demo)\n"
	plan, err := Calculate(context.Background(), "rand_core", "0.9.0", Options{
		Tree:  treeOf(tree),
		Index: &stubFetcher{}, // never consulted
	})
	require.NoError(t, err)
	require.True(t, plan.Empty())
}

func TestCalculate_NonMemberIndexMissIsError(t *testing.T) {
	// secret_crate 404s in the index but is not a workspace member and has no
	// path annotation: treat as an error, not a spurious local edit.
	tree := "0rand_core v0.6.4\n1secret_crate v1.0.0\n"
	fetch := &stubFetcher{
		max:      map[string]Version{"rand_core": mustVer(t, "0.9.3")},
		atLeast:  map[string][]Version{"rand_core": {mustVer(t, "0.9.0")}},
		notFound: map[string]bool{"secret_crate": true},
	}

	_, err := Calculate(context.Background(), "rand_core", "0.9.0", Options{
		Tree:             treeOf(tree),
		Index:            fetch,
		WorkspaceMembers: map[string]bool{"demo": true},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "secret_crate")
}
