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

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// reverseRun is the lane of a gate-stage run whose binary voted and whose chains
// are the comparator: the chains side answers as the test says, and the report
// goes to the buffer.
func reverseRun(t *testing.T, chainSide func(ctx context.Context) ([]checks.Verdict, error), grace time.Duration) (record string, report *bytes.Buffer, took time.Duration) {
	t.Helper()
	origRun, origToday, origGrace, origOut := shadowRun, shadowToday, shadowGrace, shadowOut
	t.Cleanup(func() { shadowRun, shadowToday, shadowGrace, shadowOut = origRun, origToday, origGrace, origOut })
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
	engine.stdout(`"rev-parse","HEAD^{tree}"`, fakeTree+"\n")
	m := &FoundryTools{Source: dag.Directory(), Repo: "http://door:8215/rob/ares.git", Sha: buildSha}
	start := time.Now()
	record, err := m.Gate(context.Background(), fakeTree, gatePin, "base-sha", checks.StageOrbit)
	if err != nil {
		t.Fatal(err)
	}
	return record, report, time.Since(start)
}

// The chains agree with the vote: the report says the binary voted and that
// everything compared was identical.
func TestTheReverseShadowReportsWhoVotedAndWhoAgreed(t *testing.T) {
	same := func(context.Context) ([]checks.Verdict, error) {
		vs, err := checks.ParseVector(binaryVector(t, checks.StageOrbit))
		return vs, err
	}
	record, report, _ := reverseRun(t, same, time.Minute)
	recordedOn(t, record, "0", votedMark+"orbit:contracts")
	if want := "shadow atoms (binary voted): 4 compared, 4 identical, 0 same state, 0 state differs"; !strings.Contains(report.String(), want) {
		t.Errorf("report %q lacks %q", report.String(), want)
	}
}

// (b) A CHAIN SIDE THAT FAILS, PANICS OR HANGS CHANGES NOTHING: the record is
// the one a lane with no shadow records, and the report says what happened.
func TestAFailingHangingOrPanickingReverseShadowChangesNothing(t *testing.T) {
	baseline, _, _ := func() (string, *bytes.Buffer, time.Duration) {
		return reverseRun(t, func(context.Context) ([]checks.Verdict, error) {
			return checks.ParseVector(binaryVector(t, checks.StageOrbit))
		}, time.Minute)
	}()
	stuck := make(chan struct{})
	t.Cleanup(func() { close(stuck) })
	for _, tc := range []struct {
		name  string
		chain func(context.Context) ([]checks.Verdict, error)
		want  string
	}{
		{"the chains fail", func(context.Context) ([]checks.Verdict, error) { return nil, errors.New("engine gone") },
			"not compared - the chains did not answer: engine gone"},
		{"the chains panic", func(context.Context) ([]checks.Verdict, error) { panic("boom") },
			"the shadow panicked: boom"},
		{"the chains hang", func(context.Context) ([]checks.Verdict, error) { <-stuck; return nil, nil },
			"no answer within"},
		{"the chains disagree on every atom", func(context.Context) ([]checks.Verdict, error) {
			var vs []checks.Verdict
			for _, id := range atoms.StageIDs(checks.StageOrbit) {
				vs = append(vs, checks.VerdictOf(checks.AtomByID(id), 1, "chain finds something"))
			}
			return vs, nil
		}, "4 state differs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record, report, took := reverseRun(t, tc.chain, 100*time.Millisecond)
			if record != baseline {
				t.Errorf("the record moved:\n got %.300s\nwant %.300s", record, baseline)
			}
			if !strings.Contains(report.String(), tc.want) {
				t.Errorf("report %q lacks %q", report.String(), tc.want)
			}
			if took > 10*time.Second {
				t.Errorf("the lane waited %v on its comparator", took)
			}
		})
	}
}

// A lane that settles without ever casting a vote (a refused tree) does not wait
// out the grace for one: it says so.
func TestAReverseShadowDoesNotWaitForAVoteThatCannotCome(t *testing.T) {
	m := &FoundryTools{box: newBallotBox()}
	m.box.put(nil)
	got := renderReverse([]string{"fleet:check-yaml"}, false, nil, nil, nil, func() error { _, err := m.box.wait(context.Background()); return err }(), 0)
	if !strings.Contains(got, "the binary's vote is missing: the lane settled without casting a vote") {
		t.Errorf("report %q", got)
	}
	if _, err := (*ballotBox)(nil).wait(context.Background()); err == nil {
		t.Error("a lane with no box was given votes")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := newBallotBox().wait(ctx); err == nil || !errors.Is(err, context.Canceled) {
		t.Errorf("a box nobody filled answered %v", err)
	}
}

// The chain's fleet:witness is not run beside the voter's real asks, and the
// report says it was left out; a switch brings it back.
func TestTheReverseShadowLeavesTheWitnessChainOut(t *testing.T) {
	ids, skipped := reverseIDs(checks.StagePrepush)
	if !skipped || slices.Contains(ids, "fleet:witness") || len(ids) != len(atoms.StageIDs(checks.StagePrepush))-1 {
		t.Errorf("ids %v skipped %v", ids, skipped)
	}
	if ids, skipped := reverseIDs(checks.StageOrbit); skipped || len(ids) != 4 {
		t.Errorf("orbit: ids %v skipped %v", ids, skipped)
	}
	reverseShadowWitness = true
	t.Cleanup(func() { reverseShadowWitness = false })
	if ids, skipped := reverseIDs(checks.StagePrepush); skipped || !slices.Contains(ids, "fleet:witness") {
		t.Errorf("with the switch on: ids %v skipped %v", ids, skipped)
	}
	reverseShadowWitness = false
	out := renderReverse([]string{"fleet:check-yaml"}, true, nil, nil, map[string]checks.Verdict{}, nil, 0)
	if !strings.Contains(out, "fleet:witness: not compared") || !strings.HasPrefix(out, "shadow atoms (binary voted): 0 compared") {
		t.Errorf("report %q", out)
	}
	if got := (&FoundryTools{box: newBallotBox()}).reverseShadow(context.Background(), checks.StageMutation, ""); !strings.Contains(got, "nothing to compare") {
		t.Errorf("a stage with no binary atom compared something: %q", got)
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
