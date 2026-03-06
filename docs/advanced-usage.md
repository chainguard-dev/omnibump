# Advanced Usage

This guide covers advanced features and use cases for omnibump.

## Debug Mode

Enable debug logging to troubleshoot issues or understand omnibump's behavior:

```bash
# Enable debug logging
omnibump --deps deps.yaml --log-level debug
```

Debug output includes:
- File parsing details
- Dependency resolution steps
- Version comparison logic
- Write operations

Example output:
```
DEBUG: Detecting language in /path/to/project
DEBUG: Found go.mod file
DEBUG: Detected language: go
DEBUG: Reading dependencies from deps.yaml
DEBUG: Parsing package: golang.org/x/sys@v0.28.0
DEBUG: Current version: v0.27.0
DEBUG: Target version: v0.28.0
DEBUG: Updating go.mod...
```

## Custom Log Output

Control where logs are written:

```bash
# Log to file
omnibump --deps deps.yaml --log-policy /tmp/omnibump.log

# View logs while command runs
tail -f /tmp/omnibump.log
```

## Show Changes

Display a diff of all changes before or after applying updates:

```bash
# Display diff of all changes
omnibump --deps deps.yaml --show-diff
```

Example output:
```diff
--- go.mod
+++ go.mod
@@ -5,7 +5,7 @@
 require (
-       golang.org/x/sys v0.27.0
+       golang.org/x/sys v0.28.0
        github.com/spf13/cobra v1.8.0
 )
```

## Validate Without Updating

Use dry run to see what would change without modifying files:

```bash
# Dry run shows what would change
omnibump --deps deps.yaml --dry-run --show-diff
```

This is useful for:
- Testing configuration files
- Verifying package names and versions
- Understanding impact before applying changes
- CI validation checks

## Analyze with Output Files

Generate configuration files from analysis:

```bash
# Generate configuration files from analysis
omnibump analyze \
  --packages "io.netty@netty-codec-http@4.1.94.Final" \
  --output-deps recommended-deps.yaml \
  --output-props recommended-properties.yaml

# Review the generated files
cat recommended-deps.yaml
cat recommended-properties.yaml

# Apply the recommendations
omnibump --deps recommended-deps.yaml --properties recommended-properties.yaml
```

## JSON/YAML Output for Automation

Use structured output for automation and tooling:

### JSON Output

```bash
# JSON output for parsing
omnibump analyze --output json | jq '.analysis.dependencies'

# Save for later analysis
omnibump analyze --output json > analysis.json

# Extract specific information
jq '.analysis.total_dependencies' analysis.json
jq '.analysis.dependencies[] | select(.version == "v1.2.3")' analysis.json
```

### YAML Output

```bash
# YAML output
omnibump analyze --output yaml > analysis.yaml

# Process with yq or similar tools
yq '.analysis.dependencies' analysis.yaml
```

## Combining Multiple Configuration Files

Update both dependencies and properties in one command:

```bash
# Combined updates
omnibump --deps deps.yaml --properties properties.yaml --show-diff
```

For Go projects with replacements:

```bash
# Include replaces
omnibump --deps deps.yaml --replaces replaces.yaml --tidy
```

## Working with Specific Directories

Target specific project directories:

```bash
# Update subproject
omnibump --deps deps.yaml --dir ./subproject

# Update multiple subprojects
for dir in project-a project-b project-c; do
  omnibump --deps deps.yaml --dir ./$dir
done
```

## Scripting and Automation

### Exit Codes

omnibump uses standard exit codes:
- `0` - Success
- `1` - Error (dependency not found, validation failed, etc.)

Use in scripts:

```bash
#!/bin/bash
if omnibump --deps deps.yaml --dry-run; then
  echo "Validation passed"
  omnibump --deps deps.yaml
else
  echo "Validation failed"
  exit 1
fi
```

### Conditional Updates

```bash
# Only update if analysis shows outdated dependencies
if omnibump analyze --packages "pkg@latest" | grep -q "outdated"; then
  omnibump --packages "pkg@latest"
fi
```

### Batch Processing

```bash
# Update multiple projects with error handling
for project in */; do
  echo "Updating $project"
  if omnibump --dir "$project" --deps shared-deps.yaml; then
    echo "✓ $project updated"
  else
    echo "✗ $project failed"
  fi
done
```

## Advanced Language-Specific Features

### Go: Workspace Support

omnibump automatically detects and updates `go.work` files:

```bash
# Updates both go.mod and go.work
omnibump --deps deps.yaml --tidy
```

### Go: Version Resolution and Normalization

omnibump resolves all Go package versions through `go list` to get canonical forms:

```bash
# Debug output shows resolution
omnibump --packages "github.com/docker/docker@v28.0.0" --log-level debug

# Output:
# DEBUG: Resolved github.com/docker/docker@v28.0.0 to canonical form v28.0.0+incompatible
```

**What gets normalized:**
- **+incompatible suffix**: `v28.0.0` → `v28.0.0+incompatible`
- **Pseudo-versions**: Commit hashes → `v0.0.0-20240101123456-abc123def456`
- **Version queries**: `@latest` → actual version number

**Environment:**
- Uses `GOWORK=off` to bypass workspace mode during resolution
- Uses `GOFLAGS=-mod=mod` to allow version queries
- Queries `proxy.golang.org` for version information

### Go: Transitive Dependency Analysis

omnibump performs deep analysis of dependency requirements:

