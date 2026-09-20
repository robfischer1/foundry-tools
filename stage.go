package main

import (
	"context"
	"errors"
	"fmt"
	"slices"

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
	// Logs are the lines this atom produced — every atom, passing ones
	// included. Never nil: an atom that printed nothing carries [].
	Logs []string
	// Truncated says the 1 MB per-atom cap bit, and OriginalBytes is the true
	// size before the cut. A cut is stated, never silent.
	Truncated     bool
	OriginalBytes int
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
	// Where the base can be fetched from when the tree is a linked-worktree
	// snapshot with no history of its own — the star's origin remote, the
	// door's clone URL (`git remote get-url origin`). The engine fetches the
	// base commit and the diff-scoped atoms grade the real change set instead
	// of standing down (run.gitReadyOn). Unused for a tree the engine fetched
	// itself (--repo/--sha), which carries its history.
	// +optional
	origin string,
) (*StageResult, error) {
	var (
		complex, mutation []checks.Verdict
		unreached         []string
	)
	// The origin is the push's, so it rides on the module the two halves read
	// — m is a value here, and Dagger constructs a fresh one per call.
	m.Origin = origin
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
	r := newRun(m.Source, m.Repo, base).fromOrigin(m.Origin)
	// THE SEQUENCE IS PLANNED TOO, and forgetting that was a real red: when
	// the lane gate moved out of verdictFor and into run.plan, only the fanout
	// was wired to it, so every push-stage atom ran whatever the tree
	// contained. Measured on foundry-tools' own pre-push hook, 2026-09-17:
	// rust:cargo-audit could-not-run (cargo, exit 101) and python:pip-audit
	// reported PASS — in a repo with neither a Cargo.toml nor a pyproject.toml.
	// A pass for a lane that does not exist is the exact conflation this
	// module's three states exist to prevent.
	plan, absent, err := r.plan(ctx, selected)
	if err != nil {
		return nil, nil, err
	}
	selected = plan.Run
	out := absent
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
			out[i] = AtomResult{Atom: a.Atom, Group: a.Group, State: a.State, Result: a.Result, Reason: a.Reason,
				Logs: a.Logs, Truncated: a.Truncated, OriginalBytes: a.OriginalBytes}
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
//
// THE LANE IS THE IMAGE'S BASE FIRST, THEN THE TREE'S. A Dockerfile on the
// bun or python base gets that base's release (ts:release, python:release —
// the bundle or the venv, built on the base itself). Otherwise the tree is
// read the way the cast lane reads it (cast.go release): a root go.mod is the
// Go lane's build, a Cargo.toml the Rust lane's, and a tree declaring both is
// refused — two toolchains that each build a <star> would hand over whichever
// ran last.
func (m *FoundryTools) Release(ctx context.Context) (*dagger.Directory, error) {
	r := newRun(m.Source, m.Repo, "")
	star, err := r.starName(ctx)
	if err != nil {
		return nil, err
	}
	// An image on the bun or python base is built on that base
	// (atoms_release.go); the compiled lanes below read the tree.
	if dir, onBase, err := r.releaseOnBase(ctx); onBase || err != nil {
		return dir, err
	}
	lane, err := r.releaseLane(ctx)
	if err != nil {
		return nil, err
	}
	if lane == checks.LaneRust {
		plan, why := r.rustReleasePlan(ctx, star)
		if why != "" {
			return nil, errors.New(why)
		}
		ctr, v := r.rustReleaseBuild(ctx, checks.AtomByID("rust:release"), plan)
		switch v.State {
		case 2:
			return nil, fmt.Errorf("the release build did not run: %s", lastLine(v.Reason))
		case 1:
			return nil, fmt.Errorf("the release build failed: %s", lastLine(v.Reason))
		}
		return rustReleaseDir(ctr, plan), nil
	}
	plan, why := r.releasePlan(ctx, star)
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

// releaseLane is the toolchain that builds the star's release: LaneRust for
// a root Cargo.toml, LaneGo for a root go.mod. Both is refused, and neither
// is an error naming what is missing rather than a guess.
func (r *run) releaseLane(ctx context.Context) (checks.Lane, error) {
	entries, err := r.src.Entries(ctx)
	if err != nil {
		return "", fmt.Errorf("the repository root could not be read: %w", err)
	}
	rust := checks.DeclaresLane(entries, checks.LaneRust)
	goRoot := slices.Contains(entries, "go.mod")
	switch {
	case rust && goRoot:
		return "", errors.New("the tree declares both a Cargo.toml and a root go.mod, so the release build cannot tell which toolchain builds the star")
	case rust:
		return checks.LaneRust, nil
	case goRoot:
		return checks.LaneGo, nil
	}
	return "", errors.New("no go.mod at the repository root, and no Cargo.toml either, so there is no star binary to build")
}
