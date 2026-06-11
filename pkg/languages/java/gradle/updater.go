/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package gradle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/omnibump/pkg/gradlefile"
	"github.com/chainguard-dev/omnibump/pkg/languages"
	"github.com/chainguard-dev/omnibump/pkg/pathutil"
)

var (
	// ErrVersionConflict is returned when two updates try to set different
	// versions for the same dependency, variable or catalog key.
	ErrVersionConflict = errors.New("conflicting version update detected")

	// ErrPropertyNotFound is returned when a requested property update does
	// not match any catalog version key, version variable or properties-file
	// entry in the project.
	ErrPropertyNotFound = errors.New("property not found")
)

// document is the common surface of all parsed gradlefile documents the
// apply phase needs.
type document interface {
	Content() []byte
	Changed() bool
	ChangeCount() int
}

// updatePlan tracks the outcome of strategy resolution: edits are queued
// directly on the model's parsed documents; deps satisfied nowhere are
// collected for the managed force block.
type updatePlan struct {
	model *projectModel

	// forced maps "group:artifact" to the version to pin via the managed
	// resolutionStrategy force block.
	forced map[string]string

	// requested tracks the version requested per routing target for
	// conflict detection, keyed by a human-readable target description.
	requested map[string]string

	// properties are the explicit property updates; they take precedence
	// over dependency-routed updates of the same key.
	properties map[string]string
}

// resolveUpdates routes every property and dependency update to its best
// definition site, queuing edits on the parsed documents. It mirrors Maven's
// precedence rules: explicit properties win over dependency patches that
// route to the same key, and conflicting versions for one target are an
// error.
func resolveUpdates(ctx context.Context, model *projectModel, cfg *languages.UpdateConfig) (*updatePlan, error) {
	plan := &updatePlan{
		model:      model,
		forced:     make(map[string]string),
		requested:  make(map[string]string),
		properties: cfg.Properties,
	}

	if err := plan.applyProperties(ctx); err != nil {
		return nil, err
	}

	for _, dep := range cfg.Dependencies {
		if err := plan.applyDependency(ctx, dep); err != nil {
			return nil, err
		}
	}

	return plan, nil
}

// requireVersion records the version requested for a routing target and
// errors when a different version was already requested for it.
func (p *updatePlan) requireVersion(target, version string) error {
	if existing, ok := p.requested[target]; ok && existing != version {
		return fmt.Errorf("%w: %s requested at both %s and %s", ErrVersionConflict, target, existing, version)
	}
	p.requested[target] = version
	return nil
}

// applyProperties applies cfg.Properties to their definition sites. The
// lookup order mirrors the mechanisms' specificity: catalog version keys
// first, then version variables (gradle.properties, ext definitions,
// version.properties files). A property found nowhere is a hard error,
// matching Maven.
func (p *updatePlan) applyProperties(ctx context.Context) error {
	log := clog.FromContext(ctx)

	names := make([]string, 0, len(p.properties))
	for name := range p.properties {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		value := p.properties[name]
		if err := p.requireVersion("property "+name, value); err != nil {
			return err
		}
		catalogSites := p.model.catalogVersionSites[name]
		variableSites := p.model.variableSitesFor(name)
		switch {
		case len(catalogSites) > 0:
			if len(variableSites) > 0 {
				log.Warnf("Property %s matches both a catalog version key and a variable; updating the catalog key", name)
			}
			for _, site := range catalogSites {
				if err := site.set(value); err != nil {
					return fmt.Errorf("failed to update catalog version %s in %s: %w", name, site.path(), err)
				}
				log.Infof("Updating catalog version %s from %s to %s in %s", name, site.version.Value, value, site.path())
			}
		case len(variableSites) > 0:
			for _, site := range variableSites {
				if err := site.set(value); err != nil {
					return fmt.Errorf("failed to update variable %s in %s: %w", name, site.path(), err)
				}
				log.Infof("Updating variable %s from %s to %s in %s", name, site.value(), value, site.path())
			}
		default:
			return fmt.Errorf("%w: %s is not a catalog version key, version variable or properties-file entry", ErrPropertyNotFound, name)
		}
	}
	return nil
}

