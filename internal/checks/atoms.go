package checks

import (
	"fmt"
	"strings"
)

// Stage names where in a pull's life an atom fires. The catalogue carries the
// same values (nereus.antibody_store.stage), so a row and its code agree by
// construction.
const (
	StagePrecommit = "precommit"
	StagePrepush   = "prepush"
	// StageSweep WENT 2026-09-23 (CA F18). It was repo cadence — an atom that
	// asked about the REPOSITORY rather than the change, running on ca-sweep's
	// clock and never in a pull. The clock is deleted: its three atoms needed a
	// surface that five of eighty repos have, so the walk cloned eighty to ask
	// six questions. sweep:kubeconform was already subsumed by ops:flux, which
	// runs kubeconform with the same flags over the BUILT kustomize output; the
	// other two are ops:kube-linter and template:render-matrix, in the pull path
	// of the repos that can answer them.
	// StageMutation is the mutation gate: a pull's change set, mutated, with
	// the pull's own tests asked to notice. It IS about the change and it
	// DOES block a pull's green — but it is not in PullPathStages, because
	// the gate that asks for "the vector" runs inside one cap slot and a
	// mutation run is minutes on top of it. The door dispatches this stage
	// as its own lane (`mutation`, beside gate and build), by name, so the
	// two settle independently and the gate stays as short as it was.
	//
	// WHAT IT REPLACES. Tekton's mutation-* Pipelines, retired with the
	// engine on 2026-09-09; from then until this stage landed nothing in the
	// fleet mutated anything, while ca-sweep's header, the templates'
	// `critical_modules` question and eight comments in ourea all described
	// a gate that no longer ran (ourea#8319). The scripts survived in
	// foundry-stocks ci/lib/mutation/, and all four have since been ported
	// here as plain execs settled in Go.
	StageMutation = "mutation"
	// StageOrbit is the orbit lane (Rob, 2026-10-03: "Ultimately want orbit
	// validation to be a CI check/lane of its own"): the seam contracts, a
	// star's code against them, the composed sidecars and a star's laid
	// orbit.toml. Like mutation it is NOT in PullPathStages — the door asks
	// for it by name, as its own lane (`gate-file --stage=orbit`), so it
	// settles beside the gate and never inside it. It starts REPORT-ONLY
	// (internal/orbitlane Enforce).
	StageOrbit = "orbit"
	// StageVisual is the visual lane (Rob, 2026-10-04: "Give 'em a cluster
	// lane"): a tree's Playwright screenshot suites, run in the ts lane's
	// pinned image against the baselines it commits. Like orbit it is NOT in
	// PullPathStages — the door asks for it by name (`gate-file
	// --stage=visual`) where the tree declares a suite, and waits on it or
	// not by its own list (ourea cirun.AdvisoryLanes): advisory first.
	StageVisual = "visual"
)

