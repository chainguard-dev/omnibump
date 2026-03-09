# Usage Examples

This document provides comprehensive examples for using omnibump with different language ecosystems.

## Go Projects

### Example 1: Update Single Dependency

```bash
# Update golang.org/x/sys to latest
omnibump --language go --packages "golang.org/x/sys@latest" --tidy
```

### Example 2: Update Multiple Dependencies

Create `deps.yaml`:

```yaml
# language field is optional - will auto-detect
packages:
  - name: golang.org/x/sys
    version: v0.28.0

  - name: golang.org/x/crypto
    version: v0.31.0

  - name: github.com/spf13/cobra
    version: v1.10.1
```

Run update:

```bash
omnibump --deps deps.yaml --tidy
```

### Example 3: Update with Module Replacement

Create `deps.yaml`:

```yaml
language: go

packages:
  - name: golang.org/x/net
    version: v0.32.0

replaces:
  - oldName: github.com/old/package
    name: github.com/new/package
    version: v2.0.0
```

Run update:

```bash
omnibump --deps deps.yaml
```

### Example 4: Update Workspace Projects

For projects using `go.work`:

```bash
# omnibump automatically detects and updates go.work
omnibump --deps deps.yaml --tidy
```

### Example 5: Analyze Go Dependencies

```bash
# Basic analysis
omnibump analyze .

# Check if specific version is already current
omnibump analyze --packages "golang.org/x/sys@latest"
```

### Example 6: Handling +incompatible Versions

Go modules with major version >= 2 that don't have `/vN` in their path require the `+incompatible` suffix. Omnibump handles this automatically:

```bash
# You specify without +incompatible
omnibump --packages "github.com/docker/docker@v28.0.0"

# Omnibump automatically resolves to canonical form
# Output: Resolved github.com/docker/docker@v28.0.0 to canonical form v28.0.0+incompatible

# Works with multiple packages
omnibump --packages "github.com/docker/docker@v28.0.0 github.com/docker/cli@v29.2.0"
# Both get +incompatible added automatically
```

**Why this matters:**
- Without automatic resolution, you'd get: `version "v28.0.0" invalid: should be v0 or v1, not v28`
- Omnibump queries the Go proxy to get the correct canonical version
- No need to remember which packages need `+incompatible`

### Example 7: Transitive Dependency Detection

Omnibump automatically detects when updating a package requires co-updating other dependencies:

```bash
# Try to update a single package
omnibump --packages "oras.land/oras-go@v1.2.7"

# Omnibump detects incompatibilities and provides guidance
# Output:
# Error: the following dependencies need to be co-updated:
#   - github.com/docker/docker: current v28.0.0, required >= v28.5.1
#   - github.com/docker/cli: current v25.0.1, required >= v28.5.1
#   - golang.org/x/crypto: current v0.41.0, required >= v0.43.0
#   [... more dependencies ...]
#
# To proceed, add these packages to your update:
#   omnibump --packages "oras.land/oras-go@v1.2.7 github.com/docker/docker@v28.5.1 github.com/docker/cli@v28.5.1 ..."

# Run the suggested command
omnibump --packages "oras.land/oras-go@v1.2.7 github.com/docker/docker@v28.5.1 github.com/docker/cli@v28.5.1 golang.org/x/crypto@v0.43.0 ..."
# All packages updated successfully, build will work
```

**Why this matters:**
- Prevents build failures from incompatible dependency versions
- Saves time debugging type mismatch errors
- Provides exact command to run - no trial and error
- Validates entire update set together

**How it works:**
1. Fetches target version's go.mod from Go module proxy
2. Compares its requirements against your current versions
3. Detects packages where `current < required`
4. Provides complete list of co-updates needed

See [Transitive Dependency Detection](transitive-dependency-detection.md) for detailed documentation.

## Rust Projects

### Example 1: Update Cargo Dependencies

Create `deps.yaml`:

```yaml
# language field is optional - will auto-detect
packages:
  - name: tokio
    version: 1.42.0

  - name: serde
    version: 1.0.217

  - name: serde_json
    version: 1.0.135
```

Run update:

```bash
omnibump --deps deps.yaml
```

### Example 2: Update with cargo update

Some Rust projects benefit from running `cargo update` first:

```bash
# Run cargo update before applying specific version pins
omnibump --deps deps.yaml --tidy
```

### Example 3: Update Specific Version of Package

For packages with multiple versions in `Cargo.lock`:

```yaml
language: rust

packages:
  # Update specific version of syn
  - name: syn
    version: 2.0.90
```

```bash
omnibump --deps deps.yaml
```

### Example 4: Inline Package Updates

```bash
# Quick inline update
omnibump --language rust --packages "tokio@1.42.0 serde@1.0.217"
```

### Example 5: Analyze Rust Dependencies

```bash
# See all dependencies in Cargo.lock
omnibump analyze .

# Check for version conflicts
omnibump analyze --output json > analysis.json
```

## Java (Maven) Projects

### Example 1: Update Dependencies Directly

Create `deps.yaml`:

```yaml
# language field is optional - will auto-detect
packages:
  - groupId: io.netty
    artifactId: netty-codec-http
    version: 4.1.94.Final

  - groupId: org.slf4j
    artifactId: slf4j-api
    version: 2.0.16

  - groupId: junit
    artifactId: junit
    version: 4.13.2
    scope: test
```

Run update:

```bash
omnibump --deps deps.yaml
```

### Example 2: Update via Properties (Recommended)

