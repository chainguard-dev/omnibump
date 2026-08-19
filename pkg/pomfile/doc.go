/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package pomfile is a small parsing and editing library for the parts of a
// Maven POM omnibump needs to patch: the properties declared in a project's
// top-level <properties> block.
//
// It is intentionally not a general POM model — gopom already provides one.
// Round-tripping a POM through an XML unmarshal/marshal pair is lossy:
// encoding/xml discards comments and re-indents every element, so a one-value
// change rewrites the whole file and deletes any license header, and a
// repository that enforces a header with license-maven-plugin then fails its
// own build. This package exists for the cases where that loss is
// unacceptable.
//
// Elements are located with the standard XML tokenizer, each is recorded with
// its source span, and edits are format-preserving splices at those spans:
// everything outside the edited value is preserved byte for byte. Properties
// declared inside a <profile> are deliberately not editable, because they
// apply only when that profile is active and rewriting one would change a
// conditional value while leaving the default untouched.
//
// The package performs no file I/O: callers pass content in and read content
// back via Content(). Path policy (size limits, symlink and root-boundary
// checks) belongs to the caller.
package pomfile
