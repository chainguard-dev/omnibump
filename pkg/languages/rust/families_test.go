/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package rust

import (
	"slices"
	"testing"
)

// Test_familyOf verifies set membership: any crate in a curated family resolves to
// that whole family (not just a lead), and a crate in no family resolves to nil.
func Test_familyOf(t *testing.T) {
	tests := []struct {
		crate string
		want  []string
	}{
		{"rand", []string{"rand", "rand_core", "rand_chacha"}},
		{"rand_core", []string{"rand", "rand_core", "rand_chacha"}},
		{"rustls-pemfile", []string{"rustls", "rustls-webpki", "webpki-roots", "rustls-pemfile"}},
		{"serde", nil},
		{"", nil},
	}
	for _, tt := range tests {
		t.Run(tt.crate, func(t *testing.T) {
			got := familyOf(tt.crate)
			if !slices.Equal(got, tt.want) {
				t.Errorf("familyOf(%q) = %v, want %v", tt.crate, got, tt.want)
			}
		})
	}
}
