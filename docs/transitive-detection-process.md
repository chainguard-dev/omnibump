# Transitive Dependency Detection - Step-by-Step Process

## Overview

This document explains exactly how omnibump detects when updating a package requires co-updating other dependencies.

## The Process

### Step 1: Fetch Target Version's go.mod from Go Proxy

When you update `oras.land/oras-go@v1.2.7`, omnibump fetches its go.mod file:

**URL:** `https://proxy.golang.org/oras.land/oras-go/@v/v1.2.7.mod`

**Response:**
```go
module oras.land/oras-go

go 1.24.0

require (
    github.com/containerd/containerd v1.7.28
    github.com/distribution/distribution/v3 v3.0.0
    github.com/docker/cli v28.5.1+incompatible
    github.com/docker/docker v28.5.1+incompatible
    github.com/docker/go-connections v0.6.0
    github.com/opencontainers/go-digest v1.0.0
    github.com/opencontainers/image-spec v1.1.1
    github.com/spf13/cobra v1.10.1
    github.com/stretchr/testify v1.11.1
    golang.org/x/crypto v0.43.0
    golang.org/x/sync v0.17.0
    // ... more
)
```

This tells us **what oras-go v1.2.7 needs to compile**.

### Step 2: Parse Current Project's go.mod

Read the local `gatekeeper/go.mod`:

```go
module github.com/open-policy-agent/gatekeeper/v3

go 1.24.0

require (
    oras.land/oras-go v1.2.5
    github.com/docker/cli v25.0.1+incompatible // indirect
    github.com/docker/docker v28.0.0+incompatible // indirect
    github.com/docker/go-connections v0.5.0 // indirect
    github.com/spf13/cobra v1.9.1
    github.com/stretchr/testify v1.10.0
    golang.org/x/crypto v0.41.0 // indirect
    golang.org/x/sync v0.16.0 // indirect
    // ... more
)
```

Build a map:
```go
currentVersions := map[string]string{
    "github.com/docker/cli": "v25.0.1+incompatible",
    "github.com/docker/docker": "v28.0.0+incompatible",
    "github.com/docker/go-connections": "v0.5.0",
    "github.com/spf13/cobra": "v1.9.1",
    "golang.org/x/crypto": "v0.41.0",
    // ... all requires from gatekeeper
}
```

### Step 3: Compare Requirements Against Current Versions

For **each** requirement in oras-go's go.mod, check if the current project has a compatible version:

#### Example 1: github.com/docker/cli

```go
// From oras-go@v1.2.7's go.mod
reqPkg := "github.com/docker/cli"
reqVer := "v28.5.1+incompatible"

// From gatekeeper/go.mod
currentVer := "v25.0.1+incompatible"

// Compare using semver
semver.Compare("v25.0.1+incompatible", "v28.5.1+incompatible") = -1

// Result: current < required ❌
// Add to missing list
missing = append(missing, MissingDependency{
    Package: "github.com/docker/cli",
    RequiredVersion: "v28.5.1+incompatible",
    CurrentVersion: "v25.0.1+incompatible",
    Reason: "oras.land/oras-go@v1.2.7 requires github.com/docker/cli@v28.5.1+incompatible but project has v25.0.1+incompatible",
})
```

#### Example 2: github.com/docker/docker

```go
// From oras-go@v1.2.7's go.mod
reqPkg := "github.com/docker/docker"
reqVer := "v28.5.1+incompatible"

// From gatekeeper/go.mod
currentVer := "v28.0.0+incompatible"

// Compare using semver
semver.Compare("v28.0.0+incompatible", "v28.5.1+incompatible") = -1

// Result: current < required ❌
// Add to missing list
missing = append(missing, MissingDependency{
    Package: "github.com/docker/docker",
    RequiredVersion: "v28.5.1+incompatible",
    CurrentVersion: "v28.0.0+incompatible",
})
```

#### Example 3: github.com/opencontainers/go-digest

```go
// From oras-go@v1.2.7's go.mod
reqPkg := "github.com/opencontainers/go-digest"
reqVer := "v1.0.0"

// From gatekeeper/go.mod
currentVer := "v1.0.0"

// Compare using semver
semver.Compare("v1.0.0", "v1.0.0") = 0

// Result: current == required ✅
// Skip - already at correct version
```

#### Example 4: github.com/distribution/distribution/v3

```go
// From oras-go@v1.2.7's go.mod
reqPkg := "github.com/distribution/distribution/v3"
reqVer := "v3.0.0"

// From gatekeeper/go.mod
currentVer := NOT FOUND (doesn't exist in gatekeeper)

// Result: not in current project
// Skip - go get will add it automatically when updating oras-go
```

#### Example 5: Hypothetical Higher Version

