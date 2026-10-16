package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/checks"
)

// chainVerdicts is the chains' answer to the reverse shadow: the verdicts of the
// atoms it reports on (the pull path's binary atoms that have a chain), each as
// the binary voted it.
func chainVerdicts(t *testing.T) ([]checks.Verdict, error) {
	t.Helper()
	vs, err := checks.ParseVector(binaryVector(t, ""))
	ids := chainedIDs("")
	var out []checks.Verdict
	for _, v := range vs {
		if slices.Contains(ids, v.Atom) {
			out = append(out, v)
		}
	}
	return out, err
}

// reverseRun is the lane of a gate-stage run whose binary voted and whose chains
// are the comparator: the chains side answers as the test says, and the report
// goes to the buffer.
func reverseRun(t *testing.T, chainSide func(ctx context.Context) ([]checks.Verdict, error), grace time.Duration) (record string, report *bytes.Buffer, took time.Duration) {
	t.Helper()
	origRun, origToday, origGrace, origOut, origLimit := shadowRun, shadowToday, shadowGrace, shadowOut, shadowTimeout
	t.Cleanup(func() {
		shadowRun, shadowToday, shadowGrace, shadowOut, shadowTimeout = origRun, origToday, origGrace, origOut, origLimit
	})
	shadowTimeout = 5 * time.Second
	shadowRun = defaultShadow
	shadowGrace = grace
	shadowToday = func(ctx context.Context, _ *FoundryTools, _, _, _ string) ([]checks.Verdict, error) {
		return chainSide(ctx)
	}
	report = &bytes.Buffer{}
	shadowOut = func() io.Writer { return report }
	casting(t, voterBinary)
	engine.reset()
	engine.withTree(bareTree)
	engine.portedPass()
	engine.stdout(`"rev-parse","HEAD^{tree}"`, fakeTree+"\n")
	m := &FoundryTools{Source: dag.Directory(), Repo: "http://door:8215/rob/ares.git", Sha: buildSha}
	start := time.Now()
	record, err := m.Gate(soon(t), fakeTree, gatePin, "base-sha", "")
	if err != nil {
		t.Fatal(err)
	}
	return record, report, time.Since(start)
}

// The chains agree with the vote: the report says the binary voted and that
// everything compared was identical.
func TestTheReverseShadowReportsWhoVotedAndWhoAgreed(t *testing.T) {
	same := func(context.Context) ([]checks.Verdict, error) { return chainVerdicts(t) }
	record, report, _ := reverseRun(t, same, time.Minute)
	recordedOn(t, record, "0", votedMark+"fleet:check-yaml")
	// every atom the binary carries lives in its own file, and the twelve that
	// still run as chains are not in the binary: nothing is left to hold a vote
	// against, and the report says so.
	if ids := chainedIDs(""); len(ids) != 0 {
		t.Fatalf("the shadow has chained atoms %v: the report below no longer holds", ids)
	}
	if !strings.Contains(report.String(), "nothing to compare") {
		t.Errorf("report %q", report.String())
	}
}

// A lane that settles without ever casting a vote (a refused tree) does not wait
// out the grace for one: it says so.
func TestAReverseShadowDoesNotWaitForAVoteThatCannotCome(t *testing.T) {
	m := &FoundryTools{box: newBallotBox(nil)}
	m.box.seal()
	_, _, waitErr := m.box.wait(soon(t))
	got := renderReverse([]string{"fleet:check-yaml"}, nil, nil, nil, nil, waitErr, 0)
	if !strings.Contains(got, "the binary's vote is missing: the lane settled without casting a vote") {
		t.Errorf("report %q", got)
	}
	if _, _, err := (*ballotBox)(nil).wait(soon(t)); err == nil {
		t.Error("a lane with no box was given votes")
	}
	ctx, cancel := context.WithCancel(soon(t))
	cancel()
	if _, _, err := newBallotBox(nil).wait(ctx); err == nil || !errors.Is(err, context.Canceled) {
		t.Errorf("a box nobody filled answered %v", err)
	}
}

// No chain of any atom the binary carries exists to run beside the voter's real
// asks (fleet:witness among them), so the reverse shadow never asks the chains
// for anything.
func TestTheReverseShadowAsksForNoChain(t *testing.T) {
	asked := false
	m := &FoundryTools{box: newBallotBox(func(context.Context, *FoundryTools, string, string, string) ([]checks.Verdict, error) {
		asked = true
		return nil, nil
	})}
	m.box.put(map[string]checks.Verdict{})
	m.box.seal()
	for _, stage := range []string{"", checks.StagePrecommit, checks.StagePrepush, checks.StageOrbit, checks.StageMutation} {
		if got := m.reverseShadow(soon(t), stage, ""); !strings.Contains(got, "nothing to compare") {
			t.Errorf("stage %q: a stage with no chained atom compared something: %q", stage, got)
		}
	}
	if asked {
		t.Error("the chains were asked")
	}
	for _, m := range []*FoundryTools{{}, {box: newBallotBox(nil)}} {
		if got := m.reverseShadow(soon(t), checks.StagePrepush, ""); !strings.Contains(got, "no chain side") {
			t.Errorf("a shadow with no chain side answered %q", got)
		}
	}
}

// The rolled-back shadow still says who voted.
func TestTheDryShadowNamesTheChainsAsVoters(t *testing.T) {
	yaml := checks.AtomByID("fleet:check-yaml")
	vec := []checks.Verdict{checks.VerdictOf(yaml, 0, "")}
	raw, _ := json.Marshal(vec)
	if got := renderShadowAs(voterChains, vec, nil, string(raw), nil, 0); !strings.HasPrefix(got, "shadow atoms (chains voted): 1 compared") {
		t.Errorf("report %q", got)
	}
}