// AtomDef is one catalogued check, as code.
//
// The table below is the ONE definition. Every `// +check` function is a
// three-line call into it, and the verdict vector iterates the same rows — so
// `dagger check -l` and the vector cannot drift apart, which is the two-surface
// defect the census counted 31 times.
type AtomDef struct {
	// ID is the namespaced atom id, e.g. "go:staticcheck".
	ID string
	// Stage is precommit or prepush.
	Stage string
	// Lane is the language whose manifest must be present for this atom to
	// have a surface. LaneAny runs everywhere.
	Lane Lane
	// Image is the lane container the atom runs in.
	Image string
	// Desc is the one-line description the catalogue and `-l` both carry.
	Desc string
	// SubsumedBy names the atom that covers this one when both run in the
	// same vector. THE SPLIT IS BY CADENCE, NOT BY DUPLICATION: go:test is
	// the commit's unit suite and go:test-race is the push's race + live-DB
	// suite over the same packages, so a caller that asks for both stages at
	// once (the door's gate lane, until F16 runs the stages separately) must
	// not compile and run the suite twice. The subsumed atom stands down and
	// says which atom covered it — never silently.
	SubsumedBy string
	// NeedsDies asks the caller to mount foundry-dies at /dies and name it in
	// the environment, for the atoms whose subject is the fleet's record tree
	// rather than the repo under test. A gate lane checks out ONE repository,
	// so an atom that grades the fleet has no other way to see it.
	NeedsDies bool
	// NeedsManifest says this atom needs its lane's ROOT MANIFEST, not just a
	// file of the lane's language somewhere in the tree.
	//
	// The distinction only exists because a lane can now be declared by its
	// files: a .py anywhere declares python (PythonFiles), the way a go.mod
	// anywhere declares go. That is right for lint and test, which read the
	// tree — and wrong for everything whose subject is the PROJECT, which is
	// what the manifest declares. Rob, 2026-09-25: "Just Lint/Test/etc, not a
	// build lane, which should look for the .toml".
	//
	// An atom that says so runs only where ManifestFor(its lane) is a root
	// entry, and answers a differently-worded ABSENT otherwise — one that
	// names the manifest rather than the files, because they are two different
	// facts about the repository and a reader deserves to know which one it
	// is. Nothing marks it in the go lane today; go has no atom whose subject
	// is a root go.mod rather than the modules themselves.
	NeedsManifest bool
	// NotOnForks, when set, says why this atom stands down on a fork of an
	// upstream: a repository whose foundry-dies record is kind vendored
	// (Tree.Fork). It is one of the ForkReasons, and it is the sentence the
	// ABSENT verdict carries (ForkVerdict).
	//
	// THE FORK LANE IS THE UPSTREAM'S OWN BAR PLUS THE FLEET'S SAFETY NET.
	// Rob, 2026-10-12: "gravity needs a separate/dedicated build lane so we're
	// not reformatting and refactoring upstream's forked code." A fork keeps
	// what proves the code works and is safe — build, the test suites, the
	// audits, the release build, cargo fmt (which upstreams enforce as the
	// same command) — and drops what holds upstream-owned code to the fleet's
	// own house rules: its lint set, its style, its canon, and its
	// change-scoped bars (mutation, diff coverage). Every upstream sync would
	// otherwise drag the fork toward reformatting code that is not ours, and
	// the diff against upstream is what a rebase has to read.
	NotOnForks string
}

// The reasons an atom gives for standing down on a fork (AtomDef.NotOnForks).
const (
	// ForkFleetStyle is a lint, a format or a convention the fleet holds its
	// own code to, and an upstream does not.
	ForkFleetStyle = "it holds code to the fleet's own lint set, style and conventions, which upstream-owned code does not follow"
	// ForkChangeBar is a change-scoped grading (mutation, diff coverage) whose
	// bar is the fleet's, applied to a diff that is mostly upstream's code.
	ForkChangeBar = "it grades the change against the fleet's own test-strength bar, and a fork's changes are mostly its upstream's code"
)

// ForkReasons is every value NotOnForks may take.
var ForkReasons = []string{ForkFleetStyle, ForkChangeBar}

// atoms is the catalogue: the rows still in atomTable() and, once a package
// above the atoms has called UseDefs, the atoms that moved into files.
var atoms = atomTable()

// Atoms answers the catalogue. It is a function because the ported atoms join
// it through UseDefs; a caller that ranged over a variable would miss them.
func Atoms() []AtomDef { return atoms }

// UseDefs adds the catalogue rows of the atoms that live in their own files
// (atoms/<ns>/<name>/<name>.go and atoms/<ns>/<name>.toml). internal/checks
// cannot import those packages, since they import it, so the package that
// holds the generated registry hands the rows down here at init, ahead of
// every other init in a binary that links it. An id the table still holds, or
// one given twice, is an authoring error and panics at load.
func UseDefs(rows []AtomDef) {
	merged := append(atomTable(), rows...)
	seen := map[string]bool{}
	for _, a := range merged {
		if seen[a.ID] {
			panic("checks: atom " + a.ID + " is catalogued twice")
		}
		seen[a.ID] = true
	}
	atoms = ordered(merged)
}

