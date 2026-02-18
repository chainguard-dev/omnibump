# omnibump

![omnibump](docs/images/omnibump-linky.png)

**Dependency version management tool**

`omnibump` is a CLI tool for updating dependency versions across multiple language ecosystems with an easy-to-use interface with automatic language detection.

## Features

- **Multi-Language Support**: Go, Rust, and Java (Maven, Gradle)
- **Automatic Detection**: Identifies project language automatically
- **Unified Configuration**: Single configuration format across all languages
- **Property-Based Updates**: Smart property management for Maven
- **Version Resolution**: Resolves `@latest` queries without spurious changes
- **Dependency Analysis**: Understand project's dependency structure
- **Dry Run Mode**: Preview changes before applying
- **Backward Compatible**: Works with legacy configuration file names

## Supported Languages

| Language | Build Tool | Manifest Files |
|----------|-----------|----------------|
| Go | Go Modules | `go.mod`, `go.sum` |
| Rust | Cargo | `Cargo.lock`, `Cargo.toml` |
| Java | Maven | `pom.xml` |
| Java | Gradle | `build.gradle`, `build.gradle.kts` |

## Validation and Safety Rules

omnibump includes built-in validation to prevent common mistakes and respect language ecosystem conventions:

### Go Module Validation

**Replace Directive Precedence** (Go-specific behavior)
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

**Downgrade Prevention**
- Prevents accidental downgrades in both `require` and `replace` directives
- Only allows version upgrades (higher semantic versions)

```bash
# Current: github.com/example/pkg v2.0.0
# Attempting: v1.9.0
# Result: ERROR - downgrade blocked
```

**Main Module Protection**
- Prevents bumping the main module itself (protects against accidental self-updates)

```bash
# go.mod: module github.com/myorg/myproject
# Attempting: github.com/myorg/myproject@v2.0.0
# Result: ERROR - main module bump blocked
```

### Maven Validation

**Downgrade Prevention**
- Prevents downgrades in both direct dependencies and property-based versions

**Scope Preservation**
- Maintains dependency scope (compile, test, runtime, provided) during updates

### Gradle Validation

**Format Detection**
- Automatically handles both Groovy DSL and Kotlin DSL
- Preserves exact formatting and structure

### Rust Validation

**Lock File Integrity**
- Maintains Cargo.lock consistency during updates

### Cross-Language Safety

All languages benefit from:
- **Dry run validation**: Test changes before applying
- **Diff preview**: See exactly what will change
- **Version format validation**: Ensures versions match ecosystem conventions
- **File integrity**: Preserves comments, formatting, and structure

## Installation

### From Source (Recommended)

```bash
git clone https://github.com/chainguard-dev/mono/omnibump
cd mono/omnibump
make build
sudo make install
```

### Manual Build

```bash
git clone https://github.com/chainguard-dev/omnibump
cd omnibump
go build -o omnibump .
sudo mv omnibump /usr/local/bin/
```

### Verify Installation

```bash
omnibump version
```

### Build Targets

The Makefile provides several useful targets for building and developing omnibump:

#### Building

```bash
# Build the binary with version information embedded
make build
```

The build process automatically injects version information using ldflags:
- `GIT_VERSION` - Git tag or commit (e.g., `v1.0.0` or `abc1234`)
- `GIT_COMMIT` - Short commit hash
- `GIT_TREE_STATE` - Whether the working tree is clean or dirty
- `BUILD_DATE` - Timestamp of the build

Example output:
```
Building omnibump...
  Version:    v1.0.0-3-gabc1234-dirty
  Commit:     abc1234
  Tree State: dirty
  Build Date: 2025-11-12T14:23:45Z
Build complete: ./omnibump
```

#### Installing

```bash
# Install to $GOPATH/bin (typically ~/go/bin)
make install
```

This builds the binary with version information and installs it to your Go binary path.

#### Testing

```bash
# Run all tests
make test

# Run tests with coverage report (generates coverage.html)
make test-coverage
```

The test-coverage target creates an HTML coverage report that you can open in your browser.

