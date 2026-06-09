/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package python

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLatestVersion(t *testing.T) {
	tests := []struct {
		desc     string
		versions []string
		want     string
	}{
		{
			desc:     "simple ascending",
			versions: []string{"1.0.0", "2.0.0", "3.0.0"},
			want:     "3.0.0",
		},
		{
			desc:     "lexicographic trap: 9 vs 46",
			versions: []string{"9.0.0", "46.0.6", "40.0.0"},
			want:     "46.0.6",
		},
		{
			desc:     "pre-releases excluded when final exists",
			versions: []string{"2.0.0rc1", "2.0.0", "1.9.0"},
			want:     "2.0.0",
		},
		{
			desc:     "post-release beats final",
			versions: []string{"1.0.0", "1.0.0.post1"},
			want:     "1.0.0.post1",
		},
		{
			desc:     "realistic cryptography versions",
			versions: []string{"43.0.0", "43.0.1", "43.0.3", "44.0.0", "44.0.1", "46.0.6"},
			want:     "46.0.6",
		},
		{
			desc:     "empty list",
			versions: nil,
			want:     "",
		},
		{
			desc:     "single element",
			versions: []string{"1.0.0"},
			want:     "1.0.0",
		},
		{
			desc:     "unparseable versions skipped",
			versions: []string{"not-a-version", "1.0.0"},
			want:     "1.0.0",
		},
		{
			desc:     "all unparseable",
			versions: []string{"abc", "def"},
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got := latestVersion(tt.versions)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExtractVersionsFromSimpleIndex(t *testing.T) {
	html := []byte(`
<a href="requests-2.28.0-py3-none-any.whl">requests-2.28.0-py3-none-any.whl</a>
<a href="requests-2.31.0.tar.gz">requests-2.31.0.tar.gz</a>
<a href="requests-2.33.0-py3-none-any.whl">requests-2.33.0-py3-none-any.whl</a>
`)
	versions := extractVersionsFromSimpleIndex(html)
	assert.Equal(t, []string{"2.28.0", "2.31.0", "2.33.0"}, versions)
}
