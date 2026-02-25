/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package golang

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseGoModfileFromContent(t *testing.T) {
	tests := []struct {
		name        string
		filename    string
		content     string
		wantErr     bool
		wantModule  string
		wantReqLen  int
		wantReplLen int
	}{
		{
			name:     "simple go.mod",
			filename: "go.mod",
			content: `module github.com/example/project

go 1.23

require github.com/google/uuid v1.3.0
`,
			wantModule: "github.com/example/project",
			wantReqLen: 1,
		},
		{
			name:     "go.mod with replace",
			filename: "go.mod",
			content: `module github.com/example/project

go 1.23

require github.com/example/dependency v1.0.0

replace github.com/example/dependency => github.com/fork/dependency v1.5.0
`,
			wantModule:  "github.com/example/project",
			wantReqLen:  1,
			wantReplLen: 1,
		},
		{
			name:     "invalid go.mod",
			filename: "go.mod",
			content:  `this is not a valid go.mod`,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modFile, err := ParseGoModfileFromContent(tt.filename, []byte(tt.content))
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, modFile)
			require.Equal(t, tt.wantModule, modFile.Module.Mod.Path)

			if tt.wantReqLen > 0 {
				require.Len(t, modFile.Require, tt.wantReqLen)
			}

			if tt.wantReplLen > 0 {
				require.Len(t, modFile.Replace, tt.wantReplLen)
			}
		})
	}
}

func TestAnalyzeFromContent(t *testing.T) {
	tests := []struct {
		name          string
		filename      string
		content       string
		wantErr       bool
		wantDeps      []string
		wantIndirect  []string
		wantReplaced  []string
		wantGoVersion string
	}{
		{
			name:     "simple project",
			filename: "go.mod",
			content: `module github.com/example/project

go 1.23.1

require (
	github.com/google/uuid v1.3.0
	github.com/sirupsen/logrus v1.9.0
)
`,
			wantDeps:      []string{"github.com/google/uuid", "github.com/sirupsen/logrus"},
			wantGoVersion: "1.23.1",
		},
		{
			name:     "with indirect dependencies",
			filename: "go.mod",
			content: `module github.com/example/project

go 1.23

require (
	github.com/google/uuid v1.3.0
	github.com/indirect/dep v1.0.0 // indirect
)
`,
			wantDeps:     []string{"github.com/google/uuid", "github.com/indirect/dep"},
			wantIndirect: []string{"github.com/indirect/dep"},
		},
		{
			name:     "with replace directives",
			filename: "go.mod",
			content: `module github.com/example/project

go 1.23

require github.com/example/dependency v1.0.0

replace github.com/example/dependency => github.com/fork/dependency v1.5.0
`,
			wantDeps:     []string{"github.com/example/dependency"},
			wantReplaced: []string{"github.com/example/dependency"},
		},
		{
			name:     "replace without require",
			filename: "go.mod",
			content: `module github.com/example/project

go 1.23

replace github.com/old/dep => github.com/new/dep v2.0.0
`,
			wantDeps:     []string{"github.com/old/dep"},
			wantReplaced: []string{"github.com/old/dep"},
		},
		{
			name:     "invalid content",
			filename: "go.mod",
			content:  `not a valid go.mod`,
			wantErr:  true,
		},
		{
			name:     "pseudo-version dependencies",
			filename: "go.mod",
			content: `module github.com/elastic/beats/v7

go 1.24

require (
	github.com/elastic/beats/v7 v7.0.0-alpha2.0.20251217054608-6e42552a23ce
	github.com/elastic/go-elasticsearch/v7 v7.17.0
)
`,
			wantDeps:      []string{"github.com/elastic/beats/v7", "github.com/elastic/go-elasticsearch/v7"},
			wantGoVersion: "1.24",
		},
		{
			name:     "v2+ module paths",
			filename: "go.mod",
			content: `module github.com/example/project

go 1.23

require (
	github.com/sigstore/cosign/v2 v2.6.2
	github.com/theupdateframework/go-tuf/v2 v2.3.1
)
`,
			wantDeps: []string{"github.com/sigstore/cosign/v2", "github.com/theupdateframework/go-tuf/v2"},
		},
		{
			name:     "multiple requires with mixed versions",
			filename: "go.mod",
			content: `module github.com/spiffe/spire

go 1.24.0

require (
	github.com/sigstore/cosign/v2 v2.6.2
	github.com/theupdateframework/go-tuf/v2 v2.3.1
	github.com/sigstore/rekor v1.5.0
	github.com/sigstore/sigstore v1.10.3
	golang.org/x/crypto v0.31.0
)
`,
			wantDeps: []string{
				"github.com/sigstore/cosign/v2",
				"github.com/theupdateframework/go-tuf/v2",
				"github.com/sigstore/rekor",
				"github.com/sigstore/sigstore",
				"golang.org/x/crypto",
			},
			wantGoVersion: "1.24.0",
		},
		{
			name:     "chainguard dependencies",
			filename: "go.mod",
			content: `module github.com/aws/amazon-ssm-agent

go 1.23

require (
	golang.org/x/crypto v0.44.0
	chainguard.dev/apko v1.0.0
)
`,
			wantDeps: []string{"golang.org/x/crypto", "chainguard.dev/apko"},
		},
	}

	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analyzer := &GolangAnalyzer{}
			result, err := analyzer.AnalyzeFromContent(ctx, tt.filename, []byte(tt.content))

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, "go", result.Language)

			// Check expected dependencies
			require.Len(t, result.Dependencies, len(tt.wantDeps))
			for _, dep := range tt.wantDeps {
				require.Contains(t, result.Dependencies, dep)
			}

			// Check indirect dependencies
			for _, dep := range tt.wantIndirect {
				require.Contains(t, result.Dependencies, dep)
				require.True(t, result.Dependencies[dep].Transitive)
			}

			// Check replaced dependencies
			for _, dep := range tt.wantReplaced {
				require.Contains(t, result.Dependencies, dep)
				require.Equal(t, "replace", result.Dependencies[dep].UpdateStrategy)
				replaced, ok := result.Dependencies[dep].Metadata["replaced"].(bool)
				require.True(t, ok)
				require.True(t, replaced)
			}

			// Check Go version
			if tt.wantGoVersion != "" {
				goVer, ok := result.Metadata["goVersion"].(string)
				require.True(t, ok)
				require.Equal(t, tt.wantGoVersion, goVer)
			}
		})
	}
}

