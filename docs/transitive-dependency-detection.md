# Transitive Dependency Detection

## Overview

Omnibump automatically detects when updating a Go package requires co-updating other dependencies in your project. This prevents build failures caused by incompatible transitive dependency versions.

## The Problem

When you update a dependency, its new version may require newer versions of other packages that your project also depends on. If those requirements aren't met, you'll get compilation errors.

### Example

Updating `oras.land/oras-go` from v1.2.5 to v1.2.7:

```bash
./omnibump --packages "oras.land/oras-go@v1.2.7"
```

Without transitive checking, this would:
- ✅ Successfully update oras-go to v1.2.7
- ❌ But oras-go v1.2.7 requires github.com/docker/docker@v28.5.1+
- ❌ Project has github.com/docker/docker@v28.0.0+
- ❌ Build fails with type incompatibility errors

## The Solution

Omnibump now:
1. Fetches the target version's `go.mod` from the Go module proxy
2. Compares its requirements against your current project versions
3. Detects incompatibilities (required version > current version)
4. Returns an error with the complete list of packages to update together

### Example Output

```
Error: the following dependencies need to be co-updated:
  - github.com/docker/cli: current v25.0.1+incompatible, required >= v28.5.1+incompatible
  - github.com/docker/docker: current v28.0.0+incompatible, required >= v28.5.1+incompatible
  - golang.org/x/crypto: current v0.41.0, required >= v0.43.0
  - golang.org/x/sync: current v0.16.0, required >= v0.17.0
  [... more dependencies ...]

To proceed, add these packages to your update:
  omnibump --packages "oras.land/oras-go@v1.2.7 github.com/docker/cli@v28.5.1+incompatible github.com/docker/docker@v28.5.1+incompatible ..."
```

Running the suggested command updates all dependencies together, ensuring build compatibility.

## How It Works

### Detection Process

1. **Version Resolution**: Each package version is resolved through `go list` to get the canonical form
2. **Proxy Fetch**: Target version's `go.mod` is fetched from `https://proxy.golang.org`
3. **Requirement Comparison**: Each requirement is compared against current project versions
4. **Conflict Detection**: Identifies packages where `current_version < required_version`
5. **Deduplication**: If multiple packages require the same dependency, keeps the highest version
6. **Validation**: Only reports dependencies NOT already in the update list

### Code Flow

```
Update()
  → updateSingleModule()
    → convertDependenciesToPackages()
    → resolveAndFilterPackages()
      → For each package:
        → resolveVersionQuery() - get canonical version
        → CheckTransitiveRequirements() - fetch & compare requirements
      → Build error if co-updates needed
    → DoUpdate() - perform the actual update
```

## Using This Feature

### Command Line

The feature is **automatic** when using omnibump CLI:

```bash
# Try to update a single package
./omnibump --packages "package@version"

# If co-updates needed, omnibump provides the complete command:
./omnibump --packages "package@version dep1@ver1 dep2@ver2 ..."
```

### Programmatic API

If you're building an application using omnibump as a library:

#### Option 1: Standard Interface (Automatic Detection)

```go
import (
    "context"
    "github.com/chainguard-dev/omnibump/pkg/languages"
    "github.com/chainguard-dev/omnibump/pkg/languages/golang"
)

func updateDependencies(ctx context.Context) error {
    // Get the Go language implementation
    lang := &golang.Golang{}

    // Configure the update
    cfg := &languages.UpdateConfig{
        RootDir: "path/to/project",
        Dependencies: []languages.Dependency{
            {
                Name:    "oras.land/oras-go",
                Version: "v1.2.7",
            },
        },
        Tidy: true,
    }

    // Call Update - transitive checking happens automatically
    err := lang.Update(ctx, cfg)
    if err != nil {
        // Error will contain list of missing co-updates
        return fmt.Errorf("update failed: %w", err)
    }

    return nil
}
```

**What happens:**
- `Update()` calls `resolveAndFilterPackages()`
- Automatically checks transitive requirements
- Returns error with detailed list if co-updates needed
- Error message includes suggested command

#### Option 2: Direct API (Just Check Requirements)

