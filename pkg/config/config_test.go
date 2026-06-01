/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package config

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestParseInlineReplaces tests parsing inline replace specifications.
func TestParseInlineReplaces(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []Replace
		wantErr bool
	}{
		{
			name:  "single replace",
			input: "github.com/whilp/git-urls=github.com/chainguard-dev/git-urls@v1.0.2",
			want: []Replace{
				{OldName: "github.com/whilp/git-urls", Name: "github.com/chainguard-dev/git-urls", Version: "v1.0.2"},
			},
		},
		{
			name:  "multiple replaces",
			input: "github.com/old/foo=github.com/new/foo@v2.0.0 github.com/old/bar=github.com/new/bar@v1.5.0",
			want: []Replace{
				{OldName: "github.com/old/foo", Name: "github.com/new/foo", Version: "v2.0.0"},
				{OldName: "github.com/old/bar", Name: "github.com/new/bar", Version: "v1.5.0"},
			},
		},
		{
			name:  "empty string",
			input: "",
			want:  nil,
		},
		{
			name:    "missing equals",
			input:   "github.com/old/foo@v1.0.0",
			wantErr: true,
		},
		{
			name:    "missing at sign in replacement",
			input:   "github.com/old/foo=github.com/new/foo",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseInlineReplaces(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseInlineReplaces() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ParseInlineReplaces() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestParseInlineProperties tests parsing inline property specifications.
func TestParseInlineProperties(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []Property
		wantErr bool
	}{
		{
			name:  "single property",
			input: "java.version=17",
			want: []Property{
				{Property: "java.version", Value: "17"},
			},
			wantErr: false,
		},
		{
			name:  "multiple properties",
			input: "java.version=17 spring.version=3.0.0 maven.version=3.9.0",
			want: []Property{
				{Property: "java.version", Value: "17"},
				{Property: "spring.version", Value: "3.0.0"},
				{Property: "maven.version", Value: "3.9.0"},
			},
			wantErr: false,
		},
		{
			name:  "property with dots in name",
			input: "com.example.app.version=1.2.3",
			want: []Property{
				{Property: "com.example.app.version", Value: "1.2.3"},
			},
			wantErr: false,
		},
		{
			name:  "property value with equals sign",
			input: "property=value=with=equals",
			want: []Property{
				{Property: "property", Value: "value=with=equals"},
			},
			wantErr: false,
		},
		{
			name:    "empty string",
			input:   "",
			want:    nil,
			wantErr: false,
		},
		{
			name:    "invalid format - missing equals",
			input:   "java.version",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "invalid format - no value",
			input:   "java.version=",
			want:    nil,
			wantErr: false,
		},
		{
			name:    "invalid format - no property name",
			input:   "=17",
			want:    nil,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseInlineProperties(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseInlineProperties() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ParseInlineProperties() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestParseInlineProperties_Whitespace tests handling of extra whitespace.
func TestParseInlineProperties_Whitespace(t *testing.T) {
	got, err := ParseInlineProperties("  java.version=17   spring.version=3.0.0  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []Property{
		{Property: "java.version", Value: "17"},
		{Property: "spring.version", Value: "3.0.0"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ParseInlineProperties() mismatch (-want +got):\n%s", diff)
	}
}

func TestParseInlinePackages(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []Package
	}{
		{
			name:  "js bare selector",
			input: "simple-git=3.36.0",
			want:  []Package{{Name: "simple-git", Version: "3.36.0"}},
		},
		{
			name:  "js quoted selector",
			input: `"simple-git"=3.36.0`,
			want:  []Package{{Name: "simple-git", Version: "3.36.0"}},
		},
		{
			name:  "js scoped name",
			input: `"@isaacs/brace-expansion"=5.0.1`,
			want:  []Package{{Name: "@isaacs/brace-expansion", Version: "5.0.1"}},
		},
		{
			name:  "js ranged selector",
			input: `"undici@^6"=6.24.0`,
			want:  []Package{{Name: "undici@^6", Version: "6.24.0"}},
		},
		{
			name:  "js multiple entries one line",
			input: `"undici@^6"=6.24.0 "undici@^7"=7.24.0`,
			want: []Package{
				{Name: "undici@^6", Version: "6.24.0"},
				{Name: "undici@^7", Version: "7.24.0"},
			},
		},
		{
			name: "js multi-line with comments",
			input: "\"fast-xml-parser\"=5.5.7              # GHSA-37qj-frw5-hhjh\n" +
				"# this line is a comment and should be skipped\n" +
				"\"simple-git\"=3.36.0                  # GHSA-hffm-xvc3-vprc\n",
			want: []Package{
				{Name: "fast-xml-parser", Version: "5.5.7"},
				{Name: "simple-git", Version: "3.36.0"},
			},
		},
		{
			name:  "go module path",
			input: "golang.org/x/sys@v0.21.0",
			want:  []Package{{Name: "golang.org/x/sys", Version: "v0.21.0"}},
		},
		{
			name:  "maven coordinates",
			input: "io.netty@netty-codec-http@4.1.94.Final",
			want:  []Package{{GroupID: "io.netty", ArtifactID: "netty-codec-http", Version: "4.1.94.Final"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseInlinePackages(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ParseInlinePackages mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseInlinePackages_RejectsMalformed(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty selector", "=1.0.0"},
		{"empty version", "foo="},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseInlinePackages(tt.input); err == nil {
				t.Errorf("expected error for %q, got nil", tt.input)
			}
		})
	}
}
