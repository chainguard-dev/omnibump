/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package maven

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/gopom"
	"github.com/ghodss/yaml"
)

var (
	// ErrProjectNil is returned when a POM project is nil.
	ErrProjectNil = errors.New("project is nil")

	// ErrFileTooLarge is returned when a file exceeds size limits.
	ErrFileTooLarge = errors.New("file too large")

	// ErrInvalidDependencyFormat is returned when a dependency string has invalid format.
	ErrInvalidDependencyFormat = errors.New("invalid dependencies format")

	// ErrInvalidPropertyFormat is returned when a property string has invalid format.
	ErrInvalidPropertyFormat = errors.New("invalid properties format")

	// ErrPropertyNotFound is returned when a property patch cannot be applied to
	// the current POM or its local parent POM chain.
	ErrPropertyNotFound = errors.New("property not found")

	// ErrUnsafePomPath is returned when an update would write outside the
	// configured Maven project root.
	ErrUnsafePomPath = errors.New("unsafe POM path")

	// ErrVersionConflict is returned when two updates try to set different
	// versions for the same dependency or property-backed dependency set.
	ErrVersionConflict = errors.New("conflicting version update detected")
)

// Default scope and type for a dependency.
const (
	defaultScope = "import"
	defaultType  = "jar"

	// MaxPatchFileSize limits patch/properties file size to prevent resource exhaustion.
	MaxPatchFileSize = 10 * 1024 * 1024 // 10 MB

	// MaxPomFileSize limits POM file size to prevent resource exhaustion.
	MaxPomFileSize = 10 * 1024 * 1024 // 10 MB
)

// Patch represents a Maven dependency patch.
// Ported from pombump/pkg/patch.go.
type Patch struct {
	GroupID    string `json:"groupId" yaml:"groupId"`
	ArtifactID string `json:"artifactId" yaml:"artifactId"`
	Version    string `json:"version" yaml:"version"`
	Scope      string `json:"scope,omitempty" yaml:"scope,omitempty"`
	Type       string `json:"type,omitempty" yaml:"type,omitempty"`
}

// PatchList represents a list of patches from a YAML file.
type PatchList struct {
	Patches []Patch `json:"patches" yaml:"patches"`
}

// PropertyPatch represents a property override.
type PropertyPatch struct {
	Property string `json:"property" yaml:"property"`
	Value    string `json:"value" yaml:"value"`
}

// PropertyList represents a list of property patches from a YAML file.
type PropertyList struct {
	Properties []PropertyPatch `json:"properties" yaml:"properties"`
}

// pomPropertyUpdate keeps one requested property update tied to the POM that defines it.
type pomPropertyUpdate struct {
	pomFile       string
	propertyName  string
	propertyValue string
}

