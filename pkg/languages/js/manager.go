/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package js

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chainguard-dev/clog"
)

// Lock file names recognised for manager detection.
const (
	PnpmLock      = "pnpm-lock.yaml"
	YarnLock      = "yarn.lock"
	NpmLock       = "package-lock.json"
	BunLock       = "bun.lock"
	BunLockBinary = "bun.lockb"
)

var (
	// ErrUnknownManager is returned when an explicit manager string does
	// not match any recognised manager.
	ErrUnknownManager = errors.New("unknown package manager")

	// ErrNoManagerDetected is returned when no lock file is present and
	// no explicit manager was supplied.
	ErrNoManagerDetected = errors.New("could not detect package manager")

	// ErrAmbiguousManager is returned when multiple lock files are present
	// and no explicit manager was supplied.
	ErrAmbiguousManager = errors.New("multiple lock files present, package manager is ambiguous")
)

// lockFileManager maps each recognised lock file to the manager it implies.
var lockFileManager = []struct {
	file    string
	manager Manager
}{
	{PnpmLock, ManagerPnpm},
	{YarnLock, ManagerYarn},
	{NpmLock, ManagerNpm},
	{BunLock, ManagerBun},
	{BunLockBinary, ManagerBun},
}

// SelectManagers resolves the manager(s) to use for a project rooted at
// dir. A non-empty override list always wins; each entry must be a known
// manager. With no override list, exactly one recognised lock file must
// be present.
func SelectManagers(ctx context.Context, dir string, overrides []Manager) ([]Manager, error) {
	log := clog.FromContext(ctx)

	if len(overrides) > 0 {
		for _, m := range overrides {
			if !m.IsKnown() {
				return nil, fmt.Errorf("%w: %q (expected one of %v)", ErrUnknownManager, m, AllManagers)
			}
		}
		log.Debugf("Using explicit managers %v", overrides)
		return overrides, nil
	}

	mgr, err := DetectManagerFromLocks(ctx, dir)
	if err != nil {
		return nil, err
	}
	return []Manager{mgr}, nil
}

// DetectManagerFromLocks inspects dir for recognised lock files and returns the
// implied manager. Returns ErrNoManagerDetected when none are found and
// ErrAmbiguousManager when more than one is found. Two lock files that imply
// the same manager (e.g. bun.lock and bun.lockb) are treated as a single signal
// and are not ambiguous.
func DetectManagerFromLocks(ctx context.Context, dir string) (Manager, error) {
	log := clog.FromContext(ctx)

	var (
		found    []Manager
		foundRaw []string
	)
	seen := make(map[Manager]bool)

	for _, lf := range lockFileManager {
		_, err := os.Stat(filepath.Join(dir, lf.file))
		if err != nil {
			continue
		}

		log.Debugf("Found lock file %s -> %s", lf.file, lf.manager)
		foundRaw = append(foundRaw, lf.file)
		if !seen[lf.manager] {
			seen[lf.manager] = true
			found = append(found, lf.manager)
		}
	}

	switch len(found) {
	case 0:
		return "", fmt.Errorf("%w in %s: pass --manager <%s>",
			ErrNoManagerDetected, dir, joinManagers(AllManagers, "|"))
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("%w in %s (found %s): pass --manager to disambiguate",
			ErrAmbiguousManager, dir, strings.Join(foundRaw, ", "))
	}
}

func joinManagers(ms []Manager, sep string) string {
	s := make([]string, len(ms))
	for i, m := range ms {
		s[i] = string(m)
	}
	return strings.Join(s, sep)
}
