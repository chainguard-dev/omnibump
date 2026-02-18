.PHONY: build install clean test version help

# Binary name
BINARY_NAME=omnibump

# Version information
# If not in git repo, use defaults
GIT_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
GIT_TREE_STATE ?= $(shell (git status --porcelain 2>/dev/null | grep -q .) && echo "dirty" || echo "clean")
BUILD_DATE ?= $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOMOD=$(GOCMD) mod

# Ldflags for version injection
LDFLAGS=-ldflags "\
	-X sigs.k8s.io/release-utils/version.gitVersion=$(GIT_VERSION) \
	-X sigs.k8s.io/release-utils/version.gitCommit=$(GIT_COMMIT) \
	-X sigs.k8s.io/release-utils/version.gitTreeState=$(GIT_TREE_STATE) \
	-X sigs.k8s.io/release-utils/version.buildDate=$(BUILD_DATE)"

# Default target
all: build

## help: Display this help message
help:
	@echo "omnibump Makefile targets:"
	@echo ""
	@echo "  make build        - Build the omnibump binary"
	@echo "  make install      - Install omnibump to GOPATH/bin"
	@echo "  make clean        - Remove built binaries"
	@echo "  make test         - Run tests"
	@echo "  make version      - Display version information"
	@echo "  make tidy         - Tidy and verify go modules"
	@echo "  make fmt          - Format Go code"
	@echo "  make lint         - Run golangci-lint (if installed)"
	@echo "  make help         - Display this help message"
	@echo ""
	@echo "Version Information:"
	@echo "  GIT_VERSION:    $(GIT_VERSION)"
	@echo "  GIT_COMMIT:     $(GIT_COMMIT)"
	@echo "  GIT_TREE_STATE: $(GIT_TREE_STATE)"
	@echo "  BUILD_DATE:     $(BUILD_DATE)"

## build: Build the omnibump binary with version information
build:
	@echo "Building $(BINARY_NAME)..."
	@echo "  Version:    $(GIT_VERSION)"
	@echo "  Commit:     $(GIT_COMMIT)"
	@echo "  Tree State: $(GIT_TREE_STATE)"
	@echo "  Build Date: $(BUILD_DATE)"
	$(GOBUILD) $(LDFLAGS) -o $(BINARY_NAME) .
	@echo "Build complete: ./$(BINARY_NAME)"

## install: Install omnibump to GOPATH/bin
install:
	@echo "Installing $(BINARY_NAME)..."
	$(GOBUILD) $(LDFLAGS) -o $(GOPATH)/bin/$(BINARY_NAME) .
	@echo "Installed to $(GOPATH)/bin/$(BINARY_NAME)"

## clean: Remove built binaries
clean:
	@echo "Cleaning..."
	$(GOCLEAN)
	rm -f $(BINARY_NAME)
	@echo "Clean complete"

## test: Run tests
test:
	@echo "Running tests..."
	$(GOTEST) -v ./...

## test-coverage: Run tests with coverage
test-coverage:
	@echo "Running tests with coverage..."
	$(GOTEST) -v -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

## tidy: Tidy and verify go modules
tidy:
	@echo "Tidying go modules..."
	$(GOMOD) tidy
	$(GOMOD) verify
	@echo "Go modules tidied"

## vendor: Vendor dependencies
vendor:
	@echo "Vendoring dependencies..."
	$(GOMOD) vendor
	@echo "Vendor complete"

## fmt: Format Go code
fmt:
	@echo "Formatting Go code..."
	$(GOCMD) fmt ./...
	@echo "Format complete"

## lint: Run golangci-lint (if installed)
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		echo "Running golangci-lint..."; \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed. Install from: https://golangci-lint.run/"; \
	fi

## version: Display version information that will be embedded
version:
	@echo "Version Information:"
	@echo "  GIT_VERSION:    $(GIT_VERSION)"
	@echo "  GIT_COMMIT:     $(GIT_COMMIT)"
	@echo "  GIT_TREE_STATE: $(GIT_TREE_STATE)"
	@echo "  BUILD_DATE:     $(BUILD_DATE)"

## run: Build and run with version command
run-version: build
	./$(BINARY_NAME) version

## snapshot: Create a snapshot release with goreleaser
snapshot:
	goreleaser build --snapshot --clean

## release: Create a production release with goreleaser
release:
	goreleaser release --clean
