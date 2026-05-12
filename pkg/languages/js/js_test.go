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

	"github.com/chainguard-dev/omnibump/pkg/languages"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJS_Detect(t *testing.T) {
	tests := []struct {
		name  string
		files []string
		want  bool
	}{
		{"package.json present", []string{"package.json"}, true},
		{"package.json with lock", []string{"package.json", "pnpm-lock.yaml"}, true},
		{"only lock file", []string{"pnpm-lock.yaml"}, false},
		{"unrelated files", []string{"go.mod"}, false},
		{"empty directory", []string{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tt.files {
				require.NoError(t, os.WriteFile(filepath.Join(dir, f), []byte("{}"), 0o600))
			}

			got, err := (&JS{}).Detect(context.Background(), dir)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestJS_Update_Errors(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, dir string)
		cfg     *languages.UpdateConfig
		wantErr error
	}{
		{
			name:  "no dependencies",
			setup: func(*testing.T, string) {},
			cfg: &languages.UpdateConfig{
				Dependencies: nil,
			},
			wantErr: ErrNoDependencies,
		},
		{
			name:  "package.json missing",
			setup: func(*testing.T, string) {},
			cfg: &languages.UpdateConfig{
				Dependencies: []languages.Dependency{{Name: "x", Version: "1"}},
			},
			wantErr: ErrPackageJSONNotFound,
		},
		{
			name: "no manager detectable",
			setup: func(t *testing.T, dir string) {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"),
					[]byte(`{"name":"x","version":"1"}`), 0o600))
			},
			cfg: &languages.UpdateConfig{
				Dependencies: []languages.Dependency{{Name: "x", Version: "1"}},
			},
			wantErr: ErrNoManagerDetected,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.setup(t, dir)
			tt.cfg.RootDir = dir
			tt.cfg.Options = map[string]any{}

			err := (&JS{}).Update(context.Background(), tt.cfg)
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}
