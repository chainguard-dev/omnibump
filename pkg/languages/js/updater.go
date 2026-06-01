/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package js

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var (
	// ErrEmptySelector is returned when an override has no selector.
	ErrEmptySelector = errors.New("override selector is empty")

	// ErrEmptyVersion is returned when an override has no version.
	ErrEmptyVersion = errors.New("override version is empty")

	// ErrOverrideMissing is returned by VerifyOverrides when an override
	// was expected at a JSON path but was absent or had the wrong value.
	ErrOverrideMissing = errors.New("override not present at expected path")
)

// ApplyOverrides reads pkgPath, writes each override under each
// manager's dotted path, and saves the file. sjson mutates the buffer in
// place, preserving the surrounding formatting and key order.
func ApplyOverrides(pkgPath string, managers []Manager, overrides []Override) error {
	data, err := os.ReadFile(filepath.Clean(pkgPath))
	if err != nil {
		return fmt.Errorf("read %s: %w", pkgPath, err)
	}

	updated, err := writeOverrides(data, managers, overrides)
	if err != nil {
		return err
	}

	// Match the existing file's mode rather than imposing a default.
	info, err := os.Stat(pkgPath)
	if err != nil {
		return fmt.Errorf("stat %s: %w", pkgPath, err)
	}

	if err := os.WriteFile(pkgPath, updated, info.Mode().Perm()); err != nil {
		return fmt.Errorf("write %s: %w", pkgPath, err)
	}

	return nil
}

// writeOverrides applies overrides to a package.json buffer in memory.
func writeOverrides(data []byte, managers []Manager, overrides []Override) ([]byte, error) {
	if len(managers) == 0 {
		return nil, fmt.Errorf("%w: no managers selected", ErrUnknownManager)
	}

	out := data
	for _, m := range managers {
		base := m.OverridesPath()
		if base == "" {
			return nil, fmt.Errorf("%w: %q", ErrUnknownManager, m)
		}
		for _, ov := range overrides {
			if ov.Selector == "" {
				return nil, ErrEmptySelector
			}
			if ov.Version == "" {
				return nil, ErrEmptyVersion
			}

			path := sjsonPath(base, ov.Selector)
			updated, err := sjson.SetBytes(out, path, ov.Version)
			if err != nil {
				return nil, fmt.Errorf("set %s: %w", path, err)
			}
			out = updated
		}
	}

	return out, nil
}

// VerifyOverrides reads pkgPath and confirms each override is present
// under each manager's path with the expected value.
func VerifyOverrides(pkgPath string, managers []Manager, overrides []Override) error {
	data, err := os.ReadFile(filepath.Clean(pkgPath))
	if err != nil {
		return fmt.Errorf("read %s: %w", pkgPath, err)
	}

	var missing []string
	for _, m := range managers {
		base := m.OverridesPath()
		if base == "" {
			return fmt.Errorf("%w: %q", ErrUnknownManager, m)
		}
		for _, ov := range overrides {
			path := sjsonPath(base, ov.Selector)
			got := gjson.GetBytes(data, path)
			if !got.Exists() || got.String() != ov.Version {
				missing = append(missing, fmt.Sprintf("%s (want %q, got %q)",
					path, ov.Version, got.String()))
			}
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("%w: %v", ErrOverrideMissing, missing)
	}

	return nil
}

// sjsonPath joins a base path with a selector, escaping any characters
// that sjson and gjson treat specially so the selector becomes a single
// literal key. The special set is `\.*?#|@:` — `.` is the path
// separator, `*?#|@` are query operators, `:` is a string-key prefix.
func sjsonPath(base, selector string) string {
	escaped := make([]byte, 0, len(selector)+4)
	for i := 0; i < len(selector); i++ {
		if isSjsonPathSpecial(selector[i]) {
			escaped = append(escaped, '\\')
		}
		escaped = append(escaped, selector[i])
	}
	return base + "." + string(escaped)
}

func isSjsonPathSpecial(c byte) bool {
	switch c {
	case '\\', '.', '*', '?', '#', '|', '@', ':':
		return true
	}
	return false
}
