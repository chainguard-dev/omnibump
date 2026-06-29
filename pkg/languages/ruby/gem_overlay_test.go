/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package ruby

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/chainguard-dev/omnibump/pkg/languages"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeGemDir creates a temp gem directory with the given gemspec files
// and returns the path. Helper for integration tests.
func makeGemDir(t *testing.T, gemspecs []string) string {
	t.Helper()
	gemDir := t.TempDir()
	specsDir := filepath.Join(gemDir, "specifications")
	require.NoError(t, os.MkdirAll(specsDir, 0o755))
	for _, name := range gemspecs {
		require.NoError(t, os.WriteFile(filepath.Join(specsDir, name), []byte("# gemspec"), 0o644))
	}
	return gemDir
}

// --- parseAndValidateGemSpecs ---

func TestParseAndValidateGemSpecs_Valid(t *testing.T) {
	deps := []languages.Dependency{
		{Name: "rack", Version: "3.1.9"},
		{Name: "actionpack", Version: "7.2.2.1"},
		{Name: "nokogiri", Version: "1.18.3"},
	}

	specs, err := parseAndValidateGemSpecs(deps)
	require.NoError(t, err)
	require.Len(t, specs, 3)

	assert.Equal(t, "rack", specs[0].Name)
	assert.Equal(t, "3.1.9", specs[0].Version)
	assert.Equal(t, "actionpack", specs[1].Name)
	assert.Equal(t, "7.2.2.1", specs[1].Version)
	assert.Equal(t, "nokogiri", specs[2].Name)
	assert.Equal(t, "1.18.3", specs[2].Version)
}

func TestParseAndValidateGemSpecs_HyphenatedNames(t *testing.T) {
	// Real CVE remediation gem names with hyphens
	deps := []languages.Dependency{
		{Name: "net-http", Version: "0.6.0"},
		{Name: "net-imap", Version: "0.5.6"},
		{Name: "ruby-saml", Version: "1.18.0"},
	}

	specs, err := parseAndValidateGemSpecs(deps)
	require.NoError(t, err)
	require.Len(t, specs, 3)
}

func TestParseAndValidateGemSpecs_EmptyVersion(t *testing.T) {
	deps := []languages.Dependency{
		{Name: "rack", Version: ""},
	}

	_, err := parseAndValidateGemSpecs(deps)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrGemDirEmptyVersion)
}

func TestParseAndValidateGemSpecs_DashPrefixInjection(t *testing.T) {
	deps := []languages.Dependency{
		{Name: "--install-dir", Version: "1.0.0"},
	}

	_, err := parseAndValidateGemSpecs(deps)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrGemDirInvalidGemName)
}

func TestParseAndValidateGemSpecs_DashPrefixVersion(t *testing.T) {
	deps := []languages.Dependency{
		{Name: "rack", Version: "-1.0.0"},
	}

	_, err := parseAndValidateGemSpecs(deps)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrGemDirInvalidVersionFormat)
}

func TestParseAndValidateGemSpecs_InvalidChars(t *testing.T) {
	deps := []languages.Dependency{
		{Name: "rack; rm -rf /", Version: "1.0.0"},
	}

	_, err := parseAndValidateGemSpecs(deps)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrGemDirInvalidGemName)
}

func TestParseAndValidateGemSpecs_EmptyDeps(t *testing.T) {
	specs, err := parseAndValidateGemSpecs([]languages.Dependency{})
	require.NoError(t, err)
	assert.Len(t, specs, 0)
}

func TestParseAndValidateGemSpecs_RealCVEGems(t *testing.T) {
	// Real gems from CVE remediation PRs
	deps := []languages.Dependency{
		{Name: "rack", Version: "3.1.9"},
		{Name: "actionpack", Version: "7.2.2.1"},
		{Name: "nokogiri", Version: "1.18.3"},
		{Name: "net-http", Version: "0.6.0"},
		{Name: "rexml", Version: "3.4.1"},
		{Name: "uri", Version: "1.0.3"},
		{Name: "rdoc", Version: "6.12.0"},
	}

	specs, err := parseAndValidateGemSpecs(deps)
	require.NoError(t, err)
	require.Len(t, specs, 7)

	for i, spec := range specs {
		assert.NotEmpty(t, spec.Name, "spec %d has empty name", i)
		assert.NotEmpty(t, spec.Version, "spec %d has empty version", i)
	}
}