#### Development Tasks

```bash
# Format Go code
make fmt

# Tidy and verify go modules
make tidy

# Vendor dependencies (creates vendor/ directory)
make vendor

# Run golangci-lint (if installed)
make lint
```

#### Cleanup

```bash
# Remove built binaries and clean build artifacts
make clean
```

#### Version Information

```bash
# Display version information that will be embedded in the binary
make version

# Build and run the version command
make run-version
```

Example version output:
```
Version Information:
  GIT_VERSION:    v1.0.0
  GIT_COMMIT:     abc1234
  GIT_TREE_STATE: clean
  BUILD_DATE:     2025-11-12T14:23:45Z
```

#### Help

```bash
# Display all available make targets with descriptions
make help
```

## Quick Start

### 1. Analyze Your Project

Before updating dependencies, analyze your project to understand its structure:

```bash
# Analyze current directory
omnibump analyze

# Analyze specific directory
omnibump analyze /path/to/project

# Get recommendations for updating specific dependencies
omnibump analyze --packages "golang.org/x/sys@v0.28.0"
```

### 2. Update Dependencies

```bash
# Using configuration file
omnibump --deps deps.yaml

# Using inline packages
omnibump --packages "golang.org/x/sys@v0.28.0"

# Dry run first (recommended)
omnibump --deps deps.yaml --dry-run

# With automatic tidying
omnibump --deps deps.yaml --tidy
```

## Usage Examples

### Go Projects

#### Example 1: Update Single Dependency

```bash
# Update golang.org/x/sys to latest
omnibump --language go --packages "golang.org/x/sys@latest" --tidy
```

#### Example 2: Update Multiple Dependencies

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

#### Example 3: Update with Module Replacement

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

#### Example 4: Update Workspace Projects

For projects using `go.work`:

```bash
# omnibump automatically detects and updates go.work
omnibump --deps deps.yaml --tidy
```

#### Example 5: Analyze Go Dependencies

```bash
# Basic analysis
omnibump analyze .

# Check if specific version is already current
omnibump analyze --packages "golang.org/x/sys@latest"
```

### Rust Projects

#### Example 1: Update Cargo Dependencies

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

#### Example 2: Update with cargo update

Some Rust projects benefit from running `cargo update` first:

```bash
# Run cargo update before applying specific version pins
omnibump --deps deps.yaml --tidy
```

#### Example 3: Update Specific Version of Package

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

#### Example 4: Inline Package Updates

```bash
# Quick inline update
omnibump --language rust --packages "tokio@1.42.0 serde@1.0.217"
```

#### Example 5: Analyze Rust Dependencies

```bash
# See all dependencies in Cargo.lock
omnibump analyze .

# Check for version conflicts
omnibump analyze --output json > analysis.json
```

### Java (Maven) Projects

#### Example 1: Update Dependencies Directly

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

#### Example 2: Update via Properties (Recommended)

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

#### Example 3: Combined Updates (Properties + Direct)

```bash
# Update both properties and direct dependencies
omnibump --deps deps.yaml --properties properties.yaml
```

#### Example 4: Analyze Maven Project

```bash
# Analyze which dependencies use properties
omnibump analyze .
```

Example output:
```
Dependency Analysis
==================
>>>>>>> c648b0e (omnibump(golang): respect Go replace directive precedence (#31179))

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

#### Example 5: Get Update Recommendations

```bash
# Analyze what needs updating and get recommendations
omnibump analyze --packages "io.netty@netty-codec-http@4.1.94.Final" \
  --output-deps deps.yaml \
  --output-props properties.yaml
```

This generates configuration files based on your project's structure.

#### Example 6: Multi-Module Maven Projects

```bash
# Works automatically with multi-module projects
cd my-maven-project
omnibump analyze

