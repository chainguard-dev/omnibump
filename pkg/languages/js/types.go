/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package js implements omnibump support for JavaScript projects.
//
// The package manager (pnpm, yarn, npm, bun) is determined from the
// lock file on disk or via --manager. The updater writes each override
// into the field the chosen manager honours:
//
//   - pnpm -> .pnpm.overrides.<selector>
//   - yarn -> .resolutions.<selector>
//   - npm  -> .overrides.<selector>
//   - bun  -> .overrides.<selector>
package js

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrManagerInvalidType is returned when a Managers value in JSON/YAML is
// neither a string scalar nor a list of strings.
var ErrManagerInvalidType = errors.New("manager: expected a string or list of strings")

// Manager identifies a JavaScript package manager whose conventions
// dictate where overrides live in package.json.
type Manager string

const (
	// ManagerPnpm writes to .pnpm.overrides.
	ManagerPnpm Manager = "pnpm"

	// ManagerYarn writes to .resolutions.
	ManagerYarn Manager = "yarn"

	// ManagerNpm writes to .overrides.
	ManagerNpm Manager = "npm"

	// ManagerBun writes to .overrides (bun honours npm's overrides field).
	ManagerBun Manager = "bun"
)

// AllManagers is the canonical list of supported managers.
var AllManagers = []Manager{ManagerPnpm, ManagerYarn, ManagerNpm, ManagerBun}

var validManagers = map[Manager]struct{}{
	ManagerPnpm: {},
	ManagerYarn: {},
	ManagerNpm:  {},
	ManagerBun:  {},
}

// IsKnown reports whether m is a recognised manager identifier.
func (m Manager) IsKnown() bool {
	_, ok := validManagers[m]
	return ok
}

// OverridesPath returns the dotted JSON path at which overrides for the
// given manager live in package.json.
func (m Manager) OverridesPath() string {
	switch m {
	case ManagerPnpm:
		return "pnpm.overrides"
	case ManagerYarn:
		return "resolutions"
	case ManagerNpm, ManagerBun:
		return "overrides"
	}
	return ""
}

// Managers holds one or more Manager identifiers. It accepts either a
// scalar or a sequence in YAML/JSON, which the custom unmarshaller
// normalises to a slice.
type Managers []Manager

// UnmarshalJSON accepts a scalar string or an array of strings.
func (m *Managers) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		*m = nil
		return nil
	}

	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("manager scalar: %w", err)
		}
		*m = Managers{Manager(s)}
		return nil
	}

	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("%w, got %s", ErrManagerInvalidType, trimmed)
	}

	out := make(Managers, len(list))
	for i, s := range list {
		out[i] = Manager(s)
	}
	*m = out
	return nil
}

// Override represents a single dependency override to apply.
type Override struct {
	// Selector is the package.json key used to identify the dependency.
	// May be a bare name ("simple-git"), a scoped name
	// ("@isaacs/brace-expansion") or a name with a version range qualifier
	// ("undici@^6"). It is written into package.json verbatim.
	Selector string

	// Version is the target version string.
	Version string

	// Reason is a free-form note (typically a CVE/GHSA list) recorded in
	// build logs.
	Reason string
}
