/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package rust

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// Test_indexPath covers cargo's sparse-index directory sharding for the
// different crate-name lengths, plus case normalization.
func Test_indexPath(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "a", want: "1/a"},
		{name: "go", want: "2/go"},
		{name: "rng", want: "3/r/rng"},
		{name: "rand", want: "ra/nd/rand"},
		{name: "serde_json", want: "se/rd/serde_json"},
		{name: "Rand", want: "ra/nd/rand"}, // lowercased
		{name: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, indexPath(tt.name))
		})
	}
}

// Test_latestCompatible covers the pure version-selection logic: bare picks the
// overall highest stable version, a constraint restricts to its caret line and
// floor, and pre-release/below-floor/incompatible versions are excluded.
func Test_latestCompatible(t *testing.T) {
	all := []string{"0.8.5", "0.9.0", "0.9.2", "0.9.4", "0.10.0", "0.10.1"}

	tests := []struct {
		name       string
		versions   []string
		constraint string
		want       string
	}{
		{name: "bare picks overall highest", versions: all, constraint: "", want: "0.10.1"},
		{name: "constraint restricts to caret line", versions: all, constraint: "0.9.0", want: "0.9.4"},
		{name: "constraint enforces floor", versions: all, constraint: "0.9.3", want: "0.9.4"},
		{name: "nothing in line at-or-above floor", versions: []string{"0.9.0", "0.9.2"}, constraint: "0.9.5", want: ""},
		{name: "1.x caret allows minor and patch", versions: []string{"1.0.0", "1.2.0", "2.0.0"}, constraint: "1.0.0", want: "1.2.0"},
		{name: "skips pre-release for bare", versions: []string{"1.0.0", "1.1.0-beta.1"}, constraint: "", want: "1.0.0"},
		{name: "skips pre-release within line", versions: []string{"0.9.0", "0.9.5-rc.1"}, constraint: "0.9.0", want: "0.9.0"},
		{name: "ignores unparseable", versions: []string{"not-semver", "0.9.0"}, constraint: "", want: "0.9.0"},
		{name: "empty input", versions: nil, constraint: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, latestCompatible(tt.versions, tt.constraint))
		})
	}
}

// randIndex is a stub crates.io sparse-index body for "rand": newline-delimited
// JSON with one release per line. 0.10.2 is yanked, so it must be ignored even
// though it would otherwise be the highest version.
const randIndex = `{"name":"rand","vers":"0.8.5","yanked":false}
{"name":"rand","vers":"0.9.0","yanked":false}
{"name":"rand","vers":"0.9.2","yanked":false}
{"name":"rand","vers":"0.9.4","yanked":false}
{"name":"rand","vers":"0.10.0","yanked":false}
{"name":"rand","vers":"0.10.1","yanked":false}
{"name":"rand","vers":"0.10.2","yanked":true}
`

// newIndexStub returns an httptest server serving randIndex at rand's sharded
// path and 404 elsewhere, with cratesIndexBaseURL pointed at it for the test.
func newIndexStub(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ra/nd/rand" {
			_, _ = w.Write([]byte(randIndex))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	orig := cratesIndexBaseURL
	cratesIndexBaseURL = srv.URL
	t.Cleanup(func() { cratesIndexBaseURL = orig })
}

func TestLatestCrateVersion(t *testing.T) {
	newIndexStub(t)

	tests := []struct {
		name    string
		arg     string
		want    string
		wantErr error
	}{
		{name: "bare name -> overall latest stable (yanked 0.10.2 skipped)", arg: "rand", want: "0.10.1"},
		{name: "pinned -> latest in caret line", arg: "rand@0.9.0", want: "0.9.4"},
		{name: "pinned to yanked line still resolves stable in line", arg: "rand@0.10.0", want: "0.10.1"},
		{name: "unknown crate", arg: "definitely-not-a-real-crate", wantErr: ErrCrateNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := LatestCrateVersion(context.Background(), tt.arg)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
