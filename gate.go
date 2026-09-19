package main

import (
	"context"
	"fmt"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/gatelane"
)

// THE GATE LANE, AS ONE FUNCTION. The door's gate Job runs `dagger call …
// gate` as its only process, in place of infra's ca-gate tools.sh (a dagger
// and kubectl download) and gate.py (an engine pod lookup, the tree proof, a
// watched `dagger call verdicts`). The mutation lane is the same function at
// --stage=mutation.
//
// THE EXIT IS THE VERDICT, for both lanes: 0 pass, 1 findings, 2 cannot-run,
// with the summary and every red atom's reason on stderr for the door's
// settle to carry. Until CA F16 (2026-09-18) the gate lane also ATTESTED its
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
) error {
	lane := "gate"
	if stage == "mutation" {
		lane = "mutation"
	}
	vector, code := m.gradeTree(ctx, lane, tree, stage, base)
	return settle(ctx, code, fmt.Sprintf("%s: %s\nsettled from its exit under %s", lane, gatelane.Summary(vector), pin))
}

// gradeTree proves the tree and answers the vector and its worst state. Every
// reason the grading could not happen is a one-atom cannot-run vector.
func (m *FoundryTools) gradeTree(ctx context.Context, lane, tree, stage, base string) ([]checks.Verdict, int) {
	cannot := func(reason string) ([]checks.Verdict, int) {
		return gatelane.CannotRunVector(lane, stage, reason), gatelane.CouldNotRun
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
	vector, err := gatelane.ParseVector(raw)
	if err != nil {
		return cannot(err.Error())
	}
	if len(vector) == 0 {
		return cannot("the module answered an EMPTY vector — existence is not a pass, and neither is nothing")
	}
	return vector, gatelane.Worst(vector)
}