// dependencyPropertyUpdates moves property-backed dependency patches onto the
// POM that defines the property. rootDir bounds the parent chain traversal.
func dependencyPropertyUpdates(ctx context.Context, pomPath string, patches []Patch, explicitProperties map[string]string, rootDir string) ([]Patch, []pomPropertyUpdate, error) {
	if len(patches) == 0 {
		return patches, nil, nil
	}

	// We need the current POM contents to know whether a dependency version is
	// inline or backed by a Maven property reference like ${version.netty}.
	project, err := ParsePom(pomPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse POM: %w", err)
	}

	// matchedPatches are removed from the direct dependency patch list because
	// they will be applied through propertyUpdates instead.
	matchedPatches := make(map[Patch]struct{})
	// Multiple dependencies can share one property; all requested values must agree.
	propertyValues := make(map[string]string)
	var propertyUpdates []pomPropertyUpdate

	// Check both regular dependencies and dependencyManagement entries.
	dependencySets := []*[]gopom.Dependency{project.Dependencies}
	if project.DependencyManagement != nil {
		dependencySets = append(dependencySets, project.DependencyManagement.Dependencies)
	}

	for _, deps := range dependencySets {
		if deps == nil {
			continue
		}
		for _, dep := range *deps {
			if !isPropertyReference(dep.Version) {
				continue
			}
			propertyName := strings.TrimSuffix(strings.TrimPrefix(dep.Version, "${"), "}")
			for _, patch := range patches {
				// Only dependency patches matching this exact dependency can move
				// from a direct version change to a property update.
				if dep.ArtifactID != patch.ArtifactID || dep.GroupID != patch.GroupID {
					continue
				}
				// This patch is handled by updating the referenced property.
				matchedPatches[patch] = struct{}{}
				// Explicit property updates are appended by Maven.Update below.
				if explicitValue, explicit := explicitProperties[propertyName]; explicit {
					if patch.Version != "" && explicitValue != patch.Version {
						return nil, nil, fmt.Errorf("%w: dependency %s:%s requests %s but property %s is explicitly set to %s",
							ErrVersionConflict, patch.GroupID, patch.ArtifactID, patch.Version, propertyName, explicitValue)
					}
					continue
				}
				if propertyValue, alreadySet := propertyValues[propertyName]; alreadySet {
					if propertyValue != patch.Version {
						return nil, nil, fmt.Errorf("%w: dependencies using property %s request both %s and %s",
							ErrVersionConflict, propertyName, propertyValue, patch.Version)
					}
					clog.InfoContextf(ctx, "Patching %s:%s via property %s to %s (property already updated)",
						patch.GroupID, patch.ArtifactID, dep.Version, propertyValue)
					continue
				}

				// project.version is a Maven built-in that mirrors the project's own <version>
				// tag; skip with an informational message instead of failing.
				if propertyName == "project.version" {
					clog.InfoContextf(ctx, "Skipping %s:%s: uses ${project.version} which is the project's own version tag, not a configurable property",
						patch.GroupID, patch.ArtifactID)
					continue
				}

				// Reuse the existing resolver so current-vs-parent ownership stays consistent.
				propertyPomPath, err := resolvePropertyPomPath(ctx, pomPath, propertyName, rootDir)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to resolve file where property %s is set: %w", propertyName, err)
				}
				clog.InfoContextf(ctx, "Patching %s:%s via property %s in %s to %s",
					patch.GroupID, patch.ArtifactID, dep.Version, propertyPomPath, patch.Version)
				propertyValues[propertyName] = patch.Version
				propertyUpdates = append(propertyUpdates, pomPropertyUpdate{
					pomFile:       propertyPomPath,
					propertyName:  propertyName,
					propertyValue: patch.Version,
				})
			}
		}
	}

	// Return only direct dependency patches; property-backed patches moved above.
	remainingPatches := make([]Patch, 0, len(patches)-len(matchedPatches))
	for _, patch := range patches {
		if _, matched := matchedPatches[patch]; !matched {
			remainingPatches = append(remainingPatches, patch)
		}
	}
	return remainingPatches, propertyUpdates, nil
}

// UpdatePom updates a POM file with the given patches and properties.
// Returns the marshaled XML content of the updated POM.
func UpdatePom(ctx context.Context, pomPath string, patches []Patch, properties map[string]string) ([]byte, error) {
	// Parse the POM
	project, err := ParsePom(pomPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse POM: %w", err)
	}

	// Apply patches
	project, err = PatchProject(ctx, project, patches, properties)
	if err != nil {
		return nil, fmt.Errorf("failed to patch project: %w", err)
	}

	// Marshal back to XML
	xmlBytes, err := project.Marshal()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal POM: %w", err)
	}

	clog.InfoContextf(ctx, "Successfully updated POM file")
	return xmlBytes, nil
}

// isPropertyReference checks if a version string is a Maven property reference.
func isPropertyReference(version string) bool {
	return strings.HasPrefix(version, "${") && strings.HasSuffix(version, "}")
}

// resolveVersion returns the concrete version for a dep: if the version is a
// property reference (e.g. ${log4j.version}), it looks up the value in the
// project's Properties; otherwise it returns the version unchanged.
func resolveVersion(version string, properties *gopom.Properties) string {
	if !isPropertyReference(version) || properties == nil {
		return version
	}
	propName := strings.TrimSuffix(strings.TrimPrefix(version, "${"), "}")
	if v, ok := properties.Entries[propName]; ok {
		return v
	}
	return version
}

