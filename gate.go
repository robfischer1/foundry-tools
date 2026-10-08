package main

import (
	"context"
	"fmt"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE GATE LANE, AS ONE FUNCTION. The door's gate Job runs `dagger call …
// gate` as its only process, in place of infra's ca-gate tools.sh (a dagger
// and kubectl download) and gate.py (an engine pod lookup, the tree proof, a
// watched `dagger call verdicts`). The mutation lane is the same function at
// --stage=mutation.
//
// THE RECORD IS THE VERDICT, for both lanes, and the exit is no longer part of
// it. This function RETURNS its run record — one line, sentinel-marked — and a
// dagger function that returns a value exits 0 whatever it found (value XOR
// error). So from here the exit code says only that the CLI ran. The door
// settles from the record, and refuses to settle a record-owing lane that gave
// it none: without that guard on the door's side, a record lost in transit
// would read as a clean tree. That guard landed first, deliberately.
//
// The three states are unchanged and live inside the record: 0 pass,
// 1 findings, 2 cannot-run. Until CA F16 (2026-09-18) the gate lane also ATTESTED its
// atom vector to tartarus through the pod's SPIRE socket (attest.py's job),
// and the door joined on that receipt to settle the lane; sessions stopped
// attesting at F15, the door runs its own gate Job on every head and every
// landing candidate and settles it from this exit (ourea #231), so the
// receipt, the socket and the hades round-trip are gone from here.
//
// WHAT DID NOT COME INTO THIS FUNCTION: gate.py's own watchdogs (the silence
// timeout, the engine-gone phrases, its overall ceiling). They are the door's,
// which follows the Job from outside, where a hung CLI is still visible: its
// watcher cancels a pod that has logged nothing for twenty minutes and re-asks
// it (ourea 30101ae), an engine that rolled under a run is the door's to see,
// and the Job's deadline stays the ceiling it settles as could-not-run.

// gateVector answers the vector for a stage: Verdicts, in process. A variable
// so the lane's own decisions are tested without running every atom.
var gateVector = func(ctx context.Context, m *FoundryTools, stage, base string) (string, error) {
	return m.Verdicts(ctx, stage, "", base)
}

// Gate proves the fetched commit is the tree the door named, grades it, and
// settles on the vector's worst state.
//
// IT RETURNS THE RECORD AS A STRING, which the CLI prints, which is why
// switching this lane to the volume was NOT the pure config change I claimed on
// foundry-tools#185: a string is the end of the chain, so there is nothing to
// select `record-file` off. GateFile is the same grading with the other
// transport, and it is a sibling rather than a changed signature because the
// door resolves the gate pin from main's tip — a Gate that returned an object
// would print one to a fleet whose `gate_job_call` still ended at `gate`, and
// every gate run in the fleet would settle could-not-run for the want of a
// record the instant it landed.
func (m *FoundryTools) Gate(
	ctx context.Context,
	// The tree the door named (CA_GATE_TREE). The fetched commit's tree must
	// be this one, or nothing is graded.
	tree string,
	// The foundry-tools pin the door resolved (CA_GATE_MODULE): the catalogue
	// this run grades under, named in the settle.
	pin string,
	// The change set's base (CA_GATE_BASE), for the atoms that judge a change.
	// +optional
	base string,
	// Only atoms at this stage: empty is the pull path, "mutation" the
	// mutation lane.
	// +optional
	stage string,
) (string, error) {
	record, err := m.gateStage(ctx, tree, stage, base).Record()
	m.shadowBeside(ctx, stage, base)
	return record, err
}

// GateFile is the same grading, handed over as a FILE instead of printed.
//
// The caller exports it onto the volume the door mounted:
//
//	dagger call … gate-file --tree=… --pin=… --base=… export --path /out/record.json
//
// WHY A SECOND ENTRY POINT AT ALL. containerd splits any container log line
// over max_container_log_line_size (16384, read off dev01) into partial CRI
// entries, and the pod-log endpoint has no stream selector, so stdout and
// stderr come back merged in kubelet order and a foreign entry can land between
// one line's partials. Measured in erebus: 56 of 2197 records carried on stdout
// were refused that way — 2.5% of graded runs in the fleet settling
// could-not-run on a run whose atoms had all passed. Records average 26KB
// against that 16KB ceiling and a 54-atom gate's non-log structure alone is
// 18.5KB, so the transport had to move rather than the budget.
//
// IT GRADES THROUGH THE SAME gateStage, so the two cannot answer differently
// about one tree: a lane that graded one way on stdout and another on a volume
// would be worse than the bug this replaces.
func (m *FoundryTools) GateFile(
	ctx context.Context,
	// The tree the door named (CA_GATE_TREE).
	tree string,
	// The foundry-tools pin the door resolved (CA_GATE_MODULE).
	pin string,
	// The change set's base (CA_GATE_BASE).
	// +optional
	base string,
	// Only atoms at this stage: empty is the pull path, "mutation" the
	// mutation lane.
	// +optional
	stage string,
	// The run's record token (CA_RECORD_TOKEN), which authorises posting this
	// run's record to the door it fetched the tree from. Absent and the record
	// travels on its volume alone, exactly as it did before.
	//
	// A SECRET, NOT A STRING, and that is load-bearing rather than tidy: dagger
	// echoes call arguments verbatim into its plain-progress narration
	// (dagger/dagger#14363), so a token passed as a string would be printed into
	// the pod log and shipped to Loki. A Secret is masked.
	// +optional
	recordToken *dagger.Secret,
	// Reuse the gradings an earlier run stored for a unit with the same
	// content, scope and engine, and grade only the rest (the mutation lane;
	// mutation_reuse.go). It needs the record token, which authorises the
	// lookup; without one, or with a store that does not answer, every unit is
	// graded cold.
	// +optional
	reuse bool,
	// The SPIRE agent's workload socket, forwarded by the lane pod
	// (--spire=/run/spire/agent/public/api.sock). fleet:witness asks narcissus
	// over its mTLS door as the SVID it issues — the lane's own run,
	// spiffe://notusmi.com/job/<lane>/<job>. Absent, the witness asks in the
	// clear exactly as before and says so.
	// +optional
	spire *dagger.Socket,
	// The registry credential the visual lane pushes its artifact with — the
	// diff images and the regenerated baselines (atoms_ts_visual.go), a docker
	// config JSON naming registry.notusmi.com. Only the visual stage reads it;
	// absent, that lane grades exactly the same and says it pushed nothing.
	// +optional
	artifactAuth *dagger.Secret,
) (*dagger.File, error) {
	if reuse && recordToken != nil {
		m.lookup = lookupVia(m.Repo, recordToken)
		m.audit = auditSampled(m.Sha)
	}
	m.spire = spire
	m.artifactAuth = artifactAuth
	result := m.gateStage(ctx, tree, stage, base)
	// POST FIRST, THEN HAND BACK THE FILE. Both transports carry the same bytes
	// — Record()'s whole output, sentinel included — because ourea reads
	// whichever arrives through one parser, and a record that differed by
	// transport is how two readers come to disagree about one run.
	//
	// THE POST CANNOT FAIL THE RUN. postRecord returns nothing and prints one
	// `record post:` line saying what the door answered: the file below is the
	// fallback, and a lane's verdict must not turn on whether an HTTP request
	// succeeded.
	//
	// THE MARSHAL ERROR IS DROPPED RATHER THAN BRANCHED ON, and the mutation
	// lane is why. Record() fails only if json.Marshal does, and json.Marshal
	// fails only on a type it cannot encode — a channel, a func, a cycle.
	// StageResult is strings, ints and slices of the same, so the error arm is
	// unreachable: a test cannot enter it and `err == nil` therefore survived
	// negation (gate.go:129 LIVED, 2026-09-28). A branch no test can take is a
	// branch that should not exist. An empty record is refused downstream by
	// sendRecord, so the impossible case is still handled — just not by a
	// conditional pretending to be reachable.
	record, _ := result.Record()
	postRecord(ctx, m.Repo, recordToken, record)
	// AFTER THE POST: the record is already with the door, so nothing the
	// shadow does can delay or change it (shadowBeside).
	m.shadowBeside(ctx, stage, base)
	return result.RecordFile()
}

// gateStage proves the tree, grades it and answers the stage result both entry
// points settle from.
//
// THE SAME SHAPE THE COMMIT STAGE BUILDS, on purpose: SettleStage and
// stageResult are what Check and Push already use to turn a vector into a
// StageResult, and reusing them is what makes "the verdict did not move" cheap
// to prove. The state inside the record is checks.Worst over the same vector
// the exit code was computed from.
func (m *FoundryTools) gateStage(ctx context.Context, tree, stage, base string) *StageResult {
	lane := laneOf(stage)
	vector, _ := m.gradeTree(ctx, lane, tree, gradedStage(stage), base)
	return stageResult(checks.SettleStage(lane, vector))
}

// StageMutationBg is the background mutation lane's stage (hook-check
// addendum M8): Daedalus runs it at tier 5 on a commit the hook checked, and
// it grades exactly what the mutation stage grades — only its label differs,
// so the door and the record never take a background run for the pull's
// mutation lane.
const StageMutationBg = "mutation-bg"

// gradedStage is the atom stage a stage grades: the background mutation lane
// grades the mutation atoms; every other stage grades itself.
func gradedStage(stage string) string {
	if stage == StageMutationBg {
		return checks.StageMutation
	}
	return stage
}

// laneOf is the lane a stage's record is labelled with — the name the door
// and Daedalus fold the verdict under. The pull path (no stage) and prepush
// are the gate; the mutation stage is its own lane; precommit is the commit
// hook's `check` lane, run as a Daedalus Job (the hook-check design, base
// pull 1), so its record is never mistaken for a gate's.
func laneOf(stage string) string {
	switch stage {
	case checks.StageMutation:
		return "mutation"
	case checks.StageOrbit:
		return "orbit"
	case checks.StageVisual:
		return "visual"
	case checks.StagePrecommit:
		return "check"
	case StageMutationBg:
		return "mutation-bg"
	}
	return "gate"
}

// gradeTree proves the tree and answers the vector and its worst state. Every
// reason the grading could not happen is a one-atom cannot-run vector.
func (m *FoundryTools) gradeTree(ctx context.Context, lane, tree, stage, base string) ([]checks.Verdict, int) {
	cannot := func(reason string) ([]checks.Verdict, int) {
		return checks.CannotRunVector(lane, stage, reason), int(checks.StateCannotRun)
	}
	if m.Repo == "" || m.Sha == "" {
		return cannot("the gate grades a commit the engine fetched — construct the module with --repo and --sha")
	}
	got, err := m.Tree(ctx)
	if err != nil {
		return cannot(fmt.Sprintf("the engine could not fetch %s at %.12s from the door, or read its tree: %v", starOf(m.Repo), m.Sha, err))
	}
	if got != tree {
		return cannot(fmt.Sprintf("the fetched commit's tree is %.12s, the door named %.12s — refusing to grade a tree the settle would not describe", got, tree))
	}
	raw, err := gateVector(ctx, m, stage, base)
	if err != nil {
		return cannot("the module could not produce a vector: " + err.Error())
	}
	vector, err := checks.ParseVector(raw)
	if err != nil {
		return cannot(err.Error())
	}
	if len(vector) == 0 {
		return cannot("the module answered an EMPTY vector — existence is not a pass, and neither is nothing")
	}
	return vector, checks.Worst(vector)
}
