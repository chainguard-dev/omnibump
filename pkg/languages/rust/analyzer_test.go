/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package rust

import (
	"context"
	"strings"
	"testing"

	"github.com/chainguard-dev/omnibump/pkg/analyzer"
	"github.com/stretchr/testify/require"
)

// analysisWith builds an AnalysisResult from a list of name@version pairs,
// keyed the same way RustAnalyzer.Analyze keys them, so multiple versions of
// the same crate can coexist.
func analysisWith(t *testing.T, pkgs ...analyzer.DependencyInfo) *analyzer.AnalysisResult {
	t.Helper()
	deps := make(map[string]*analyzer.DependencyInfo, len(pkgs))
	for i := range pkgs {
		p := pkgs[i]
		deps[p.Name+"@"+p.Version] = &p
	}
	return &analyzer.AnalysisResult{
		Language:     "rust",
		Dependencies: deps,
	}
}

// hasWarningContaining reports whether any warning contains every given substring.
func hasWarningContaining(warnings []string, subs ...string) bool {
	for _, w := range warnings {
		matched := true
		for _, s := range subs {
			if !strings.Contains(w, s) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// stubResolve returns a version resolver that always yields v, so the strategy
// tests run without touching the network.
func stubResolve(v string) func(context.Context, string) (string, error) {
	return func(context.Context, string) (string, error) { return v, nil }
}

// Test_RecommendStrategy_FromVersion verifies the upgrade-from version is
// recorded in Dependency.Metadata so the analyze output can show "from -> to"
// for a Rust crate (whose analysis map is keyed name@version) instead of
// mislabeling an existing dependency as new.
func Test_RecommendStrategy_FromVersion(t *testing.T) {
	analysis := analysisWith(t,
		analyzer.DependencyInfo{Name: "rand", Version: "0.8.5", UpdateStrategy: "direct"},
		analyzer.DependencyInfo{Name: "rand", Version: "0.9.0", UpdateStrategy: "direct"},
	)

	tests := []struct {
		name     string
		dep      analyzer.Dependency
		wantFrom string
		wantTo   string
	}{
		{
			name:     "version constraint picks the in-line locked version",
			dep:      analyzer.Dependency{Name: "rand", Version: "0.9.0"},
			wantFrom: "0.9.0", // not 0.8.5, which is a different caret line
			wantTo:   "0.9.4",
		},
		{
			name:     "precise pin reports the in-line locked version",
			dep:      analyzer.Dependency{Name: "rand@0.9.0", Version: "0.9.4"},
			wantFrom: "0.9.0",
			wantTo:   "0.9.4",
		},
	}

	ra := &RustAnalyzer{resolveLatest: stubResolve("0.9.4")}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			strategy, err := ra.RecommendStrategy(context.Background(), analysis, []analyzer.Dependency{tt.dep})
			require.NoError(t, err)
			require.Len(t, strategy.DirectUpdates, 1)

			got := strategy.DirectUpdates[0]
			require.Equal(t, "rand", got.Name)
			require.Equal(t, tt.wantTo, got.Version)
			require.Equal(t, tt.wantFrom, got.Metadata[analyzer.FromVersionMetadataKey])
		})
	}
}

// Test_Analyze_DirectIndirect verifies that Analyze classifies each locked
// crate as the project's own crate, a direct dependency (listed in a local
// crate's dependencies), or an indirect/transitive one, using the cargo_v3.lock
// fixture whose root crate "app" directly depends on memchr 1.0.2, regex, and
// regex-syntax 0.5.6.
func Test_Analyze_DirectIndirect(t *testing.T) {
	ra := &RustAnalyzer{}
	result, err := ra.Analyze(context.Background(), "testdata-parser/cargo_v3.lock")
	require.NoError(t, err)

	wantDirect := map[string]bool{
		"memchr@1.0.2":       true,
		"regex@1.7.3":        true,
		"regex-syntax@0.5.6": true,
	}
	wantIndirect := map[string]bool{
		"aho-corasick@0.7.20": true,
		"libc@0.2.140":        true,
		"memchr@2.5.0":        true,
		"regex-syntax@0.6.29": true,
		"ucd-util@0.1.10":     true,
	}

	var direct, indirect, root []string
	for key, info := range result.Dependencies {
		switch {
		case info.Metadata["root"] == true:
			root = append(root, key)
		case info.Transitive:
			indirect = append(indirect, key)
		default:
			direct = append(direct, key)
		}
	}

	require.Equal(t, []string{"app@0.1.0"}, root, "the local crate should be flagged root")
	require.Len(t, direct, len(wantDirect))
	for _, key := range direct {
		require.True(t, wantDirect[key], "unexpected direct dependency %q", key)
	}
	require.Len(t, indirect, len(wantIndirect))
	for _, key := range indirect {
		require.True(t, wantIndirect[key], "unexpected indirect dependency %q", key)
	}

	require.Equal(t, len(wantDirect), result.Metadata["directCount"])
	require.Equal(t, len(wantIndirect), result.Metadata["indirectCount"])
}

