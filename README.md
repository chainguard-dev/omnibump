# omnibump

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

A: Not yet. Gradle support is planned. See the design document in `docs/GRADLE_INTEGRATION_DESIGN.md`.

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