// applyDependency routes one dependency update. Every definition site found
// for the module is updated (catalog entries, declarations, variables they
// reference); when no site exists at all the module is recorded for the
// managed force block — the Gradle analog of Maven adding the dependency to
// DependencyManagement.
func (p *updatePlan) applyDependency(ctx context.Context, dep languages.Dependency) error {
	log := clog.FromContext(ctx)

	if dep.Version == "" {
		log.Warnf("Skipping dependency %s: no target version", depDisplayName(dep))
		return nil
	}
	group, artifact, err := depCoordinates(dep)
	if err != nil {
		return err
	}
	module := group + ":" + artifact

	if dep.Scope != "" || dep.Type != "" {
		log.Debugf("Ignoring scope/type for %s: not applicable to Gradle", module)
	}

	catalogHandled, err := p.applyCatalogTier(ctx, module, dep.Version)
	if err != nil {
		return err
	}
	declHandled, err := p.applyDeclarationTier(ctx, module, artifact, dep.Version)
	if err != nil {
		return err
	}
	ruleHandled, err := p.applyRuleTier(ctx, module, group, artifact, dep.Version)
	if err != nil {
		return err
	}

	if catalogHandled || declHandled || ruleHandled {
		return nil
	}

	log.Infof("Dependency %s not declared in any Gradle file: pinning via resolutionStrategy force block", module)
	if err := p.requireVersion("dependency "+module, dep.Version); err != nil {
		return err
	}
	p.forced[module] = dep.Version
	return nil
}

// applyCatalogTier updates version-catalog entries for module: the
// referenced [versions] key, or the library's inline version. Strictly
// blocks tied to the module's catalog alias are kept consistent.
func (p *updatePlan) applyCatalogTier(ctx context.Context, module, version string) (bool, error) {
	log := clog.FromContext(ctx)
	handled := false

	for _, site := range p.model.catalogLibrarySites[module] {
		library := site.library
		switch {
		case library.VersionRef != "":
			ok, err := p.applyCatalogRef(ctx, module, library.VersionRef, version)
			if err != nil {
				return false, err
			}
			handled = handled || ok
		case library.Version != "":
			// Settings-script catalogs only declare libraries via
			// versionRef, so inline versions always live in a TOML catalog.
			if site.catalog == nil {
				continue
			}
			if err := p.requireVersion("dependency "+module, version); err != nil {
				return false, err
			}
			if err := site.catalog.SetLibraryVersion(library, version); err != nil {
				return false, fmt.Errorf("failed to update catalog library %s in %s: %w", library.Alias, site.path(), err)
			}
			log.Infof("Updating catalog library %s (%s) from %s to %s in %s",
				library.Alias, module, library.Version, version, site.path())
			handled = true
		}
	}

	return handled, nil
}

// editSite is one definition site of a keyed value (catalog version key or
// version variable) that can be rewritten in place.
type editSite interface {
	set(value string) error
	path() string
}

// applyCatalogRef updates all definition sites of a catalog version key on
// behalf of module, honouring explicit-property precedence.
func (p *updatePlan) applyCatalogRef(ctx context.Context, module, key, version string) (bool, error) {
	return p.applyKeyedSites(ctx, module, "catalog version", key, version, toEditSites(p.model.catalogVersionSites[key]))
}

// toEditSites adapts a concrete site slice to the editSite interface.
func toEditSites[S editSite](sites []S) []editSite {
	adapted := make([]editSite, len(sites))
	for i, site := range sites {
		adapted[i] = site
	}
	return adapted
}

// applyRuleTier updates the version source of dependency resolve rules that
// match module: a rule reading a catalog version accessor routes to that
// [versions] key, a rule interpolating a variable routes to the variable's
// definition, and a rule with a literal version is edited in place. This
// links modules to their governing version when the project pins them only
// through resolutionStrategy.eachDependency (kafbat/kayenta pattern).
func (p *updatePlan) applyRuleTier(ctx context.Context, module, group, artifact, version string) (bool, error) {
	log := clog.FromContext(ctx)
	handled := false

	for _, site := range p.model.resolutionRuleSites[group] {
		rule := site.rule
		if rule.Artifact != "" && rule.Artifact != artifact {
			continue
		}
		switch {
		case rule.CatalogKey != "":
			ok, err := p.applyCatalogRef(ctx, module, rule.CatalogKey, version)
			if err != nil {
				return false, err
			}
			handled = handled || ok
		case rule.VarRef != "":
			ok, err := p.applyVariableOrCatalogRef(ctx, module, rule.VarRef, version)
			if err != nil {
				return false, err
			}
			handled = handled || ok
		case rule.Version != "":
			if err := p.requireVersion("dependency "+module, version); err != nil {
				return false, err
			}
			if err := site.build.SetResolutionRuleVersion(rule, version); err != nil {
				return false, fmt.Errorf("failed to update resolution rule for %s in %s: %w", module, site.build.Path(), err)
			}
			log.Infof("Patching %s via resolution rule from %s to %s in %s", module, rule.Version, version, site.build.Path())
			handled = true
		}
	}

	return handled, nil
}

