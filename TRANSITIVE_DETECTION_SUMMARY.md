# Transitive Dependency Detection - Summary

## What It Does

Automatically detects when updating a Go package requires co-updating other dependencies, preventing build failures from incompatible transitive dependencies.

## Key Features

**Automatic Detection**
- Fetches target version's go.mod from Go module proxy
- Compares requirements against current project versions
- Identifies all packages that need co-updating

**Helpful Error Messages**
- Lists all missing co-updates with current vs required versions
- Provides exact command to run with all required packages
- No guesswork - just copy and run the suggested command

**Built Into Core Library**
- Located in `pkg/languages/golang/`
- Works through standard `Language` interface
- Available to any application using omnibump as a library

## How Applications Use It

### Standard Interface (Automatic)

```go
lang := &golang.Golang{}
cfg := &languages.UpdateConfig{
    RootDir: "path/to/project",
    Dependencies: []languages.Dependency{
        {Name: "oras.land/oras-go", Version: "v1.2.7"},
    },
}
err := lang.Update(ctx, cfg)
// Returns error with co-update list if needed
```

### Direct API (Analysis Only)

```go
modFile, _, _ := golang.ParseGoModfile("go.mod")
missing, _ := golang.CheckTransitiveRequirements(ctx, "package", "version", modFile)
// Returns []golang.MissingDependency
```

## Real Example

**Before:**
```bash
$ omnibump --packages "oras.land/oras-go@v1.2.7"
$ make build
# Error: cannot convert AuthConfig type
```

**After:**
```bash
$ omnibump --packages "oras.land/oras-go@v1.2.7"
Error: the following dependencies need to be co-updated:
  - github.com/docker/docker: current v28.0.0, required >= v28.5.1
  - github.com/docker/cli: current v25.0.1, required >= v28.5.1
  [... 13 more ...]

To proceed:
  omnibump --packages "oras.land/oras-go@v1.2.7 github.com/docker/docker@v28.5.1 ..."

$ omnibump --packages "oras.land/oras-go@v1.2.7 github.com/docker/docker@v28.5.1 ..."
# All 17 packages updated ✓

$ make build
# Build succeeds ✓
```

## Implementation

**Core Functions:**
- `CheckTransitiveRequirements()` - Checks single package requirements
- `resolveAndFilterPackages()` - Validates all updates together
- `fetchGoModForPackage()` - Fetches go.mod from proxy

**Location:** `pkg/languages/golang/`
- `indirect_resolver.go` - CheckTransitiveRequirements implementation
- `golang.go` - Integration into update flow

## Benefits

✅ Prevents broken builds from incompatible dependencies
✅ Saves debugging time finding which packages need updating
✅ Provides exact commands - no trial and error
✅ Works automatically - no configuration needed
✅ Available to all applications using omnibump library