```bash
# See detailed detection process
omnibump --packages "oras.land/oras-go@v1.2.7" --log-level debug

# Debug output shows:
# DEBUG: Checking transitive requirements package=oras.land/oras-go version=v1.2.7
# DEBUG: Fetching https://proxy.golang.org/oras.land/oras-go/@v/v1.2.7.mod
# WARN: Dependency requires newer version updating=oras.land/oras-go requires=github.com/docker/docker required_version=v28.5.1 current_version=v28.0.0
# INFO: Found missing co-updates count=15
```

**Process:**
1. Fetches target version's go.mod from Go proxy (e.g., `oras.land/oras-go@v1.2.7.mod`)
2. Parses its `require` statements
3. Compares each requirement against current project's versions
4. Detects incompatibilities where `current < required`
5. Deduplicates across all packages being updated
6. Returns complete list of missing co-updates

**Deduplication logic:**
- If multiple packages require same dependency, keeps highest version
- If dependency already in update list with sufficient version, skips
- Only reports genuine missing updates

**Example with multiple packages:**
```bash
omnibump --packages "pkg-a@v2.0.0 pkg-b@v3.0.0" --log-level debug

# pkg-a requires dep-x@v1.5.0
# pkg-b requires dep-x@v1.8.0
# Result: Only reports dep-x@v1.8.0 (highest)
```

### Go: Vendor Directory Handling

When vendor directory exists, omnibump ensures go.sum is up-to-date:

```bash
# Omnibump detects vendor directory and runs go mod tidy before vendoring
omnibump --packages "pkg@version"

# Debug output:
# INFO: Vendor directory detected, running go mod tidy to update go.sum
# INFO: Running go mod vendor
```

**Why this matters:**
- When using `AddRequire()` to update go.mod, go.sum isn't automatically updated
- Running `go vendor` on stale go.sum fails with "missing go.sum entry" errors
- Omnibump automatically runs `go mod tidy` first to refresh go.sum

### Maven: Property Analysis

Understand property usage before updating:

```bash
# See which dependencies use which properties
omnibump analyze --output json | jq '.analysis.properties'

# Update property to affect multiple dependencies
omnibump --properties properties.yaml
```

### Gradle: Multi-Format Support

Works with both Groovy and Kotlin DSL automatically:

```bash
# Updates build.gradle or build.gradle.kts
omnibump --deps deps.yaml

# Handles Spring Boot library() pattern
omnibump --packages "org.apache.commons:Commons Lang3@3.18.0"
```

## Performance Optimization

### Limit Scope

For large projects, limit the scope of operations:

```bash
# Update specific directory only
omnibump --deps deps.yaml --dir ./critical-module

# Skip tidy for faster updates (run manually later)
omnibump --deps deps.yaml
go mod tidy  # Run separately
```

### Parallel Updates

Update independent projects in parallel:

```bash
# Using GNU parallel
parallel omnibump --deps deps.yaml --dir ::: project-*

# Or with xargs
ls -d project-* | xargs -P 4 -I {} omnibump --deps deps.yaml --dir {}
```

## Integration with Other Tools

### Pre-commit Hooks

```bash
#!/bin/bash
# .git/hooks/pre-commit

# Validate dependency configuration
omnibump --deps deps.yaml --dry-run || exit 1
```

### Makefile Integration

```makefile
.PHONY: update-deps
update-deps:
	omnibump --deps deps.yaml --dry-run
	@read -p "Apply updates? [y/N] " -n 1 -r; \
	if [[ $$REPLY =~ ^[Yy]$$ ]]; then \
		omnibump --deps deps.yaml --tidy; \
	fi

.PHONY: analyze-deps
analyze-deps:
	omnibump analyze --output yaml > deps-analysis.yaml
```

### Docker Integration

```dockerfile
FROM golang:1.21 AS builder

# Install omnibump
RUN wget https://github.com/chainguard-dev/omnibump/releases/latest/download/omnibump-linux-amd64 && \
    chmod +x omnibump-linux-amd64 && \
    mv omnibump-linux-amd64 /usr/local/bin/omnibump

# Update dependencies during build
COPY deps.yaml .
RUN omnibump --deps deps.yaml --tidy
```

## Security Considerations

### CVE Response Workflow

```bash
# 1. Identify vulnerable dependency
omnibump analyze --output json | jq '.analysis.dependencies[] | select(.name == "vulnerable-pkg")'

# 2. Test patch
omnibump --packages "vulnerable-pkg@patched-version" --dry-run

# 3. Apply and verify
omnibump --packages "vulnerable-pkg@patched-version"
make test
```

### Audit Trail

Keep records of dependency updates:

```bash
# Before update
omnibump analyze --output json > before-$(date +%Y%m%d).json

# Apply update
omnibump --deps deps.yaml

# After update
omnibump analyze --output json > after-$(date +%Y%m%d).json

# Commit both for audit trail
git add before-*.json after-*.json
git commit -m "Dependency update audit trail"
```

## Troubleshooting Advanced Issues

### Enable All Debug Output

```bash
# Maximum verbosity
omnibump --deps deps.yaml --log-level debug --show-diff --dry-run 2>&1 | tee omnibump-debug.log
```

### Inspect Internal State

```bash
# Analyze internal parsing
omnibump analyze --output json | jq '.'

# Check version resolution
omnibump --packages "pkg@latest" --dry-run --log-level debug
```

## Performance Profiling

For development or debugging performance issues:

```bash
# Time operations
time omnibump --deps deps.yaml

# Profile with Go tools (if building from source)
go build -o omnibump .
./omnibump --deps deps.yaml --log-level debug
```
