package main

import (
	"context"

	"dagger/foundry-tools/internal/checks"
)

// THE STAGES (CA master-plan F11–F16). A stage is a set of atoms settled as one
// answer — its worst state, what ran, what had no surface here, and the log —
// returned as a VALUE, never an error, so a stage whose atoms found something
// is still an answer the engine can hold (F11: a function that returns an
// error is never cached, and nothing that is not an answer should be).

// StageResult is one stage's answer.
type StageResult struct {
	// Stage is the stage's name: check.
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
	// Log is every atom that ran, in order, then the omitted list.
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
	return &StageResult{Stage: st.Name, State: st.State, Lanes: st.Lanes, Atoms: rows(st.Ran), Omitted: rows(st.Omitted), Log: st.Log}
}
