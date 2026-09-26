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
	return m.gateStage(ctx, tree, stage, base).Record()
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
) (*dagger.File, error) {
	return m.gateStage(ctx, tree, stage, base).RecordFile()
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
	lane := "gate"
	if stage == "mutation" {
		lane = "mutation"
	}
	vector, _ := m.gradeTree(ctx, lane, tree, stage, base)
	return stageResult(checks.SettleStage(lane, vector))
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
