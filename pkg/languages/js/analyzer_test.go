/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package js

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/chainguard-dev/omnibump/pkg/analyzer"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSAnalyzer_FindsExistingOverrides(t *testing.T) {
	dir := t.TempDir()
	pkgJSON := `{
  "name": "example",
  "version": "1.0.0",
  "pnpm": {
    "overrides": {
      "simple-git": "3.36.0",
      "@isaacs/brace-expansion": "5.0.1"
    }
  }
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkgJSON), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, PnpmLock), []byte(""), 0o600))

	got, err := (&JSAnalyzer{}).Analyze(context.Background(), dir)
	require.NoError(t, err)

	want := &analyzer.AnalysisResult{
		Language: "js",
		Dependencies: map[string]*analyzer.DependencyInfo{
			"pnpm:simple-git": {
				Name:           "simple-git",
				Version:        "3.36.0",
				UpdateStrategy: "override",
				Metadata:       map[string]any{"manager": "pnpm"},
			},
			"pnpm:@isaacs/brace-expansion": {
				Name:           "@isaacs/brace-expansion",
				Version:        "5.0.1",
				UpdateStrategy: "override",
				Metadata:       map[string]any{"manager": "pnpm"},
			},
		},
		Properties:    map[string]string{},
		PropertyUsage: map[string]int{},
		Metadata: map[string]any{
			"totalOverrides": 2,
		},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Analyze mismatch (-want +got):\n%s", diff)
	}
}

func TestJSAnalyzer_NoPackageJSON(t *testing.T) {
	_, err := (&JSAnalyzer{}).Analyze(context.Background(), t.TempDir())
	assert.Error(t, err)
}