// mavenVersionIsNewer reports whether current is strictly newer than requested,
// using a simplified Maven version comparison that handles common formats:
// numeric segments, dot/hyphen separators, and the most common qualifiers
// (Final, RELEASE, GA, SNAPSHOT, jreN).
//
// This function is intentionally conservative: when a segment cannot be parsed
// as an integer (e.g. an unknown qualifier), it is treated as 0. This ensures
// we never incorrectly skip a genuine upgrade due to an unrecognised qualifier.
func mavenVersionIsNewer(current, requested string) bool {
	if current == "" || requested == "" || current == requested {
		return false
	}
	cs := mavenVersionSegments(current)
	rs := mavenVersionSegments(requested)
	for len(cs) < len(rs) {
		cs = append(cs, 0)
	}
	for len(rs) < len(cs) {
		rs = append(rs, 0)
	}
	for i := range cs {
		if cs[i] > rs[i] {
			return true
		}
		if cs[i] < rs[i] {
			return false
		}
	}
	return false // equal
}

// mavenVersionSegments splits a Maven version string into integer segments.
// Pre-processing: strips .Final/-Final, .RELEASE/-RELEASE, .GA/-GA, -SNAPSHOT,
// and .jreN/-jreN classifier suffixes, then splits on "." and "-".
// Non-numeric parts become 0 (conservative).
func mavenVersionSegments(v string) []int {
	for _, suffix := range []string{
		".Final", "-Final",
		".RELEASE", "-RELEASE",
		".GA", "-GA",
		"-SNAPSHOT",
	} {
		if strings.HasSuffix(v, suffix) {
			v = v[:len(v)-len(suffix)]
			break
		}
	}
	if idx := strings.LastIndexAny(v, ".-"); idx >= 0 {
		tail := v[idx+1:]
		if strings.HasPrefix(strings.ToLower(tail), "jre") {
			v = v[:idx]
		}
	}
	var segments []int
	for _, part := range strings.FieldsFunc(v, func(r rune) bool {
		return r == '.' || r == '-'
	}) {
		n, err := strconv.Atoi(part)
		if err != nil {
			n = 0
		}
		segments = append(segments, n)
	}
	return segments
}

// resolveBOMVersion looks up the managed version for groupID:artifactID from
// BOM imports declared in this project's dependencyManagement.
//
// The lookup is best-effort: BOMs that are missing from local Maven cache are
// skipped. Returns ("", nil) when no matching managed dependency is found.
func resolveBOMVersion(ctx context.Context, project *gopom.Project, groupID, artifactID string) (string, error) {
	if project == nil || project.DependencyManagement == nil || project.DependencyManagement.Dependencies == nil {
		return "", nil
	}

	m2repo := os.Getenv("M2_REPO")
	if m2repo == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			clog.DebugContextf(ctx, "Unable to resolve user home directory for Maven cache lookup, skipping BOM check: %v", err)
			return "", nil
		}
		m2repo = filepath.Join(home, ".m2", "repository")
	}

	for _, dep := range *project.DependencyManagement.Dependencies {
		if dep.Type != "pom" || dep.Scope != "import" {
			continue
		}

		bomGroupID := resolveVersion(dep.GroupID, project.Properties)
		bomArtifactID := resolveVersion(dep.ArtifactID, project.Properties)
		bomVersion := resolveVersion(dep.Version, project.Properties)
		if isPropertyReference(bomGroupID) || isPropertyReference(bomArtifactID) || isPropertyReference(bomVersion) {
			continue
		}
		if bomGroupID == "" || bomArtifactID == "" || bomVersion == "" {
			continue
		}

		groupPath := strings.ReplaceAll(bomGroupID, ".", string(filepath.Separator))
		bomPomPath := filepath.Join(m2repo, groupPath, bomArtifactID, bomVersion, bomArtifactID+"-"+bomVersion+".pom")

		bomProject, err := ParsePom(bomPomPath)
		if err != nil {
			clog.DebugContextf(ctx, "BOM %s:%s:%s not found in local Maven cache (%s), skipping BOM check",
				bomGroupID, bomArtifactID, bomVersion, bomPomPath)
			continue
		}

		if bomProject.DependencyManagement == nil || bomProject.DependencyManagement.Dependencies == nil {
			continue
		}

		for _, bomDep := range *bomProject.DependencyManagement.Dependencies {
			resolvedGroupID := resolveVersion(bomDep.GroupID, bomProject.Properties)
			resolvedArtifactID := resolveVersion(bomDep.ArtifactID, bomProject.Properties)
			if resolvedGroupID != groupID || resolvedArtifactID != artifactID {
				continue
			}

			version := resolveVersion(bomDep.Version, bomProject.Properties)
			if isPropertyReference(version) || version == "" {
				continue
			}

			clog.DebugContextf(ctx, "Found %s:%s @ %s in BOM %s:%s:%s",
				groupID, artifactID, version, bomGroupID, bomArtifactID, bomVersion)
			return version, nil
		}
	}

	return "", nil
}