```go
// From oras-go@v1.2.7's go.mod
reqPkg := "github.com/google/uuid"
reqVer := "v1.5.0"

// From gatekeeper/go.mod
currentVer := "v1.6.0"

// Compare using semver
semver.Compare("v1.6.0", "v1.5.0") = 1

// Result: current > required ✅
// Skip - newer version is fine (backward compatible)
```

### Step 4: Aggregate and Deduplicate

If **multiple** packages being updated require the same dependency, keep the **highest** version:

```go
// libp2p requires quic-go@v0.58.0
// webtransport-go requires quic-go@v0.59.0

// Keep v0.59.0 (higher)
allMissingDeps["github.com/quic-go/quic-go"] = MissingDependency{
    RequiredVersion: "v0.59.0",
}
```

### Step 5: Filter Out Already-Updating Packages

If a missing dependency is **already in the update list**, check if the version is sufficient:

```go
// Missing: quic-go needs v0.59.0
// Already updating: quic-go@v0.59.0

// Compare
semver.Compare("v0.59.0", "v0.59.0") >= 0  // true

// Result: SKIP - already being updated to sufficient version ✅
```

### Step 6: Generate Error or Proceed

**If missing dependencies found:**
```
Error: the following dependencies need to be co-updated:
  - github.com/docker/cli: current v25.0.1, required >= v28.5.1
  - github.com/docker/docker: current v28.0.0, required >= v28.5.1
  ...

To proceed, add these packages to your update:
  omnibump --packages "oras.land/oras-go@v1.2.7 github.com/docker/cli@v28.5.1 ..."
```

**If all requirements satisfied:**
```
2026/03/06 13:06:34 INFO Update completed successfully
```

## Real-World Examples

### Example 1: Gatekeeper - Many Co-Updates

**Input:**
```bash
./omnibump --packages "oras.land/oras-go@v1.2.7"
```

**What Happens:**

1. Fetch `oras.land/oras-go@v1.2.7.mod` from proxy
2. It requires 15 packages at newer versions than gatekeeper has
3. **Result:** Error with list of 15 co-updates needed

**Why:** oras-go v1.2.7 jumped from Docker v25 → v28 API, requires many newer versions

---

### Example 2: helm-controller - No Co-Updates

**Input:**
```bash
./omnibump --packages "github.com/docker/cli@v29.2.0"
```

**What Happens:**

1. Fetch `github.com/docker/cli@v29.2.0.mod` from proxy
2. All its requirements are already satisfied by helm-controller's current versions
3. **Result:** Update succeeds, no co-updates needed

**Why:** helm-controller already has compatible versions of docker/cli's dependencies

---

### Example 3: RKE2 - Some Co-Updates

**Input:**
```bash
./omnibump --packages "github.com/libp2p/go-libp2p@v0.47.0 [... 6 more]"
```

**What Happens:**

1. Checks each of the 7 packages
2. Finds that `otel/sdk@v1.40.0` requires `otel@v1.40.0`, `otel/metric@v1.40.0`, `otel/trace@v1.40.0`
3. Finds that `nats-server@v2.12.3` requires `go-tpm@v0.9.7`, `nkeys@v0.4.12`
4. **Result:** Error with 7 additional co-updates needed

**Why:** Some packages have transitive dependencies that need bumping

## Decision Logic

```
FOR each package being updated:

    1. Fetch target version's go.mod from proxy

    2. FOR each requirement in target's go.mod:

        a. Does current project have this package?
           NO  → SKIP (go get will add it)
           YES → Continue to b

        b. Compare versions using semver:
           current < required  → ADD TO MISSING LIST ❌
           current == required → SKIP ✅
           current > required  → SKIP ✅ (backward compatible)

    3. Deduplicate missing list (keep highest version)

    4. Filter out packages already being updated

    5. If missing list not empty:
       → Return error with recommendations

    6. If missing list empty:
       → Proceed with update
```

## What Gets Checked

### Checked ✅
- All `require` statements in target version's go.mod
- Semantic version comparisons (v1.2.3 format)
- +incompatible versions (v28.5.1+incompatible)
- Indirect dependencies (marked with `// indirect`)

### Not Checked ⏭️
- Dependencies not in current project (will be added automatically)
- Commit hashes / non-semver versions
- Replace directives (replace overrides require)
- Build-time compatibility (only checks versions, not APIs)

## Performance

- **Network calls:** 1 HTTP request per package being updated
- **Cache:** Go module proxy responses are cached by HTTP client
- **Speed:** ~100-500ms per package check
- **Parallel:** Could be parallelized (currently sequential)

## Limitations

1. **One level deep:** Only checks direct requirements, not transitive of transitive
   - May need multiple iterations for deeply nested requirements

2. **Version only:** Can't detect breaking API changes within major version
   - If v28.0.0 removed an API that v28.5.1 doesn't have, we can't detect it

3. **Network required:** Must fetch from proxy.golang.org
   - Will fail if offline or proxy is down

4. **Semver only:** Non-semver versions (commit hashes, branches) are skipped
