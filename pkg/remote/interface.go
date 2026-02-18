/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package remote provides interfaces for fetching dependency manifest files from remote repositories.
package remote

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	// ErrRepositoryRefNil is returned when a repository reference is nil.
	ErrRepositoryRefNil = errors.New("repository reference cannot be nil")

	// ErrEmptyField is returned when a required field is empty.
	ErrEmptyField = errors.New("field cannot be empty")

	// ErrFieldTooLong is returned when a field exceeds maximum length.
	ErrFieldTooLong = errors.New("field exceeds maximum length")

	// ErrInvalidCharacters is returned when a field contains invalid characters.
	ErrInvalidCharacters = errors.New("contains invalid characters")

	// ErrCommandInjection is returned when a value could enable command injection.
	ErrCommandInjection = errors.New("potential command injection")

	// ErrPathTraversal is returned when a path contains traversal attempts.
	ErrPathTraversal = errors.New("path traversal attempt")

	// ErrAbsolutePath is returned when a path is absolute instead of relative.
	ErrAbsolutePath = errors.New("path must be relative, not absolute")

	// ErrNegativeSize is returned when content size is negative.
	ErrNegativeSize = errors.New("content size cannot be negative")

	// ErrFileTooLarge is returned when a file exceeds size limits.
	ErrFileTooLarge = errors.New("file size exceeds maximum")

	// ErrSearchOperatorInjection is returned when a pattern contains GitHub search operators.
	ErrSearchOperatorInjection = errors.New("pattern cannot contain GitHub search operator")

	// ErrPatternContainsPath is returned when a pattern contains path separators.
	ErrPatternContainsPath = errors.New("pattern cannot contain path separators")
)

// RemoteFetcher defines the interface for fetching files from remote repositories.
// Implementations can support different providers (GitHub, GitLab, Gitea, etc.)
//
//nolint:revive // Explicit name preferred for clarity in remote package
type RemoteFetcher interface {
	// SearchFiles searches for files matching the given patterns in a repository.
	// Recursively searches the entire repository tree.
	// patterns is a list of filename patterns to search for (e.g., ["go.mod", "pom.xml"])
	// Returns a list of ALL found files with their paths and content.
	// For multi-module repos, this will return multiple files (e.g., go.mod, api/go.mod, server/go.mod).
	SearchFiles(ctx context.Context, repo RepositoryRef, patterns []string) ([]RemoteFile, error)

	// GetFile fetches a specific file from a repository.
	GetFile(ctx context.Context, repo RepositoryRef, path string) (*RemoteFile, error)
}

// RepositoryRef identifies a remote repository and ref (tag/branch/commit).
type RepositoryRef struct {
	// Host is the git host (e.g., "github.com", "gitlab.com")
	Host string

	// Owner is the repository owner/organization
	Owner string

	// Repo is the repository name
	Repo string

	// Ref is the git reference (tag, branch, or commit SHA)
	Ref string
}

// RemoteFile represents a file fetched from a remote repository.
//
//nolint:revive // Explicit name preferred for clarity in remote package
type RemoteFile struct {
	// Path is the file path relative to repository root
	// Examples: "go.mod", "api/go.mod", "pkg/server/go.mod"
	Path string

	// Content is the file content as bytes
	Content []byte

	// Metadata stores additional information about the file
	Metadata map[string]string
}

const (
	// MaxFileSize is the maximum allowed file size in bytes (10 MB).
	MaxFileSize = 10 * 1024 * 1024

	// MaxPathLength is the maximum allowed path length.
	MaxPathLength = 4096

	// MaxRefLength is the maximum allowed git reference length.
	MaxRefLength = 256

	// MaxOwnerLength is the maximum allowed owner/org name length.
	MaxOwnerLength = 256

	// MaxRepoLength is the maximum allowed repository name length.
	MaxRepoLength = 256

	// MaxHostLength is the maximum allowed DNS hostname length.
	MaxHostLength = 253
)