# Update from root directory
omnibump --deps deps.yaml
```

#### Example 7: Inline Maven Updates

```bash
# Update using inline format: groupId@artifactId@version
omnibump --packages "io.netty@netty-codec-http@4.1.94.Final junit@junit@4.13.2@test"
```

### Java (Gradle) Projects

#### Example 1: Update Gradle Dependencies (String Notation)

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

#### Example 2: Spring Boot library() Pattern

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

#### Example 3: CVE Remediation (Replacing sed)

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

#### Example 4: Multi-Module Gradle Projects

For projects with subprojects:

```bash
# Update root build.gradle
omnibump --deps deps.yaml --dir .

# Or update specific subproject
omnibump --deps deps.yaml --dir subproject-name
```

#### Example 5: Inline Gradle Updates

```bash
# Quick CVE fix
omnibump --language java \
  --packages "org.apache.commons:commons-lang3@3.18.0 io.netty:netty-all@4.1.101.Final"

# With dry run to preview
omnibump --packages "org.apache.commons:commons-lang3@3.18.0" \
  --dry-run --show-diff
```

#### Example 6: Gradle Wrapper Projects

Projects using `gradlew` work automatically:

```bash
# omnibump updates build.gradle[.kts]
omnibump --deps deps.yaml

# Then build with gradlew as usual
./gradlew build
```

#### Example 7: Both Groovy and Kotlin DSL

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

### Cross-Language Projects

#### Example 1: Automatic Detection

```bash
# omnibump detects the language automatically
cd any-project
omnibump analyze
omnibump --deps deps.yaml
```

#### Example 2: Explicit Language Selection

```bash
# Force specific language if detection fails
omnibump --language go --deps deps.yaml
omnibump --language rust --deps deps.yaml
omnibump --language java --deps deps.yaml
```

## Configuration Files

### Unified Format (deps.yaml)

The modern, unified configuration format that works across all languages:

```yaml
# Language field is OPTIONAL - will auto-detect if omitted
# You can also explicitly set: go, rust, java, or auto
language: auto

packages:
  # Go/Rust format
  - name: package-name
    version: 1.2.3

  # Maven format
  - groupId: com.example
    artifactId: library
    version: 1.0.0
    scope: compile      # optional: compile, test, runtime, provided
    type: jar           # optional: jar, war, pom, etc.
```

**Minimal example (language auto-detected):**

```yaml
packages:
  - name: golang.org/x/sys
    version: v0.28.0
```

### Properties File (properties.yaml)

For Maven projects using properties:

```yaml
properties:
  - property: property-name
    value: property-value

  - property: slf4j.version
    value: 2.0.16
```

### Replaces File (replaces.yaml)

For Go module replacements:

```yaml
replaces:
  - oldName: github.com/old/package
    name: github.com/new/package
    version: v2.0.0
```

### Legacy File Names (Backward Compatible)

omnibump automatically recognizes and migrates from legacy file names:

| Legacy Name | New Name |
|------------|----------|
| `gobump-deps.yaml` | `deps.yaml` |
| `cargobump-deps.yaml` | `deps.yaml` |
| `pombump-deps.yaml` | `deps.yaml` |
| `pombump-properties.yaml` | `properties.yaml` |
| `gobump-replaces.yaml` | `replaces.yaml` |

## CLI Reference

### Main Command

```bash
omnibump [flags]
```

#### Flags

| Flag | Short | Description | Default |
|------|-------|-------------|---------|
| `--language` | `-l` | Language (auto, go, rust, java) | `auto` |
| `--deps` | | Dependencies file path | |
| `--properties` | | Properties file path (Maven) | |
| `--packages` | | Inline package list | |
| `--props` | | Inline properties list | |
| `--dir` | | Project root directory | `.` |
| `--tidy` | | Run tidy command after update | `false` |
| `--show-diff` | | Show diff of changes | `false` |
| `--dry-run` | | Simulate without changes | `false` |
| `--log-level` | | Log level (debug, info, warn, error) | `info` |

### Analyze Command

```bash
omnibump analyze [project-path] [flags]
```

#### Flags

| Flag | Short | Description | Default |
|------|-------|-------------|---------|
| `--language` | `-l` | Force language detection | `auto` |
| `--output` | | Output format (text, json, yaml) | `text` |
| `--deps` | | Dependencies file to analyze | |
| `--packages` | | Inline packages to analyze | |
| `--output-deps` | | Write deps recommendations to file | |
| `--output-props` | | Write properties recommendations to file | |

### Supported Command

```bash
omnibump supported
```

Display all supported languages and build systems. Useful for understanding what omnibump can handle.

Example output:
```
Supported Languages and Build Systems
=====================================

