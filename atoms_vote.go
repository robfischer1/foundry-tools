package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// THE VOTE. The atoms binary (internal/atoms) carries the cheap fleet atoms.
// Until F5a it ran beside their chains and could not vote (atoms_shadow.go);
// from here the verdict the door reads for each atom it registers is the
// BINARY'S, run once for the lane in the tools container, and the chains of
// those atoms are the non-voting comparator (reverseShadow). The atoms it does
// not register (the toolchain tier: go, python, rust, ts, kube-linter, and
// mutation) keep the chain path unchanged.
//
// THE SWITCH IS atomsVoter's default, defaultVoter, and it is the only line a
// rollback changes: voterChains puts every atom back on its chain and the
// binary back to the dry, non-voting shadow it was.

// voter names a side of the comparison.
type voter string

const (
	// voterBinary votes with the atoms binary's verdicts.
	voterBinary voter = "binary"
	// voterChains votes with the per-atom container chains.
	voterChains voter = "chains"
)

// defaultVoter is THE SWITCH. Set it to voterChains and commit to roll the cutover
// back: the vector, the push sequence and the local hook read the chains again,
// the shadow is the binary's dry run, and the report says "chains voted".
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
type poll struct {
	m           *FoundryTools
	run         castFunc
	stage, base string
	want        []checks.AtomDef
	begin       sync.Once
	done        chan struct{}
	votes       map[string]checks.Verdict
}

// pollFor answers the lane's poll: the atoms of run that the binary registers at
// this stage, when the binary votes. Nil when it does not, or has none here.
func (m *FoundryTools) pollFor(side voter, stage, base string, run []checks.AtomDef) *poll {
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
	return &poll{m: m, run: castBinary, stage: stage, base: base, want: want, done: make(chan struct{})}
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
			p.votes = p.m.cast(ctx, p.run, p.stage, p.base, p.want)
		}()
	})
}

// vote is the binary's verdict for id, waiting for the run if it is still going.
// False says the chain answers this atom. A lane that ends first (its context is
// done) does not wait on the binary for ever: the atom is a could-not-run.
func (p *poll) vote(ctx context.Context, id string) (checks.Verdict, bool) {
	a, ok := p.atom(id)
	if !ok {
		return checks.Verdict{}, false
	}
	p.start(ctx)
	select {
	case <-p.done:
		return p.votes[id], true
	case <-ctx.Done():
		return checks.VerdictOf(a, int(checks.StateCannotRun),
			a.ID+": CANNOT RUN - the lane ended before the atoms binary answered: "+ctx.Err().Error()), true
	}
}

// cast runs the binary once and answers a verdict for every atom in want. A
// binary that would not run, an output that is not a vector and an atom it left
// out are each that atom's could-not-run, never a pass and never a fallback to
// the chain: the verdict is the binary's or it is a 2 that says why.
func (m *FoundryTools) cast(ctx context.Context, run castFunc, stage, base string, want []checks.AtomDef) map[string]checks.Verdict {
	raw, err := run(ctx, m, stage, base)
	var vector []checks.Verdict
	if err == nil {
		vector, err = checks.ParseVector(raw)
	}
	byID := map[string]checks.Verdict{}
	for _, v := range vector {
		byID[v.Atom] = v
	}
	votes := make(map[string]checks.Verdict, len(want))
	for _, a := range want {
		v, ok := byID[a.ID]
		switch {
		case err != nil:
			v = checks.VerdictOf(a, int(checks.StateCannotRun), a.ID+": CANNOT RUN - the atoms binary did not answer: "+err.Error())
		case !ok:
			v = checks.VerdictOf(a, int(checks.StateCannotRun), a.ID+": CANNOT RUN - the atoms binary returned no verdict for this atom")
		}
		votes[a.ID] = v
	}
	m.box.put(votes)
	return votes
}

// ballotBox hands the voter's verdicts to the reverse shadow. The shadow starts
// before grading and the vote arrives during it, so they meet here: put once by
// the lane that voted, closed by the lane when it settles without one.
//
// IT ALSO CARRIES THE SHADOW'S OWN INPUTS, copied when the lane starts: the
// chain side and the witness switch. An abandoned shadow outlives the call that
// started it, and reading a package variable from it would race with whatever
// the next call (or test) writes there; startShadow's rule is that the
// goroutine reads copies, and these are the copies.
type ballotBox struct {
	once  sync.Once
	done  chan struct{}
	votes map[string]checks.Verdict
	// chains answers the comparator's vector: every atom of `only` by its chain.
	chains chainSide
	// witness says whether the chain's fleet:witness runs in the comparator.
	witness bool
}

