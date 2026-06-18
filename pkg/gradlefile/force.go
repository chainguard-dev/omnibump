/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradlefile

import "regexp"

// Marker comments delimiting the omnibump-managed block. The block is hosted
// in the root settings script and pins transitive dependency versions (via
// dependency constraints) and coordinate swaps (via dependencySubstitution);
// re-running omnibump merges into the existing block instead of appending
// duplicates. See managed.go for the writer.
const (
	// ForceBlockBegin opens the managed block.
	ForceBlockBegin = "// omnibump:resolutionStrategy:begin"
	// ForceBlockEnd closes the managed block.
	ForceBlockEnd = "// omnibump:resolutionStrategy:end"
)

// forceLinePattern extracts the coordinates of a legacy `force` entry inside a
// managed block. Retained so build scripts carrying a force block written by
// an older omnibump are still recognized as pinning those coordinates.
var forceLinePattern = regexp.MustCompile(`force\s*\(?\s*["']([A-Za-z0-9._-]+):([A-Za-z0-9._-]+):([A-Za-z0-9._+-]+)["']\s*\)?`)

// ForcedCoordinates returns the "group:artifact" -> version entries of a
// legacy force block in this build file, or an empty map when none exists.
// Current omnibump writes the managed block to the settings script (see
// SettingsFile.EnsureManagedBlock); this reader keeps older build-script force
// blocks visible to the project model.
func (f *BuildFile) ForcedCoordinates() map[string]string {
	coords := make(map[string]string)
	blockSpan, ok := f.forceBlockSpan()
	if !ok {
		return coords
	}
	block := f.buf.original[blockSpan.start:blockSpan.end]
	for _, m := range forceLinePattern.FindAllSubmatch(block, -1) {
		coords[string(m[1])+":"+string(m[2])] = string(m[3])
	}
	return coords
}

// forceBlockSpan returns the span of an existing managed block in this build
// file. It delegates to the shared managedBlockSpan finder (managed.go).
func (f *BuildFile) forceBlockSpan() (span, bool) {
	return managedBlockSpan(f.buf.original)
}