// atomTable builds the table inside a function, and that is deliberate:
// Go instruments coverage counters in function bodies only, so a
// package-level composite literal has none, and the joins that built each
// row read NOT COVERED to the mutation lane on the first pull that touched
// one (foundry-tools #25, mutation-foundry-tools-84e09d2). The rows carry no
// script now, but the coverage reason is a property of the composite literal
// rather than of what is in it. The tests read every row through this; a
// mutant in a row is a mutant they see.
func atomTable() []AtomDef {
	return []AtomDef{
		// ---- go ----
		// THE PUSH STAGE'S ROWS ARE IN THE ORDER IT RUNS THEM, cheapest first:
		// the sequence fails fast (Rob, 2026-09-17: "Each stage fails fast, and
		// returns the cumulative logs up until the failing step"), so a lint
		// that answers in seconds must not wait behind a suite that answers in
		// minutes. (The order is catalogueOrder's now: go:test, go:test-race and the
		// rest of the go lane live in atoms/go.)
		{
			ID: "go:release", Stage: StagePrepush, Lane: LaneGo, Image: ImageGo,
			Desc: "The release binaries the image will carry compile, with the image's own flags — for a Dockerfile that copies from release/; one that compiles itself is built by the build lane, and this is absent.",
		},

		// ---- python ----
		{
			ID: "python:release", Stage: StagePrepush, Lane: LanePython, Image: ImagePython,
			Desc: "The venv the image will carry builds from the star's lock on the image's own python base — for a Dockerfile on foundry/base-images/python that copies from release/; any other image is absent.",
			// The build lane, and the one Rob named: a release builds from the
			// star's lock, which is the manifest's own artifact.
			NeedsManifest: true,
		},

		// ---- rust ----
		{
			ID: "rust:release", Stage: StagePrepush, Lane: LaneRust, Image: ImageRust,
			Desc: "The release binaries the image will carry compile under cargo's release profile — for a Dockerfile that copies from release/; one that compiles itself is built by the build lane, and this is absent.",
		},
		{
			ID: "rust:wit-guest", Stage: StagePrepush, Lane: LaneRust, Image: ImageRust,
			Desc:          "Each wit-guest-<world> feature (discovered from Cargo.toml) builds for wasm32-unknown-unknown, lifts to a component that validates, and passes its native cargo test; absent where the manifest declares none.",
			NeedsManifest: true,
		},
		{
			ID: "rust:wit-compose", Stage: StagePrepush, Lane: LaneRust, Image: ImageRust,
			Desc:          "Every wit-guest world composes into one valid component through tools/compose/compose.sh, and every tests/tapes/<world>.json replays through it with the native core's answers, and every tests/red/*.json replays as red (the answer is its `today`, never its prior-art response); absent where the tree carries no composer or replay host.",
			NeedsManifest: true,
		},

		// ---- ts ----
		{
			ID: "ts:regen", Stage: StagePrepush, Lane: LaneTS, Image: ImageTS,
			Desc:          "Each core the justfile's regen-check names regenerates byte-identical from its pinned WIT, core commit and tool versions (the star's own build and transpile scripts, run on the engine); absent where the justfile calls no scripts/regen-check.sh.",
			NeedsManifest: true,
		},
		{
			ID: "ts:release", Stage: StagePrepush, Lane: LaneTS, Image: ImageTS,
			Desc: "The record's release steps leave the image's files under release/, run on the image's own bun base — for a Dockerfile on foundry/base-images/bun that copies from release/; any other image is absent.",
		},

		// ---- compose: the host stacks. Ported off the act-runner's validate.yml ----

		// ---- visual: the visual lane, asked for by name (StageVisual) ----
		{
			ID: "ts:visual", Stage: StageVisual, Lane: LaneTS, Image: ImageTS, NeedsManifest: true,
			Desc: "Every Playwright screenshot suite visual.toml declares matches its committed baselines, rendered in the lane's pinned image.",
		},
	}
}

// AtomByID answers the definition for an id, and panics if there is none —
// every `// +check` function names a row in the table above, and a name that
// does not resolve is an authoring error caught by this repo's own tests, not a
// runtime condition.
func AtomByID(id string) AtomDef {
	for _, a := range atoms {
		if a.ID == id {
			return a
		}
	}
	panic("no atom defined for id " + id)
}

