// Package opskubelinter is the atom ops:kube-linter: every workload under flux/
// passes kube-linter's default checks. One file holds the atom and both runners
// execute it (lld.md FN-085).
package opskubelinter

import (
	"context"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. kube-linter is the pinned binary the tools container carries,
// copied out of the stackrox image the chain ran it in.
var Def = atomkit.Def{
	ID: "ops:kube-linter", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "Every workload under flux/ passes kube-linter's default checks.",
	Surface:   "any:**",
	Toolchain: "none",
	Needs:     []string{"tool:kube-linter"},
	Check:     check,
}

const id = "ops:kube-linter"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check: every workload under flux/ passes kube-linter's default checks.
//
// --fail-if-no-objects-found is kube-linter's own zero-population refusal, and
// it exits 1 for it - the same code it uses for findings. Reading that as
// findings would be wrong in the direction that still looks like the check
// worked, so the message is matched and remapped to 2 - in
// checks.KubeLinterState, where a table test holds it, rather than in a case
// statement nobody can run.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	if in.FilesErr != nil {
		return checks.CannotEnumerate(row, in.FilesErr)
	}
	root, ok := checks.FluxRoot(in.Files)
	if !ok {
		return checks.VerdictOf(row, int(checks.StatePass), id+": ABSENT - no Flux tree here: neither flux/ nor clusters/<name>/kustomization.yaml at the root.")
	}
	// WHAT TO LINT. The infra shape keeps every manifest under flux/, so the
	// directory is the population. A repository that IS the Flux tree keeps
	// them in one directory per Kustomization beside docs and tooling, so the
	// population is exactly the paths its CRs apply (and clusters/ itself).
	targets := []string{"flux/"}
	if root == "" {
		manifests := map[string]string{}
		t := in.Tree()
		for _, m := range checks.OpsFluxClusterManifests(checks.OpsFiles(in.Files), root) {
			src, err := t.Read(m)
			if err != nil {
				return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - "+m+" could not be read: "+err.Error())
			}
			manifests[m] = src
		}
		paths, _ := checks.OpsFluxPaths(manifests)
		if len(paths) == 0 {
			paths = checks.OpsFluxFallback(checks.OpsFiles(in.Files), root)
		}
		targets = append([]string{"clusters/"}, paths...)
	}

	// The refusal arrives on stderr and the findings on stdout, and the code
	// is the same 1 for both - so both streams are read before anything is
	// decided.
	out, code := in.Run(ctx, atoms.Cmd{Dir: in.Root, Name: "kube-linter", Args: append([]string{"lint", "--fail-if-no-objects-found"}, targets...), Both: true})
	if code < 0 {
		return checks.VerdictOf(row, int(checks.StateCannotRun), "the atom never ran: "+out)
	}
	state, reason := checks.KubeLinterState(code, out)
	return checks.VerdictOf(row, state, reason)
}
