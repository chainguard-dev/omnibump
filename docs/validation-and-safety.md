# Validation and Safety Rules

omnibump includes built-in validation to prevent common mistakes and respect language ecosystem conventions.

## Go Module Validation

### Replace Directive Precedence (Go-specific behavior)

- In Go, `replace` directives take precedence over `require` directives
- When a package has a replace, omnibump validates against the **replacement version only**
- The require directive is ignored during validation

```yaml
# Example go.mod:
# replace github.com/example/pkg => github.com/example/pkg v1.1.0
# require github.com/example/pkg v1.4.0
#
# Actual version used: v1.1.0 (replace takes precedence)
# omnibump will allow: v1.2.0 (upgrade from v1.1.0)
# omnibump will block: v1.0.0 (downgrade from v1.1.0)
```

### Downgrade Prevention

- Prevents accidental downgrades in both `require` and `replace` directives
- Only allows version upgrades (higher semantic versions)

```bash
# Current: github.com/example/pkg v2.0.0
# Attempting: v1.9.0
# Result: ERROR - downgrade blocked
```

### Main Module Protection

- Prevents bumping the main module itself (protects against accidental self-updates)

```bash
# go.mod: module github.com/myorg/myproject
# Attempting: github.com/myorg/myproject@v2.0.0
# Result: ERROR - main module bump blocked
```

### Transitive Dependency Validation (Go)

- Automatically detects when updating a package requires co-updating other dependencies
- Prevents build failures from incompatible transitive dependency versions
- Provides exact command to run with all required co-updates

```bash
# Attempt to update single package
omnibump --packages "oras.land/oras-go@v1.2.7"

# Omnibump detects incompatibilities:
# Error: the following dependencies need to be co-updated:
#   - github.com/docker/docker: current v28.0.0, required >= v28.5.1
#   - github.com/docker/cli: current v25.0.1, required >= v28.5.1
#
# To proceed, add these packages to your update:
#   omnibump --packages "oras.land/oras-go@v1.2.7 github.com/docker/docker@v28.5.1 ..."
```

**How it works:**
1. Fetches target version's go.mod from Go module proxy
2. Compares requirements against current project versions
3. Detects packages where `current_version < required_version`
4. Returns error with complete list of missing co-updates

**Benefits:**
- Prevents compilation errors from type mismatches
- Saves debugging time
- Ensures all dependencies are compatible before updating
- Works in both `update` and `analyze` commands

See [Transitive Dependency Detection](transitive-dependency-detection.md) for details.

### +incompatible Version Resolution (Go)

- Automatically resolves versions to canonical forms with `+incompatible` suffix
- Prevents invalid go.mod entries for packages with major version >= 2

```bash
# You specify: v28.0.0
omnibump --packages "github.com/docker/docker@v28.0.0"

# Omnibump queries Go proxy and resolves to: v28.0.0+incompatible
# Result: Valid go.mod entry created
```

**Why this matters:**
- Go modules with major version >= 2 must have `/vN` in path OR `+incompatible` suffix
- Packages like `github.com/docker/docker` don't have `/v28` in their path
- Without `+incompatible`, go.mod becomes invalid: `version "v28.0.0" invalid: should be v0 or v1`
- Omnibump handles this automatically by querying the Go proxy for canonical version

**What's normalized:**
- Semantic versions: `v28.0.0` → `v28.0.0+incompatible`
- Pseudo-versions: Converted to canonical form
- Commit hashes: Resolved to tagged versions when available

## Maven Validation

### Downgrade Prevention

- Prevents downgrades in both direct dependencies and property-based versions

### Scope Preservation

- Maintains dependency scope (compile, test, runtime, provided) during updates

## Gradle Validation

### Format Detection

- Automatically handles both Groovy DSL and Kotlin DSL
- Preserves exact formatting and structure

## Rust Validation

### Lock File Integrity

- Maintains Cargo.lock consistency during updates

## Cross-Language Safety

All languages benefit from:

- **Dry run validation**: Test changes before applying
- **Diff preview**: See exactly what will change
- **Version format validation**: Ensures versions match ecosystem conventions
- **File integrity**: Preserves comments, formatting, and structure