// PatchProject updates a gopom.Project with the given patches and properties.
// applyPatchesToDeps applies patches to a dep slice in place, removing matched
// entries from missingDeps. A nil deps slice is a no-op.
// A patch whose version is empty is treated as a scope-only entry: if the dep
// already exists its version is preserved; if absent it stays in missingDeps
// so it is later added to DependencyManagement without a <version> element.
// When a dependency version is a property reference (e.g. ${log4j2.version}),
// the property is automatically added to propertyPatches so it gets updated.
func applyPatchesToDeps(ctx context.Context, deps *[]gopom.Dependency, patches []Patch, missingDeps map[Patch]Patch, propertyPatches map[string]string) {
	if deps == nil {
		return
	}
	for i, dep := range *deps {
		clog.DebugContextf(ctx, "Checking dependency: %s:%s @ %s", dep.GroupID, dep.ArtifactID, dep.Version)
		for _, patch := range patches {
			if dep.ArtifactID != patch.ArtifactID || dep.GroupID != patch.GroupID {
				continue
			}
			if isPropertyReference(dep.Version) {
				propName := strings.TrimSuffix(strings.TrimPrefix(dep.Version, "${"), "}")
				if _, alreadySet := propertyPatches[propName]; !alreadySet {
					clog.InfoContextf(ctx, "Patching %s:%s via property %s to %s",
						patch.GroupID, patch.ArtifactID, dep.Version, patch.Version)
					propertyPatches[propName] = patch.Version
				} else {
					clog.InfoContextf(ctx, "Patching %s:%s via property %s to %s (property already updated)",
						patch.GroupID, patch.ArtifactID, dep.Version, propertyPatches[propName])
				}
				delete(missingDeps, patch)
				continue
			}
			// A patch with no version is a scope-only entry (e.g. scope: provided
			// to suppress a relocated artifact). Don't overwrite the existing version.
			if patch.Version == "" {
				clog.InfoContextf(ctx, "Found %s:%s — patch has no version, preserving existing version %s",
					patch.GroupID, patch.ArtifactID, dep.Version)
				delete(missingDeps, patch)
				continue
			}
			// Skip if this would be a downgrade
			if mavenVersionIsNewer(dep.Version, patch.Version) {
				clog.WarnContextf(ctx, "Package %s:%s: current version %s is newer than requested %s, skipping",
					patch.GroupID, patch.ArtifactID, dep.Version, patch.Version)
				delete(missingDeps, patch)
				continue
			}
			clog.InfoContextf(ctx, "Patching %s:%s from %s to %s (scope: %s)",
				patch.GroupID, patch.ArtifactID, dep.Version, patch.Version, patch.Scope)
			(*deps)[i].Version = patch.Version
			delete(missingDeps, patch)
		}
	}
}