// applyVariableOrCatalogRef routes a variable reference: definition sites
// (ext properties, ext maps, properties files) win; otherwise paths shaped
// like catalog version accessors (versions.x / libs.versions.x) fall back to
// the catalog version key they bridge to.
func (p *updatePlan) applyVariableOrCatalogRef(ctx context.Context, module, varPath, version string) (bool, error) {
	if _, explicit := p.properties[varPath]; !explicit && len(p.model.variableSites[varPath]) == 0 {
		if key, ok := p.model.catalogKeyForVarPath(varPath); ok {
			return p.applyCatalogRef(ctx, module, key, version)
		}
	}
	return p.applyVariableRef(ctx, module, varPath, version)
}

// applyVariableRef updates all definition sites of a version variable on
// behalf of module, honouring explicit-property precedence.
func (p *updatePlan) applyVariableRef(ctx context.Context, module, varPath, version string) (bool, error) {
	return p.applyKeyedSites(ctx, module, "variable", varPath, version, toEditSites(p.model.variableSites[varPath]))
}

// applyKeyedSites updates every definition site of a named key on behalf of
// module. Explicit cfg.Properties for the same key take precedence (a
// mismatching dependency version is a conflict); a key with no definition
// site is left to the caller's fallback handling.
func (p *updatePlan) applyKeyedSites(ctx context.Context, module, kind, key, version string, sites []editSite) (bool, error) {
	log := clog.FromContext(ctx)

	if explicit, isExplicit := p.properties[key]; isExplicit {
		if explicit != version {
			return false, fmt.Errorf("%w: dependency %s requests %s but property %s is explicitly set to %s",
				ErrVersionConflict, module, version, key, explicit)
		}
		log.Infof("Dependency %s is covered by explicit property %s", module, key)
		return true, nil
	}

	if len(sites) == 0 {
		log.Warnf("Dependency %s references %s %s which is not defined in this project", module, kind, key)
		return false, nil
	}
	if err := p.requireVersion(kind+" "+key, version); err != nil {
		return false, err
	}
	for _, site := range sites {
		if err := site.set(version); err != nil {
			return false, fmt.Errorf("failed to update %s %s in %s: %w", kind, key, site.path(), err)
		}
		log.Infof("Patching %s via %s %s in %s to %s", module, kind, key, site.path(), version)
	}
	return true, nil
}

// applyDeclarationTier updates direct build-script declarations of module:
// literal versions in place, variable-referenced versions at the variable's
// definition sites, and Spring Boot library() entries matched by artifact.
func (p *updatePlan) applyDeclarationTier(ctx context.Context, module, artifact, version string) (bool, error) {
	log := clog.FromContext(ctx)
	handled := false

	for _, site := range p.model.declarationSites[module] {
		decl := site.decl
		switch {
		case decl.VarRef != "":
			ok, err := p.applyVariableOrCatalogRef(ctx, module, decl.VarRef, version)
			if err != nil {
				return false, err
			}
			handled = handled || ok
		case decl.Version != "":
			if err := p.requireVersion("dependency "+module, version); err != nil {
				return false, err
			}
			if err := site.build.SetDependencyVersion(decl, version); err != nil {
				return false, fmt.Errorf("failed to update %s in %s: %w", module, site.build.Path(), err)
			}
			log.Infof("Patching %s from %s to %s in %s", module, decl.Version, version, site.build.Path())
			handled = true
		}
	}

	for _, site := range p.model.libraryFnSites[artifact] {
		if err := p.requireVersion("dependency "+module, version); err != nil {
			return false, err
		}
		if err := site.build.SetDependencyVersion(site.decl, version); err != nil {
			return false, fmt.Errorf("failed to update library(%q) in %s: %w", artifact, site.build.Path(), err)
		}
		log.Infof("Patching library(%q) from %s to %s in %s", artifact, site.decl.Version, version, site.build.Path())
		handled = true
	}

	return handled, nil
}

