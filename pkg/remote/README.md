# Remote Repository Analysis

This package provides language-agnostic remote repository analysis capabilities for omnibump.

## Features

- Fetch dependency manifest files from remote repositories (GitHub, GitLab, etc.)
- No cloning required - uses repository APIs
- Recursive search for manifest files (finds nested go.mod, pom.xml, etc.)
- Supports multi-module repositories

## Usage

```go
import (
    "context"
    "github.com/chainguard-dev/omnibump/pkg/remote"
    omnibump_go "github.com/chainguard-dev/omnibump/pkg/languages/golang"
)

// Create GitHub fetcher
fetcher := remote.NewGitHubFetcher(yourGitHubClient)

// Search for go.mod files
repo := remote.RepositoryRef{
    Owner: "spiffe",
    Repo:  "spire",
    Ref:   "v1.10.3",
}

files, err := fetcher.SearchFiles(ctx, repo, []string{"go.mod"})

// Convert to map for analysis
fileMap := make(map[string][]byte)
for _, f := range files {
    fileMap[f.Path] = f.Content
}

// Analyze with omnibump
analyzer := &omnibump_go.GolangAnalyzer{}
result, err := analyzer.AnalyzeRemote(ctx, fileMap)

// Result contains analysis for each found file
for _, fa := range result.FileAnalyses {
    fmt.Printf("File: %s\n", fa.FilePath)
    fmt.Printf("Dependencies: %d\n", len(fa.Analysis.Dependencies))
}
```

## Integration Tests

Integration tests verify the complete workflow using real GitHub repositories.

### Running Integration Tests

```bash
# Set GitHub token
export GITHUB_TOKEN=your_token_here

# Run all integration tests
go test -tags=integration ./pkg/remote/... -v
go test -tags=integration ./pkg/languages/golang/... -run Remote -v

# Run specific test
go test -tags=integration ./pkg/remote/... -run TestGitHubFetcher_SearchFiles_SpireServer -v
```

### Test Repositories

The integration tests use these real repositories:
- `spiffe/spire` - Single module with sigstore dependencies
- `aws/amazon-ssm-agent` - Contains golang.org/x/crypto
- `hashicorp/consul` - Uses replace directives
- `kubernetes/kubernetes` - Multi-module repository
- `elastic/beats` - Contains pseudo-version dependencies

### What Gets Tested

1. **GitHub Fetcher** (`pkg/remote/github_integration_test.go`):
   - Searching for files by pattern
   - Fetching specific files
   - Handling multi-module repos
   - Error cases (non-existent files, etc.)

2. **Remote Analysis** (`pkg/languages/golang/remote_integration_test.go`):
   - Complete workflow: fetch → analyze
   - Dependency discovery and version tracking
   - Replace directive handling
   - Multi-module analysis
   - CVE remediation scenarios

## Implementing Other Languages

To add remote support for other languages (Maven, Rust, etc.):

1. Implement `AnalyzeRemote()` in your language's analyzer
2. Add integration tests using real repos
3. Update manifest file patterns (e.g., `["pom.xml"]`, `["Cargo.toml"]`)

Example for Maven:

```go
func (ma *MavenAnalyzer) AnalyzeRemote(ctx context.Context, files map[string][]byte) (*analyzer.RemoteAnalysisResult, error) {
    // Similar to Go implementation
    result := &analyzer.RemoteAnalysisResult{
        Language: "maven",
        FileAnalyses: []analyzer.FileAnalysis{},
    }

    for path, content := range files {
        // Parse pom.xml from content
        // Add to result.FileAnalyses
    }

    return result, nil
}
```