// PatchProject applies dependency and property patches to a parsed pom.xml.
// project is a gopom.Project — a Go struct that mirrors the Maven POM XML
// schema and can be round-tripped back to XML via project.Marshal().
func PatchProject(ctx context.Context, project *gopom.Project, patches []Patch, propertyPatches map[string]string) (*gopom.Project, error) {
	if propertyPatches == nil {
		propertyPatches = make(map[string]string)
	}

	if project == nil {
		return nil, ErrProjectNil
	}

	// Track dependencies that weren't found (will be added to DependencyManagement)
	missingDeps := make(map[Patch]Patch)
	for _, p := range patches {
		clog.InfoContextf(ctx, "Processing patch: %s:%s @ %s", p.GroupID, p.ArtifactID, p.Version)
		missingDeps[p] = p
	}

	applyPatchesToDeps(ctx, project.Dependencies, patches, missingDeps, propertyPatches)
	if project.DependencyManagement != nil {
		applyPatchesToDeps(ctx, project.DependencyManagement.Dependencies, patches, missingDeps, propertyPatches)
	}

	// Add missing dependencies to DependencyManagement
	if len(missingDeps) > 0 {
		if project.DependencyManagement == nil {
			project.DependencyManagement = &gopom.DependencyManagement{
				Dependencies: &[]gopom.Dependency{},
			}
		} else if project.DependencyManagement.Dependencies == nil {
			project.DependencyManagement.Dependencies = &[]gopom.Dependency{}
		}

		for _, md := range missingDeps {
			if md.Version != "" {
				bomVersion, err := resolveBOMVersion(ctx, project, md.GroupID, md.ArtifactID)
				if err != nil {
					clog.WarnContextf(ctx, "Package %s:%s: error checking BOM version: %v",
						md.GroupID, md.ArtifactID, err)
				} else if bomVersion != "" && mavenVersionIsNewer(bomVersion, md.Version) {
					clog.WarnContextf(ctx, "Package %s:%s: BOM-managed version %s is newer than requested %s, skipping",
						md.GroupID, md.ArtifactID, bomVersion, md.Version)
					continue
				}
			}

			clog.InfoContextf(ctx, "Adding missing dependency: %s:%s @ %s", md.GroupID, md.ArtifactID, md.Version)
			*project.DependencyManagement.Dependencies = append(*project.DependencyManagement.Dependencies, gopom.Dependency{
				GroupID:    md.GroupID,
				ArtifactID: md.ArtifactID,
				Version:    md.Version,
				Scope:      md.Scope,
				Type:       md.Type,
			})
		}
	}

	// Update properties
	if len(propertyPatches) == 0 {
		return project, nil
	}

	// Initialize properties if nil
	if project.Properties == nil {
		project.Properties = &gopom.Properties{Entries: propertyPatches}
		return project, nil
	}

	// Update existing properties
	for k, v := range propertyPatches {
		val, exists := project.Properties.Entries[k]
		if exists {
			if mavenVersionIsNewer(val, v) {
				clog.WarnContextf(ctx, "Property %s: current value %s is newer than requested %s, skipping",
					k, val, v)
				continue
			}
			clog.InfoContextf(ctx, "Updating property: %s from %s to %s", k, val, v)
			project.Properties.Entries[k] = v
		}
	}

	return project, nil
}

// resolvePropertyPomPath returns the current or parent POM file that defines property.
// rootDir is the project root boundary: traversal stops if the next parent would escape it.
func resolvePropertyPomPath(ctx context.Context, pomPath, property, rootDir string) (string, error) {
	currentPath := pomPath
	visited := make(map[string]struct{})
	checkedParent := false

	for {
		pathKey, err := pomPathKey(currentPath)
		if err != nil {
			return "", fmt.Errorf("failed to resolve POM path %s while resolving property %s: %w", currentPath, property, err)
		}
		if _, seen := visited[pathKey]; seen {
			return "", fmt.Errorf("%w: property %s not found in parent POM chain; cycle detected at %s", ErrPropertyNotFound, property, currentPath)
		}
		visited[pathKey] = struct{}{}

		project, err := ParsePom(currentPath)
		if err != nil {
			if currentPath == pomPath {
				return "", fmt.Errorf("failed to parse POM: %w", err)
			}
			return "", fmt.Errorf("failed to parse parent POM %s while resolving property %s: %w", currentPath, property, err)
		}
		if projectHasProperty(project, property) {
			clog.InfoContextf(ctx, "Property %s found in %s", property, currentPath)
			return currentPath, nil
		}

		parentPath, hasParent := parentPomPath(currentPath, project)
		if !hasParent {
			if !checkedParent {
				return "", fmt.Errorf("%w: property %s not found in %s and no parent POM is configured", ErrPropertyNotFound, property, pomPath)
			}
			return "", fmt.Errorf("%w: property %s not found in %s or parent POM chain", ErrPropertyNotFound, property, pomPath)
		}

		// Stop traversal if the next parent escapes the project root boundary.
		if err := validatePathWithinRoot(rootDir, parentPath); err != nil {
			return "", err
		}

		checkedParent = true
		currentPath = parentPath
	}
}

func parentPomPath(pomPath string, project *gopom.Project) (string, bool) {
	if project == nil || project.Parent == nil {
		return "", false
	}

	relativePath := strings.TrimSpace(project.Parent.RelativePath)
	if relativePath == "" {
		relativePath = filepath.Join("..", "pom.xml")
	}
	if filepath.IsAbs(relativePath) {
		return pomPathFromParentPath(filepath.Clean(relativePath)), true
	}
	return pomPathFromParentPath(filepath.Clean(filepath.Join(filepath.Dir(pomPath), relativePath))), true
}