func TestAnalyzeRemote(t *testing.T) {
	tests := []struct {
		name          string
		files         map[string][]byte
		wantErr       bool
		wantFileCount int
	}{
		{
			name: "single go.mod",
			files: map[string][]byte{
				"go.mod": []byte(`module github.com/example/project

go 1.23

require github.com/google/uuid v1.3.0
`),
			},
			wantFileCount: 1,
		},
		{
			name: "multi-module repo",
			files: map[string][]byte{
				"go.mod": []byte(`module github.com/example/project

go 1.23

require github.com/google/uuid v1.3.0
`),
				"api/go.mod": []byte(`module github.com/example/project/api

go 1.23

require github.com/sirupsen/logrus v1.9.0
`),
				"pkg/server/go.mod": []byte(`module github.com/example/project/server

go 1.23

require golang.org/x/crypto v0.45.0
`),
			},
			wantFileCount: 3,
		},
		{
			name:    "empty files map",
			files:   map[string][]byte{},
			wantErr: true,
		},
		{
			name: "invalid go.mod",
			files: map[string][]byte{
				"go.mod": []byte(`not a valid go.mod`),
			},
			wantFileCount: 0,
		},
		{
			name: "mixed valid and invalid",
			files: map[string][]byte{
				"go.mod": []byte(`module github.com/example/project

go 1.23

require github.com/google/uuid v1.3.0
`),
				"api/go.mod": []byte(`invalid content`),
			},
			wantFileCount: 1,
		},
	}

	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analyzer := &GolangAnalyzer{}
			result, err := analyzer.AnalyzeRemote(ctx, tt.files)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, "go", result.Language)
			require.Len(t, result.FileAnalyses, tt.wantFileCount)

			for _, fa := range result.FileAnalyses {
				require.NotEmpty(t, fa.FilePath)
				require.NotNil(t, fa.Analysis)
				require.Equal(t, "go", fa.Analysis.Language)
				t.Logf("%s: %d dependencies", fa.FilePath, len(fa.Analysis.Dependencies))
			}
		})
	}
}

func TestAnalyzeWorkspace(t *testing.T) {
	ctx := context.Background()

	// Create temporary workspace directory
	tmpDir := t.TempDir()

	// Create go.work file
	workContent := `go 1.23

use (
	.
	./moduleA
	./moduleB
)
`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "go.work"), []byte(workContent), 0o600))

	// Create root go.mod
	rootMod := `module github.com/example/workspace

go 1.23

require (
	github.com/google/uuid v1.3.0
	github.com/shared/dep v1.0.0
)
`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(rootMod), 0o600))

	// Create moduleA
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "moduleA"), 0o755))
	modAContent := `module github.com/example/workspace/moduleA

go 1.23

require (
	github.com/sirupsen/logrus v1.9.0
	github.com/shared/dep v1.0.0
)
`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "moduleA", "go.mod"), []byte(modAContent), 0o600))

	// Create moduleB
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "moduleB"), 0o755))
	modBContent := `module github.com/example/workspace/moduleB

go 1.23

require (
	golang.org/x/crypto v0.45.0
	github.com/unique/dep v1.2.0
)
`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "moduleB", "go.mod"), []byte(modBContent), 0o600))

	// Test workspace analysis
	analyzer := &GolangAnalyzer{}
	result, err := analyzer.Analyze(ctx, tmpDir)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Check metadata
	require.Equal(t, "go", result.Language)
	isWorkspace, ok := result.Metadata["workspace"].(bool)
	require.True(t, ok)
	require.True(t, isWorkspace)

	moduleCount, ok := result.Metadata["moduleCount"].(int)
	require.True(t, ok)
	require.Equal(t, 3, moduleCount)

	// Check dependencies are aggregated
	// Should have: uuid, shared/dep, logrus, crypto, unique/dep
	require.Equal(t, 5, len(result.Dependencies))

	// Check shared dependency appears in multiple modules
	sharedDep, ok := result.Dependencies["github.com/shared/dep"]
	require.True(t, ok)
	modules, ok := sharedDep.Metadata["foundInModules"].([]string)
	require.True(t, ok)
	require.Len(t, modules, 2)
	require.Contains(t, modules, ".")
	require.Contains(t, modules, "./moduleA")

	// Check unique dependencies
	uniqueDep, ok := result.Dependencies["github.com/unique/dep"]
	require.True(t, ok)
	uniqueModules, ok := uniqueDep.Metadata["foundInModules"].([]string)
	require.True(t, ok)
	require.Len(t, uniqueModules, 1)
	require.Contains(t, uniqueModules, "./moduleB")
}