// chainSide is how the comparator asks the chains for a vector (shadowToday).
type chainSide func(ctx context.Context, m *FoundryTools, stage, only, base string) ([]checks.Verdict, error)

func newBallotBox(chains chainSide, witness bool) *ballotBox {
	return &ballotBox{done: make(chan struct{}), chains: chains, witness: witness}
}

// put files the votes; the first filing stands. A nil box files nothing.
func (b *ballotBox) put(votes map[string]checks.Verdict) {
	if b == nil {
		return
	}
	b.once.Do(func() {
		b.votes = votes
		close(b.done)
	})
}

// wait answers the votes, or why there are none: the context ended, or the lane
// settled without casting any.
func (b *ballotBox) wait(ctx context.Context) (map[string]checks.Verdict, error) {
	if b == nil {
		return nil, fmt.Errorf("no ballot box: the lane is not voting with the binary")
	}
	select {
	case <-b.done:
	case <-ctx.Done():
		return nil, fmt.Errorf("no vote was cast before the shadow's deadline: %w", ctx.Err())
	}
	if b.votes == nil {
		return nil, fmt.Errorf("the lane settled without casting a vote")
	}
	return b.votes, nil
}

// THE REVERSE SHADOW: the chains of the atoms the binary registers, run beside
// the vote and held against it. It is startShadow's shadow with the roles
// swapped, so it inherits what makes that one unable to vote: it starts before
// grading, returns text and no error, writes stderr only, recovers a panic into
// a line of the report and is abandoned at most shadowGrace after the record
// settles. Nothing in this file is read by a gate except the votes themselves.

// reverseShadowWitness says whether the chain's fleet:witness runs in the
// reverse shadow (copied into the lane's ballot box when it starts). It does not: the voter asks narcissus for real, and the
// chain's witness asks for real too, so running both doubles the load on a
// service that saturated on 2026-10-07. The report names the atom as not compared.
var reverseShadowWitness = false

// reverseIDs are the atoms the reverse shadow runs the chains of: the stage's
// binary atoms, less fleet:witness unless withWitness. skipped says whether the
// witness was left out.
func reverseIDs(stage string, withWitness bool) (ids []string, skipped bool) {
	for _, id := range atoms.StageIDs(stage) {
		if id == "fleet:witness" && !withWitness {
			skipped = true
			continue
		}
		ids = append(ids, id)
	}
	return ids, skipped
}

// reverseShadow runs the chains of the stage's binary atoms and holds them
// against the verdicts the lane voted. NON-VOTING: it returns text.
func (m *FoundryTools) reverseShadow(ctx context.Context, stage, base string) string {
	if m.box == nil || m.box.chains == nil {
		return "shadow atoms (binary voted): not compared - the lane gave the shadow no chain side to ask"
	}
	ids, skipped := reverseIDs(stage, m.box.witness)
	if len(ids) == 0 {
		return fmt.Sprintf("shadow atoms (binary voted): nothing to compare - the binary carries no atom of the stage %q", stage)
	}
	started := time.Now()
	chains, chainsErr := m.box.chains(ctx, m, stage, strings.Join(ids, ","), base)
	votes, votesErr := m.box.wait(ctx)
	return renderReverse(ids, skipped, chains, chainsErr, votes, votesErr, time.Since(started))
}

// renderReverse is the report from the two sides' answers. It is pure so the
// ways either side can fail are tested without an engine.
func renderReverse(ids []string, skipped bool, chains []checks.Verdict, chainsErr error, votes map[string]checks.Verdict, votesErr error, elapsed time.Duration) string {
	const head = "shadow atoms (binary voted): not compared - "
	if chainsErr != nil {
		return head + "the chains did not answer: " + chainsErr.Error()
	}
	if votesErr != nil {
		return head + "the binary's vote is missing: " + votesErr.Error()
	}
	var voted []checks.Verdict
	for _, id := range ids {
		if v, ok := votes[id]; ok {
			voted = append(voted, v)
		}
	}
	rep := atoms.Compare(chains, voted)
	rep.Voter, rep.Elapsed = string(voterBinary), elapsed
	out := rep.Render()
	if skipped {
		out += "\nfleet:witness: not compared - the binary asked narcissus for real and the chain was not run, so it is asked once\n"
	}
	return out
}

// defaultShadow is the shadow a gate or orbit lane starts, by who votes: the
// reverse shadow when the binary does (startShadow gave the lane a ballot box),
// the binary's dry run beside the chains when the chains do.
func defaultShadow(ctx context.Context, m *FoundryTools, stage, base string) string {
	if m.box != nil {
		return m.reverseShadow(ctx, stage, base)
	}
	return m.shadowAtoms(ctx, base, stage, voterChains)
}
