/*
Copyright 2026 Chainguard, Inc.
SPDX-License-Identifier: Apache-2.0
*/

package revdep

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var (
	errTreeRequired  = errors.New("revdep: Options.Tree is required")
	errRootMismatch  = errors.New("tree root does not match requested crate")
	errNoPublished   = errors.New("crate has no published versions")
	errTargetTooHigh = errors.New("target is higher than the latest published version")
	errNotMember     = errors.New("crate is absent from the index but is not a known workspace member")
)

// TreeProvider yields `cargo tree -i <crate> -e normal,build --charset ascii
// --prefix depth` output rooted in the target workspace. It is injected so the
// caller controls how cargo is invoked (toolchain, working directory) and tests
// can feed canned trees.
type TreeProvider func(ctx context.Context, crate string) (string, error)

// Options configures a Calculate run.
type Options struct {
	// Tree provides the inverted dependency tree for a crate. Required.
	Tree TreeProvider
	// Index answers version queries. Defaults to a live crates.io client built
	// from IndexURL/HTTP when nil.
	Index Fetcher
	// IndexURL is the sparse-index base URL used when Index is nil.
	IndexURL string
	// HTTP is the client used when Index is nil.
	HTTP *http.Client
	// AllowPre considers pre-release versions as upgrade candidates.
	AllowPre bool
	// WorkspaceMembers names the project's local crates. A crate that is absent
	// from the index is only treated as a local (editable) crate when it is a
	// known member (or cargo annotated it with a path); this guards crates from
	// private registries that merely 404 on crates.io. When nil, any index miss
	// is treated as local (the standalone tool's behavior).
	WorkspaceMembers map[string]bool
}

// DirectEdit says a workspace member must widen its constraint on Dependency to
// allow at least MinVersion.
type DirectEdit struct {
	Member     string
	Dependency string
	MinVersion string
}

// Boundary is a published crate to pin with `cargo update -p Crate@From --precise To`.
type Boundary struct {
	Crate string
	From  string
	To    string
}

// Plan is the result of Calculate: the direct-dependency manifest edits and the
// precise version pins needed to make the target reachable.
type Plan struct {
	Target     string
	From       string
	To         string
	Edits      []DirectEdit
	Boundaries []Boundary
}

// Empty reports whether the plan requires no changes (the target is already
// satisfied, or nothing needs upgrading).
func (p *Plan) Empty() bool {
	return len(p.Edits) == 0 && len(p.Boundaries) == 0
}

// Calculate walks the inverted dependency tree for crate and computes the
// direct-dependency edits and precise boundary pins needed to make targetVersion
// (a floor, i.e. ">= target") reachable. An empty, non-error Plan means the crate
// already satisfies the floor.
func Calculate(ctx context.Context, crate, targetVersion string, opts Options) (*Plan, error) {
	if opts.Tree == nil {
		return nil, errTreeRequired
	}
	target, err := ParseVersion(targetVersion)
	if err != nil {
		return nil, fmt.Errorf("invalid target version %q: %w", targetVersion, err)
	}

	treeText, err := opts.Tree(ctx, crate)
	if err != nil {
		return nil, err
	}
	root, err := ParseTree(treeText)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(root.Name, crate) {
		return nil, fmt.Errorf("%w: tree root is %q but requested %q", errRootMismatch, root.Name, crate)
	}
	rootCurrent, err := ParseVersion(root.Version)
	if err != nil {
		return nil, fmt.Errorf("parsing current version of %s: %w", root.Name, err)
	}

	plan := &Plan{Target: crate, From: root.Version, To: target.String()}

	// The target is a floor: if the crate already satisfies it, nothing changes.
	if rootCurrent.Compare(target) >= 0 {
		return plan, nil
	}

	client := opts.Index
	if client == nil {
		indexURL := opts.IndexURL
		if indexURL == "" {
			indexURL = DefaultIndexURL
		}
		client = NewClient(indexURL, opts.HTTP)
	}

	// Fail early with a clear message if the target simply doesn't exist yet. A
	// pre-release target implies pre-releases are in play, so consider them here too.
	if maxV, ok, err := client.MaxVersion(ctx, root.Name, opts.AllowPre || target.Pre != ""); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("%w: %s", errNoPublished, root.Name)
	} else if maxV.Compare(target) < 0 {
		return nil, fmt.Errorf("%w: target %s > latest published %s (%s)", errTargetTooHigh, target, root.Name, maxV)
	}

	calc := &calculator{
		client:      client,
		allowPre:    opts.AllowPre,
		members:     opts.WorkspaceMembers,
		byName:      map[string]*result{},
		localByKey:  map[string]*localEdit{},
		boundaryVer: map[string]string{},
	}
	calc.record(root.Name, root.Version, target.String())
	if err := calc.walk(ctx, root, target); err != nil {
		return nil, err
	}

	for _, key := range calc.localOrder {
		e := calc.localByKey[key]
		plan.Edits = append(plan.Edits, DirectEdit{Member: e.member, Dependency: e.dependency, MinVersion: e.minVersion})
	}
	for _, name := range calc.boundaryOrder {
		b := Boundary{Crate: name, To: calc.boundaryVer[name]}
		if r, ok := calc.byName[name]; ok {
			b.From = r.from
		}
		plan.Boundaries = append(plan.Boundaries, b)
	}
	return plan, nil
}

// result is one published crate that must be upgraded.
type result struct {
	crate string
	from  string
	to    string
}

// localEdit is a workspace crate whose direct dependency must be bumped.
type localEdit struct {
	member     string
	dependency string
	minVersion string
}

