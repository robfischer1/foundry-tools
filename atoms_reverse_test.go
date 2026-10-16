package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/catalogue"
	"dagger/foundry-tools/internal/checks"
)

// chainVerdicts is the chains' answer to the reverse shadow: the verdicts of the
// atoms it reports on (the pull path's binary atoms that have a chain), each as
// the binary voted it.
func chainVerdicts(t *testing.T) ([]checks.Verdict, error) {
	t.Helper()
	vs, err := checks.ParseVector(binaryVector(t, ""))
	ids, _ := reverseIDs("", false)
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
	ids, _ := reverseIDs("", false)
	n := len(ids)
	if want := fmt.Sprintf("shadow atoms (binary voted): %d compared, %d identical, 0 same state, 0 state differs", n, n); !strings.Contains(report.String(), want) {
		t.Errorf("report %q lacks %q", report.String(), want)
	}
}

// (b) A CHAIN SIDE THAT FAILS, PANICS OR HANGS CHANGES NOTHING: the record is
// the one a lane with no shadow records, and the report says what happened.
func TestAFailingHangingOrPanickingReverseShadowChangesNothing(t *testing.T) {
	baseline, _, _ := func() (string, *bytes.Buffer, time.Duration) {
		return reverseRun(t, func(context.Context) ([]checks.Verdict, error) { return chainVerdicts(t) }, time.Minute)
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
			ids, _ := reverseIDs("", false)
			for _, id := range ids {
				vs = append(vs, checks.VerdictOf(checks.AtomByID(id), 1, "chain finds something"))
			}
			return vs, nil
		}, fmt.Sprintf("%d state differs", len(mustIDs()))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record, report, took := reverseRun(t, tc.chain, 100*time.Millisecond)
			if got, want := scrubTimes(record), scrubTimes(baseline); got != want {
				i := 0
				for i < min(len(got), len(want)) && got[i] == want[i] {
					i++
				}
				from := max(i-150, 0)
				t.Errorf("the record moved at byte %d:\n got ...%.400s\nwant ...%.400s", i, got[from:], want[from:])
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
	m := &FoundryTools{box: newBallotBox(nil, false)}
	m.box.seal()
	_, _, waitErr := m.box.wait(soon(t))
	got := renderReverse([]string{"fleet:check-yaml"}, false, nil, nil, nil, nil, waitErr, 0)
	if !strings.Contains(got, "the binary's vote is missing: the lane settled without casting a vote") {
		t.Errorf("report %q", got)
	}
	if _, _, err := (*ballotBox)(nil).wait(soon(t)); err == nil {
		t.Error("a lane with no box was given votes")
	}
	ctx, cancel := context.WithCancel(soon(t))
	cancel()
	if _, _, err := newBallotBox(nil, false).wait(ctx); err == nil || !errors.Is(err, context.Canceled) {
		t.Errorf("a box nobody filled answered %v", err)
	}
}

// The chain's fleet:witness is not run beside the voter's real asks, and the
// report says it was left out; a switch brings it back.
func TestTheReverseShadowLeavesTheWitnessChainOut(t *testing.T) {
	ids, skipped := reverseIDs(checks.StagePrepush, false)
	chained := 0 // the binary's atoms that no file of their own answers
	for _, id := range atoms.StageIDs(checks.StagePrepush) {
		if !catalogue.Ported(id) {
			chained++
		}
	}
	if !skipped || slices.Contains(ids, "fleet:witness") || len(ids) != chained-1 {
		t.Errorf("ids %v skipped %v", ids, skipped)
	}
	// an atom that lives in its own file has no chain to hold against
	if ids, skipped := reverseIDs(checks.StageOrbit, false); skipped || len(ids) != 0 {
		t.Errorf("orbit: ids %v skipped %v", ids, skipped)
	}
	if ids, skipped := reverseIDs(checks.StagePrepush, true); skipped || !slices.Contains(ids, "fleet:witness") {
		t.Errorf("with the switch on: ids %v skipped %v", ids, skipped)
	}
	for _, with := range []bool{false, true} {
		var only string
		m := &FoundryTools{box: newBallotBox(func(_ context.Context, _ *FoundryTools, _, o, _ string) ([]checks.Verdict, error) {
			only = o
			return nil, nil
		}, with)}
		m.box.put(map[string]checks.Verdict{})
		m.box.seal()
		m.reverseShadow(soon(t), checks.StagePrepush, "")
		if strings.Contains(only, "fleet:witness") != with {
			t.Errorf("witness switch %v: the chains were asked for %q", with, only)
		}
	}
	for _, m := range []*FoundryTools{{}, {box: newBallotBox(nil, false)}} {
		if got := m.reverseShadow(soon(t), checks.StagePrepush, ""); !strings.Contains(got, "no chain side") {
			t.Errorf("a shadow with no chain side answered %q", got)
		}
	}
	out := renderReverse([]string{"fleet:check-yaml"}, true, nil, nil, map[string]checks.Verdict{}, nil, nil, 0)
	if !strings.Contains(out, "fleet:witness: not compared") || !strings.HasPrefix(out, "shadow atoms (binary voted): 0 compared") {
		t.Errorf("report %q", out)
	}
	noChains := func(context.Context, *FoundryTools, string, string, string) ([]checks.Verdict, error) {
		return nil, nil
	}
	if got := (&FoundryTools{box: newBallotBox(noChains, false)}).reverseShadow(soon(t), checks.StageMutation, ""); !strings.Contains(got, "nothing to compare") {
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

// mustIDs are the atoms the reverse shadow reports on over the pull path.
func mustIDs() []string {
	ids, _ := reverseIDs("", false)
	return ids
}

// scrubTimes blanks the clock a record carries for each atom: an atom that runs
// in the lane (the ones that live in their own files and have no binary vote)
// stamps its own start and finish, and two runs never stamp alike.
func scrubTimes(record string) string {
	return timesRE.ReplaceAllString(record, `"$1":""`)
}

var timesRE = regexp.MustCompile(`"(started_at|finished_at)":"[^"]*"`)
