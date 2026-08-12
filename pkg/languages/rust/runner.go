/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package rust

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"

	"github.com/chainguard-dev/clog"
)

var (
	// ErrInvalidCrateName is returned when a crate name is invalid.
	ErrInvalidCrateName = errors.New("invalid crate name")

	// ErrInvalidVersion is returned when a version string is invalid.
	ErrInvalidVersion = errors.New("invalid version string")

	// crateNameRegex validates Rust crate names.
	// Rust crate names must be alphanumeric, hyphens, or underscores.
	// See: https://doc.rust-lang.org/cargo/reference/manifest.html#the-name-field
	crateNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

	// versionRegex validates semantic version strings.
	// Allows: 1.2.3, 1.2.3-alpha, 1.2.3+build, etc.
	versionRegex = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*(-[a-zA-Z0-9.-]+)?(\+[a-zA-Z0-9.-]+)?$`)
)

// cargoToolchainEnv names the environment variable that overrides the rustup
// toolchain applied to cargo invocations.
const cargoToolchainEnv = "OMNIBUMP_CARGO_TOOLCHAIN"

// cargoToolchain returns the rustup toolchain to pin cargo to, as the bare
// toolchain name (the "+" prefix is added by cargoCommand). Some projects pin an
// old nightly toolchain that lacks features omnibump relies on (notably
// `cargo add`), which fails unless cargo is run against a known-good toolchain.
// The default is "stable"; operators can select a different toolchain, or disable
// the override entirely with an empty value, via OMNIBUMP_CARGO_TOOLCHAIN.
func cargoToolchain() string {
	if tc, ok := os.LookupEnv(cargoToolchainEnv); ok {
		return tc
	}
	return "stable"
}

// toolchainProbe reports whether `cargo +<toolchain>` is accepted by the cargo on
// PATH. Only a rustup proxy (with the toolchain installed) understands the
// `+toolchain` syntax; a plain cargo binary (or a wrapper like cargo auditable) fails with
// "error: no such command: +<toolchain>", and a rustup proxy missing the toolchain
// fails with "toolchain '<toolchain>' is not installed". Overridable in tests.
var toolchainProbe = func(ctx context.Context, toolchain string) bool {
	// Output is discarded (nil Stdout/Stderr); this is a capability check only.
	cmd := exec.CommandContext(ctx, "cargo", "+"+toolchain, "--version") //nolint:gosec // toolchain is from OMNIBUMP_CARGO_TOOLCHAIN or the "stable" default
	return cmd.Run() == nil
}

var (
	toolchainMu   sync.Mutex
	toolchainArg  string
	toolchainDone bool
)

// cargoToolchainArg returns the leading "+<toolchain>" argument to prepend to cargo
// invocations, or "" when no override should be applied. The configured toolchain
// (cargoToolchain) is used only when this cargo actually supports the `+toolchain`
// syntax and that toolchain is available — otherwise cargo would fail with
// "no such command: +<toolchain>". The support check is probed at most once and
// cached for the process.
func cargoToolchainArg(ctx context.Context) string {
	toolchainMu.Lock()
	defer toolchainMu.Unlock()
	if toolchainDone {
		return toolchainArg
	}
	toolchainDone = true

	tc := cargoToolchain()
	if tc == "" {
		return ""
	}
	if !toolchainProbe(ctx, tc) {
		clog.FromContext(ctx).Debugf("cargo does not support the +%s toolchain override; running without it", tc)
		return ""
	}
	toolchainArg = "+" + tc
	return toolchainArg
}

// cargoCommand builds an *exec.Cmd for `cargo [+toolchain] args...` rooted at dir.
// The toolchain override (see cargoToolchainArg) is inserted before the subcommand,
// where rustup expects it, and only when this cargo supports it. All cargo
// invocations in this package go through here so the toolchain is applied
// consistently.
func cargoCommand(ctx context.Context, dir string, args ...string) *exec.Cmd {
	log := clog.FromContext(ctx)

	if arg := cargoToolchainArg(ctx); arg != "" {
		args = append([]string{arg}, args...)
	}

	log.Debugf("Running: cargo %s in %s", strings.Join(args, " "), dir)
	cmd := exec.CommandContext(ctx, "cargo", args...) //nolint:gosec // fixed "cargo" binary; args are cargo specs/flags derived from the lockfile and manifest
	cmd.Dir = dir
	return cmd
}

// featureSelection captures which Cargo features cargo should activate when
// omnibump discovers the dependency graph (cargo metadata / cargo tree). cargo
// resolves only the default feature set unless told otherwise, so a crate
// reachable *only* through a non-default feature is invisible to discovery and its
// pin is silently skipped (AUTO-1142). Discovery therefore runs with the package's
// features: an explicit list when the pipeline threads one, otherwise every
// feature (--all-features) so no feature-gated crate is missed.
type featureSelection struct {
	// features, when non-empty, is passed as `--features a,b,c` — the exact set
	// the melange package builds with. It takes precedence over the all-features
	// default.
	features []string
}

// Cargo feature-selection flags for discovery commands (cargo metadata / cargo
// tree).
const (
	featuresFlag    = "--features"
	allFeaturesFlag = "--all-features"
)

// args renders the cargo feature flags for a discovery command (cargo metadata /
// cargo tree), to be appended after the subcommand and its arguments. An explicit
// feature list is passed verbatim as `--features <list>`; otherwise `--all-features`
// is used as the safe default so discovery sees the whole graph rather than only
// default features.
func (fs featureSelection) args() []string {
	if len(fs.features) > 0 {
		return []string{featuresFlag, strings.Join(fs.features, ",")}
	}
	return []string{allFeaturesFlag}
}

// featureSelectionKey is the context key under which a run's featureSelection is
// carried.
type featureSelectionKey struct{}

// withFeatureSelection returns ctx carrying fs, so the discovery helpers
// (presentVersions, cargoTreeInverted) can read the run's feature set without
// threading it through every intermediate signature — mirroring how the toolchain
// override is applied run-wide. It is set once per run in Rust.Update.
func withFeatureSelection(ctx context.Context, fs featureSelection) context.Context {
	return context.WithValue(ctx, featureSelectionKey{}, fs)
}

// featureSelectionFrom returns the run's featureSelection, or the zero value when
// none was set. The zero value resolves to --all-features (see args), so any code
// path that reaches discovery without an explicit selection still errs on the side
// of seeing the whole graph rather than silently missing feature-gated crates.
func featureSelectionFrom(ctx context.Context) featureSelection {
	if fs, ok := ctx.Value(featureSelectionKey{}).(featureSelection); ok {
		return fs
	}
	return featureSelection{}
}

// resolveFeatureSelection reads the run's Cargo feature selection from the
// language-agnostic Options map (populated from the deps.yaml `features` field and
// the --features CLI flag). A non-empty list is used verbatim; anything else yields
// the zero value, which resolves to --all-features (see featureSelection.args). It
// accepts both []string (CLI / typed config) and []any (generic YAML decode) so the
// same key works whichever path stamped it. Blank entries are dropped.
func resolveFeatureSelection(options map[string]any) featureSelection {
	raw, ok := options["features"]
	if !ok {
		return featureSelection{}
	}
	var list []string
	switch v := raw.(type) {
	case []string:
		list = v
	case []any:
		for _, e := range v {
			if s, ok := e.(string); ok {
				list = append(list, s)
			}
		}
	default:
		return featureSelection{}
	}
	out := make([]string, 0, len(list))
	for _, s := range list {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return featureSelection{features: out}
}

// validateCrateName validates a Rust crate name against the allowed character set.
// Crate names must be alphanumeric, hyphens, or underscores per Cargo spec.
func validateCrateName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: crate name cannot be empty", ErrInvalidCrateName)
	}
	if !crateNameRegex.MatchString(name) {
		return fmt.Errorf("%w: %q (allowed characters: a-zA-Z0-9_-)", ErrInvalidCrateName, name)
	}
	return nil
}

// validateVersion validates that a version string conforms to semantic versioning.
func validateVersion(version string) error {
	if version == "" {
		return fmt.Errorf("%w: version cannot be empty", ErrInvalidVersion)
	}
	if !versionRegex.MatchString(version) {
		return fmt.Errorf("%w: %q (must be valid semver)", ErrInvalidVersion, version)
	}
	return nil
}

// CargoCheck runs `cargo check --workspace --release` to verify the project still
// compiles. It returns the combined output (so compiler errors can be surfaced) and
// an error if the check fails. Used to gate SemVer-breaking upgrades, which can
// leave the project unbuildable.
//
// --release compiles in the release profile so a subsequent `cargo build --release`
// can reuse these artifacts rather than recompiling from scratch.
func CargoCheck(ctx context.Context, cargoRoot string) (string, error) {
	cmd := cargoCommand(ctx, cargoRoot, "check", "--workspace", "--release")
	if bytes, err := cmd.CombinedOutput(); err != nil {
		return strings.TrimSpace(string(bytes)), err
	}
	return "", nil
}

// CargoUpdate runs 'cargo update' to refresh the Cargo.lock file.
// Ported from cargobump/pkg/run/cargo.go.
func CargoUpdate(ctx context.Context, cargoRoot string) (string, error) {
	cmd := cargoCommand(ctx, cargoRoot, "update")
	if bytes, err := cmd.CombinedOutput(); err != nil {
		return strings.TrimSpace(string(bytes)), err
	}
	return "", nil
}

// CargoUpdatePackage updates a specific package to a precise version.
// Uses: cargo update --precise <newVersion> --package <name>@<oldVersion>
// Ported from cargobump/pkg/run/cargo.go.
func CargoUpdatePackage(ctx context.Context, name, oldVersion, newVersion, cargoRoot string) (string, error) {
	// Validate crate name and versions before passing to command.
	if err := validateCrateName(name); err != nil {
		return "", err
	}
	if err := validateVersion(oldVersion); err != nil {
		return "", fmt.Errorf("invalid old version: %w", err)
	}
	if err := validateVersion(newVersion); err != nil {
		return "", fmt.Errorf("invalid new version: %w", err)
	}

	cmd := cargoCommand(ctx, cargoRoot, "update", "--precise", newVersion, "--package", fmt.Sprintf("%s@%s", name, oldVersion))
	if bytes, err := cmd.CombinedOutput(); err != nil {
		return strings.TrimSpace(string(bytes)), err
	}
	return "", nil
}