// --- isGemVersionLower ---

func TestIsGemVersionLower_BasicOrdering(t *testing.T) {
	tests := []struct {
		v1, v2 string
		want   bool
	}{
		// v1 < v2 → true
		{"1.0.0", "2.0.0", true},
		{"2.0.0", "2.1.0", true},
		{"2.1.0", "2.1.1", true},
		{"3.1.8", "3.1.9", true},
		// v1 >= v2 → false
		{"2.0.0", "1.0.0", false},
		{"3.1.9", "3.1.8", false},
		// v1 == v2 → false
		{"1.0.0", "1.0.0", false},
		{"3.1.9", "3.1.9", false},
	}

	for _, tt := range tests {
		t.Run(tt.v1+"-vs-"+tt.v2, func(t *testing.T) {
			got := isGemVersionLower(tt.v1, tt.v2)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsGemVersionLower_FourSegmentVersions(t *testing.T) {
	// Ruby commonly uses 4-segment versions (e.g. Rails actionpack)
	tests := []struct {
		v1, v2 string
		want   bool
	}{
		{"7.2.2.1", "7.2.2.2", true},
		{"7.2.2.2", "7.2.2.1", false},
		{"7.2.2.1", "7.2.2.1", false},
		// This was broken with lexicographic fallback
		{"7.2.2.9", "7.2.2.10", true},
		{"7.2.2.10", "7.2.2.9", false},
		{"0.6.4.1", "0.6.4.2", true},
	}

	for _, tt := range tests {
		t.Run(tt.v1+"-vs-"+tt.v2, func(t *testing.T) {
			got := isGemVersionLower(tt.v1, tt.v2)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsGemVersionLower_DifferentSegmentCounts(t *testing.T) {
	tests := []struct {
		v1, v2 string
		want   bool
	}{
		// Missing segments treated as 0
		{"1.2", "1.2.0", false},    // equal
		{"1.2.0", "1.2", false},    // equal
		{"1.2", "1.2.1", true},     // 1.2.0 < 1.2.1
		{"1.2.1", "1.2", false},    // 1.2.1 > 1.2.0
		{"3.1", "3.1.9", true},     // 3.1.0 < 3.1.9
		{"7.2.2", "7.2.2.1", true}, // 7.2.2.0 < 7.2.2.1
	}

	for _, tt := range tests {
		t.Run(tt.v1+"-vs-"+tt.v2, func(t *testing.T) {
			got := isGemVersionLower(tt.v1, tt.v2)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsGemVersionLower_PreRelease(t *testing.T) {
	// In RubyGems, non-numeric segments sort before numeric (pre-release < release)
	tests := []struct {
		v1, v2 string
		want   bool
		desc   string
	}{
		{"1.0.alpha", "1.0.0", true, "alpha < release"},
		{"1.0.beta", "1.0.0", true, "beta < release"},
		{"1.0.0", "1.0.alpha", false, "release > alpha"},
		{"1.0.alpha", "1.0.beta", true, "alpha < beta (lexicographic)"},
		{"1.0.rc1", "1.0.0", true, "rc < release"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got := isGemVersionLower(tt.v1, tt.v2)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsGemVersionLower_DowngradeDetection(t *testing.T) {
	tests := []struct {
		current, target string
		isDowngrade     bool
	}{
		{"3.1.9", "3.1.8", true},  // downgrade
		{"3.1.9", "3.1.9", false}, // same version
		{"3.1.9", "3.2.0", false}, // upgrade
		{"7.2.2", "7.1.0", true},  // downgrade
		{"7.2.2", "7.2.2", false}, // same
		{"7.2.2", "7.2.3", false}, // upgrade
		// 4-segment downgrade detection
		{"7.2.2.1", "7.2.2.0", true},  // downgrade
		{"7.2.2.1", "7.2.2.1", false}, // same
		{"7.2.2.1", "7.2.2.2", false}, // upgrade
	}

	for _, tt := range tests {
		t.Run(tt.current+"-to-"+tt.target, func(t *testing.T) {
			// isGemVersionLower(target, current) == true means downgrade
			isDowngrade := isGemVersionLower(tt.target, tt.current)
			assert.Equal(t, tt.isDowngrade, isDowngrade)
		})
	}
}

func TestCompareGemVersions_Equality(t *testing.T) {
	tests := []struct {
		v1, v2 string
	}{
		{"1.0.0", "1.0.0"},
		{"1.2", "1.2.0"},
		{"3.1.9", "3.1.9"},
		{"7.2.2.1", "7.2.2.1"},
	}

	for _, tt := range tests {
		t.Run(tt.v1+"-eq-"+tt.v2, func(t *testing.T) {
			assert.Equal(t, 0, compareGemVersions(tt.v1, tt.v2))
		})
	}
}

// TestCompareGemVersions_RubySpaceship tests cases from Ruby's own
// test_gem_version.rb (test_spaceship method). These are the canonical
// Gem::Version comparison cases. We handle the subset relevant to CVE
// remediation; cases involving version canonicalization (e.g. "0.beta.1"
// == "0.0.beta.1") are noted but skipped.
func TestCompareGemVersions_RubySpaceship(t *testing.T) {
	tests := []struct {
		v1, v2 string
		want   int
		desc   string
	}{
		// From Ruby's test_spaceship
		{"1.0", "1.0.0", 0, "trailing zero padding"},
		{"1.0", "1.0.a", 1, "release > pre-release"},
		{"1.8.2", "0.0.0", 1, "major version wins"},
		{"1.8.2", "1.8.2.a", 1, "release > pre-release suffix"},
		{"1.8.2.b", "1.8.2.a", 1, "pre-release b > a"},
		{"1.8.2.a", "1.8.2", -1, "pre-release < release"},
		// Skipped cases that require Gem::Version canonicalization
		// (splitting "a10" into ["a", 10], or dropping trailing zeros
		// before pre-release segments). Not needed for CVE remediation
		// where versions are always pure numeric segments.
		// {"1.8.2.a10", "1.8.2.a9", 1, "mixed alpha-numeric segment"},
		// {"5.x", "5.0.0.rc2", 1, "x > rc2 (requires canonicalization)"},
		// {"0.beta.1", "0.0.beta.1", 0, "zero-padded pre-release equality"},
		// {"0.0.beta", "0.beta.1", -1, "zero-padded pre-release ordering"},
		{"5.a", "5.0.0.rc2", -1, "a < rc2 (pre-release at same position)"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got := compareGemVersions(tt.v1, tt.v2)
			assert.Equal(t, tt.want, got, "compareGemVersions(%q, %q)", tt.v1, tt.v2)
		})
	}
}

// --- listInstalledGems ---

func TestListInstalledGems_FromSpecifications(t *testing.T) {
	// Create a temp gem dir with specifications/
	gemDir := t.TempDir()
	specsDir := filepath.Join(gemDir, "specifications")
	require.NoError(t, os.MkdirAll(specsDir, 0o755))

	// Create fake gemspec files
	gemspecs := []string{
		"rack-3.1.9.gemspec",
		"actionpack-7.2.2.gemspec",
		"nokogiri-1.18.3.gemspec",
		"net-http-0.6.0.gemspec",
	}
	for _, name := range gemspecs {
		require.NoError(t, os.WriteFile(filepath.Join(specsDir, name), []byte("# gemspec"), 0o644))
	}

	installed, err := listInstalledGems(gemDir)
	require.NoError(t, err)

	assert.Equal(t, "3.1.9", installed["rack"])
	assert.Equal(t, "7.2.2", installed["actionpack"])
	assert.Equal(t, "1.18.3", installed["nokogiri"])
	assert.Equal(t, "0.6.0", installed["net-http"])
}

func TestListInstalledGems_MissingDir(t *testing.T) {
	gemDir := t.TempDir()
	// No specifications/ dir exists

	_, err := listInstalledGems(gemDir)
	assert.Error(t, err)
}

func TestListInstalledGems_EmptyDir(t *testing.T) {
	gemDir := t.TempDir()
	specsDir := filepath.Join(gemDir, "specifications")
	require.NoError(t, os.MkdirAll(specsDir, 0o755))

	installed, err := listInstalledGems(gemDir)
	require.NoError(t, err)
	assert.Empty(t, installed)
}

func TestListInstalledGems_MultipleVersions(t *testing.T) {
	gemDir := t.TempDir()
	specsDir := filepath.Join(gemDir, "specifications")
	require.NoError(t, os.MkdirAll(specsDir, 0o755))

	// Multiple versions of the same gem — highest should win
	gemspecs := []string{
		"rack-3.1.8.gemspec",
		"rack-3.1.9.gemspec",
	}
	for _, name := range gemspecs {
		require.NoError(t, os.WriteFile(filepath.Join(specsDir, name), []byte("# gemspec"), 0o644))
	}

	installed, err := listInstalledGems(gemDir)
	require.NoError(t, err)
	assert.Equal(t, "3.1.9", installed["rack"])
}

// --- gemspecFileRe ---

func TestGemspecFileRe_SimpleName(t *testing.T) {
	matches := gemspecFileRe.FindStringSubmatch("rack-3.1.9.gemspec")
	require.NotNil(t, matches)
	assert.Equal(t, "rack", matches[1])
	assert.Equal(t, "3.1.9", matches[2])
}

func TestGemspecFileRe_HyphenatedName(t *testing.T) {
	matches := gemspecFileRe.FindStringSubmatch("net-http-0.6.0.gemspec")
	require.NotNil(t, matches)
	assert.Equal(t, "net-http", matches[1])
	assert.Equal(t, "0.6.0", matches[2])
}

func TestGemspecFileRe_UnderscoredName(t *testing.T) {
	matches := gemspecFileRe.FindStringSubmatch("ruby_parser-3.21.1.gemspec")
	require.NotNil(t, matches)
	assert.Equal(t, "ruby_parser", matches[1])
	assert.Equal(t, "3.21.1", matches[2])
}

func TestGemspecFileRe_FourSegmentVersion(t *testing.T) {
	matches := gemspecFileRe.FindStringSubmatch("actionpack-7.2.2.1.gemspec")
	require.NotNil(t, matches)
	assert.Equal(t, "actionpack", matches[1])
	assert.Equal(t, "7.2.2.1", matches[2])
}

func TestGemspecFileRe_NonGemspec(t *testing.T) {
	matches := gemspecFileRe.FindStringSubmatch("README.md")
	assert.Nil(t, matches)
}

// --- updateGemDir integration tests ---

func TestUpdateGemDir_DryRun(t *testing.T) {
	gemDir := makeGemDir(t, []string{
		"rack-session-2.1.0.gemspec",
		"erb-6.0.3.gemspec",
		"net-imap-0.6.3.gemspec",
		"concurrent-ruby-1.3.6.gemspec",
	})

	cfg := &languages.UpdateConfig{
		Dependencies: []languages.Dependency{
			{Name: "rack-session", Version: "2.1.2"},
			{Name: "erb", Version: "6.0.4"},
			{Name: "net-imap", Version: "0.6.4.1"},
			{Name: "concurrent-ruby", Version: "1.3.7"},
		},
		Options: map[string]any{},
		DryRun:  true,
	}

	err := updateGemDir(context.Background(), cfg, gemDir)
	require.NoError(t, err)

	// Verify gemspec files were NOT modified (dry run)
	installed, err := listInstalledGems(gemDir)
	require.NoError(t, err)
	assert.Equal(t, "2.1.0", installed["rack-session"])
	assert.Equal(t, "6.0.3", installed["erb"])
}

func TestUpdateGemDir_RejectsDowngrade(t *testing.T) {
	gemDir := makeGemDir(t, []string{
		"rack-session-2.1.2.gemspec",
	})

	cfg := &languages.UpdateConfig{
		Dependencies: []languages.Dependency{
			{Name: "rack-session", Version: "2.1.0"},
		},
		Options: map[string]any{},
	}

	err := updateGemDir(context.Background(), cfg, gemDir)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrGemDirDowngrade)
	assert.Contains(t, err.Error(), "rack-session")
	assert.Contains(t, err.Error(), "2.1.2")
	assert.Contains(t, err.Error(), "2.1.0")
}

func TestUpdateGemDir_MissingDirectory(t *testing.T) {
	cfg := &languages.UpdateConfig{
		Dependencies: []languages.Dependency{
			{Name: "rack", Version: "3.1.9"},
		},
		Options: map[string]any{},
	}

	err := updateGemDir(context.Background(), cfg, "/tmp/nonexistent-gem-dir-test")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrGemDirNotFound)
}

func TestUpdateGemDir_EmptyDeps(t *testing.T) {
	gemDir := makeGemDir(t, []string{"rack-3.1.9.gemspec"})

	cfg := &languages.UpdateConfig{
		Dependencies: []languages.Dependency{},
		Options:      map[string]any{},
	}

	err := updateGemDir(context.Background(), cfg, gemDir)
	require.NoError(t, err)
}

func TestUpdateGemDir_AlreadyAtVersion(t *testing.T) {
	gemDir := makeGemDir(t, []string{"rack-3.1.9.gemspec"})

	cfg := &languages.UpdateConfig{
		Dependencies: []languages.Dependency{
			{Name: "rack", Version: "3.1.9"},
		},
		Options: map[string]any{},
		DryRun:  true,
	}

	// Should succeed (same version is not a downgrade)
	err := updateGemDir(context.Background(), cfg, gemDir)
	require.NoError(t, err)
}

func TestUpdateGemDir_NewGemNotPreviouslyInstalled(t *testing.T) {
	// specifications/ dir exists but doesn't contain the target gem
	gemDir := makeGemDir(t, []string{"rack-3.1.9.gemspec"})

	cfg := &languages.UpdateConfig{
		Dependencies: []languages.Dependency{
			{Name: "rexml", Version: "3.4.1"},
		},
		Options: map[string]any{},
		DryRun:  true,
	}

	// Should succeed — new gem with no prior version is fine
	err := updateGemDir(context.Background(), cfg, gemDir)
	require.NoError(t, err)
}

func TestUpdateGemDir_NoSpecificationsDir(t *testing.T) {
	// gem dir exists but has no specifications/ subdirectory
	gemDir := t.TempDir()

	cfg := &languages.UpdateConfig{
		Dependencies: []languages.Dependency{
			{Name: "rack", Version: "3.1.9"},
		},
		Options: map[string]any{},
		DryRun:  true,
	}

	// Should succeed — missing specifications/ is non-fatal (treated as empty)
	err := updateGemDir(context.Background(), cfg, gemDir)
	require.NoError(t, err)
}

func TestUpdateGemDir_InvalidGemName(t *testing.T) {
	gemDir := makeGemDir(t, []string{})

	cfg := &languages.UpdateConfig{
		Dependencies: []languages.Dependency{
			{Name: "--force", Version: "1.0.0"},
		},
		Options: map[string]any{},
	}

	err := updateGemDir(context.Background(), cfg, gemDir)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrGemDirInvalidGemName)
}

// --- validateGemDir integration tests ---

func TestValidateGemDir_Success(t *testing.T) {
	gemDir := makeGemDir(t, []string{
		"rack-session-2.1.2.gemspec",
		"erb-6.0.4.gemspec",
		"net-imap-0.6.4.1.gemspec",
		"concurrent-ruby-1.3.7.gemspec",
	})

	cfg := &languages.UpdateConfig{
		Dependencies: []languages.Dependency{
			{Name: "rack-session", Version: "2.1.2"},
			{Name: "erb", Version: "6.0.4"},
			{Name: "net-imap", Version: "0.6.4.1"},
			{Name: "concurrent-ruby", Version: "1.3.7"},
		},
		Options: map[string]any{},
	}

	err := validateGemDir(context.Background(), cfg, gemDir)
	require.NoError(t, err)
}

func TestValidateGemDir_VersionMismatch(t *testing.T) {
	gemDir := makeGemDir(t, []string{
		"rack-session-2.1.0.gemspec", // old version still installed
	})

	cfg := &languages.UpdateConfig{
		Dependencies: []languages.Dependency{
			{Name: "rack-session", Version: "2.1.2"}, // expected newer
		},
		Options: map[string]any{},
	}

	err := validateGemDir(context.Background(), cfg, gemDir)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrValidationFailed)
	assert.Contains(t, err.Error(), "rack-session")
	assert.Contains(t, err.Error(), "2.1.2")
	assert.Contains(t, err.Error(), "2.1.0")
}

func TestValidateGemDir_MissingGem(t *testing.T) {
	gemDir := makeGemDir(t, []string{
		"rack-3.1.9.gemspec",
	})

	cfg := &languages.UpdateConfig{
		Dependencies: []languages.Dependency{
			{Name: "rexml", Version: "3.4.1"}, // not installed
		},
		Options: map[string]any{},
	}

	err := validateGemDir(context.Background(), cfg, gemDir)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPackageNotFound)
	assert.Contains(t, err.Error(), "rexml")
}

func TestValidateGemDir_MissingDirectory(t *testing.T) {
	cfg := &languages.UpdateConfig{
		Dependencies: []languages.Dependency{
			{Name: "rack", Version: "3.1.9"},
		},
		Options: map[string]any{},
	}

	err := validateGemDir(context.Background(), cfg, "/tmp/nonexistent-gem-dir-test")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrGemDirNotFound)
}

// --- Ruby.Update / Ruby.Validate gem-dir routing tests ---

func TestRuby_Update_GemDirRouting_DryRun(t *testing.T) {
	gemDir := makeGemDir(t, []string{
		"rack-3.1.8.gemspec",
	})

	r := &Ruby{}
	cfg := &languages.UpdateConfig{
		RootDir: t.TempDir(), // no Gemfile.lock needed in gem-dir mode
		Dependencies: []languages.Dependency{
			{Name: "rack", Version: "3.1.9"},
		},
		Options: map[string]any{
			"gem-dir": gemDir,
		},
		DryRun: true,
	}

	err := r.Update(context.Background(), cfg)
	require.NoError(t, err)

	// Verify it went through gem-dir path (not lockfile path) —
	// if it tried lockfile mode, it would fail with ErrGemfileLockNotFound
	// since RootDir has no Gemfile.lock.
}

func TestRuby_Update_GemDirRouting_MissingDir(t *testing.T) {
	r := &Ruby{}
	cfg := &languages.UpdateConfig{
		RootDir: t.TempDir(),
		Dependencies: []languages.Dependency{
			{Name: "rack", Version: "3.1.9"},
		},
		Options: map[string]any{
			"gem-dir": "/tmp/nonexistent-gem-dir-routing-test",
		},
	}

	err := r.Update(context.Background(), cfg)
	require.Error(t, err)
	// Should be gem-dir error, NOT ErrGemfileLockNotFound
	assert.ErrorIs(t, err, ErrGemDirNotFound)
}

func TestRuby_Validate_GemDirRouting_Success(t *testing.T) {
	gemDir := makeGemDir(t, []string{
		"rack-3.1.9.gemspec",
	})

	r := &Ruby{}
	cfg := &languages.UpdateConfig{
		RootDir: t.TempDir(), // no Gemfile.lock needed in gem-dir mode
		Dependencies: []languages.Dependency{
			{Name: "rack", Version: "3.1.9"},
		},
		Options: map[string]any{
			"gem-dir": gemDir,
		},
	}

	err := r.Validate(context.Background(), cfg)
	require.NoError(t, err)
}

func TestRuby_Validate_GemDirRouting_Mismatch(t *testing.T) {
	gemDir := makeGemDir(t, []string{
		"rack-3.1.8.gemspec",
	})

	r := &Ruby{}
	cfg := &languages.UpdateConfig{
		RootDir: t.TempDir(),
		Dependencies: []languages.Dependency{
			{Name: "rack", Version: "3.1.9"},
		},
		Options: map[string]any{
			"gem-dir": gemDir,
		},
	}

	err := r.Validate(context.Background(), cfg)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrValidationFailed)
}

func TestRuby_Update_NoGemDir_FallsBackToLockfile(t *testing.T) {
	// When gem-dir is NOT set, Update should use the lockfile path.
	// With no Gemfile.lock, it should return ErrGemfileLockNotFound.
	r := &Ruby{}
	cfg := &languages.UpdateConfig{
		RootDir: t.TempDir(),
		Dependencies: []languages.Dependency{
			{Name: "rack", Version: "3.1.9"},
		},
		Options: map[string]any{}, // no gem-dir
	}

	err := r.Update(context.Background(), cfg)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrGemfileLockNotFound)
}
