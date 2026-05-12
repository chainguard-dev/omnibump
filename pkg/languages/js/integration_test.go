/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package js_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/chainguard-dev/omnibump/pkg/config"
	"github.com/chainguard-dev/omnibump/pkg/languages/js"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
)

// TestIntegration_Fixtures drives every testdata fixture through the
// same code path the CLI uses: load deps.yaml via pkg/config,
// project it into a languages.UpdateConfig, run JS.Update on a copy of
// package.json.orig, then compare the result byte-for-byte against
// package.json.want.
//
// Adding a fixture means dropping a directory under testdata/ with
// three files; no test code changes are required.
func TestIntegration_Fixtures(t *testing.T) {
	fixtures, err := os.ReadDir("testdata")
	require.NoError(t, err)

	for _, entry := range fixtures {
		if !entry.IsDir() {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			runFixture(t, entry.Name(), false)
		})
	}
}

// TestIntegration_DryRunLeavesFixtureUntouched picks one fixture and
// confirms that --dry-run does not mutate the file at all.
func TestIntegration_DryRunLeavesFixtureUntouched(t *testing.T) {
	runFixture(t, "pnpm", true)
}

func runFixture(t *testing.T, name string, dryRun bool) {
	t.Helper()

	src := filepath.Join("testdata", name)

	origBytes := readFixture(t, src, "package.json.orig")
	depsBytes := readFixture(t, src, "deps.yaml")

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), origBytes, 0o600))
	depsPath := filepath.Join(dir, "deps.yaml")
	require.NoError(t, os.WriteFile(depsPath, depsBytes, 0o600))

	ctx := context.Background()
	cfg, err := config.LoadConfig(ctx, depsPath)
	require.NoError(t, err, "loading deps.yaml")

	updateCfg := cfg.ToUpdateConfig()
	updateCfg.RootDir = dir
	updateCfg.DryRun = dryRun

	require.NoError(t, (&js.JS{}).Update(ctx, updateCfg))
	if !dryRun {
		require.NoError(t, (&js.JS{}).Validate(ctx, updateCfg))
	}

	gotBytes, err := os.ReadFile(filepath.Join(dir, "package.json"))
	require.NoError(t, err)

	var wantName string
	var wantBytes []byte
	if dryRun {
		wantName = "package.json.orig"
		wantBytes = origBytes
	} else {
		wantName = "package.json.want"
		wantBytes = readFixture(t, src, "package.json.want")
	}

	if diff := cmp.Diff(string(wantBytes), string(gotBytes)); diff != "" {
		t.Errorf("package.json mismatch against testdata/%s/%s (-want +got):\n%s",
			name, wantName, diff)
	}
}

func readFixture(t *testing.T, fixtureDir, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtureDir, name))
	require.NoError(t, err, "fixture %s/%s", fixtureDir, name)
	return data
}
