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
}

var Atoms = atomTable()

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
		// ---- fleet: every repository, whatever it is written in ----
		{
			ID: "fleet:check-yaml", Stage: StagePrecommit, Lane: LaneAny, Image: ImageFleet,
			Desc: "Every YAML file in the tree parses.",
		},
		{
			ID: "fleet:check-added-large-files", Stage: StagePrecommit, Lane: LaneAny, Image: ImageFleet,
			Desc: "No file in the tree exceeds 2 MB.",
		},
		{
			ID: "fleet:check-merge-conflict", Stage: StagePrecommit, Lane: LaneAny, Image: ImageFleet,
			Desc: "No conflict markers were committed.",
		},
		{
			ID: "fleet:stop-justifications", Stage: StagePrecommit, Lane: LaneAny, Image: ImageFleet,
			Desc: "No silent suppression of any gate — a suppression carries a tool-conflict line.",
		},
		{
			ID: "fleet:sast-ruleset-lanes", Stage: StagePrecommit, Lane: LaneAny, Image: ImageFleet,
			Desc: "The SAST ruleset declares every lane this repository actually builds.",
		},
		{
			ID: "fleet:copier-answers-intact", Stage: StagePrecommit, Lane: LaneAny, Image: ImageFleet,
			Desc: "No crashed copier render is recorded in .copier-answers.yml.",
		},
		{
			ID: "fleet:orbit-drift", Stage: StagePrepush, Lane: LaneAny, Image: ImageFleet,
			Desc: "This repo's declared seams agree with the canonical contracts in foundry-dies/orbits.",
		},
		{
			ID: "fleet:opengrep-sast", Stage: StagePrecommit, Lane: LaneAny, Image: ImageFleet,
			Desc: "SAST scan that refuses a zero-file scan.",
		},
		{
			ID: "fleet:hadolint", Stage: StagePrecommit, Lane: LaneAny, Image: ImageFleet,
			Desc: "Every Dockerfile in the tree passes hadolint under the fleet's ruleset.",
		},

		// ---- ops: the trees the cluster and the hosts converge to ----
		// Ported off Tekton's ci-ops-pipeline (retired 2026-09-09); the body is
		// foundry-stocks ci/lib/ops/ops.sh, now one phase per atom in Go.
		// Every one is ABSENT on a repo with no ops shape (checks.IsOpsTree).
		{
			ID: "ops:shell", Stage: StagePrecommit, Lane: LaneAny, Image: ImageFleet,
			Desc: "Every tracked shell script passes shellcheck at severity error; the warning count is reported, not gated.",
		},
		{
			ID: "ops:chezmoi", Stage: StagePrecommit, Lane: LaneAny, Image: ImageFleet,
			Desc: "Every tracked chezmoi *.tmpl renders with `chezmoi execute-template` — the one check a dotfiles source tree has.",
		},
		{
			ID: "ops:yaml", Stage: StagePrecommit, Lane: LaneAny, Image: ImageFleet,
			Desc: "No tracked YAML carries a duplicate key — the defect a YAML loader resolves last-wins and never reports; a repo with its own tools/yaml-strict (infra) runs that.",
		},
		{
			ID: "ops:dup", Stage: StagePrecommit, Lane: LaneAny, Image: ImageFleet,
			Desc: "The repo's own tools/dup-check (infra) finds no blocking duplicate; ABSENT where the tree has no such tool.",
		},
		{
			ID: "ops:declaration", Stage: StagePrecommit, Lane: LaneAny, Image: ImageFleet,
			Desc: "The repo's own tools/declaration-integrity (infra) holds: every declared host file has its payload and every payload is declared.",
		},
		{
			ID: "ops:specs", Stage: StagePrecommit, Lane: LaneAny, Image: ImageFleet,
			Desc: "The repo's own tools/console-specs (infra): the console ConfigMap's read-model specs follow the stacks' emits, or it names which drifted.",
		},
		{
			ID: "ops:ansible", Stage: StagePrecommit, Lane: LaneAny, Image: ImageFleet,
			Desc: "Every playbook under ansible/playbooks passes ansible-playbook --syntax-check and ansible-lint at profile min; the basic-profile count is reported, not gated.",
		},
		{
			ID: "ops:flux", Stage: StagePrecommit, Lane: LaneAny, Image: ImageFleet,
			Desc: "Every Flux Kustomization under flux/ builds with kubectl kustomize — a duplicate resource id, a missing base or a bad patch is a finding before flux meets it.",
		},
		// CAME HOME FROM THE SWEEP 2026-09-23 (CA F18). It was sweep:kube-linter,
		// on a clock, because F9 read it as a question about a REPOSITORY. The
		// surface it needs is flux/, which one repo in eighty has, so the clock
		// walked 80 repos to ask infra one question. `ops` already claims that
		// exact tree, so the question now runs in infra's own pull path, where
		// whoever broke a workload is the one who is told.
		{
			ID: "ops:kube-linter", Stage: StagePrecommit, Lane: LaneAny, Image: ImageKubeLinter,
			Desc: "Every workload under flux/ passes kube-linter's default checks.",
		},

		// ---- template: the copier templates ----
		//
		// CAME HOME FROM THE SWEEP 2026-09-23 (CA F18), and this one was the
		// inversion worth naming. F9's acceptance criterion was that no sweep
		// atom appears in a pull's path — which is also why a template could
		// break its own ci-matrix.toml and nothing noticed until Sunday. It is
		// prepush rather than precommit because rendering the whole matrix with
		// copier is the slow half of a template's gate.
		{
			ID: "template:render-matrix", Stage: StagePrepush, Lane: LaneAny, Image: ImageFleet,
			Desc: "Every case in this template's ci-matrix.toml still renders.",
		},

		// ---- go ----
		{
			ID: "go:gofmt", Stage: StagePrecommit, Lane: LaneGo, Image: ImageGo,
			Desc: "Every Go file is gofmt-clean.",
		},
		{
			ID: "go:vet", Stage: StagePrecommit, Lane: LaneGo, Image: ImageGo,
			Desc: "go vet ./... reports nothing.",
		},
		{
			ID: "go:test", Stage: StagePrecommit, Lane: LaneGo, Image: ImageGo,
			Desc:       "go test ./... passes — the unit suite, no race detector and no database.",
			NeedsDies:  true,
			SubsumedBy: "go:test-race",
		},
		// THE PUSH STAGE'S ROWS ARE IN THE ORDER IT RUNS THEM, cheapest first:
		// the sequence fails fast (Rob, 2026-09-17: "Each stage fails fast, and
		// returns the cumulative logs up until the failing step"), so a lint
		// that answers in seconds must not wait behind a suite that answers in
		// minutes.
		{
			ID: "go:staticcheck", Stage: StagePrepush, Lane: LaneGo, Image: ImageGo,
			Desc: "staticcheck ./... reports nothing.",
		},
		{
			ID: "go:govulncheck", Stage: StagePrepush, Lane: LaneGo, Image: ImageGo,
			Desc: "govulncheck ./... reports no known vulnerability.",
		},
		{
			ID: "go:build", Stage: StagePrepush, Lane: LaneGo, Image: ImageGo,
			Desc: "go build ./... succeeds.",
		},
		{
			ID: "go:release", Stage: StagePrepush, Lane: LaneGo, Image: ImageGo,
			Desc: "The release binaries the image will carry compile, with the image's own flags — for a Dockerfile that copies from release/; one that compiles itself is built by the build lane, and this is absent.",
		},
		{
			ID: "go:test-race", Stage: StagePrepush, Lane: LaneGo, Image: ImageGo,
			Desc:      "go test -race ./... passes against the live databases the record declares.",
			NeedsDies: true,
		},

		// ---- python ----
		{
			ID: "python:ruff-check", Stage: StagePrecommit, Lane: LanePython, Image: ImagePython,
			Desc: "ruff lint is clean over every .py in the tree, under the fleet's ruleset.",
		},
		{
			ID: "python:ruff-format", Stage: StagePrecommit, Lane: LanePython, Image: ImagePython,
			Desc: "ruff format --check is clean over the product python, under the fleet's ruleset.",
		},
		{
			ID: "python:forge-testkit-assertion-free", Stage: StagePrecommit, Lane: LanePython, Image: ImagePython,
			Desc: "No assertion-free test bodies (forge-testkit).",
		},
		{
			ID: "python:forge-testkit-fake-placement", Stage: StagePrecommit, Lane: LanePython, Image: ImagePython,
			Desc: "Fake and Stub doubles live where they belong (forge-testkit).",
		},
		{
			ID: "python:forge-testkit-schema-budget", Stage: StagePrecommit, Lane: LanePython, Image: ImagePython,
			Desc: "MCP verb descriptions stay inside the schema budget (forge-testkit).",
		},
		{
			ID: "python:mypy", Stage: StagePrecommit, Lane: LanePython, Image: ImagePython,
			Desc: "mypy is clean over src and tests, under the fleet's strict configuration.",
			// A TYPE CHECK IS AGAINST A DEPENDENCY SET, and only the manifest
			// declares one. This atom's own note says why --all-extras is
			// there: "a type check that cannot import the optional
			// dependencies the code declares is a type check of a different
			// program." A tree with no manifest is that argument's limit case
			// — nothing is installed, so EVERY third-party import is
			// unresolvable and mypy reports on a program that does not exist.
			//
			// MEASURED on foundry-dies, 2026-09-25, the day the lane started
			// reaching file-declared trees (Scotty19 hit it within the hour):
			//
			//   uv run --no-project --with mypy mypy tests
			//     -> Cannot find implementation or library stub for module
			//        named "pytest"  [import-not-found]        (+ the strict
			//        untyped-decorator cascade off every @pytest.mark)
			//   ...--with mypy --with pytest mypy tests
			//     -> Success: no issues found in 1 source file
			//
			// The repository was clean. `--with pytest` would have fixed that
			// one import and nothing about the unbounded class it belongs to,
			// so the atom waits for the manifest instead. Lint reads files and
			// needs no imports; pytest RUNS the code and brings what it needs;
			// type-checking needs the declared set and has no way to guess it.
			NeedsManifest: true,
		},
		{
			ID: "python:pytest", Stage: StagePrecommit, Lane: LanePython, Image: ImagePython,
			Desc: "pytest passes, and there is something for it to pass.",
		},
		{
			ID: "python:pip-audit", Stage: StagePrepush, Lane: LanePython, Image: ImagePython,
			Desc: "pip-audit reports no known vulnerability.",
			// It audits DECLARED dependencies. A tree of loose .py declares
			// none, so without the manifest it would examine nothing and
			// report a pass — the exact failure StateCannotRun exists to make
			// unrepresentable.
			NeedsManifest: true,
		},
		{
			ID: "python:release", Stage: StagePrepush, Lane: LanePython, Image: ImagePython,
			Desc: "The venv the image will carry builds from the star's lock on the image's own python base — for a Dockerfile on foundry/base-images/python that copies from release/; any other image is absent.",
			// The build lane, and the one Rob named: a release builds from the
			// star's lock, which is the manifest's own artifact.
			NeedsManifest: true,
		},

		// ---- rust ----
		{
			ID: "rust:cargo-fmt", Stage: StagePrecommit, Lane: LaneRust, Image: ImageRust,
			Desc: "cargo fmt --all --check is clean.",
		},
		{
			ID: "rust:cargo-clippy", Stage: StagePrecommit, Lane: LaneRust, Image: ImageRust,
			Desc: "cargo clippy is clean under the fleet's lint set, warnings denied.",
		},
		{
			ID: "rust:cargo-test", Stage: StagePrecommit, Lane: LaneRust, Image: ImageRust,
			Desc: "cargo test --workspace passes, and there is something for it to pass.",
		},
		{
			ID: "rust:release", Stage: StagePrepush, Lane: LaneRust, Image: ImageRust,
			Desc: "The release binaries the image will carry compile under cargo's release profile — for a Dockerfile that copies from release/; one that compiles itself is built by the build lane, and this is absent.",
		},
		{
			ID: "rust:cargo-audit", Stage: StagePrepush, Lane: LaneRust, Image: ImageRust,
			Desc: "cargo audit reports no known vulnerability.",
		},

		// ---- ts ----
		{
			ID: "ts:bun-gate", Stage: StagePrecommit, Lane: LaneTS, Image: ImageTS,
			Desc: "bun run gate passes against a frozen lockfile under the fleet's eslint config, and the tree carries tests for it to run.",
		},
		{
			ID: "ts:bun-audit", Stage: StagePrepush, Lane: LaneTS, Image: ImageTS,
			Desc: "bun audit reports nothing at high or above.",
		},
		{
			ID: "ts:release", Stage: StagePrepush, Lane: LaneTS, Image: ImageTS,
			Desc: "The record's release steps leave the image's files under release/, run on the image's own bun base — for a Dockerfile on foundry/base-images/bun that copies from release/; any other image is absent.",
		},

		// ---- compose: the host stacks. Ported off the act-runner's validate.yml ----
		//
		// nas01-stacks and llm01-stacks ARE the boxes: every compose spec, the
		// Caddyfile, the runner config. Their `validate.yml` was the only thing that
		// had ever validated any of it, and the act-runner it ran on is being
		// removed — so these three atoms are what "validated by the gate or not at
		// all" means for those repos.
		//
		// WHAT A GREEN HERE MEANS, kept from the workflow's own header: "this parses
		// and its schema is valid", nothing stronger. It does not say the file
		// matches what is RUNNING on the box; that is drift, not syntax, and no gate
		// can see it from here.
		{
			ID: "compose:config", Stage: StagePrecommit, Lane: LaneAny, Image: ImageFleet,
			Desc: "Every tracked compose spec parses and its schema validates.",
		},
		{
			ID: "compose:no-tracked-secrets", Stage: StagePrecommit, Lane: LaneAny, Image: ImageFleet,
			Desc: "No credential-shaped file is tracked in a repository that ships compose specs.",
		},
		{
			ID: "compose:third-party-pins", Stage: StagePrecommit, Lane: LaneAny, Image: ImageFleet,
			Desc: "Zero ${PIN_} image interpolations — the BP6b ratchet stays closed.",
		},

		// ---- dies: the policy die's source. Ported off ci / contracts / schema ----
		//
		// Six atoms, and THE SPLIT BETWEEN THE FIRST TWO AND THE NEXT TWO IS THE
		// ARGUMENT. `opa test` and the dogfood eval grade the SOURCE; data-keys and
		// canary-visibility grade the ARTIFACT, and the gap between them is
		// measured rather than theoretical: a source tree can pass 312 assertions
		// and build a bundle that admits everything. diesBundle in atoms_dies.go
		// carries that measurement.
		//
		// build.yml and fleet-bundle.yml are deliberately NOT here. They publish
		// rather than validate, and the bundle recipe lane owns them.
		{
			ID: "dies:opa-test", Stage: StagePrecommit, Lane: LaneAny, Image: ImageFleet,
			Desc: "The rego unit and invariant suite passes.",
		},
		{
			ID: "dies:admission-dogfood", Stage: StagePrepush, Lane: LaneAny, Image: ImageFleet,
			Desc: "The admission domain admits this repo's own star shape.",
		},
		{
			ID: "dies:data-keys", Stage: StagePrepush, Lane: LaneAny, Image: ImageFleet,
			Desc: "The BUILT bundle carries every data root the policy reads, non-empty.",
		},
		{
			ID: "dies:canary-visibility", Stage: StagePrepush, Lane: LaneAny, Image: ImageFleet,
			Desc: "The BUILT bundle still hides a curated verb from a session principal.",
		},
		{
			ID: "dies:contracts", Stage: StagePrecommit, Lane: LaneAny, Image: ImageFleet,
			Desc: "Every copy of every shared closed set agrees — and the checker is proved to detect first.",
		},
		{
			ID: "fleet:witness", Stage: StagePrepush, Lane: LaneAny, Image: ImageFleet,
			Desc: "Every changed .py/.go file is shown to the code witness (narcissus): a canonical-class or Standard match is a finding, a Convention is advisory, novel is clean.",
		},
		{
			ID: "dies:schema", Stage: StagePrecommit, Lane: LaneAny, Image: ImageFleet,
			Desc: "The slag schema is a valid Draft 2020-12 document and every v2 record satisfies it.",
		},
		{
			ID: "dies:findings", Stage: StagePrecommit, Lane: LaneAny, Image: ImageFleet,
			Desc: "The findings schema is a valid Draft 2020-12 document and still refuses what it exists to refuse — every fixture classifies as its name claims.",
		},
		{
			ID: "dies:schemas", Stage: StagePrecommit, Lane: LaneAny, Image: ImageFleet,
			Desc: "EVERY schema in schema/ is a valid Draft 2020-12 document, and every one with fixtures still refuses what it exists to refuse — discovered, never listed.",
		},
		{
			ID: "dies:canonical", Stage: StagePrecommit, Lane: LaneAny, Image: ImageFleet,
			Desc: "Every committed fleet/stars/<star>/slag.json is byte-for-byte its own canonical form (sorted keys, two-space indent, ensure_ascii, trailing LF).",
		},

		// ---- mutation: a pull's change set, mutated ----
		//
		// ONE VERDICT, FOUR LANGUAGES. Each atom mutates in DIFF mode against
		// GATE_BASE — the pull's merge base as the door names it — and answers 0
		// clean, 1 survivors, 2 could not measure. Each runs its tools as plain
		// execs and settles in Go (GoMutationVerdict, RustMutationVerdict,
		// TSMutationVerdict, PythonReportVerdict); a tool the engine never ran
		// is could-not-run, never a pass.
		//
		// THE HISTORY IS THERE IN THE LANE THAT MATTERS. The mutation Job clones
		// the repository whole and checks the head out, so the base resolves and
		// the diff is real. A local pre-push run hands the engine a linked
		// worktree, which r.gitReady turns into a throwaway repository with no
		// history: the atom then stands down 0 with "no usable PR base sha", and
		// the door's Job is the one that measures.
		//
		// critical_modules IS THE REPO'S DECLARATION for python, rust and ts,
		// read off .copier-answers.yml where the template question puts it — the
		// same string the retired mutation.yml rendered into its `modules` input.
		// AN EMPTY LIST IS NOT AN OPT-OUT: a repo that declares nothing gets the
		// whole diff as its scope, and the atom says so rather than going ABSENT
		// (Rob, 2026-09-11 — nothing with tests goes un-mutation-tested). Go
		// needs none: gremlins scopes to the diff itself, exactly as mutation-go
		// did.
		//
		// A REPO HAS NO SAY. The first cut of these atoms sourced a repo-root
		// ci/mutation.env — MUT_* knobs standing in for the retired workflow's
		// inputs — and Rob asked why a repo should have a say in anything
		// (2026-09-11). It should not: the scripts honour MUT_GATE=false, so that
		// file was a one-line switch to turn a fleet gate off, the exact shape
		// stop-justifications exists to refuse (Rule #2: one canonical gate,
		// always the latest, a red is the committer's to fix). It is gone. The
		// one repo fact the lane reads is critical_modules — a declaration of
		// WHAT matters, not a dial on HOW hard to look. Everything else is the
		// fleet's default.
		//
		// NeedsDies ON go:mutation FOR THE SAME REASON go:test-race CARRIES IT:
		// gremlins gathers coverage by running the repo's `go test`, and an atom
		// that runs a repo's `go test` runs its armed goldens
		// (TestTheGoTestAtomCarriesTheFleetRecordTree holds both).
		{
			ID: "go:mutation", Stage: StageMutation, Lane: LaneGo, Image: ImageGo, NeedsDies: true,
			Desc: "Every mutant gremlins makes of this pull's changed Go is killed by the tests.",
		},
		{
			ID: "python:mutation", Stage: StageMutation, Lane: LanePython, Image: ImagePython,
			Desc: "Every mutant cosmic-ray makes of this pull's changes to the declared critical modules is killed by the tests.",
			// Its scope is critical_modules off .copier-answers.yml and it
			// runs cosmic-ray against the project — neither of which a tree
			// with no manifest has. Rob, 2026-09-25: "Leave mutation off for
			// now." Off is this line; the lane's lint and test still run.
			NeedsManifest: true,
		},
		{
			ID: "rust:mutation", Stage: StageMutation, Lane: LaneRust, Image: ImageRust,
			Desc: "Every viable mutant cargo-mutants makes of this pull's changes to the declared critical modules is killed by the tests.",
		},
		{
			ID: "ts:mutation", Stage: StageMutation, Lane: LaneTS, Image: ImageTS,
			Desc: "Every mutant StrykerJS makes of this pull's changes to the declared critical modules is killed by the tests.",
		},
	}
}

// AtomByID answers the definition for an id, and panics if there is none —
// every `// +check` function names a row in the table above, and a name that
// does not resolve is an authoring error caught by this repo's own tests, not a
// runtime condition.
func AtomByID(id string) AtomDef {
	for _, a := range Atoms {
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
	out := make([]AtomDef, 0, len(Atoms))
	for _, a := range Atoms {
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
	for _, a := range Atoms {
		if a.ID == id {
			return true
		}
	}
	return false
}