func pomPathFromParentPath(path string) string {
	// Maven relativePath can point at a directory; in that case use its pom.xml.
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return filepath.Join(path, DefaultManifestFile)
	}
	return path
}

func validatePathWithinRoot(root, path string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("failed to resolve project root %s: %w", root, err)
	}
	rootAbs, err = filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return fmt.Errorf("failed to resolve project root symlinks %s: %w", rootAbs, err)
	}

	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("failed to resolve POM path %s: %w", path, err)
	}
	pathAbs, err = filepath.EvalSymlinks(pathAbs)
	if err != nil {
		return fmt.Errorf("failed to resolve POM path symlinks %s: %w", pathAbs, err)
	}

	insideRoot, err := pathIsWithinRoot(rootAbs, pathAbs)
	if err != nil {
		return fmt.Errorf("%w: failed to compare POM path %s to project root %s: %w", ErrUnsafePomPath, pathAbs, rootAbs, err)
	}
	if !insideRoot {
		return fmt.Errorf("%w: POM path %s escapes project root %s", ErrUnsafePomPath, pathAbs, rootAbs)
	}
	return nil
}

func pathIsWithinRoot(rootAbs, pathAbs string) (bool, error) {
	rel, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil {
		return false, err
	}
	if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, nil
	}
	return true, nil
}

func pomPathKey(path string) (string, error) {
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(pathAbs), nil
}

// projectHasProperty reports whether the parsed POM defines name in <properties>.
func projectHasProperty(project *gopom.Project, name string) bool {
	if project == nil || project.Properties == nil || project.Properties.Entries == nil {
		return false
	}
	_, exists := project.Properties.Entries[name]
	return exists
}

func pomProperties(ctx context.Context, pomPath string, project *gopom.Project) *gopom.Properties {
	properties := make(map[string]string)
	projects := make([]*gopom.Project, 0)
	currentPath := pomPath
	currentProject := project
	visited := make(map[string]struct{})

	for currentProject != nil {
		pathKey, err := pomPathKey(currentPath)
		if err != nil {
			clog.FromContext(ctx).Debugf("failed to resolve POM path %s while collecting properties: %v", currentPath, err)
			break
		}
		if _, seen := visited[pathKey]; seen {
			clog.FromContext(ctx).Debugf("detected parent POM cycle at %s while collecting properties", currentPath)
			break
		}
		visited[pathKey] = struct{}{}
		projects = append(projects, currentProject)

		parentPath, hasParent := parentPomPath(currentPath, currentProject)
		if !hasParent {
			break
		}
		parentProject, err := ParsePom(parentPath)
		if err != nil {
			clog.FromContext(ctx).Debugf("failed to parse parent POM %s while collecting properties: %v", parentPath, err)
			break
		}
		currentPath = parentPath
		currentProject = parentProject
	}

	for i := len(projects) - 1; i >= 0; i-- {
		if projects[i].Properties != nil {
			for k, v := range projects[i].Properties.Entries {
				properties[k] = v
			}
		}
	}
	if len(properties) == 0 {
		return nil
	}
	return &gopom.Properties{Entries: properties}
}

// ParsePom parses a POM file and returns a gopom.Project.
func ParsePom(pomPath string) (*gopom.Project, error) {
	// Check file size before reading to prevent resource exhaustion.
	fileInfo, err := os.Stat(pomPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat POM file %s: %w", pomPath, err)
	}
	if fileInfo.Size() > MaxPomFileSize {
		return nil, fmt.Errorf("%w: POM file %s is %d bytes (max: %d)", ErrFileTooLarge, pomPath, fileInfo.Size(), MaxPomFileSize)
	}

	project, err := gopom.Parse(pomPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse POM file %s: %w", pomPath, err)
	}
	return project, nil
}

