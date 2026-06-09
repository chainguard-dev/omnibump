/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package pathutil

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidatePathWithinRoot_Inside(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	insidePath := filepath.Join(root, "module", "file.txt")
	outsidePath := filepath.Join(dir, "outside", "file.txt")

	writeFile(t, insidePath, "inside")
	writeFile(t, outsidePath, "outside")

	if err := ValidatePathWithinRoot(root, insidePath); err != nil {
		t.Fatalf("ValidatePathWithinRoot() inside path error = %v", err)
	}
	if err := ValidatePathWithinRoot(root, outsidePath); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("ValidatePathWithinRoot() outside path error = %v, want ErrUnsafePath", err)
	}
}

func TestValidatePathWithinRoot_RejectsSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	outsidePath := filepath.Join(dir, "outside", "file.txt")
	linkPath := filepath.Join(root, "linked-file.txt")

	writeFile(t, outsidePath, "outside")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", root, err)
	}
	if err := os.Symlink(outsidePath, linkPath); err != nil {
		t.Fatalf("Symlink(%s, %s): %v", outsidePath, linkPath, err)
	}

	if err := ValidatePathWithinRoot(root, linkPath); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("ValidatePathWithinRoot() symlink escape error = %v, want ErrUnsafePath", err)
	}
}

func TestValidatePathWithinRoot_RootItself(t *testing.T) {
	root := t.TempDir()
	if err := ValidatePathWithinRoot(root, root); err != nil {
		t.Fatalf("ValidatePathWithinRoot() root itself error = %v", err)
	}
}

func TestValidatePathWithinRoot_DotDot(t *testing.T) {
	// Both root and the escape target must exist so EvalSymlinks can resolve them.
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	escape := filepath.Join(parent, "escape")
	for _, d := range []string{root, escape} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", d, err)
		}
	}
	// Construct the path via dotdot — resolves to the sibling dir outside root.
	dotdotEscape := filepath.Join(root, "..", "escape")
	if err := ValidatePathWithinRoot(root, dotdotEscape); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("ValidatePathWithinRoot() dotdot error = %v, want ErrUnsafePath", err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}
