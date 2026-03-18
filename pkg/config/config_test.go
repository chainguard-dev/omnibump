/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package config

import (
	"testing"
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

			if len(got) != len(tt.want) {
				t.Errorf("ParseInlineReplaces() got %d replaces, want %d", len(got), len(tt.want))
				return
			}

			for i := range got {
				if got[i].OldName != tt.want[i].OldName {
					t.Errorf("ParseInlineReplaces() replace[%d].OldName = %v, want %v", i, got[i].OldName, tt.want[i].OldName)
				}
				if got[i].Name != tt.want[i].Name {
					t.Errorf("ParseInlineReplaces() replace[%d].Name = %v, want %v", i, got[i].Name, tt.want[i].Name)
				}
				if got[i].Version != tt.want[i].Version {
					t.Errorf("ParseInlineReplaces() replace[%d].Version = %v, want %v", i, got[i].Version, tt.want[i].Version)
				}
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

			if len(got) != len(tt.want) {
				t.Errorf("ParseInlineProperties() got %d properties, want %d", len(got), len(tt.want))
				return
			}

			for i := range got {
				if got[i].Property != tt.want[i].Property {
					t.Errorf("ParseInlineProperties() property[%d].Property = %v, want %v", i, got[i].Property, tt.want[i].Property)
				}
				if got[i].Value != tt.want[i].Value {
					t.Errorf("ParseInlineProperties() property[%d].Value = %v, want %v", i, got[i].Value, tt.want[i].Value)
				}
			}
		})
	}
}

// TestParseInlineProperties_Whitespace tests handling of extra whitespace.
func TestParseInlineProperties_Whitespace(t *testing.T) {
	input := "  java.version=17   spring.version=3.0.0  "
	got, err := ParseInlineProperties(input)
	if err != nil {
		t.Fatalf("ParseInlineProperties() unexpected error: %v", err)
	}

	if len(got) != 2 {
		t.Errorf("ParseInlineProperties() got %d properties, want 2", len(got))
	}

	if got[0].Property != "java.version" || got[0].Value != "17" {
		t.Errorf("ParseInlineProperties() property[0] = %v=%v, want java.version=17", got[0].Property, got[0].Value)
	}

	if got[1].Property != "spring.version" || got[1].Value != "3.0.0" {
		t.Errorf("ParseInlineProperties() property[1] = %v=%v, want spring.version=3.0.0", got[1].Property, got[1].Value)
	}
}
