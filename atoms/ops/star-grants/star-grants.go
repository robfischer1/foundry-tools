// Package opsstargrants is the atom ops:star-grants: a pull that adds a star's
// Deployment to prime/ leaves the tree with that star's grants-<n> policy, or says
// why it has none (daedalus#25923). One file holds the atom and both runners
// execute it (lld.md FN-085).
package opsstargrants

import (
	"context"
	"fmt"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. It reads git (the merge base) and the tree's own manifests: no
// tool of its own.
var Def = atomkit.Def{
	ID: "ops:star-grants", Stage: checks.StagePrepush, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "A pull that adds prime/star-<n>.yaml with a Deployment also leaves a CiliumNetworkPolicy grants-<n> in prime/, or the Deployment carries forge.notusmi.com/no-grants with its reason — stars already on main are not graded.",
	Surface:   "any:**",
	Toolchain: "none",
	Check:     check,
}

const id = "ops:star-grants"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LaneAny}

// check lists the star manifests the pull adds against the merge base (the same
// resolve ops:immutable uses) and grades each with checks.StarGrants. No base (a
// tip or local run) has no pull to grade: ABSENT.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	settle := func(state int, reason string) checks.Verdict {
		return checks.VerdictOf(row, state, id+": "+reason)
	}
	if in.FilesErr != nil {
		return checks.CannotEnumerate(row, in.FilesErr)
	}
	var primeFiles []string
	star := false
	for _, f := range in.Files {
		if strings.HasPrefix(f, "prime/") && (strings.HasSuffix(f, ".yaml") || strings.HasSuffix(f, ".yml")) {
			primeFiles = append(primeFiles, f)
			star = star || strings.HasPrefix(f, "prime/star-")
		}
	}
	if !star {
		return settle(0, "ABSENT - no prime/star-*.yaml in this tree, so it is not the fleet's flux tree.")
	}
	if in.Base == "" {
		return settle(0, "ABSENT - no base to compare against (a tip or local run), so no star is being added.")
	}
	since, err := atoms.MergeBase(ctx, in.Root, in.Base)
	if err != nil {
		return settle(2, "CANNOT RUN - "+err.Error())
	}
	diff, rc := atoms.Git(ctx, in.Root, "diff", "-z", "--name-only", "--diff-filter=A", since, "HEAD", "--", "prime")
	if rc != 0 {
		return settle(2, fmt.Sprintf("CANNOT RUN - the added files against %s would not list (exit %d): %.200s", since, rc, diff))
	}
	files, names := checks.StarFiles(checks.SplitNul(diff))
	if len(files) == 0 {
		return settle(0, "PASS - no prime/star-*.yaml added against "+since+"; stars already on main are not graded.")
	}
	t := in.Tree()
	prime := map[string]string{}
	for _, f := range primeFiles {
		src, err := t.Read(f)
		if err != nil {
			return settle(2, "CANNOT RUN - "+f+" could not be read: "+err.Error())
		}
		prime[f] = src
	}
	report, bad, err := checks.StarGrants(files, names, prime)
	if err != nil {
		return settle(2, "CANNOT RUN - "+err.Error())
	}
	state := 0
	if bad {
		state = 1
	}
	return checks.VerdictOf(row, state, id+": "+report)
}
