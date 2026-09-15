package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
	"dagger/foundry-tools/internal/gatelane"
)

// THE GATE LANE, AS ONE FUNCTION. The door's gate Job runs `dagger call …
// gate` as its only process, in place of infra's ca-gate tools.sh (a dagger
// and kubectl download), gate.py (an engine pod lookup, the tree proof, a
// watched `dagger call verdicts`) and attest.py (the receipt, over mTLS as the
// pod). The mutation lane is the same function at --stage=mutation with no
// socket: it settles from its exit and attests nothing, as gate.py did for it.
//
// EVERY GATE EXIT ATTESTS: the vector the module produced, or a one-atom
// cannot-run vector that says why there is none, so the door's join always has
// a receipt for the tree and never guesses between "not yet" and "never". A
// receipt that does not land turns any exit into could-not-run — a run the
// door cannot read is a run that did not happen.
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

// The receipt's delivery through a hades hold-down: attest.py's six attempts,
// backing off from four seconds, inside a three-minute budget. The durations
// are parsed, not multiplied: coverage never reaches a package-level
// initializer, so an operator in one is a mutant no test can kill.
var (
	attestAttempts = 6
	attestBackoff  = duration("4s")
	attestBudget   = duration("3m")
)

// duration reads a duration literal; one that does not parse is zero.
func duration(s string) time.Duration {
	d, _ := time.ParseDuration(s)
	return d
}

// Gate proves the fetched commit is the tree the door named, grades it,
// attests the receipt as the calling pod, and settles on the vector's worst
// state.
func (m *FoundryTools) Gate(
	ctx context.Context,
	// The tree the door named (CA_GATE_TREE): the receipt's key. The fetched
	// commit's tree must be this one, or nothing is graded.
	tree string,
	// The foundry-tools pin the door resolved (CA_GATE_MODULE): what the
	// receipt says ran.
	pin string,
	// The change set's base (CA_GATE_BASE), for the atoms that judge a change.
	// +optional
	base string,
	// Only atoms at this stage: empty is the pull path, "mutation" the
	// mutation lane.
	// +optional
	stage string,
	// The pod's SPIRE agent socket. With it the receipt is attested as that
	// pod; without it (the mutation lane) the lane settles from its exit alone.
	// +optional
	spire *dagger.Socket,
	// hades' mTLS address.
	// +optional
	// +default="https://hades.default.svc.cluster.local:8102"
	hades string,
	// The SPIFFE id hades must present.
	// +optional
	// +default="spiffe://notusmi.com/star/hades"
	hadesID string,
) error {
	lane := "gate"
	if stage == "mutation" {
		lane = "mutation"
	}
	vector, code := m.gradeTree(ctx, lane, tree, stage, base)
	summary := gatelane.Summary(vector)
	if spire == nil {
		return settle(ctx, code, lane+": "+summary+"\nsettled from its exit; no receipt by design")
	}
	if tree == "" {
		return settle(ctx, gatelane.CouldNotRun, lane+": "+summary+"\nno tree to key a receipt on, so nothing was attested")
	}
	receipt := gatelane.Receipt{Tree: tree, ModulePin: pin, ClientID: gatelane.ClientID(starOf(m.Repo), tree), Verdict: vector}
	if acode, why := attest(ctx, spire, hades, hadesID, receipt); acode != gatelane.Clean {
		return settle(ctx, gatelane.CouldNotRun, lane+": "+summary+"\nthe verdict was produced but NOT attested, so the door will see no receipt for this tree: "+why)
	}
	return settle(ctx, code, fmt.Sprintf("%s: %s\nattested for tree %.12s", lane, summary, tree))
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
		return cannot(fmt.Sprintf("the fetched commit's tree is %.12s, the door named %.12s — refusing to grade a tree the receipt would not describe", got, tree))
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

// attest lands the receipt on ci-attest as the pod the socket came from,
// waiting out a hades hold-down or a lost connection, and answers Clean only
// when it landed.
func attest(ctx context.Context, spire *dagger.Socket, hades, hadesID string, r gatelane.Receipt) (int, string) {
	body, err := json.Marshal(r)
	if err != nil {
		return gatelane.CouldNotRun, err.Error()
	}
	caller := hadesCaller(spire, hades, hadesID, strconv.FormatInt(time.Now().UnixNano(), 10)).
		WithEnvVariable("HADESCALL_SVID_FIELD", "svid")
	wait, budget := attestBackoff, attestBudget
	why := "no attempt was made"
	for i := range attestAttempts {
		attempt := i + 1
		out, code, err := output(ctx, caller.
			WithEnvVariable("GATE_ATTEST_ATTEMPT", strconv.Itoa(attempt)).
			WithExec([]string{"/usr/local/bin/hadescall", "tartarus_attest_emit", string(body)}, anyExit))
		if err != nil {
			return gatelane.CouldNotRun, "hadescall did not run: " + err.Error()
		}
		if code != 0 {
			// hadescall could not ask: no identity yet, or hades did not answer.
			why = "could not ask hades: " + out
		} else {
			status, answer, perr := buildlane.ParseCall(out)
			if perr != nil {
				return gatelane.CouldNotRun, perr.Error()
			}
			var outcome gatelane.Outcome
			outcome, why = gatelane.Attested(status, answer)
			switch outcome {
			case gatelane.Landed:
				return gatelane.Clean, why
			case gatelane.Refused:
				return gatelane.CouldNotRun, why
			}
		}
		if attempt == attestAttempts || budget <= 0 {
			break
		}
		pause := min(wait, budget)
		time.Sleep(pause)
		budget -= pause
		wait = min(wait*2, 30*time.Second)
	}
	return gatelane.CouldNotRun, why
}
