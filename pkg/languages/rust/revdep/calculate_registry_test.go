/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package revdep

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestCalculate_UnchangedDependentStopsPropagation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		target       string
		version      string
		tree         string
		index        map[string][]IndexVersion
		watched      string
		wantRequests int64
		want         *Plan
		wantErr      error
	}{
		{
			name:    "locked reqwest permits Rustls without resolving Git fletch as a registry crate",
			target:  "rustls",
			version: "0.23.46",
			tree: "0rustls v0.23.45\n" +
				"1reqwest v0.12.28\n" +
				"2fletch v2.0.1 (https://github.com/atomdrift-project/fletch.git#0305b8dd)\n" +
				"3atomscan v2.12.1 (/work/scan)\n",
			index: map[string][]IndexVersion{
				"rustls": {
					{Name: "rustls", Vers: "0.23.45"},
					{Name: "rustls", Vers: "0.23.46"},
				},
				"reqwest": {{Name: "reqwest", Vers: "0.12.28", Deps: []IndexDep{
					{Name: "rustls", Req: "^0.23.4", Kind: "normal", Optional: true},
				}}},
				"fletch": {{Name: "fletch", Vers: "0.2.0"}},
			},
			watched: "fletch",
			want:    &Plan{Target: "rustls", From: "0.23.45", To: "0.23.46"},
		},
		{
			name:    "two unchanged reqwest versions ascending do not become an upgrade",
			target:  "rustls",
			version: "0.23.46",
			tree:    "0rustls v0.23.45\n1reqwest v0.12.28\n2fletch v2.0.1 (https://github.com/atomdrift-project/fletch.git#0305b8dd)\n3atomscan v2.12.1 (/work/scan)\n1reqwest v0.13.1\n2cleave v2.12.1 (/work/scan/cleave-backport)\n3atomscan v2.12.1 (/work/scan)\n",
			index: map[string][]IndexVersion{
				"rustls": {
					{Name: "rustls", Vers: "0.23.45"},
					{Name: "rustls", Vers: "0.23.46"},
				},
				"reqwest": {
					{Name: "reqwest", Vers: "0.12.28", Deps: []IndexDep{{Name: "rustls", Req: "^0.23.4", Kind: "normal", Optional: true}}},
					{Name: "reqwest", Vers: "0.13.1", Deps: []IndexDep{{Name: "rustls", Req: "^0.23.4", Kind: "normal", Optional: true}}},
				},
				"fletch": {{Name: "fletch", Vers: "0.2.0"}},
			},
			watched: "fletch",
			want:    &Plan{Target: "rustls", From: "0.23.45", To: "0.23.46"},
		},
		{
			name:    "two unchanged reqwest versions descending do not become an upgrade",
			target:  "rustls",
			version: "0.23.46",
			tree:    "0rustls v0.23.45\n1reqwest v0.13.1\n2cleave v2.12.1 (/work/scan/cleave-backport)\n3atomscan v2.12.1 (/work/scan)\n1reqwest v0.12.28\n2fletch v2.0.1 (https://github.com/atomdrift-project/fletch.git#0305b8dd)\n3atomscan v2.12.1 (/work/scan)\n",
			index: map[string][]IndexVersion{
				"rustls": {
					{Name: "rustls", Vers: "0.23.45"},
					{Name: "rustls", Vers: "0.23.46"},
				},
				"reqwest": {
					{Name: "reqwest", Vers: "0.12.28", Deps: []IndexDep{{Name: "rustls", Req: "^0.23.4", Kind: "normal", Optional: true}}},
					{Name: "reqwest", Vers: "0.13.1", Deps: []IndexDep{{Name: "rustls", Req: "^0.23.4", Kind: "normal", Optional: true}}},
				},
				"fletch": {{Name: "fletch", Vers: "0.2.0"}},
			},
			watched: "fletch",
			want:    &Plan{Target: "rustls", From: "0.23.45", To: "0.23.46"},
		},
		{
			name:    "changed registry dependent still reaches the workspace edit point",
			target:  "leaf",
			version: "2.0.0",
			tree:    "0leaf v1.0.0\n1parent v1.0.0\n2atomscan v2.12.1 (/work/scan)\n",
			index: map[string][]IndexVersion{
				"leaf": {
					{Name: "leaf", Vers: "1.0.0"},
					{Name: "leaf", Vers: "2.0.0"},
				},
				"parent": {
					{Name: "parent", Vers: "1.0.0", Deps: []IndexDep{{Name: "leaf", Req: "^1.0.0"}}},
					{Name: "parent", Vers: "2.0.0", Deps: []IndexDep{{Name: "leaf", Req: "^2.0.0"}}},
				},
			},
			watched:      "parent",
			wantRequests: 1,
			want: &Plan{
				Target: "leaf", From: "1.0.0", To: "2.0.0",
				Edits:      []DirectEdit{{Member: "atomscan", Dependency: "parent", MinVersion: "2.0.0"}},
				Boundaries: []Boundary{{Crate: "parent", From: "1.0.0", To: "2.0.0"}},
			},
		},
		{
			name:    "mixed same-name versions changed first retain the genuine upgrade",
			target:  "leaf",
			version: "2.0.0",
			tree:    "0leaf v1.0.0\n1parent v1.0.0\n2atomscan v2.12.1 (/work/scan)\n1parent v3.0.0\n2git-parent v1.0.0 (https://example.invalid/git-parent#abcdef01)\n3atomscan v2.12.1 (/work/scan)\n",
			index: map[string][]IndexVersion{
				"leaf": {{Name: "leaf", Vers: "1.0.0"}, {Name: "leaf", Vers: "2.0.0"}},
				"parent": {
					{Name: "parent", Vers: "1.0.0", Deps: []IndexDep{{Name: "leaf", Req: "^1.0.0"}}},
					{Name: "parent", Vers: "2.0.0", Deps: []IndexDep{{Name: "leaf", Req: "^2.0.0"}}},
					{Name: "parent", Vers: "3.0.0", Deps: []IndexDep{{Name: "leaf", Req: "^2.0.0"}}},
				},
				"git-parent": {{Name: "git-parent", Vers: "0.1.0"}},
			},
			watched: "git-parent",
			want: &Plan{
				Target: "leaf", From: "1.0.0", To: "2.0.0",
				Edits:      []DirectEdit{{Member: "atomscan", Dependency: "parent", MinVersion: "2.0.0"}},
				Boundaries: []Boundary{{Crate: "parent", From: "1.0.0", To: "2.0.0"}},
			},
		},
		{
			name:    "mixed same-name versions unchanged first retain the genuine upgrade",
			target:  "leaf",
			version: "2.0.0",
			tree:    "0leaf v1.0.0\n1parent v3.0.0\n2git-parent v1.0.0 (https://example.invalid/git-parent#abcdef01)\n3atomscan v2.12.1 (/work/scan)\n1parent v1.0.0\n2atomscan v2.12.1 (/work/scan)\n",
			index: map[string][]IndexVersion{
				"leaf": {{Name: "leaf", Vers: "1.0.0"}, {Name: "leaf", Vers: "2.0.0"}},
				"parent": {
					{Name: "parent", Vers: "1.0.0", Deps: []IndexDep{{Name: "leaf", Req: "^1.0.0"}}},
					{Name: "parent", Vers: "2.0.0", Deps: []IndexDep{{Name: "leaf", Req: "^2.0.0"}}},
					{Name: "parent", Vers: "3.0.0", Deps: []IndexDep{{Name: "leaf", Req: "^2.0.0"}}},
				},
				"git-parent": {{Name: "git-parent", Vers: "0.1.0"}},
			},
			watched: "git-parent",
			want: &Plan{
				Target: "leaf", From: "1.0.0", To: "2.0.0",
				Edits:      []DirectEdit{{Member: "atomscan", Dependency: "parent", MinVersion: "2.0.0"}},
				Boundaries: []Boundary{{Crate: "parent", From: "1.0.0", To: "2.0.0"}},
			},
		},
		{
			name:    "changed dependent retains unsupported Git ancestor refusal",
			target:  "leaf",
			version: "2.0.0",
			tree: "0leaf v1.0.0\n1parent v1.0.0\n" +
				"2git-parent v1.0.0 (https://example.invalid/git-parent#abcdef01)\n" +
				"3atomscan v2.12.1 (/work/scan)\n",
			index: map[string][]IndexVersion{
				"leaf": {
					{Name: "leaf", Vers: "1.0.0"},
					{Name: "leaf", Vers: "2.0.0"},
				},
				"parent": {
					{Name: "parent", Vers: "1.0.0", Deps: []IndexDep{{Name: "leaf", Req: "^1.0.0"}}},
					{Name: "parent", Vers: "2.0.0", Deps: []IndexDep{{Name: "leaf", Req: "^2.0.0"}}},
				},
				"git-parent": {{Name: "git-parent", Vers: "0.1.0"}},
			},
			watched:      "git-parent",
			wantRequests: 1,
			wantErr:      errNoDependency,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			payloads := make(map[string][]byte, len(tc.index))
			for name, versions := range tc.index {
				for _, version := range versions {
					data, err := json.Marshal(version)
					if err != nil {
						t.Fatal(err)
					}
					path := "/" + indexPath(name)
					payloads[path] = append(payloads[path], append(data, '\n')...)
				}
			}
			var watched atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/"+indexPath(tc.watched) {
					watched.Add(1)
				}
				data, ok := payloads[r.URL.Path]
				if !ok {
					http.NotFound(w, r)
					return
				}
				if _, err := w.Write(data); err != nil {
					t.Errorf("write index fixture: %v", err)
				}
			}))
			t.Cleanup(server.Close)

			got, err := Calculate(t.Context(), tc.target, tc.version, Options{
				Tree:             treeOf(tc.tree),
				Index:            NewClient(server.URL, server.Client()),
				WorkspaceMembers: map[string]bool{"atomscan": true},
			})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Calculate error: got = %v, want = %v", err, tc.wantErr)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("Calculate (-want, +got):\n%s", diff)
			}
			if got := watched.Load(); got != tc.wantRequests {
				t.Errorf("index requests for %q: got = %d, want = %d", tc.watched, got, tc.wantRequests)
			}
		})
	}
}
