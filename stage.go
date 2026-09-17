package main

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/sync/errgroup"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE STAGES (CA master-plan F11–F16). A stage is a set of atoms settled as one
// answer — its worst state, what ran, what had no surface here, and the log —
// returned as a VALUE, never an error, so a stage whose atoms found something
// is still an answer the engine can hold (F11: a function that returns an
// error is never cached, and nothing that is not an answer should be).

// StageResult is one stage's answer.
type StageResult struct {
	// Stage is the stage's name: check (the commit) or push (the gate).
	Stage string
	// State is the worst state of the atoms that ran: 0 clean, 1 findings,
	// 2 could not run.
	State int
	// Lanes are the languages and surfaces the tree turned out to contain.
	Lanes []string
	// Atoms are the atoms that looked: the basic fanout, then the language
	// fanout.
	Atoms []AtomResult
	// Omitted are the atoms that found no surface in this tree, each with why.
	Omitted []AtomResult
	// Unreached are the sequence's atoms after the one that stopped it, in
	// the order they would have run. Empty for a stage that ran everything.
	Unreached []string
	// Log is every atom that ran, in order, then the omitted list, then what
	// the stage never reached.
	Log string
}

// AtomResult is one atom's line in a stage.
type AtomResult struct {
	// Atom is the namespaced id, e.g. go:vet.
	Atom string
	// Group is the fanout: basic or language.
	Group string
	// State is 0 clean, 1 findings, 2 could not run.
	State int
	// Result is pass, findings, cannot-run or absent.
	Result string
	// Reason is the atom's own output or reason.
	Reason string
}

// stageLogLimit bounds the log the exit exec carries as its argument.
const stageLogLimit = 64 << 10

// Check is the commit stage: the basic checks every tree gets, and the language
// checks for what this tree contains — format, lint and tests — each fanout
// run to completion and the stage settled on its worst state.
//
// NEVER CACHED AS A WHOLE, for Verdicts' reason: a stage whose atom could not
// run is still a successful return. The atoms' execs stay cached.
//
// +cache="never"
func (m *FoundryTools) Check(ctx context.Context) (*StageResult, error) {
	vs, err := m.vector(ctx, checks.StagePrecommit, "", "")
	if err != nil {
		return nil, err
	}
	return stageResult(checks.SettleStage("check", vs)), nil
}

// Push is the push stage: the slow checks IN SEQUENCE — deep lint, the
// known-vulnerability scan, the release build, then the race + live-database
// suite — beside the mutation gate, which runs concurrently over the change
// set. Rob, 2026-09-17: "Complex Checks (Sequential, parallel with Mutation)
// … Each stage fails fast, and returns the cumulative logs up until the
// failing step."
//
// FAIL FAST MEANS THE SEQUENCE STOPS, NOT THAT THE STAGE ABANDONS MUTATION.
// The sequence stops at the first atom that found something or could not run,
// and names the atoms it never reached. Mutation is its own lane and is
// allowed to finish: it is already running, its answer is about the same
// push, and cancelling it would throw away work the engine would otherwise
// cache for the next call on this tree.
//
// NEVER CACHED AS A WHOLE, for Verdicts' reason.
//
// +cache="never"
func (m *FoundryTools) Push(
	ctx context.Context,
	// The change set's base — the pull's merge base — for the atoms that
	// grade a change rather than a tree (mutation, fleet:witness).
	// +optional
	base string,
) (*StageResult, error) {
	var (
		complex, mutation []checks.Verdict
		unreached         []string
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		complex, unreached, err = m.sequence(gctx, checks.StagePrepush, base)
		return err
	})
	g.Go(func() error {
		var err error
		mutation, err = m.vector(gctx, checks.StageMutation, "", base)
		return err
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return stageResult(checks.SettleStage("push", append(complex, mutation...), unreached...)), nil
}

// sequence runs a stage's atoms ONE AT A TIME, in the catalogue's order, and
// stops at the first that found something or could not run. It answers the
// verdicts it collected and the ids it never reached.
//
// The catalogue's order is the running order and it is cheapest-first on
// purpose (internal/checks/atoms.go says so at the go rows): a push that is
// going to fail staticcheck should not first spend minutes in the suite.
func (m *FoundryTools) sequence(ctx context.Context, stage, base string) ([]checks.Verdict, []string, error) {
	selected := checks.AtomsForStage(stage)
	selected, covered := checks.Subsumed(selected)
	r := newRun(m.Source, m.Repo, base)
	var out []checks.Verdict
	var unreached []string
	for i, a := range selected {
		v, err := verdictFor(ctx, r, a.ID)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, v)
		if checks.Stops(v) {
			unreached = checks.TailAfter(selected, i)
			break
		}
	}
	// The atoms another selected atom covered are answered either way — a
	// sequence that stopped still says what stood down, for vector's reason.
	for _, a := range covered {
		out = append(out, checks.CoveredVerdict(a))
	}
	return out, unreached, nil
}

// Exit ends on the stage's state, so `dagger call check exit` exits 0, 1 or 2
// and prints the stage's log.
func (s *StageResult) Exit(ctx context.Context) error {
	return settle(ctx, s.State, checks.LogTail(s.Log, stageLogLimit))
}

func stageResult(st checks.Stage) *StageResult {
	rows := func(as []checks.StageAtom) []AtomResult {
		out := make([]AtomResult, len(as))
		for i, a := range as {
			out[i] = AtomResult{Atom: a.Atom, Group: a.Group, State: a.State, Result: a.Result, Reason: a.Reason}
		}
		return out
	}
	return &StageResult{
		Stage: st.Name, State: st.State, Lanes: st.Lanes,
		Atoms: rows(st.Ran), Omitted: rows(st.Omitted), Unreached: st.Unreached, Log: st.Log,
	}
}

// Release is the artifact the Gate compiled, as a directory of binaries — what
// F14's Build copies onto the language base instead of compiling again.
//
// IT IS THE SAME EXEC THE go:release ATOM RAN. Same container, same argv, same
// inputs, so the engine answers this from the cache the push already paid for
// (F11: a cached result exists only because that exec ran on that input). A
// tree whose release build failed has no directory to give, and says so as an
// error rather than handing back an empty one.
func (m *FoundryTools) Release(ctx context.Context) (*dagger.Directory, error) {
	r := newRun(m.Source, m.Repo, "")
	plan, why := r.releasePlan(ctx)
	if why != "" {
		return nil, errors.New(why)
	}
	ctr, err := r.releaseBuild(ctx, plan)
	if err != nil {
		return nil, err
	}
	code, err := ctr.ExitCode(ctx)
	if err != nil {
		return nil, fmt.Errorf("the release build did not run: %w", err)
	}
	if code != 0 {
		out, _ := ctr.Stderr(ctx)
		return nil, fmt.Errorf("the release build failed (exit %d): %s", code, lastLine(out))
	}
	return ctr.Directory(checks.ReleaseOut), nil
}
