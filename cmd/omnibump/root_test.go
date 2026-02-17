/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package omnibump

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateLogPath_ValidPaths tests that safe log paths are accepted
func TestValidateLogPath_ValidPaths(t *testing.T) {
	tmpDir := t.TempDir()

	validPaths := []string{
		filepath.Join(tmpDir, "logs", "app.log"),
		filepath.Join(tmpDir, "output.log"),
		"/var/log/omnibump.log",
		"/tmp/test.log",
		filepath.Join(tmpDir, "my-app", "logs", "debug.log"),
	}

	for _, path := range validPaths {
		t.Run(path, func(t *testing.T) {
			err := validateLogPath(path)
			if err != nil {
				t.Errorf("validateLogPath(%q) should be valid, got error: %v", path, err)
			}
		})
	}
}

// TestValidateLogPath_DisallowedPaths tests that dangerous paths are rejected (FINDING-OMNIBUMP-004)
func TestValidateLogPath_DisallowedPaths(t *testing.T) {
	disallowedPaths := []struct {
		path string
		desc string
	}{
		{
			path: "/etc/passwd",
			desc: "system password file",
		},
		{
			path: "/etc/shadow",
			desc: "system shadow file",
		},
		{
			path: "/root/.ssh/authorized_keys",
			desc: "SSH authorized keys",
		},
		{
			path: "/var/spool/cron/crontabs/root",
			desc: "cron job file",
		},
		{
			path: "/etc/cron.d/malicious",
			desc: "cron.d file",
		},
		{
			path: "/bin/bash",
			desc: "system binary",
		},
		{
			path: "/usr/bin/malicious",
			desc: "user binary",
		},
		{
			path: "/boot/grub/grub.cfg",
			desc: "boot configuration",
		},
		{
			path: "/home/user/.ssh/authorized_keys",
			desc: "user SSH keys",
		},
	}

	for _, tt := range disallowedPaths {
		t.Run(tt.desc, func(t *testing.T) {
			err := validateLogPath(tt.path)
			if err == nil {
				t.Errorf("validateLogPath(%q) should be rejected for: %s", tt.path, tt.desc)
			}
			if err != nil && !strings.Contains(err.Error(), "invalid log-policy path") {
				t.Errorf("validateLogPath(%q) error should mention invalid path, got: %v", tt.path, err)
			}
		})
	}
}

// TestValidateLogPath_PathTraversal tests that path traversal attacks are prevented
func TestValidateLogPath_PathTraversal(t *testing.T) {
	tmpDir := t.TempDir()

	pathTraversalAttempts := []string{
		filepath.Join(tmpDir, "..", "..", "etc", "passwd"),
		filepath.Join(tmpDir, "logs", "..", "..", "..", "etc", "shadow"),
		filepath.Join(tmpDir, "..", "..", "root", ".ssh", "authorized_keys"),
	}

	for _, path := range pathTraversalAttempts {
		t.Run(path, func(t *testing.T) {
			err := validateLogPath(path)
			// Should reject if the cleaned path ends up in a disallowed directory
			if err == nil {
				// Check if the absolute path ends up in a dangerous location
				absPath, _ := filepath.Abs(path)
				cleanPath := filepath.Clean(absPath)
				for _, disallowed := range []string{"/etc/", "/root/", "/.ssh/"} {
					if strings.HasPrefix(cleanPath, disallowed) {
						t.Errorf("validateLogPath(%q) should reject path traversal to %s", path, disallowed)
					}
				}
			}
		})
	}
}

// TestValidateLogPath_CronInjection tests prevention of cron job injection
func TestValidateLogPath_CronInjection(t *testing.T) {
	cronPaths := []string{
		"/var/spool/cron/root",
		"/etc/cron.d/backdoor",
		"/etc/cron.daily/malicious",
		"/var/spool/cron/crontabs/user",
	}

	for _, path := range cronPaths {
		t.Run(path, func(t *testing.T) {
			err := validateLogPath(path)
			if err == nil {
				t.Errorf("validateLogPath(%q) should reject cron-related paths", path)
			}
		})
	}
}

// TestValidateLogPath_SSHKeyInjection tests prevention of SSH key injection
func TestValidateLogPath_SSHKeyInjection(t *testing.T) {
	sshPaths := []string{
		"/root/.ssh/authorized_keys",
		"/home/user/.ssh/authorized_keys",
		"/home/user/.ssh/id_rsa",
		filepath.Join("subdir", ".ssh", "authorized_keys"),
	}

	for _, path := range sshPaths {
		t.Run(path, func(t *testing.T) {
			err := validateLogPath(path)
			if err == nil {
				t.Errorf("validateLogPath(%q) should reject SSH-related paths", path)
			}
		})
	}
}

// TestSetupLogging_RejectsArbitraryFileWrite tests that setupLogging validates paths (FINDING-OMNIBUMP-004)
func TestSetupLogging_RejectsArbitraryFileWrite(t *testing.T) {
	// Save original flags and restore after test
	originalLogPolicy := flags.logPolicy
	defer func() {
		flags.logPolicy = originalLogPolicy
	}()

	maliciousPaths := []string{
		"/etc/passwd",
		"/root/.ssh/authorized_keys",
		"/var/spool/cron/crontabs/root",
	}

	for _, path := range maliciousPaths {
		t.Run(path, func(t *testing.T) {
			flags.logPolicy = []string{path}

			err := setupLogging()
			if err == nil {
				t.Errorf("setupLogging() should reject malicious path %q", path)
			}

			if err != nil && !strings.Contains(err.Error(), "log-policy validation failed") {
				t.Errorf("setupLogging() error should mention validation failure, got: %v", err)
			}
		})
	}
}

// TestSetupLogging_AllowsBuiltinStderr tests that builtin:stderr is always allowed
func TestSetupLogging_AllowsBuiltinStderr(t *testing.T) {
	// Save original flags and restore after test
	originalLogPolicy := flags.logPolicy
	defer func() {
		flags.logPolicy = originalLogPolicy
	}()

	flags.logPolicy = []string{"builtin:stderr"}

	err := setupLogging()
	if err != nil {
		t.Errorf("setupLogging() should allow builtin:stderr, got error: %v", err)
	}
}