For dependencies that use Maven properties like `${slf4j.version}`:

Create `properties.yaml`:

```yaml
properties:
  - property: slf4j.version
    value: 2.0.16

  - property: netty.version
    value: 4.1.94.Final

  - property: junit.version
    value: 4.13.2
```

Run update:

```bash
omnibump --properties properties.yaml
```

### Example 3: Combined Updates (Properties + Direct)

```bash
# Update both properties and direct dependencies
omnibump --deps deps.yaml --properties properties.yaml
```

### Example 4: Analyze Maven Project

```bash
# Analyze which dependencies use properties
omnibump analyze .
```

Example output:
```
Dependency Analysis
==================

Language: java
Total dependencies: 15
Dependencies using properties: 12
Properties defined: 5

Property Usage:
---------------
  slf4j.version = 2.0.13 (used by 3 dependencies)
  netty.version = 4.1.90.Final (used by 8 dependencies)
  junit.version = 4.13.2 (used by 1 dependency)
```

### Example 5: Get Update Recommendations

```bash
# Analyze what needs updating and get recommendations
omnibump analyze --packages "io.netty@netty-codec-http@4.1.94.Final" \
  --output-deps deps.yaml \
  --output-props properties.yaml
```

This generates configuration files based on your project's structure.

### Example 6: Multi-Module Maven Projects

```bash
# Works automatically with multi-module projects
cd my-maven-project
omnibump analyze

# Update from root directory
omnibump --deps deps.yaml
```

### Example 7: Inline Maven Updates

```bash
# Update using inline format: groupId@artifactId@version
omnibump --packages "io.netty@netty-codec-http@4.1.94.Final junit@junit@4.13.2@test"
```

## Java (Gradle) Projects

### Example 1: Update Gradle Dependencies (String Notation)

For projects using string notation like `implementation("group:artifact:version")`:

Create `deps.yaml`:

```yaml
# language field is optional - will auto-detect
packages:
  - name: "org.apache.commons:commons-lang3"
    version: "3.18.0"

  - name: "io.netty:netty-all"
    version: "4.1.101.Final"

  - name: "junit:junit"
    version: "4.13.3"
    scope: test
```

Run update:

```bash
omnibump --deps deps.yaml
```

This updates dependencies in `build.gradle` or `build.gradle.kts`:

```kotlin
// Before
implementation("org.apache.commons:commons-lang3:3.12.0")

// After
implementation("org.apache.commons:commons-lang3:3.18.0")
```

### Example 2: Spring Boot library() Pattern

For Spring Boot projects using the `library()` function:

```yaml
packages:
  # Use the display name as it appears in library()
  - name: "org.apache.commons:Commons Lang3"
    version: "3.18.0"

  - name: "io.netty:Netty"
    version: "4.1.101.Final"
```

Run update:

```bash
omnibump --deps deps.yaml --dir spring-boot-project/spring-boot-dependencies
```

This updates:

```groovy
// Before
library("Commons Lang3", "3.17.0") {
  group("org.apache.commons") {
    modules = ["commons-lang3"]
  }
}

// After
library("Commons Lang3", "3.18.0") {
  group("org.apache.commons") {
    modules = ["commons-lang3"]
  }
}
```

### Example 3: CVE Remediation (Replacing sed)

**Old approach (manual sed):**

```bash
sed -i 's/library("Commons Lang3", "3.17.0")/library("Commons Lang3", "3.18.0")/g' \
  spring-boot-project/spring-boot-dependencies/build.gradle
```

**New approach (omnibump):**

```bash
omnibump --packages "org.apache.commons:Commons Lang3@3.18.0" \
  --dir spring-boot-project/spring-boot-dependencies
```

Benefits:
- Type-safe (no typos in sed patterns)
- Works across Groovy and Kotlin DSL
- Handles multiple dependency patterns automatically
- Shows clear diffs with `--show-diff`

### Example 4: Multi-Module Gradle Projects

For projects with subprojects:

```bash
# Update root build.gradle
omnibump --deps deps.yaml --dir .

# Or update specific subproject
omnibump --deps deps.yaml --dir subproject-name
```

### Example 5: Inline Gradle Updates

```bash
# Quick CVE fix
omnibump --language java \
  --packages "org.apache.commons:commons-lang3@3.18.0 io.netty:netty-all@4.1.101.Final"

# With dry run to preview
omnibump --packages "org.apache.commons:commons-lang3@3.18.0" \
  --dry-run --show-diff
```

### Example 6: Gradle Wrapper Projects

Projects using `gradlew` work automatically:

```bash
# omnibump updates build.gradle[.kts]
omnibump --deps deps.yaml

# Then build with gradlew as usual
./gradlew build
```

### Example 7: Both Groovy and Kotlin DSL

omnibump supports both DSL formats transparently:

```yaml
# Same configuration works for both
packages:
  - name: "org.springframework.boot:spring-boot-dependencies"
    version: "3.2.0"
```

Works with:
- `build.gradle` (Groovy DSL)
- `build.gradle.kts` (Kotlin DSL)

## Cross-Language Projects

### Example 1: Automatic Detection

```bash
# omnibump detects the language automatically
cd any-project
omnibump analyze
omnibump --deps deps.yaml
```

### Example 2: Explicit Language Selection

```bash
# Force specific language if detection fails
omnibump --language go --deps deps.yaml
omnibump --language rust --deps deps.yaml
omnibump --language java --deps deps.yaml
```