// calculator carries the shared state for the recursive walk.
type calculator struct {
	client   Fetcher
	allowPre bool
	members  map[string]bool

	byName map[string]*result
	order  []string

	localByKey map[string]*localEdit
	localOrder []string

	// boundaries are the published crates a workspace crate depends on directly
	// (or direct dependencies at a leaf of the tree). These are pinned with
	// `cargo update`; transitive crates follow automatically.
	boundaryVer   map[string]string
	boundaryOrder []string
}

// changed reports whether a recorded crate actually needs a version bump.
func (c *calculator) changed(crate string) bool {
	r, ok := c.byName[crate]
	return ok && r.from != r.to
}

// knownLocal reports whether a node is definitely a local (editable) workspace
// crate, decided without consulting the index: cargo annotated it with a path, or
// it is in the caller-supplied member set. Checked before any index lookup so
// workspace members are never queried against crates.io (a member name may also
// exist as an unrelated published crate).
func (c *calculator) knownLocal(n *TreeNode) bool {
	if n.Path != "" {
		return true
	}
	return c.members != nil && c.members[n.Name]
}

// recordBoundary notes a published crate to pin, keeping the highest version if
// it is reached more than once.
func (c *calculator) recordBoundary(crate, ver string) {
	if cur, ok := c.boundaryVer[crate]; ok {
		a, e1 := ParseVersion(cur)
		b, e2 := ParseVersion(ver)
		if e1 == nil && e2 == nil && b.Compare(a) > 0 {
			c.boundaryVer[crate] = ver
		}
		return
	}
	c.boundaryVer[crate] = ver
	c.boundaryOrder = append(c.boundaryOrder, crate)
}

// recordLocal notes a workspace crate that must have its dependency bumped,
// keeping the highest required version per (member, dependency) pair.
func (c *calculator) recordLocal(member, dep string, minVer Version) {
	key := member + "\x00" + dep
	if e, ok := c.localByKey[key]; ok {
		cur, e1 := ParseVersion(e.minVersion)
		if e1 == nil && minVer.Compare(cur) > 0 {
			e.minVersion = minVer.String()
		}
		return
	}
	c.localByKey[key] = &localEdit{member: member, dependency: dep, minVersion: minVer.String()}
	c.localOrder = append(c.localOrder, key)
}

// record stores an upgrade, keeping the highest target when a crate is reached
// via multiple paths.
func (c *calculator) record(crate, from, to string) {
	if r, ok := c.byName[crate]; ok {
		cur, e1 := ParseVersion(r.to)
		next, e2 := ParseVersion(to)
		if e1 == nil && e2 == nil && next.Compare(cur) > 0 {
			r.to = to
		}
		return
	}
	c.byName[crate] = &result{crate: crate, from: from, to: to}
	c.order = append(c.order, crate)
}

// walk visits each dependent of node. node must resolve to some version >=
// nodeFloor; for every child (a crate depending on node) it finds the minimal
// child version that permits an acceptable node version, then recurses with that
// child version as the new floor.
func (c *calculator) walk(ctx context.Context, node *TreeNode, nodeFloor Version) error {
	// A leaf published crate that changed is a direct dependency of the (unshown)
	// consumer, so it is a boundary to pass to `cargo update`.
	if len(node.Children) == 0 {
		if c.changed(node.Name) {
			c.recordBoundary(node.Name, nodeFloor.String())
		}
		return nil
	}
	acceptable, err := c.client.VersionsAtLeast(ctx, node.Name, nodeFloor, c.allowPre)
	if err != nil {
		return err
	}
	hasWorkspaceChild := false
	for _, child := range node.Children {
		local, err := c.walkChild(ctx, node, child, nodeFloor, acceptable)
		if err != nil {
			return err
		}
		if local {
			hasWorkspaceChild = true
		}
	}
	// If this changed crate is consumed directly by a workspace crate, it is the
	// version boundary the user pins with `cargo update`.
	if c.changed(node.Name) && hasWorkspaceChild {
		c.recordBoundary(node.Name, nodeFloor.String())
	}
	return nil
}

// walkChild resolves one dependent (child) of node. It returns local=true when the
// child is a workspace crate (the manifest edit point); otherwise it records the
// child's minimal version bump and recurses. A workspace crate only needs a manual
// edit when node actually changed; otherwise its existing requirement is fine.
func (c *calculator) walkChild(ctx context.Context, node, child *TreeNode, nodeFloor Version, acceptable []Version) (local bool, err error) {
	nodeChanged := c.changed(node.Name)

	// A workspace member (or cargo-annotated path crate) is where the manifest edit
	// happens; it is never a published crate, so do not query the index for it (its
	// name may collide with an unrelated crates.io crate).
	if c.knownLocal(child) {
		if nodeChanged {
			c.recordLocal(child.Name, node.Name, nodeFloor)
		}
		return true, nil
	}

	floor, err := ParseVersion(child.Version)
	if err != nil {
		return false, fmt.Errorf("parsing current version of %s: %w", child.Name, err)
	}
	minVer, err := c.client.MinVersionRequiring(ctx, child.Name, floor, node.Name, nodeFloor, acceptable, c.allowPre)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			return false, err
		}
		// Absent from the index but not a declared member. With no member list
		// (standalone use) treat it as a local path crate; otherwise it is
		// unexpected (e.g. a private-registry crate) and cannot be resolved.
		if c.members != nil {
			return false, fmt.Errorf("%w: %s (dependent of %s)", errNotMember, child.Name, node.Name)
		}
		if nodeChanged {
			c.recordLocal(child.Name, node.Name, nodeFloor)
		}
		return true, nil
	}

	c.record(child.Name, child.Version, minVer.String())
	return false, c.walk(ctx, child, minVer)
}