var (
	// validOwnerRegex validates GitHub owner/organization names.
	// Matches: alphanumeric with hyphens/underscores, but not starting/ending with hyphen.
	// Examples: "spiffe", "kubernetes-sigs", "my_org".
	validOwnerRegex = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-_])*[a-zA-Z0-9]$|^[a-zA-Z0-9]$`)

	// validRepoRegex validates GitHub repository names.
	// Matches: alphanumeric with dots, hyphens, underscores.
	// Examples: "spire", "go-github", "node.js".
	validRepoRegex = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9._-])*$`)

	// validRefRegex validates git references (tags, branches, commit SHAs).
	// Matches: alphanumeric with dots, slashes, hyphens, plus signs.
	// Examples: "v1.10.3", "main", "release/v1.0", "v1.35.0+rke2r1".
	validRefRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/+-]*$`)

	// validHostRegex validates git host domain names.
	// Matches: standard DNS hostname format.
	// Examples: "github.com", "gitlab.example.com".
	validHostRegex = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9.-])*[a-zA-Z0-9]$`)

	// dangerousShellChars are characters that could enable command injection.
	dangerousShellChars = []string{";", "&", "|", "`", "$", "(", ")", "<", ">", "\n", "\r", "\t", "\\"}
)

// Validate validates all fields of a RepositoryRef.
func (r *RepositoryRef) Validate() error {
	if r == nil {
		return ErrRepositoryRefNil
	}

	// Validate each field using the helper function
	if err := validateStringField("owner", r.Owner, MaxOwnerLength, validOwnerRegex); err != nil {
		return err
	}

	if err := validateStringField("repo", r.Repo, MaxRepoLength, validRepoRegex); err != nil {
		return err
	}

	if err := validateRef(r.Ref); err != nil {
		return err
	}

	// Validate Host (optional field)
	if r.Host != "" {
		if err := validateStringField("host", r.Host, MaxHostLength, validHostRegex); err != nil {
			return err
		}
	}

	return nil
}

// validateStringField validates a string field against common rules.
func validateStringField(fieldName, value string, maxLength int, pattern *regexp.Regexp) error {
	if value == "" {
		return fmt.Errorf("%w: %s", ErrEmptyField, fieldName)
	}

	if len(value) > maxLength {
		return fmt.Errorf("%w: %s %q (max: %d)", ErrFieldTooLong, fieldName, value, maxLength)
	}

	if !pattern.MatchString(value) {
		return fmt.Errorf("%w: %s %q", ErrInvalidCharacters, fieldName, value)
	}

	return nil
}

// validateRef validates a git reference with additional security checks.
func validateRef(ref string) error {
	if ref == "" {
		return fmt.Errorf("%w: ref", ErrEmptyField)
	}

	if len(ref) > MaxRefLength {
		return fmt.Errorf("%w: ref %q (max: %d)", ErrFieldTooLong, ref, MaxRefLength)
	}

	// Git injection prevention: reject refs starting with dashes
	if strings.HasPrefix(ref, "--") {
		return fmt.Errorf("%w: ref %q cannot start with '--'", ErrCommandInjection, ref)
	}
	if strings.HasPrefix(ref, "-") {
		return fmt.Errorf("%w: ref %q cannot start with '-'", ErrCommandInjection, ref)
	}

	// Command injection prevention: reject shell metacharacters
	for _, char := range dangerousShellChars {
		if strings.Contains(ref, char) {
			return fmt.Errorf("%w: ref %q contains dangerous character: %q", ErrCommandInjection, ref, char)
		}
	}

	// Basic pattern validation
	if !validRefRegex.MatchString(ref) {
		return fmt.Errorf("%w: ref %q", ErrInvalidCharacters, ref)
	}

	return nil
}

// ValidatePath validates a file path to prevent path traversal attacks.
func ValidatePath(path string) error {
	if path == "" {
		return fmt.Errorf("%w: path", ErrEmptyField)
	}

	if len(path) > MaxPathLength {
		return fmt.Errorf("%w: path %q (max: %d)", ErrFieldTooLong, path, MaxPathLength)
	}

	// Security: Prevent paths starting with "/"
	if strings.HasPrefix(path, "/") {
		return fmt.Errorf("%w: path %q cannot start with '/'", ErrAbsolutePath, path)
	}

	// Security: Prevent absolute paths (e.g., Windows C:\)
	if filepath.IsAbs(path) {
		return fmt.Errorf("%w: path %q", ErrAbsolutePath, path)
	}

	// Security: Prevent path traversal attacks
	cleaned := filepath.Clean(path)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) || strings.Contains(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: path %q", ErrPathTraversal, path)
	}

	return nil
}

// ValidateContentSize validates that content size is within acceptable limits.
func ValidateContentSize(size int, path string) error {
	if size < 0 {
		return fmt.Errorf("%w: %d", ErrNegativeSize, size)
	}

	if size > MaxFileSize {
		return fmt.Errorf("%w: file %q size %d bytes (max: %d MB)",
			ErrFileTooLarge, path, size, MaxFileSize/(1024*1024))
	}

	return nil
}
