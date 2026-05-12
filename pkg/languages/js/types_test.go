/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package js

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestManagers_Unmarshal(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Managers
		wantErr bool
	}{
		{"scalar", `"pnpm"`, Managers{"pnpm"}, false},
		{"single-element list", `["pnpm"]`, Managers{"pnpm"}, false},
		{"multi list", `["pnpm","yarn"]`, Managers{"pnpm", "yarn"}, false},
		{"null clears", `null`, nil, false},
		{"empty list", `[]`, Managers{}, false},
		{"number rejected", `42`, nil, true},
		{"object rejected", `{"x":"y"}`, nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Managers
			err := got.UnmarshalJSON([]byte(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %#v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Managers unmarshal (-want +got):\n%s", diff)
			}
		})
	}
}
