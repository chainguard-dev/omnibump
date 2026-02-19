# Installation Guide

## Installation Methods

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

## Build Targets

The Makefile provides several useful targets for building and developing omnibump.

### Building

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

### Installing

```bash
# Install to $GOPATH/bin (typically ~/go/bin)
make install
```

This builds the binary with version information and installs it to your Go binary path.

### Testing

```bash
# Run all tests
make test

# Run tests with coverage report (generates coverage.html)
make test-coverage
```

The test-coverage target creates an HTML coverage report that you can open in your browser.

### Development Tasks

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

### Cleanup

```bash
# Remove built binaries and clean build artifacts
make clean
```

### Version Information

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

### Help

```bash
# Display all available make targets with descriptions
make help
```
