/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package languages

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubLanguage is a minimal Language implementation for testing detection.
type stubLanguage struct {
	name      string
	manifests []string
	detectFn  func(ctx context.Context, dir string) (bool, error)
}

func (s *stubLanguage) Name() string               { return s.name }
func (s *stubLanguage) GetManifestFiles() []string { return s.manifests }
func (s *stubLanguage) SupportsAnalysis() bool     { return false }

func (s *stubLanguage) Update(_ context.Context, _ *UpdateConfig) error   { return nil }
func (s *stubLanguage) Validate(_ context.Context, _ *UpdateConfig) error { return nil }

func (s *stubLanguage) Detect(ctx context.Context, dir string) (bool, error) {
	return s.detectFn(ctx, dir)
}

// withCleanRegistry replaces the global registry for the duration of a test,
// restoring it when the test completes.
func withCleanRegistry(t *testing.T) {
	t.Helper()
	mu.Lock()
	old := registry
	registry = make(map[string]Language)
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		registry = old
		mu.Unlock()
	})
}

func TestDetectLanguage_SingleMatch(t *testing.T) {
	withCleanRegistry(t)

	tmpDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module test"), 0o600))

	Register(&stubLanguage{
		name:      "go",
		manifests: []string{"go.mod"},
		detectFn: func(_ context.Context, dir string) (bool, error) {
			_, err := os.Stat(filepath.Join(dir, "go.mod"))
			return err == nil, nil
		},
	})

	lang, err := DetectLanguage(context.Background(), tmpDir)
	require.NoError(t, err)
	assert.Equal(t, "go", lang)
}

func TestDetectLanguage_PrefersRootManifest(t *testing.T) {
	withCleanRegistry(t)

	tmpDir := t.TempDir()

	// Root has go.mod
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module test"), 0o600))

	// Subdirectory has pom.xml (simulates testdata fixtures)
	subDir := filepath.Join(tmpDir, "testdata", "maven")
	require.NoError(t, os.MkdirAll(subDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "pom.xml"), []byte("<project/>"), 0o600))

	Register(&stubLanguage{
		name:      "go",
		manifests: []string{"go.mod"},
		detectFn: func(_ context.Context, dir string) (bool, error) {
			_, err := os.Stat(filepath.Join(dir, "go.mod"))
			return err == nil, nil
		},
	})

	Register(&stubLanguage{
		name:      "java",
		manifests: []string{"pom.xml"},
		detectFn: func(_ context.Context, dir string) (bool, error) {
			// Simulates Maven's recursive detection — finds pom.xml in subdirectories
			found := false
			_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if info.Name() == "pom.xml" {
					found = true
				}
				return nil
			})
			return found, nil
		},
	})

	// Run detection multiple times to verify determinism.
	for range 20 {
		lang, err := DetectLanguage(context.Background(), tmpDir)
		require.NoError(t, err, "should not error when root manifest disambiguates")
		assert.Equal(t, "go", lang, "should prefer go (root manifest) over java (subdirectory only)")
	}
}

func TestDetectLanguage_MultipleRootManifests_Deterministic(t *testing.T) {
	withCleanRegistry(t)

	tmpDir := t.TempDir()

	// Both manifests in root
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module test"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "Cargo.lock"), []byte(""), 0o600))

	Register(&stubLanguage{
		name:      "rust",
		manifests: []string{"Cargo.toml", "Cargo.lock"},
		detectFn: func(_ context.Context, dir string) (bool, error) {
			_, err := os.Stat(filepath.Join(dir, "Cargo.lock"))
			return err == nil, nil
		},
	})

	Register(&stubLanguage{
		name:      "go",
		manifests: []string{"go.mod"},
		detectFn: func(_ context.Context, dir string) (bool, error) {
			_, err := os.Stat(filepath.Join(dir, "go.mod"))
			return err == nil, nil
		},
	})

	// Should always pick "go" (alphabetically first) and warn.
	for range 20 {
		lang, err := DetectLanguage(context.Background(), tmpDir)
		require.Error(t, err, "should warn about ambiguity")
		assert.Contains(t, err.Error(), "multiple languages detected")
		assert.Equal(t, "go", lang, "should deterministically pick alphabetically first")
	}
}

func TestDetectLanguage_NoMatch(t *testing.T) {
	withCleanRegistry(t)

	tmpDir := t.TempDir()

	Register(&stubLanguage{
		name:      "go",
		manifests: []string{"go.mod"},
		detectFn:  func(_ context.Context, _ string) (bool, error) { return false, nil },
	})

	lang, err := DetectLanguage(context.Background(), tmpDir)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoLanguageDetected)
	assert.Empty(t, lang)
}

func TestDetectLanguages_ReturnsSorted(t *testing.T) {
	withCleanRegistry(t)

	tmpDir := t.TempDir()

	for _, name := range []string{"rust", "go", "java"} {
		n := name
		Register(&stubLanguage{
			name:     n,
			detectFn: func(_ context.Context, _ string) (bool, error) { return true, nil },
		})
	}

	for range 20 {
		langs, err := DetectLanguages(context.Background(), tmpDir)
		require.NoError(t, err)
		assert.Equal(t, []string{"go", "java", "rust"}, langs, "should return sorted")
	}
}
