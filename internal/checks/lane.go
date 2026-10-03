package checks

import (
	"sort"
	"strings"
)

// Lane is a language a repository actually builds. A lane is a FACT ABOUT THE
// TREE — the presence of the manifest that declares it — not a label somebody
// attached to the repo. That is the same predicate `sast-ruleset-lanes` uses,
// and for the same reason: a heuristic can be argued with, a manifest cannot.
type Lane string

const (
	// LaneAny marks an atom that runs in every repository, whatever it is
	// written in.
	LaneAny Lane = ""

	LaneGo     Lane = "go"
	LanePython Lane = "python"
	LaneRust   Lane = "rust"
	LaneTS     Lane = "ts"
)

// laneManifest maps the root manifest that DECLARES a lane to that lane.
//
// Root-relative on purpose, for every lane but Go. A vendored go.mod three
// directories down does not make a python star a Go repo, and a lane check that
// fired on one would report CANNOT RUN forever on a repo with nothing to check.
//
// THE GO LANE IS NOT READ FROM HERE ANY MORE (2026-09-16, Rob: fleet wide). A
// root-only rule also left every NESTED module ungated — foundry-stocks'
// tools/forge had no go.mod above it, so the forge that publishes the fleet's
// base images ran none of the lane. verdictFor declares Go from GoModuleDirs,
// which takes a go.mod anywhere the go command would build one and keeps the
// vendored case out. go.mod stays in this map so Lanes, the catalogue and the
// other lanes' root reads still name it.
var laneManifest = map[string]Lane{
	"go.mod":         LaneGo,
	"pyproject.toml": LanePython,
	"Cargo.toml":     LaneRust,
	"package.json":   LaneTS,
}

// ManifestFor answers the root manifest whose presence declares this lane.
func ManifestFor(l Lane) string {
	for manifest, lane := range laneManifest {
		if lane == l {
			return manifest
		}
	}
	return ""
}

// LanesOf reports every lane the given repository-root entries declare, sorted.
//
// A repo may declare several — themis and urania are both Go and Python — and
// each declared lane's atoms run. A lane it does not declare reports ABSENT and
// exits 0 saying so, which is Rob's rule for the canonical config: "Repos that
// don't have that filetype will just skip the hook and report not-present."
func LanesOf(entries []string) []Lane {
	seen := map[Lane]bool{}
	for _, e := range entries {
		if lane, ok := laneManifest[e]; ok {
			seen[lane] = true
		}
	}
	out := make([]Lane, 0, len(seen))
	for lane := range seen {
		out = append(out, lane)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// SurfaceNamespaces are the cross-lane namespaces that are NOT `fleet:`, and
// the surface each one's atoms find for themselves inside the tree.
//
// WHY THIS EXISTS. Until 2026-09-10 every cross-lane atom on a pull's path was
// `fleet:`, and the rule was written as "a cross-lane atom is namespaced by WHEN
// it runs, because that is the only thing left to namespace it by". That premise
// was true while `fleet:` meant BOTH "runs everywhere" and "cross-lane", and it
// stopped being true when the act-runner's last workflows were ported: those
// atoms run in every repository the way a fleet atom does, and in almost all of
// them the answer is ABSENT, because their surface is a fact about the tree
// rather than a root manifest Lane can name.
//
// Calling them `fleet:` would have been the actual defect. `fleet:` is the
// namespace whose atoms have something to say about EVERY repository — that is
// what makes `dagger check fleet:` a meaningful thing to type — and filing nine
// atoms there that report ABSENT in 80-odd of 86 repos would drown the six that
// do not. So the namespace names the surface.
//
// THE SET IS CLOSED, and that is what keeps this from being a licence to invent
// a namespace per atom. A new one is an edit here, next to the sentence saying
// what surface it claims, and TestEveryAtomIsWellFormed refuses any cross-lane
// id that is neither `fleet:` nor a member. The sweep's own invariant is
// The sweep's own invariant is moot since 2026-09-23: StageSweep is deleted
// and there is no clock left for a namespace to blur with.
var SurfaceNamespaces = map[string]string{
	"compose": "a tracked compose.yaml/compose.yml — the host-stacks repos",
	"dies":    "policy/.manifest and fleet/stars/ together — the policy die's source",
	// ops: the trees the cluster and the hosts converge to (2026-09-13,
	// Tekton's ci-ops-pipeline ported): flux/, ansible/ playbooks, a chezmoi
	// source, a compose spec, a rego policy, or one of infra's own tools —
	// IsOpsTree, the shape ops.sh gated and never a star.
	"ops": "an ops tree — flux/, ansible/playbooks, a chezmoi source, a compose spec, a rego policy or an infra tool (IsOpsTree)",
	// template: a ci-matrix.toml at the root — the four copier templates, and
	// nothing else. Added 2026-09-23 (CA F18) when sweep:template-render-matrix
	// came home: four repos of eighty carry the surface, which is exactly the
	// argument above for why it is NOT `fleet:`.
	"template": "a ci-matrix.toml at the root — the copier templates",
	// orbit: the orbit lane (2026-10-03), its own stage and Job. Its surface
	// is a seam: the contracts' repository (orbits/ beside fleet/stars/), a
	// star that is party to a contract, flux's prime/orbits, or a laid root
	// orbit.toml — each atom finds its own and answers ABSENT elsewhere.
	"orbit": "a seam — foundry-dies/orbits, a contracted star, flux prime/orbits, or a laid orbit.toml",
}

// IsSurfaceNamespace reports whether an atom id sits in a declared surface
// namespace.
func IsSurfaceNamespace(id string) bool {
	for ns := range SurfaceNamespaces {
		if strings.HasPrefix(id, ns+":") {
			return true
		}
	}
	return false
}

// DeclaresLane reports whether these repository-root entries declare the lane.
// LaneAny is declared by every repository, including an empty one.
func DeclaresLane(entries []string, l Lane) bool {
	if l == LaneAny {
		return true
	}
	for _, lane := range LanesOf(entries) {
		if lane == l {
			return true
		}
	}
	return false
}