```go
import (
    "context"
    "github.com/chainguard-dev/omnibump/pkg/languages/golang"
)

func checkRequirements(ctx context.Context) error {
    // Parse current project's go.mod
    modFile, _, err := golang.ParseGoModfile("path/to/go.mod")
    if err != nil {
        return err
    }

    // Check what updating a package would require
    missingDeps, err := golang.CheckTransitiveRequirements(
        ctx,
        "oras.land/oras-go",
        "v1.2.7",
        modFile,
    )
    if err != nil {
        return err
    }

    // Process the results
    if len(missingDeps) > 0 {
        fmt.Printf("Found %d missing dependencies:\n", len(missingDeps))
        for _, dep := range missingDeps {
            fmt.Printf("  %s: current %s, required %s\n",
                dep.Package,
                dep.CurrentVersion,
                dep.RequiredVersion)
        }
    }

    return nil
}
```

**Returns:**
```go
type MissingDependency struct {
    Package         string  // e.g., "github.com/docker/docker"
    RequiredVersion string  // e.g., "v28.5.1+incompatible"
    CurrentVersion  string  // e.g., "v28.0.0+incompatible"
    Reason          string  // Human-readable explanation
}
```

## Configuration

No configuration required - the feature is enabled by default.

### Behavior

- **Automatic**: Runs during every update
- **Non-blocking for queries**: `@latest`, `@upgrade`, `@patch` work as before
- **Blocks on conflicts**: Specific versions with missing requirements return errors
- **Helpful errors**: Provides exact command to run with all required packages

## Implementation Details

### Key Functions

**`CheckTransitiveRequirements()`** (`pkg/languages/golang/indirect_resolver.go`)
- Fetches target version's go.mod from Go proxy
- Compares requirements against current project
- Returns list of missing dependencies

**`resolveAndFilterPackages()`** (`pkg/languages/golang/golang.go`)
- Resolves all package versions to canonical forms
- Calls `CheckTransitiveRequirements()` for each package
- Validates all packages being updated together
- Returns error if co-updates needed

### What's Checked

- ✅ All `require` statements in target version's go.mod
- ✅ Semantic version comparisons (using golang.org/x/mod/semver)
- ✅ Deduplication (highest required version wins)
- ✅ Already-updating packages (skips if already in update list)

### What's Skipped

- ⏭️ Dependencies not in current project (go get will add them)
- ⏭️ Non-semver versions (commit hashes, branches)
- ⏭️ Replaced dependencies (replace directives override)

## Real-World Example

### Scenario

Gatekeeper project needs CVE fix in `oras.land/oras-go@v1.2.7`

### Without This Feature

```bash
$ omnibump --packages "oras.land/oras-go@v1.2.7"
# Updates successfully ✓

$ make build
# Build fails with:
# vendor/oras.land/oras-go/pkg/auth/docker/login.go:102:69:
# cannot convert cred (variable of struct type
# "github.com/docker/docker/api/types/registry".AuthConfig)
# to type types.AuthConfig
```

**Problem**: oras-go v1.2.7 uses new Docker API, but project has old Docker version.

### With This Feature

```bash
$ omnibump --packages "oras.land/oras-go@v1.2.7"
Error: the following dependencies need to be co-updated:
  - github.com/docker/docker: current v28.0.0+incompatible, required >= v28.5.1+incompatible
  - github.com/docker/cli: current v25.0.1+incompatible, required >= v28.5.1+incompatible
  [... 13 more dependencies ...]

To proceed, add these packages to your update:
  omnibump --packages "oras.land/oras-go@v1.2.7 github.com/docker/docker@v28.5.1 ..."
```

Run the suggested command:
```bash
$ omnibump --packages "oras.land/oras-go@v1.2.7 github.com/docker/docker@v28.5.1 github.com/docker/cli@v28.5.1 ..."
# Updates all 17 packages ✓

$ make build
# Build succeeds ✓
```

## Benefits

✅ **Prevents broken builds**: Catches incompatibilities before they break compilation

✅ **Saves time**: No trial-and-error finding which packages need updating

✅ **Complete solutions**: Provides exact command with all required updates

✅ **Automatic**: No configuration or flags needed

✅ **Intelligent**: Deduplicates and validates across all packages being updated

## Limitations

- Only checks **direct requirements** (not transitive of transitive)
  - The recursive checking stops after one level
  - May need multiple iterations for deeply nested requirements

- Only compares **semantic versions**
  - Commit hashes and non-semver comparisons are skipped

- Requires **network access** to Go module proxy
  - Fetches go.mod files from https://proxy.golang.org

## Related Features

- **+incompatible Version Handling**: Automatically adds `+incompatible` suffix when needed
- **Indirect Dependency Resolution**: Finds which parent to bump for indirect CVEs
- **Workspace Support**: Works with go.work multi-module projects
- **Vendor Support**: Automatically runs `go mod tidy` before `go vendor`
