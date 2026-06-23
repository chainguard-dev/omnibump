# Configuration Guide

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

**Version normalization (automatic):**

omnibump automatically resolves versions to canonical forms:

```yaml
# You specify without +incompatible
packages:
  - name: github.com/docker/docker
    version: v28.0.0

# Omnibump automatically resolves to: v28.0.0+incompatible
```

**Note:** You can also specify the `+incompatible` suffix explicitly if preferred:
```yaml
packages:
  - name: github.com/docker/docker
    version: v28.0.0+incompatible
```

Both formats work - omnibump queries the Go module proxy to get the canonical version.

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
    classifier: ""      # optional: see "Classifiers" below
```

#### Classifiers

The `classifier` field selects which classifier variants of an artifact a pin
governs. This matters for artifacts published under multiple classifiers, such as
Netty's native transports (`osx-x86_64`, `linux-x86_64`, …).

| `classifier:` value | Matches |
| --- | --- |
| unset / empty (default) | **every** variant — the classifier-less dependency *and* all classifier'd ones |
| `none` | **only** the classifier-less dependency |
| a value, e.g. `osx-x86_64` | **only** that exact classifier |

So a single unset-classifier entry bumps the whole family at once:

```yaml
packages:
  # Bumps netty-transport-native-epoll for every classifier present in the POM.
  - groupId: io.netty
    artifactId: netty-transport-native-epoll
    version: 4.1.135.Final

  # Pins only the macOS variant; other classifiers are left untouched.
  - groupId: io.netty
    artifactId: netty-transport-native-kqueue
    version: 4.1.135.Final
    classifier: osx-x86_64

  # Pins only the plain (classifier-less) artifact, even if classifier'd
  # siblings of the same coordinate exist.
  - groupId: com.example
    artifactId: library
    version: 1.2.3
    classifier: none
```

Combining an unset (wildcard) entry with a specific-classifier entry for the same
`groupId:artifactId` at **different** versions is rejected as a version conflict.

**Inline format:**
```bash
--packages "groupId@artifactId@version"
--packages "groupId@artifactId@version@scope"
--packages "groupId@artifactId@version@scope@type"
```

> The inline format does not carry a classifier; use the YAML form for
> classifier-specific pins.

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
