package main

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// THE VOTE. The atoms binary (internal/atoms) carries the cheap fleet atoms:
// the verdict the door reads for each atom it registers is the BINARY'S, run
// once for the lane in the tools container. The atoms it does not register (the
// toolchain tier: go, python, rust, ts, and mutation) keep the chain path
// unchanged.
//
// THE SWITCH IS atomsVoter's default, defaultVoter, and it is the only line a
// rollback changes: voterChains puts every atom back on its chain.

// voter names a side that can vote.
type voter string

const (
	// voterBinary votes with the atoms binary's verdicts.
	voterBinary voter = "binary"
	// voterChains votes with the per-atom container chains.
	voterChains voter = "chains"
)

// defaultVoter is THE SWITCH. Set it to voterChains and commit to roll the cutover
// back: the vector, the push sequence and the local hook read the chains again.
const defaultVoter = voterBinary

// atomsVoter is who votes. A variable so a test can flip it; nothing else sets it.
var atomsVoter = defaultVoter

// castBinary is the binary's one run for a lane: its vector as JSON. A variable
// so a test names what the binary answers without an engine.
var castBinary castFunc = func(ctx context.Context, m *FoundryTools, stage, base string) (string, error) {
	return m.atomsBallot(ctx, stage, base)
}

// castFunc is the binary's run for a lane. A poll COPIES it when the lane
// starts: the run can outlive the lane (its context ended first), and a package
// variable read from there would race with whatever writes it next.
type castFunc func(ctx context.Context, m *FoundryTools, stage, base string) (string, error)

// poll is the binary's vote for one lane: which of the lane's atoms it answers,
// started once, read by id. A nil poll answers nothing, so a lane the binary
// has no atom in (mutation, visual) and a chain-voted lane read the same.
//
// THE CHAINS KEEP THEIR RESILIENCE. An atom the binary settles as could-not-run
// (a network error, a tool layer that did not build, a crash, an OOM, a lane
// deadline: the reason does not matter) is asked ONCE more through its old chain,
// and the chain's answer is the vote. That restores the chains' per-atom blast
// radius and their re-ask: a broken binary or tools layer cannot turn all 50
// atoms red, because each of them goes back to the path that answered before.
// A finding (state 1) is a verdict and is never re-asked.
type poll struct {
	m           *FoundryTools
	run         castFunc
	chain       chainFunc
	stage, base string
	want        []checks.AtomDef
	begin       sync.Once
	done        chan struct{}
	votes       map[string]checks.Verdict
}

// chainFunc is one atom's old chain, the lane's own (verdictFor on its run),
// which asks the atom a second time past the cache when it could not run.
type chainFunc func(ctx context.Context, id string) (checks.Verdict, error)

// pollFor answers the lane's poll: the atoms of run that the binary registers at
// this stage, when the binary votes. Nil when it does not, or has none here.
// chain is how a could-not-run atom is asked again.
func (m *FoundryTools) pollFor(side voter, stage, base string, run []checks.AtomDef, chain chainFunc) *poll {
	if side != voterBinary {
		return nil
	}
	carried := atoms.StageIDs(stage)
	var want []checks.AtomDef
	for _, a := range run {
		if slices.Contains(carried, a.ID) {
			want = append(want, a)
		}
	}
	if len(want) == 0 {
		return nil
	}
	return &poll{m: m, run: castBinary, chain: chain, stage: stage, base: base, want: want, done: make(chan struct{})}
}

// atom is the catalogue row of an atom the binary answers in this lane.
func (p *poll) atom(id string) (checks.AtomDef, bool) {
	if p == nil {
		return checks.AtomDef{}, false
	}
	i := slices.IndexFunc(p.want, func(a checks.AtomDef) bool { return a.ID == id })
	if i < 0 {
		return checks.AtomDef{}, false
	}
	return p.want[i], true
}

// carries reports whether the binary answers this atom in the lane.
func (p *poll) carries(id string) bool {
	_, ok := p.atom(id)
	return ok
}

