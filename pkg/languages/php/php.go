/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package php

import (
	"context"
	"errors"
	"fmt"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/languages"
	"github.com/chainguard-dev/omnibump/pkg/languages/php/composer"
)

// ErrNoBuildToolFound indicates no supported PHP build tool was detected.
var ErrNoBuildToolFound = errors.New("no supported PHP build tool found")

// PHP implements the Language interface for PHP projects.
// It auto-detects the build tool (Composer, etc.) and delegates to it.
type PHP struct {
	buildTool BuildTool
}

// registeredBuildTools is the list of supported build tools in priority order.
// Build tools are checked in order until one is detected.
var registeredBuildTools = []BuildTool{
	&composer.Composer{},
}

// init registers PHP with the language registry.
func init() {
	languages.Register(&PHP{})
}

// Name returns the language identifier.
func (p *PHP) Name() string {
	return "php"
}

// Detect checks if any PHP build tool is present in the directory.
func (p *PHP) Detect(ctx context.Context, dir string) (bool, error) {
	buildTool := detectBuildTool(ctx, dir)
	if buildTool == nil {
		return false, nil
	}
	p.buildTool = buildTool
	return true, nil
}

// GetManifestFiles returns PHP manifest files from the detected build tool.
func (p *PHP) GetManifestFiles() []string {
	// Return all possible manifest files from all build tools
	files := make([]string, 0, len(registeredBuildTools)*2) //nolint:mnd // estimate 2 manifest files per build tool
	for _, tool := range registeredBuildTools {
		files = append(files, tool.GetManifestFiles()...)
	}
	return files
}

// SupportsAnalysis returns true since PHP has analysis capabilities.
func (p *PHP) SupportsAnalysis() bool {
	return true
}

// Update performs dependency updates on a PHP project.
func (p *PHP) Update(ctx context.Context, cfg *languages.UpdateConfig) error {
	log := clog.FromContext(ctx)

	// Detect build tool if not already detected
	if p.buildTool == nil {
		buildTool := detectBuildTool(ctx, cfg.RootDir)
		if buildTool == nil {
			return fmt.Errorf("%w in: %s", ErrNoBuildToolFound, cfg.RootDir)
		}
		p.buildTool = buildTool
	}

	log.Infof("Detected PHP build tool: %s", p.buildTool.Name())

	// Delegate to the build tool
	return p.buildTool.Update(ctx, cfg)
}

// Validate checks if the updates were applied successfully.
func (p *PHP) Validate(ctx context.Context, cfg *languages.UpdateConfig) error {
	// Detect build tool if not already detected
	if p.buildTool == nil {
		buildTool := detectBuildTool(ctx, cfg.RootDir)
		if buildTool == nil {
			return fmt.Errorf("%w in: %s", ErrNoBuildToolFound, cfg.RootDir)
		}
		p.buildTool = buildTool
	}

	// Delegate to the build tool
	return p.buildTool.Validate(ctx, cfg)
}

// GetBuildTool returns the detected build tool.
// This is useful for the analyzer to get build tool-specific analyzers.
func (p *PHP) GetBuildTool(ctx context.Context, dir string) (BuildTool, error) {
	if p.buildTool != nil {
		return p.buildTool, nil
	}

	buildTool := detectBuildTool(ctx, dir)
	if buildTool == nil {
		return nil, fmt.Errorf("%w in: %s", ErrNoBuildToolFound, dir)
	}

	p.buildTool = buildTool
	return buildTool, nil
}

// detectBuildTool detects which PHP build tool is present in the directory.
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
			log.Debugf("Detected PHP build tool: %s", tool.Name())
			return tool
		}
	}

	return nil
}
