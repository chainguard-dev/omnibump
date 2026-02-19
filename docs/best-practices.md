# Best Practices

## 1. Always Use Dry Run First

```bash
omnibump --deps deps.yaml --dry-run
```

Preview changes before applying them. This helps you catch potential issues and understand what will change.

## 2. Analyze Before Updating

```bash
omnibump analyze
```

Understand your project structure before making changes. The analyze command helps you:
- See current dependency versions
- Identify dependencies that use properties (Maven)
- Understand which updates are needed

## 3. Use Version Control

```bash
git add -A
git commit -m "Pre-omnibump state"
omnibump --deps deps.yaml
git diff  # Review changes
```

Always commit your current state before running updates. This makes it easy to:
- Review changes with `git diff`
- Rollback if needed
- Track what changed and why

## 4. Run Tests After Updates

```bash
omnibump --deps deps.yaml --tidy
make test  # or your test command
```

Always run your test suite after updating dependencies. This catches:
- Breaking API changes
- Incompatibilities between updated packages
- Issues with new versions

## 5. Use Properties for Maven Projects

When multiple dependencies share the same version, use properties:

```yaml
# Better: Update property once
properties:
  - property: netty.version
    value: 4.1.94.Final

# Affects all netty-* dependencies
```

This approach:
- Keeps versions synchronized
- Reduces configuration
- Matches Maven best practices

## 6. Keep Configuration in Version Control

```bash
git add deps.yaml properties.yaml
git commit -m "Add omnibump configuration"
```

This makes updates reproducible and auditable. Benefits:
- Team members can run the same updates
- Changes are documented
- Configuration evolves with the project

## 7. Document Why

Add comments to your configuration:

```yaml
# CVE-2024-12345: netty vulnerability patch
packages:
  - groupId: io.netty
    artifactId: netty-codec-http
    version: 4.1.94.Final
```

This helps:
- Future maintainers understand the context
- Track security updates
- Explain version pins

## Additional Recommendations

### Start Small

When first using omnibump, start with a single dependency update:
```bash
omnibump --packages "package@version" --dry-run
```

### Use Show Diff

Review changes before committing:
```bash
omnibump --deps deps.yaml --show-diff
```

### Automate Safely

When automating with CI/CD:
- Always run in dry-run mode first
- Verify changes with tests
- Use pull requests for review
- Don't auto-merge dependency updates

### Keep omnibump Updated

Regularly update omnibump itself to get:
- New features
- Bug fixes
- Support for new language versions
- Security improvements