Language: java
  Detects: [pom.xml build.gradle build.gradle.kts ...]
  Build Tools:
    - Maven (pom.xml)
    - Gradle (build.gradle, build.gradle.kts)

Language: go
  Detects: [go.mod go.sum go.work]

Language: rust
  Detects: [Cargo.toml Cargo.lock]
```

### Version Command

```bash
omnibump version
```

## Common Workflows

### Workflow 1: CVE Response

When a CVE is announced, quickly patch affected dependencies:

```bash
# Step 1: Analyze current state
omnibump analyze --output json > before.json

# Step 2: Create patch configuration
cat > deps.yaml <<EOF
language: auto
packages:
  - name: vulnerable-package
    version: 1.2.3-patched
EOF

# Step 3: Test update (dry run)
omnibump --deps deps.yaml --dry-run

# Step 4: Apply update
omnibump --deps deps.yaml --tidy

# Step 5: Verify
omnibump analyze --output json > after.json
```

### Workflow 2: Batch Updates

Update multiple projects with the same dependencies:

```bash
# Create shared configuration
cat > shared-deps.yaml <<EOF
language: auto
packages:
  - name: common-lib
    version: 2.0.0
EOF

# Update all projects
for project in project-a project-b project-c; do
  cd $project
  omnibump --deps ../shared-deps.yaml --dry-run
  omnibump --deps ../shared-deps.yaml
  cd ..
done
```

### Workflow 3: Migration from Legacy Tools

Migrating from gobump, cargobump, or pombump:

```bash
# Your existing files work automatically
omnibump --deps gobump-deps.yaml

# Or rename them (optional)
mv gobump-deps.yaml deps.yaml
omnibump --deps deps.yaml

# The tool warns about legacy names and suggests migration
```

### Workflow 4: CI/CD Integration

Example GitHub Actions workflow:

```yaml
name: Update Dependencies
on:
  workflow_dispatch:
    inputs:
      package:
        description: 'Package to update (name@version)'
        required: true

jobs:
  update:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Install omnibump
        run: |
          wget https://github.com/chainguard-dev/omnibump/releases/latest/download/omnibump-linux-amd64
          chmod +x omnibump-linux-amd64
          sudo mv omnibump-linux-amd64 /usr/local/bin/omnibump

      - name: Update dependency
        run: |
          omnibump --packages "${{ github.event.inputs.package }}" --tidy

      - name: Create PR
        uses: peter-evans/create-pull-request@v5
        with:
          commit-message: "Update ${{ github.event.inputs.package }}"
          title: "Dependency Update: ${{ github.event.inputs.package }}"
```

### Workflow 5: Property Management (Maven)

Understand and manage Maven properties:

```bash
# Step 1: Analyze property usage
omnibump analyze > analysis.txt

# Step 2: Identify which dependencies use properties
grep "used by" analysis.txt

# Step 3: Update properties (affects multiple dependencies)
cat > properties.yaml <<EOF
properties:
  - property: netty.version
    value: 4.1.94.Final
EOF

omnibump --properties properties.yaml

# This single property update affects all netty dependencies
```

## Package Format Reference

### Go

```yaml
packages:
  - name: module-path
    version: v1.2.3
```

**Inline format:**
```bash
--packages "module-path@v1.2.3"
```

**Special versions:**
- `@latest` - Latest available version
- `@v1` - Latest v1.x.x version
- `@v1.2` - Latest v1.2.x version

### Rust

```yaml
packages:
  - name: crate-name
    version: 1.2.3
