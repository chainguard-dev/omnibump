/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradle

import (
	"errors"
	"fmt"
	"strings"

	"github.com/chainguard-dev/omnibump/pkg/languages"
)

// ErrMissingCoordinates is returned when a dependency carries neither a
// parseable "group:artifact" name nor groupId/artifactId metadata.
var ErrMissingCoordinates = errors.New("missing group/artifact coordinates for dependency")

// depCoordinates extracts Maven coordinates from a dependency, preferring
// explicit groupId/artifactId metadata (set by the groupId@artifactId@version
// input format used by the melange bump pipeline) and falling back to a
// "group:artifact" Name. Mirrors Maven's extractGroupID/extractArtifactID so
// both input shapes work identically across the two build tools.
func depCoordinates(dep languages.Dependency) (group, artifact string, err error) {
	if dep.Metadata != nil {
		gid, _ := dep.Metadata["groupId"].(string)
		aid, _ := dep.Metadata["artifactId"].(string)
		if gid != "" && aid != "" {
			return gid, aid, nil
		}
	}
	group, artifact = parseDependencyName(dep.Name)
	if group == "" || artifact == "" {
		return "", "", fmt.Errorf("%w: name=%q (expected groupId:artifactId or groupId/artifactId metadata)",
			ErrMissingCoordinates, dep.Name)
	}
	return group, artifact, nil
}

// depDisplayName returns a human-readable identifier for log messages.
func depDisplayName(dep languages.Dependency) string {
	if group, artifact, err := depCoordinates(dep); err == nil {
		return group + ":" + artifact
	}
	if dep.Name != "" {
		return dep.Name
	}
	return "<unknown>"
}

// parseDependencyName parses "groupId:artifactId" format.
func parseDependencyName(name string) (groupID, artifactID string) {
	parts := strings.Split(name, ":")
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	if len(parts) == 1 {
		// Assume it's just artifactID (less common)
		return "", parts[0]
	}
	return "", ""
}