// PullPathStages are the stages a pull's gate may run. Sweep is deliberately
// not one of them, and neither is mutation: it blocks the same pull, but it
// runs as its own lane, asked for by name (see StageMutation).
var PullPathStages = []string{StagePrecommit, StagePrepush}

// MutationAtoms answers the mutation stage — the set the door's `mutation`
// lane runs beside the gate.
func MutationAtoms() []AtomDef {
	return AtomsForStage(StageMutation)
}

// IsPullPath reports whether a stage belongs on a pull's path.
//
// The empty stage means "every stage a pull runs", which is every stage EXCEPT
// sweep — not "everything in the table". That distinction is the whole of CA
// F9's acceptance ("no `stage: sweep` atom appears in any pull's path"), and
// making it the default here is what turns the acceptance from a convention
// somebody has to remember into a fact about the code: a gate that asks for the
// vector and names no stage cannot be handed a sweep atom.
func IsPullPath(stage string) bool {
	for _, s := range PullPathStages {
		if s == stage {
			return true
		}
	}
	return false
}

// PullPathAtoms answers every atom a pull may run — the set the door reads.
func PullPathAtoms() []AtomDef {
	return AtomsForStage("")
}

// AtomsForStage selects the atoms for one stage; the empty stage selects every
// PULL-PATH atom, never the sweep. See IsPullPath for why that is the default.
func AtomsForStage(stage string) []AtomDef {
	out := make([]AtomDef, 0, len(atoms))
	for _, a := range atoms {
		if stage == "" {
			if !IsPullPath(a.Stage) {
				continue
			}
		} else if a.Stage != stage {
			continue
		}
		out = append(out, a)
	}
	return out
}

// Subsumed splits a selection into the atoms that run and the atoms another
// SELECTED atom covers (AtomDef.SubsumedBy). A subsumer that is not in the
// selection subsumes nothing: the commit stage alone still runs go:test.
func Subsumed(selected []AtomDef) (run, covered []AtomDef) {
	present := map[string]bool{}
	for _, a := range selected {
		present[a.ID] = true
	}
	for _, a := range selected {
		if a.SubsumedBy != "" && present[a.SubsumedBy] {
			covered = append(covered, a)
			continue
		}
		run = append(run, a)
	}
	return run, covered
}

// CoveredVerdict is a subsumed atom's answer: absent, because the atom that
// covers it ran here instead. It reads as an omission with a reason, which is
// what it is.
func CoveredVerdict(a AtomDef) Verdict {
	return Verdict{
		Atom:   a.ID,
		Stage:  a.Stage,
		Lane:   string(a.Lane),
		State:  int(StatePass),
		Result: "absent",
		Reason: a.ID + ": ABSENT - " + a.SubsumedBy + " runs in this same vector over the same packages and covers it.",
	}
}

// Select narrows a stage's atoms to a comma-separated list of ids.
//
// An id that names nothing, or names an atom the stage does not admit, is an
// ERROR rather than an omission. A selector is written by hand into a CronJob's
// env, where a typo is invisible: `sweep:kube_linter` for `sweep:kube-linter`
// would otherwise produce an empty vector, the sweep would report nothing, and
// nothing reported reads exactly like nothing wrong. That is the same shape as
// a ruleset matching zero files, and it gets the same answer — refuse.
func Select(from []AtomDef, ids string) ([]AtomDef, error) {
	have := map[string]AtomDef{}
	for _, a := range from {
		have[a.ID] = a
	}
	out := make([]AtomDef, 0, len(from))
	for _, id := range strings.Split(ids, ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		a, ok := have[id]
		if !ok {
			if AtomExists(id) {
				return nil, fmt.Errorf("atom %q exists but this stage does not admit it — a selector that silently drops an atom reports nothing, and nothing reported reads exactly like nothing wrong", id)
			}
			return nil, fmt.Errorf("no atom %q — check the id against `dagger check -l`", id)
		}
		out = append(out, a)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the selector %q chose no atom; refusing to answer an empty vector", ids)
	}
	return out, nil
}

// AtomExists reports whether the table carries this id, without panicking.
func AtomExists(id string) bool {
	for _, a := range atoms {
		if a.ID == id {
			return true
		}
	}
	return false
}