// start runs the binary in the background, once.
func (p *poll) start(ctx context.Context) {
	if p == nil {
		return
	}
	p.begin.Do(func() {
		go func() {
			defer close(p.done)
			// A PANIC IN THE BINARY'S RUN OR ITS PARSING IS AN ERROR for every atom,
			// which vote then sends to the chains: it must never kill the lane.
			defer func() {
				if r := recover(); r != nil {
					p.votes = failedVotes(p.want, fmt.Errorf("the atoms binary's run panicked: %v", r))
				}
			}()
			p.votes = p.m.cast(ctx, p.run, p.stage, p.base, p.want)
		}()
	})
}

// failedVotes is a could-not-run for every atom of want, naming why.
func failedVotes(want []checks.AtomDef, err error) map[string]checks.Verdict {
	votes := make(map[string]checks.Verdict, len(want))
	for _, a := range want {
		votes[a.ID] = checks.VerdictOf(a, int(checks.StateCannotRun), a.ID+": CANNOT RUN - the atoms binary did not answer: "+err.Error())
	}
	return votes
}

// fallbackWithin bounds a chain ask made after the lane's own context ended.
const fallbackWithin = 5 * time.Minute

// vote is the lane's vote for id: the binary's verdict, waiting for the run if it
// is still going, or, when the binary could not run the atom, the chain's. False
// says the chain answers this atom anyway (the binary does not carry it). A lane
// that ends first (its context is done) does not wait on the binary for ever.
func (p *poll) vote(ctx context.Context, id string) (checks.Verdict, bool) {
	a, ok := p.atom(id)
	if !ok {
		return checks.Verdict{}, false
	}
	p.start(ctx)
	var v checks.Verdict
	select {
	case <-p.done:
		v = p.votes[id]
	case <-ctx.Done():
		v = checks.VerdictOf(a, int(checks.StateCannotRun),
			a.ID+": CANNOT RUN - the lane ended before the atoms binary answered: "+ctx.Err().Error())
	}
	if v.State == int(checks.StatePass) || v.State == int(checks.StateFindings) || p.chain == nil {
		return v, true
	}
	// ANY OTHER STATE IS A COULD-NOT-RUN (checks.Worst reads it so): one ask of
	// the old chain, whose answer is the vote. A chain that cannot be asked at all
	// (verdictFor errors only for an atom with no runner) leaves the binary's 2
	// standing. A lane whose context is already over would hand the chain a
	// cancelled context, and verdictFor does not error on that: it returns the
	// chain's own 2, and that 2 would be the vote. So the ask runs on a context
	// detached from the lane's cancellation and bounded by fallbackWithin.
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), fallbackWithin)
		defer cancel()
	}
	cv, err := p.chain(ctx, id)
	if err != nil {
		return v, true
	}
	return cv, true
}

// cast runs the binary once and answers a verdict for every atom in want. A
// binary that would not run, an output that is not a vector and an atom it left
// out are each that atom's could-not-run, never a pass and never a fallback to
// the chain here: vote asks the chain once for each of them.
func (m *FoundryTools) cast(ctx context.Context, run castFunc, stage, base string, want []checks.AtomDef) map[string]checks.Verdict {
	raw, err := run(ctx, m, stage, base)
	var vector []checks.Verdict
	if err == nil {
		var tr checks.RunTrailer
		vector, tr, err = checks.ParseRun(raw)
		tallyOf(ctx).add(tr.StageCPUMs)
	}
	byID := map[string]checks.Verdict{}
	for _, v := range vector {
		byID[v.Atom] = v
	}
	votes := make(map[string]checks.Verdict, len(want))
	if err != nil {
		votes = failedVotes(want, err)
	}
	for _, a := range want {
		if _, set := votes[a.ID]; set {
			continue
		}
		v, ok := byID[a.ID]
		if !ok {
			v = checks.VerdictOf(a, int(checks.StateCannotRun), a.ID+": CANNOT RUN - the atoms binary returned no verdict for this atom")
		}
		votes[a.ID] = v
	}
	return votes
}
