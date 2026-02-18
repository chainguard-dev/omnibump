/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

// Package java implements omnibump support for Java projects.
// Supports multiple build tools (Maven, Gradle, etc.) through a unified interface.
package java

import (
	"context"
	"errors"
	"fmt"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/languages"
	"github.com/chainguard-dev/omnibump/pkg/languages/java/gradle"
	"github.com/chainguard-dev/omnibump/pkg/languages/java/maven"
)

// ErrNoBuildToolFound is returned when no supported build tool is detected.
var ErrNoBuildToolFound = errors.New("no supported Java build tool found")

// Java implements the Language interface for Java projects.
// It auto-detects the build tool (Maven, Gradle, etc.) and delegates to it.
type Java struct {
	buildTool BuildTool
}

// registeredBuildTools is the list of supported build tools in priority order.
// Build tools are checked in order until one is detected.
var registeredBuildTools = []BuildTool{
	&maven.Maven{},
	&gradle.Gradle{},
}

// init registers Java with the language registry.
func init() {
	languages.Register(&Java{})
}

// Name returns the language identifier.
func (j *Java) Name() string {
	return "java"
}

// Detect checks if any Java build tool is present in the directory.
func (j *Java) Detect(ctx context.Context, dir string) (bool, error) {
	buildTool := detectBuildTool(ctx, dir)
	if buildTool == nil {
		return false, nil
	}
	j.buildTool = buildTool
	return true, nil
}

// GetManifestFiles returns Java manifest files from the detected build tool.
func (j *Java) GetManifestFiles() []string {
	// Return all possible manifest files from all build tools
	var files []string //nolint: prealloc
	for _, tool := range registeredBuildTools {
		files = append(files, tool.GetManifestFiles()...)
	}
	return files
}

// SupportsAnalysis returns true since Java has analysis capabilities.
func (j *Java) SupportsAnalysis() bool {
	return true
}

// Update performs dependency updates on a Java project.
func (j *Java) Update(ctx context.Context, cfg *languages.UpdateConfig) error {
	log := clog.FromContext(ctx)

	// Detect build tool if not already detected
	if j.buildTool == nil {
		buildTool := detectBuildTool(ctx, cfg.RootDir)
		if buildTool == nil {
			return fmt.Errorf("%w in: %s", ErrNoBuildToolFound, cfg.RootDir)
		}
		j.buildTool = buildTool
	}

	log.Infof("Detected Java build tool: %s", j.buildTool.Name())

	// Delegate to the build tool
	return j.buildTool.Update(ctx, cfg)
}

// Validate checks if the updates were applied successfully.
func (j *Java) Validate(ctx context.Context, cfg *languages.UpdateConfig) error {
	// Detect build tool if not already detected
	if j.buildTool == nil {
		buildTool := detectBuildTool(ctx, cfg.RootDir)
		if buildTool == nil {
			return fmt.Errorf("%w in: %s", ErrNoBuildToolFound, cfg.RootDir)
		}
		j.buildTool = buildTool
	}

	// Delegate to the build tool
	return j.buildTool.Validate(ctx, cfg)
}

// GetBuildTool returns the detected build tool.
// This is useful for the analyzer to get build tool-specific analyzers.
func (j *Java) GetBuildTool(ctx context.Context, dir string) (BuildTool, error) {
	if j.buildTool != nil {
		return j.buildTool, nil
	}

	buildTool := detectBuildTool(ctx, dir)
	if buildTool == nil {
		return nil, fmt.Errorf("%w in: %s", ErrNoBuildToolFound, dir)
	}

	j.buildTool = buildTool
	return buildTool, nil
}

// detectBuildTool detects which Java build tool is present in the directory.
// Returns the first build tool that reports a positive detection.
func detectBuildTool(ctx context.Context, dir string) BuildTool {
	log := clog.FromContext(ctx)

	for _, tool := range registeredBuildTools {
		detected, err := tool.Detect(ctx, dir)
		if err != nil {
			log.Debugf("Error detecting %s: %v", tool.Name(), err)
			continue
		}
		if detected {
			log.Debugf("Detected Java build tool: %s", tool.Name())
			return tool
		}
	}

	return nil
}
