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
	"os"
	"path/filepath"

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

// Compile-time assertions that the registered build tools satisfy BuildTool.
// They live here rather than in each build-tool package because BuildTool is
// defined in this package and those packages cannot import it (import cycle).
var (
	_ BuildTool = (*maven.Maven)(nil)
	_ BuildTool = (*gradle.Gradle)(nil)
)

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
	files := make([]string, 0, len(registeredBuildTools))
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
	if err := j.ensureBuildTool(ctx, cfg); err != nil {
		return err
	}
	clog.InfoContextf(ctx, "Detected Java build tool: %s", j.buildTool.Name())

	// Delegate to the build tool
	return j.buildTool.Update(ctx, cfg)
}

// Validate checks if the updates were applied successfully.
func (j *Java) Validate(ctx context.Context, cfg *languages.UpdateConfig) error {
	if err := j.ensureBuildTool(ctx, cfg); err != nil {
		return err
	}

	// Delegate to the build tool
	return j.buildTool.Validate(ctx, cfg)
}

// ensureBuildTool resolves and caches the build tool for cfg.
func (j *Java) ensureBuildTool(ctx context.Context, cfg *languages.UpdateConfig) error {
	if j.buildTool != nil {
		return nil
	}
	tool, err := resolveBuildTool(ctx, cfg)
	if err != nil {
		return err
	}
	j.buildTool = tool
	return nil
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

// resolveBuildTool returns the appropriate build tool for the given config.
// When ManifestFile is set it identifies the tool by file content; otherwise
// it falls back to directory-based detection.
func resolveBuildTool(ctx context.Context, cfg *languages.UpdateConfig) (BuildTool, error) {
	if cfg.ManifestFile != "" {
		ok, err := maven.IsMavenPom(cfg.ManifestFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read manifest file: %w", err)
		}
		if ok {
			return &maven.Maven{}, nil
		}
	}
	tool := detectBuildTool(ctx, cfg.RootDir)
	if tool == nil {
		return nil, fmt.Errorf("%w in: %s", ErrNoBuildToolFound, cfg.RootDir)
	}
	return tool, nil
}

// detectBuildTool detects which Java build tool is present in the directory.
//
// Root-level manifests win over recursive detection: a Gradle project that
// vendors a pom.xml somewhere in its tree (e.g. Kafka's streams quickstart
// archetype) must resolve to Gradle, and a Maven project with a stray Gradle
// script in a subdirectory must resolve to Maven. Only when no tool has a
// manifest at the project root does the deeper per-tool detection decide.
func detectBuildTool(ctx context.Context, dir string) BuildTool {
	log := clog.FromContext(ctx)

	for _, tool := range registeredBuildTools {
		for _, manifest := range tool.GetManifestFiles() {
			path := filepath.Join(dir, manifest)
			if _, err := os.Stat(path); err != nil {
				continue
			}
			// A file merely named pom.xml must not outrank a valid Gradle
			// root: Maven roots are content-validated.
			if manifest == maven.DefaultManifestFile {
				if ok, err := maven.IsMavenPom(path); err != nil || !ok {
					continue
				}
			}
			log.Debugf("Detected Java build tool %s via root manifest %s", tool.Name(), manifest)
			return tool
		}
	}

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

// DetectBuildToolFromPaths picks the Java build tool for a remote repository
// from its file path list, mirroring detectBuildTool's precedence without disk
// access: a tool with a manifest at the repository root wins (a root pom.xml
// beats a Gradle script elsewhere), otherwise the first registered tool with a
// matching manifest basename at any depth wins. Maven keeps priority at equal
// depth via registeredBuildTools order. Returns nil when no Java build tool
// matches.
//
// Unlike detectBuildTool, a root pom.xml is accepted by name: remote detection
// only has paths, not content, so it cannot run maven.IsMavenPom. This matches
// how languages.DetectLanguageFromPaths already resolves remote Java repos.
func DetectBuildToolFromPaths(paths []string) BuildTool {
	pathSet := make(map[string]struct{}, len(paths))
	baseSet := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		pathSet[p] = struct{}{}
		baseSet[filepath.Base(p)] = struct{}{}
	}

	// Root manifest wins.
	for _, tool := range registeredBuildTools {
		for _, manifest := range tool.GetManifestFiles() {
			if _, ok := pathSet[manifest]; ok {
				return tool
			}
		}
	}

	// Otherwise any-depth basename match, in registration order.
	for _, tool := range registeredBuildTools {
		for _, manifest := range tool.GetManifestFiles() {
			if _, ok := baseSet[filepath.Base(manifest)]; ok {
				return tool
			}
		}
	}

	return nil
}
