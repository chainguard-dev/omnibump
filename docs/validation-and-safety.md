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