// Test_RecommendStrategy_Targets covers how RecommendStrategy resolves the
// different dependency-target forms (bare name, name@version, and the precise
// "name@version" + new version pin) against the analyzed lock graph, when the
// "multiple versions" warning fires, and how a resolver failure is handled.
func Test_RecommendStrategy_Targets(t *testing.T) {
	serdeSingle := analysisWith(t,
		analyzer.DependencyInfo{Name: "serde", Version: "1.0.0", UpdateStrategy: "direct"},
		analyzer.DependencyInfo{Name: "tokio", Version: "1.28.0", UpdateStrategy: "direct"},
	)
	serdeMulti := analysisWith(t,
		analyzer.DependencyInfo{Name: "serde", Version: "1.0.0", UpdateStrategy: "direct"},
		analyzer.DependencyInfo{Name: "serde", Version: "1.1.0", UpdateStrategy: "direct"},
	)

	// resolveErr is a resolver that always fails, simulating a crate missing
	// upstream or a network error.
	resolveErr := func(context.Context, string) (string, error) {
		return "", ErrCrateNotFound
	}

	tests := []struct {
		name           string
		analysis       *analyzer.AnalysisResult
		deps           []analyzer.Dependency
		resolver       func(context.Context, string) (string, error) // nil -> stubResolve
		wantUpdates    int
		wantNotFound   string // substring of a "not found" warning; "" if none expected
		wantMultiple   bool   // expect a "Multiple versions" warning
		wantResolveErr bool   // expect a "Could not resolve" warning
		wantNoWarnings bool
	}{
		{
			name:           "bare name, single version present",
			analysis:       serdeSingle,
			deps:           []analyzer.Dependency{{Name: "serde"}},
			wantUpdates:    1,
			wantNoWarnings: true,
		},
		{
			name:         "bare name, multiple versions present warns",
			analysis:     serdeMulti,
			deps:         []analyzer.Dependency{{Name: "serde"}},
			wantUpdates:  1,
			wantMultiple: true,
		},
		{
			name:           "name@version with multiple present does not warn",
			analysis:       serdeMulti,
			deps:           []analyzer.Dependency{{Name: "serde", Version: "1.2.0"}},
			wantUpdates:    1,
			wantNoWarnings: true,
		},
		{
			name:           "precise pin with multiple present does not warn",
			analysis:       serdeMulti,
			deps:           []analyzer.Dependency{{Name: "serde@1.0.0", Version: "1.1.0"}},
			wantUpdates:    1,
			wantNoWarnings: true,
		},
		{
			name:         "dependency not in lock warns and is skipped",
			analysis:     serdeSingle,
			deps:         []analyzer.Dependency{{Name: "missing-dep", Version: "1.0.0"}},
			wantUpdates:  0,
			wantNotFound: "missing-dep",
		},
		{
			name:     "mix of found and missing",
			analysis: serdeSingle,
			deps: []analyzer.Dependency{
				{Name: "serde", Version: "1.0.1"},
				{Name: "tokio", Version: "1.29.0"},
				{Name: "missing-dep", Version: "1.0.0"},
			},
			wantUpdates:  2,
			wantNotFound: "missing-dep",
		},
		{
			name:           "resolver failure warns and skips, does not abort",
			analysis:       serdeSingle,
			deps:           []analyzer.Dependency{{Name: "serde"}},
			resolver:       resolveErr,
			wantUpdates:    0,
			wantResolveErr: true,
		},
		{
			name:           "no dependencies requested",
			analysis:       serdeSingle,
			deps:           nil,
			wantUpdates:    0,
			wantNoWarnings: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := tt.resolver
			if resolver == nil {
				resolver = stubResolve("9.9.9")
			}
			ra := &RustAnalyzer{resolveLatest: resolver}

			strategy, err := ra.RecommendStrategy(context.Background(), tt.analysis, tt.deps)
			require.NoError(t, err)
			require.NotNil(t, strategy)

			require.Len(t, strategy.DirectUpdates, tt.wantUpdates)

			if tt.wantNoWarnings {
				require.Empty(t, strategy.Warnings)
			}
			if tt.wantNotFound != "" {
				require.True(t, hasWarningContaining(strategy.Warnings, tt.wantNotFound, "not found"),
					"expected a not-found warning for %q, got %v", tt.wantNotFound, strategy.Warnings)
			}
			if tt.wantResolveErr {
				require.True(t, hasWarningContaining(strategy.Warnings, "Could not resolve"),
					"expected a resolver-failure warning, got %v", strategy.Warnings)
			}
			if tt.wantMultiple {
				require.True(t, hasWarningContaining(strategy.Warnings, "Multiple versions"),
					"expected a multiple-versions warning, got %v", strategy.Warnings)
			} else {
				require.False(t, hasWarningContaining(strategy.Warnings, "Multiple versions"),
					"did not expect a multiple-versions warning, got %v", strategy.Warnings)
			}
		})
	}
}
