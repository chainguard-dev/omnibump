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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectManagerFromLocks_SingleLockFile(t *testing.T) {
	tests := []struct {
		lockFile string
		want     Manager
	}{
		{PnpmLock, ManagerPnpm},
		{YarnLock, ManagerYarn},
		{NpmLock, ManagerNpm},
		{BunLock, ManagerBun},
		{BunLockBinary, ManagerBun},
	}

	for _, tt := range tests {
		t.Run(tt.lockFile, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, tt.lockFile), []byte(""), 0o600))

			got, err := DetectManagerFromLocks(context.Background(), dir)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDetectManagerFromLocks_BunBothFormatsNotAmbiguous(t *testing.T) {
	// bun ships projects sometimes with both bun.lock (text) and the
	// legacy bun.lockb (binary). They imply the same manager and should
	// not be flagged as ambiguous.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, BunLock), []byte(""), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, BunLockBinary), []byte(""), 0o600))

	got, err := DetectManagerFromLocks(context.Background(), dir)
	require.NoError(t, err)
	assert.Equal(t, ManagerBun, got)
}

func TestDetectManagerFromLocks_NoLockFile(t *testing.T) {
	_, err := DetectManagerFromLocks(context.Background(), t.TempDir())
	assert.ErrorIs(t, err, ErrNoManagerDetected)
	assert.ErrorContains(t, err, "--manager")
}

func TestDetectManagerFromLocks_Ambiguous(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, PnpmLock), []byte(""), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, YarnLock), []byte(""), 0o600))

	_, err := DetectManagerFromLocks(context.Background(), dir)
	assert.ErrorIs(t, err, ErrAmbiguousManager)
	assert.ErrorContains(t, err, "--manager")
}

func TestSelectManagers_ExplicitOverrideWins(t *testing.T) {
	// Lock file says pnpm, but the caller asks for yarn — they win.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, PnpmLock), []byte(""), 0o600))

	got, err := SelectManagers(context.Background(), dir, []Manager{ManagerYarn})
	require.NoError(t, err)
	assert.Equal(t, []Manager{ManagerYarn}, got)
}

func TestSelectManagers_MultipleExplicit(t *testing.T) {
	// No lock file present — explicit list is the only signal.
	got, err := SelectManagers(context.Background(), t.TempDir(),
		[]Manager{ManagerPnpm, ManagerYarn})
	require.NoError(t, err)
	assert.Equal(t, []Manager{ManagerPnpm, ManagerYarn}, got)
}

func TestSelectManagers_RejectsUnknownOverride(t *testing.T) {
	_, err := SelectManagers(context.Background(), t.TempDir(),
		[]Manager{ManagerPnpm, Manager("bower")})
	assert.ErrorIs(t, err, ErrUnknownManager)
}

func TestSelectManagers_EmptyOverrideDelegatesToDetection(t *testing.T) {
	// Nil override must fall through to lock-file detection (here, error).
	_, err := SelectManagers(context.Background(), t.TempDir(), nil)
	assert.ErrorIs(t, err, ErrNoManagerDetected)
}

func TestManager_OverridesPath(t *testing.T) {
	tests := []struct {
		manager Manager
		want    string
	}{
		{ManagerPnpm, "pnpm.overrides"},
		{ManagerYarn, "resolutions"},
		{ManagerNpm, "overrides"},
		{ManagerBun, "overrides"},
		{Manager("nonsense"), ""},
	}

	for _, tt := range tests {
		t.Run(string(tt.manager), func(t *testing.T) {
			assert.Equal(t, tt.want, tt.manager.OverridesPath())
		})
	}
}