// parsePatchesFromFile reads and parses patches from a YAML file.
func parsePatchesFromFile(ctx context.Context, patchFile string) ([]Patch, error) {
	var patchList PatchList
	absPath, err := filepath.Abs(patchFile)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve patch file path: %w", err)
	}
	file, err := os.Open(absPath) //nolint:gosec // G304: path is a user-supplied CLI flag resolved to absolute
	if err != nil {
		return nil, fmt.Errorf("failed reading file: %w", err)
	}
	// Ensure we handle err from file.Close()
	defer func() {
		if err := file.Close(); err != nil {
			clog.FromContext(ctx).Warnf("failed to close file: %v", err)
		}
	}()
	// Limit file size to prevent resource exhaustion
	byteValue, err := io.ReadAll(io.LimitReader(file, MaxPatchFileSize))
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}
	// Check if file was truncated (too large)
	if len(byteValue) >= MaxPatchFileSize {
		return nil, fmt.Errorf("%w: patch file (max: %d bytes)", ErrFileTooLarge, MaxPatchFileSize)
	}
	if err := yaml.Unmarshal(byteValue, &patchList); err != nil {
		return nil, err
	}
	for i := range patchList.Patches {
		if patchList.Patches[i].Scope == "" {
			patchList.Patches[i].Scope = defaultScope
		}
		if patchList.Patches[i].Type == "" {
			patchList.Patches[i].Type = defaultType
		}
	}
	return patchList.Patches, nil
}

// parsePatches parses Maven patches from a file or inline string.
// Ported from pombump/pkg/patch.go.
func parsePatches(ctx context.Context, patchFile, patchFlag string) ([]Patch, error) {
	if patchFile != "" {
		return parsePatchesFromFile(ctx, patchFile)
	}
	dependencies := strings.Split(patchFlag, " ")
	patches := []Patch{}
	for _, dep := range dependencies {
		if dep == "" {
			continue
		}
		parts := strings.Split(dep, "@")
		if len(parts) < 3 {
			return nil, fmt.Errorf("%w (%s): each dependency should be in the format <groupID@artifactID@version[@scope]>", ErrInvalidDependencyFormat, dep)
		}
		// Default scope
		scope := defaultScope
		if len(parts) >= 4 {
			scope = parts[3]
		}
		depType := defaultType
		if len(parts) >= 5 {
			depType = parts[4]
		}
		patches = append(patches, Patch{GroupID: parts[0], ArtifactID: parts[1], Version: parts[2], Scope: scope, Type: depType})
	}
	return patches, nil
}

// parsePropertiesFromFile reads and parses properties from a YAML file.
func parsePropertiesFromFile(ctx context.Context, propertyFile string) (map[string]string, error) {
	var propertyList PropertyList
	absPath, err := filepath.Abs(propertyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve properties file path: %w", err)
	}
	file, err := os.Open(absPath) //nolint:gosec // G304: path is a user-supplied CLI flag resolved to absolute
	if err != nil {
		return nil, fmt.Errorf("failed reading file: %w", err)
	}
	// Ensure we handle err from file.Close()
	defer func() {
		if err := file.Close(); err != nil {
			clog.FromContext(ctx).Warnf("failed to close file: %v", err)
		}
	}()
	// Limit file size to prevent resource exhaustion
	byteValue, err := io.ReadAll(io.LimitReader(file, MaxPatchFileSize))
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}
	// Check if file was truncated (too large)
	if len(byteValue) >= MaxPatchFileSize {
		return nil, fmt.Errorf("%w: properties file (max: %d bytes)", ErrFileTooLarge, MaxPatchFileSize)
	}
	if err := yaml.Unmarshal(byteValue, &propertyList); err != nil {
		return nil, err
	}
	propertiesPatches := make(map[string]string)
	for _, v := range propertyList.Properties {
		propertiesPatches[v.Property] = v.Value
	}
	return propertiesPatches, nil
}

// parseProperties parses Maven properties from a file or inline string.
// Ported from pombump/pkg/patch.go.
func parseProperties(ctx context.Context, propertyFile, propertiesFlag string) (map[string]string, error) {
	if propertyFile != "" {
		return parsePropertiesFromFile(ctx, propertyFile)
	}

	propertiesPatches := make(map[string]string)
	for prop := range strings.SplitSeq(propertiesFlag, " ") {
		if prop == "" {
			continue
		}
		parts := strings.Split(prop, "@")
		if len(parts) != 2 {
			return nil, fmt.Errorf("%w: each property should be in the format <property@value>", ErrInvalidPropertyFormat)
		}
		propertiesPatches[parts[0]] = parts[1]
	}

	return propertiesPatches, nil
}
