# Migration Guide

This guide helps you migrate from legacy dependency update tools to omnibump.

## From gobump

### Direct Migration

Your existing gobump configuration files work directly with omnibump:

```bash
# Your gobump-deps.yaml works directly
omnibump --deps gobump-deps.yaml --tidy

# Optional: Rename to new standard
mv gobump-deps.yaml deps.yaml
mv gobump-replaces.yaml replaces.yaml
```

### Configuration Changes

No changes needed to configuration format. The same YAML structure works:

```yaml
# Works with both gobump and omnibump
packages:
  - name: golang.org/x/sys
    version: v0.28.0
```

### Command Changes

| gobump | omnibump |
|--------|----------|
| `gobump --deps deps.yaml` | `omnibump --deps deps.yaml` |
| `gobump --tidy` | `omnibump --tidy` |
| `gobump --dry-run` | `omnibump --dry-run` |

## From cargobump

### Direct Migration

Your existing cargobump configuration files work directly with omnibump:

```bash
# Your cargobump-deps.yaml works directly
omnibump --deps cargobump-deps.yaml

# Optional: Rename to new standard
mv cargobump-deps.yaml deps.yaml
```

### Configuration Changes

No changes needed to configuration format:

```yaml
# Works with both cargobump and omnibump
packages:
  - name: tokio
    version: 1.42.0
```

### Command Changes

| cargobump | omnibump |
|-----------|----------|
| `cargobump --deps deps.yaml` | `omnibump --deps deps.yaml` |
| `cargobump --dry-run` | `omnibump --dry-run` |

## From pombump

### Direct Migration

Your existing pombump configuration files work directly with omnibump:

```bash
# Your pombump files work directly
omnibump --deps pombump-deps.yaml --properties pombump-properties.yaml

# Optional: Rename to new standard
mv pombump-deps.yaml deps.yaml
mv pombump-properties.yaml properties.yaml
```

### Configuration Changes

No changes needed to configuration format:

```yaml
# Works with both pombump and omnibump
packages:
  - groupId: io.netty
    artifactId: netty-codec-http
    version: 4.1.94.Final
```

### Command Changes

| pombump | omnibump |
|---------|----------|
| `pombump --deps deps.yaml` | `omnibump --deps deps.yaml` |
| `pombump --properties props.yaml` | `omnibump --properties props.yaml` |
| `pombump --dry-run` | `omnibump --dry-run` |

## Unified Configuration

One of omnibump's advantages is support for a unified configuration format across all languages:

### Before (Multiple Tools)

```bash
# Different configuration files for different languages
gobump --deps gobump-deps.yaml         # Go projects
cargobump --deps cargobump-deps.yaml   # Rust projects
pombump --deps pombump-deps.yaml       # Java projects
```

### After (One Tool)

```bash
# Same tool and format for all projects
omnibump --deps deps.yaml              # Auto-detects language
```

### Unified deps.yaml

```yaml
# language field is optional - will auto-detect
packages:
  # Works for Go, Rust, and Maven
  - name: package-name
    version: 1.2.3

  # Maven-specific format also supported
  - groupId: com.example
    artifactId: library
    version: 1.0.0
```

## Migration Checklist

- [ ] Install omnibump
- [ ] Test with existing configuration files (no changes needed)
- [ ] Verify updates work correctly with `--dry-run`
- [ ] Optionally rename configuration files to standard names
- [ ] Update CI/CD pipelines to use omnibump
- [ ] Update documentation and scripts
- [ ] Remove old tools (gobump, cargobump, pombump)

## Gradual Migration

You can migrate gradually:

1. Install omnibump alongside existing tools
2. Test omnibump with existing configuration files
3. Migrate one project at a time
4. Update automation last
5. Remove old tools when comfortable

## Benefits of Migration

### Single Tool

- One tool to learn and maintain
- Consistent commands across projects
- Unified documentation

### Automatic Detection

- No need to specify language explicitly
- Works seamlessly across project types
- Reduces configuration

### Modern Features

- Better error messages
- Enhanced validation
- Improved performance
- Active development and support

## Need Help?

If you encounter issues during migration:

1. Check the [troubleshooting guide](troubleshooting.md)
2. Compare behavior with `--dry-run`
3. Open an issue with details about your use case

## Related Documentation

- [Configuration Guide](configuration.md)
- [Usage Examples](usage-examples.md)
- [Best Practices](best-practices.md)