```

**Inline format:**
```bash
--packages "crate-name@1.2.3"
```

### Maven

```yaml
packages:
  - groupId: com.example
    artifactId: library
    version: 1.2.3
    scope: compile      # optional
    type: jar           # optional
```

**Inline format:**
```bash
--packages "groupId@artifactId@version"
--packages "groupId@artifactId@version@scope"
--packages "groupId@artifactId@version@scope@type"
```

**Examples:**
```bash
--packages "io.netty@netty-codec-http@4.1.94.Final"
--packages "junit@junit@4.13.2@test"
--packages "org.example@custom@1.0.0@compile@war"
```

### Gradle

```yaml
packages:
  # Standard format (groupId:artifactId)
  - name: "org.apache.commons:commons-lang3"
    version: "3.18.0"

  # For Spring Boot library() - use display name
  - name: "org.apache.commons:Commons Lang3"
    version: "3.18.0"
```

**Inline format:**
```bash
# String notation dependencies
--packages "groupId:artifactId@version"

# Spring Boot library() dependencies (use display name)
--packages "groupId:Display Name@version"
```

**Examples:**
```bash
# Standard Gradle dependencies
--packages "org.apache.commons:commons-lang3@3.18.0"
--packages "io.netty:netty-all@4.1.101.Final"

# Spring Boot library() pattern
--packages "org.apache.commons:Commons Lang3@3.18.0"
```

**Note:** Gradle uses `:` separator in package names (not `@` like Maven). The `@` only appears before the version in inline format.

## Advanced Usage

### Debug Mode

```bash
# Enable debug logging
omnibump --deps deps.yaml --log-level debug
```

### Custom Log Output

```bash
# Log to file
omnibump --deps deps.yaml --log-policy /tmp/omnibump.log
```

### Show Changes

```bash
# Display diff of all changes
omnibump --deps deps.yaml --show-diff
```

### Validate Without Updating

```bash
# Dry run shows what would change
omnibump --deps deps.yaml --dry-run --show-diff
```

### Analyze with Output Files

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

### JSON/YAML Output for Automation

```bash
# JSON output for parsing
omnibump analyze --output json | jq '.analysis.dependencies'

# YAML output
omnibump analyze --output yaml > analysis.yaml
```

## Troubleshooting

### Issue: Language Detection Fails

```
ERROR: failed to detect language
```

**Solution:** Specify language explicitly
```bash
omnibump --language go --deps deps.yaml
```

### Issue: Dependency Not Found

```
ERROR: dependency not found: package-name
```

**Solutions:**
1. Verify the package name is correct
2. For Maven: Check groupId and artifactId
3. For Go: Ensure module path is complete
4. Run analyze first to see existing dependencies

### Issue: Version Already Current

```
INFO: Package xyz is already at v1.2.3, skipping
```

This is normal behavior. omnibump skips unnecessary updates to avoid spurious changes.

### Issue: Property Not Found (Maven)

```
ERROR: property not defined: property-name
```

**Solution:** Add property definition to your pom.xml first, or use direct dependency update instead.

### Issue: Permission Denied

```
ERROR: failed to write file: permission denied
```

**Solution:** Ensure you have write permissions to the project directory:
```bash
ls -la go.mod  # Check file permissions
sudo chown $USER:$USER go.mod  # Fix if needed
```

### Issue: go.mod vs go.work Conflicts

```
ERROR: version mismatch between go.mod and go.work
```

**Solution:** omnibump automatically handles workspaces. If you see this error, ensure go.work and go.mod are synchronized:
```bash
go work sync
omnibump --deps deps.yaml
```

### Issue: Downgrade Prevention

```
WARN: Refusing to downgrade package from v2.0.0 to v1.9.0
```

This is a safety feature. To force a downgrade, update manually or adjust your deps.yaml.

## Best Practices

### 1. Always Use Dry Run First

```bash
omnibump --deps deps.yaml --dry-run
```

Preview changes before applying them.

### 2. Analyze Before Updating

```bash
omnibump analyze
```

Understand your project structure before making changes.

### 3. Use Version Control

```bash
git add -A
git commit -m "Pre-omnibump state"
omnibump --deps deps.yaml
git diff  # Review changes
```

### 4. Run Tests After Updates

```bash
omnibump --deps deps.yaml --tidy
make test  # or your test command
```

### 5. Use Properties for Maven Projects

When multiple dependencies share the same version, use properties:

```yaml
# Better: Update property once
properties:
  - property: netty.version
    value: 4.1.94.Final

