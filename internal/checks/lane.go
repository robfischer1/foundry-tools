package checks

import "sort"

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
// Root-relative on purpose. A vendored go.mod three directories down does not
// make a python star a Go repo, and a lane check that fired on one would report
// CANNOT RUN forever on a repo with nothing to check.
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
