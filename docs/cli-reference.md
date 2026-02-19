# CLI Reference

## Main Command

```bash
omnibump [flags]
```

### Flags

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

## Analyze Command

```bash
omnibump analyze [project-path] [flags]
```

Analyze your project to understand its dependency structure before making updates.

### Flags

| Flag | Short | Description | Default |
|------|-------|-------------|---------|
| `--language` | `-l` | Force language detection | `auto` |
| `--output` | | Output format (text, json, yaml) | `text` |
| `--deps` | | Dependencies file to analyze | |
| `--packages` | | Inline packages to analyze | |
| `--output-deps` | | Write deps recommendations to file | |
| `--output-props` | | Write properties recommendations to file | |

### Examples

```bash
# Analyze current directory
omnibump analyze

# Analyze specific directory
omnibump analyze /path/to/project

# Get recommendations for updating specific dependencies
omnibump analyze --packages "golang.org/x/sys@v0.28.0"

# Generate configuration files
omnibump analyze --output-deps deps.yaml --output-props properties.yaml

# Get JSON output for automation
omnibump analyze --output json
```

## Supported Command

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

## Version Command

```bash
omnibump version
```

Display version information about the omnibump binary.

Example output:
```
omnibump version v1.0.0
  Commit:     abc1234
  Tree State: clean
  Build Date: 2025-11-12T14:23:45Z
```