# Affects all netty-* dependencies
```

### 6. Keep Configuration in Version Control

```bash
git add deps.yaml properties.yaml
git commit -m "Add omnibump configuration"
```

This makes updates reproducible and auditable.

### 7. Document Why

Add comments to your configuration:

```yaml
# CVE-2024-12345: netty vulnerability patch
packages:
  - groupId: io.netty
    artifactId: netty-codec-http
    version: 4.1.94.Final
```

## Migration Guide

### From gobump

```bash
# Your gobump-deps.yaml works directly
omnibump --deps gobump-deps.yaml --tidy

# Optional: Rename to new standard
mv gobump-deps.yaml deps.yaml
```

### From cargobump

```bash
# Your cargobump-deps.yaml works directly
omnibump --deps cargobump-deps.yaml

# Optional: Rename to new standard
mv cargobump-deps.yaml deps.yaml
```

### From pombump

```bash
# Your pombump files work directly
omnibump --deps pombump-deps.yaml --properties pombump-properties.yaml

# Optional: Rename to new standard
mv pombump-deps.yaml deps.yaml
mv pombump-properties.yaml properties.yaml
```

### Unified Configuration

You can now use a single configuration format across all tools:

```yaml
# language field is optional - will auto-detect
packages:
  # Works for Go, Rust, and Maven
  - name: package-name
    version: 1.2.3
```

## FAQ

### Q: Can omnibump update multiple languages in one command?

A: Not directly. omnibump operates on one language at a time. For monorepos with multiple languages, run omnibump separately in each project directory.

### Q: Does omnibump modify my source code?

A: No. omnibump only modifies manifest files (go.mod, Cargo.lock, pom.xml), not your source code.

### Q: Can I use omnibump in CI/CD?

A: Yes! omnibump is designed for automation. Use `--dry-run` for validation and regular mode for updates.

### Q: What if I want to update all dependencies to latest?

A: Use the analyze command to discover dependencies, then create a configuration file. Future versions may add an "update all" feature.

### Q: Does omnibump support Gradle?

A: Yes! Gradle support is fully implemented. omnibump auto-detects Gradle projects and supports both Groovy DSL (`build.gradle`) and Kotlin DSL (`build.gradle.kts`). See the Gradle examples section below.

### Q: Can omnibump handle complex version constraints?

A: omnibump updates to specific versions. It doesn't modify version constraints in dependency declarations (like `^1.2.3` or `~1.2` in package.json).

### Q: Is omnibump safe to use in production?

A: Yes, with proper testing. Always use `--dry-run` first, review changes, run tests, and maintain backups.

### Q: How does omnibump compare to Dependabot or Renovate?

A: omnibump is a CLI tool for manual/scripted updates. Dependabot and Renovate are automated services that create PRs. They serve different use cases and can complement each other.

## Contributing

Contributions are welcome! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

Copyright 2024 Chainguard, Inc.

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for details.

## Support

- **Issues**: https://github.com/chainguard-dev/omnibump/issues
- **Documentation**: https://github.com/chainguard-dev/omnibump/tree/main/docs
- **Discussions**: https://github.com/chainguard-dev/omnibump/discussions

## Related Tools

- **gobump**: Legacy Go-specific dependency updater (superseded by omnibump)
- **cargobump**: Legacy Rust-specific dependency updater (superseded by omnibump)
- **pombump**: Legacy Maven-specific dependency updater (superseded by omnibump)
- **Dependabot**: Automated dependency updates via GitHub
- **Renovate**: Automated dependency updates across platforms

---

Made with 💜 by [Chainguard](https://chainguard.dev)