// apply writes every changed document back to disk, injecting the managed
// force block first when needed. Honors dry-run and validates every write
// stays within the project root.
func (p *updatePlan) apply(ctx context.Context, cfg *languages.UpdateConfig) error {
	log := clog.FromContext(ctx)

	newFilePath, newFileContent, err := p.injectForceBlock(ctx)
	if err != nil {
		return err
	}

	changes := 0
	for _, path := range p.model.sortedFiles {
		doc := p.model.document(path)
		if doc == nil || !doc.Changed() {
			continue
		}
		if err := pathutil.ValidatePathWithinRoot(cfg.RootDir, path); err != nil {
			return fmt.Errorf("refusing to update %s: %w", path, err)
		}
		if cfg.DryRun {
			log.Infof("Dry run mode: would write %d change(s) to %s", doc.ChangeCount(), path)
			changes += doc.ChangeCount()
			continue
		}
		if err := os.WriteFile(path, doc.Content(), gradleFilePerms); err != nil {
			return fmt.Errorf("failed to write %s: %w", path, err)
		}
		log.Infof("Successfully updated %s with %d change(s)", path, doc.ChangeCount())
		changes += doc.ChangeCount()
	}

	if newFilePath != "" {
		if err := writeNewForceFile(ctx, cfg, newFilePath, newFileContent); err != nil {
			return err
		}
		changes++
	}

	if changes == 0 {
		log.Infof("No Gradle changes needed: everything already at the requested versions")
	}
	return nil
}

// writeNewForceFile creates a brand-new root build script that hosts only
// the managed force block, honouring dry-run and root-boundary checks.
func writeNewForceFile(ctx context.Context, cfg *languages.UpdateConfig, path, content string) error {
	log := clog.FromContext(ctx)

	if err := pathutil.ValidatePathWithinRoot(cfg.RootDir, filepath.Dir(path)); err != nil {
		return fmt.Errorf("refusing to create %s: %w", path, err)
	}
	if cfg.DryRun {
		log.Infof("Dry run mode: would create %s with the managed force block", path)
		return nil
	}
	if err := os.WriteFile(path, []byte(content), gradleFilePerms); err != nil {
		return fmt.Errorf("failed to create %s: %w", path, err)
	}
	log.Infof("Created %s with the managed force block", path)
	return nil
}

// injectForceBlock queues the managed force block on the root build script,
// or prepares a new root build script when the project has none. Returns the
// path and content of the new file to create, if any.
func (p *updatePlan) injectForceBlock(ctx context.Context) (string, string, error) {
	if len(p.forced) == 0 {
		return "", "", nil
	}
	log := clog.FromContext(ctx)

	if root := p.model.rootBuildFile(); root != nil {
		if err := root.EnsureForceBlock(p.forced); err != nil {
			return "", "", fmt.Errorf("failed to update force block in %s: %w", root.Path(), err)
		}
		log.Infof("Pinning %d transitive dependencies via force block in %s", len(p.forced), root.Path())
		return "", "", nil
	}

	dsl := p.model.rootSettingsDSL()
	name := buildGradleFile
	if dsl == gradlefile.Kotlin {
		name = buildGradleKtsFile
	}
	content, err := gradlefile.NewBuildFileContent(dsl, p.forced)
	if err != nil {
		return "", "", fmt.Errorf("failed to render force block: %w", err)
	}
	path := filepath.Join(p.model.rootDir, name)
	log.Infof("Pinning %d transitive dependencies via new root build script %s", len(p.forced), path)
	return path, content, nil
}

// document returns the parsed document for path, whichever kind it is.
func (m *projectModel) document(path string) document {
	if f, ok := m.builds[path]; ok {
		return f
	}
	if f, ok := m.settings[path]; ok {
		return f
	}
	if f, ok := m.props[path]; ok {
		return f
	}
	if f, ok := m.catalogs[path]; ok {
		return f
	}
	return nil
}
