package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE SHADOW OF THE ATOMS BINARY. internal/atoms runs the cheap fleet atoms
// in one process. Before F5a CI ran it BESIDE the chains over the same tree and
// read whether the two agreed, and this file is that run, built so that it
// CANNOT vote; from F5a the binary votes (atoms_vote.go) and this run is what the
// ROLLBACK restores (voterChains), the chains voting and the binary dry beside
// them. Either way the shadow is built the same:
//
//   - It is its own function, not a stage. gate, gate-file, Verdicts and Push
//     never read it, so no record, vector or exit a door reads contains it.
//   - It RETURNS A STRING, with no error: a chain that would not evaluate, a
//     binary that would not build, an output that will not parse are all lines
//     of the report, and the function still answers. The caller prints it.
//   - It reuses the lane's chains for the chain side (m.chainVector, selected by
//     id), so the engine's cache serves them when the gate ran the same tree.

// atomsSourceInclude is the part of this module the atoms binary is built
// from, and nothing else. A build that mounted the module whole would key its
// cache on every file in it, so an edit to a README would rebuild the binary
// (the helper CLIs did, until helpers.go filtered them the same way). This
// binary takes a real dependency (the YAML parser), so it is built in
// goToolchain() with the Go cache volumes mounted, from go.mod, go.sum and the
// packages it imports. TestAtomsSourceCoversImports holds this list to the
// import closure: a package the binary imports and this list omits is a build
// that fails in the engine and nowhere a test can see.
var atomsSourceInclude = []string{
	"go.mod", "go.sum",
	"atoms/**",
	"internal/atoms/**",
	"internal/checks/**",
	"internal/execmem/**",
	"internal/unitkey/**",
	"internal/orbitcompose/**",
	"internal/orbitlane/**",
	"internal/retiredverbs/**",
}

// atomsBinPath is where the binary sits in the fleet lane's container.
const atomsBinPath = "/usr/local/bin/atoms"

// atomsBinary builds ./atoms from the filtered source. Tests are excluded: they
// are not compiled into the binary, and a test edit must not rebuild it.
func atomsBinary() *dagger.File {
	src := dag.CurrentModule().Source().Filter(dagger.DirectoryFilterOpts{
		Include: atomsSourceInclude,
		Exclude: []string{"**/*_test.go"},
	})
	return goToolchain().
		WithMountedDirectory("/src", src).
		WithWorkdir("/src").
		WithExec([]string{"go", "build", "-trimpath", "-o", "/out/atoms", "./atoms"}).
		File("/out/atoms")
}

// ShadowAtoms runs the in-process atoms of a stage beside the same atoms as
// Dagger chains and reports whether they agree. NON-VOTING: it returns text and
// nothing that a gate reads, and it answers even when either side could not run.
//
// THE STAGE SELECTS BOTH SIDES. The atoms of a stage are the catalogue's own
// answer (atoms.StageIDs, checks.AtomsForStage): the pull path's gate grades
// precommit and prepush together, the orbit lane grades the orbit atoms and
// nothing else. The chains are asked for exactly those ids at that stage, and
// the binary is run with the same -stage, so an orbit lane's report compares the
// orbit lane's atoms, and the gate's never carries an atom a lane of its own
// grades.
//
// +cache="never"
func (m *FoundryTools) ShadowAtoms(
	ctx context.Context,
	// The change set's base, as Verdicts takes it. Empty reads HEAD against
	// its parent.
	// +optional
	base string,
	// The stage whose atoms are compared, as Verdicts takes it. Empty is the
	// pull path.
	// +optional
	stage string,
) string {
	return m.shadowAtoms(ctx, base, stage, "")
}

// shadowAtoms is ShadowAtoms with the side that voted named in the report; empty
// names none, as the public verb does (it votes on nothing). The lane's shadow
// under a chain-voted rollback names the chains.
func (m *FoundryTools) shadowAtoms(ctx context.Context, base, stage string, voted voter) string {
	ids := atoms.StageIDs(stage)
	if len(ids) == 0 {
		return fmt.Sprintf("shadow atoms: nothing to compare - the binary carries no atom of the stage %q", stage)
	}
	started := time.Now()
	today, todayErr := shadowToday(ctx, m, stage, strings.Join(ids, ","), base)
	raw, rawErr := shadowBinary(ctx, m, stage, base)
	return renderShadowAs(voted, today, todayErr, raw, rawErr, time.Since(started))
}

// The two sides of the comparison, as variables so a test names what each
// answers without an engine.
var (
	shadowToday = func(ctx context.Context, m *FoundryTools, stage, only, base string) ([]checks.Verdict, error) {
		return m.chainVector(ctx, stage, only, base)
	}
	shadowBinary = func(ctx context.Context, m *FoundryTools, stage, base string) (string, error) {
		return m.atomsVector(ctx, stage, base)
	}
)

// atomsNeedingDies are the atoms that grade against foundry-dies main.
var atomsNeedingDies = []string{"ops:orbit-composed", "orbit:contracts", "orbit:repo", "orbit:sidecars", "orbit:surface"}

// reaskNonce keys the binary's exec afresh on every call; a variable so a test
// can read what each call carried.
var reaskNonce = func() string { return strconv.FormatInt(time.Now().UnixNano(), 10) }

// atomsDiesPath is where the binary finds foundry-dies when an atom reads it.
const atomsDiesPath = "/dies"

// withAtomDies mounts, for the atoms of a stage, the one input that is not the
// tree and not a tool: foundry-dies at main (the contracts). The tools the
// atoms exec are layers of the tools container (atoms_tools.go), so a stage
// mounts nothing else.
//
// foundry-dies is mounted only when a selected atom reads it, and only if it
// fetched: listing the root first forces the clone, so a clone that fails is the
// dies atoms' 2 and not the whole binary's "not compared". Without the flag the
// atoms that need it say the checkout was not supplied.
func (r *run) withAtomDies(ctx context.Context, ctr *dagger.Container, stage string) (*dagger.Container, []string) {
	has := map[string]bool{}
	for _, id := range atoms.StageIDs(stage) {
		has[id] = true
	}
	if slices.ContainsFunc(atomsNeedingDies, func(id string) bool { return has[id] }) {
		if _, err := r.dies.Entries(ctx); err == nil {
			return ctr.WithMountedDirectory(atomsDiesPath, r.dies), []string{"-dies", atomsDiesPath}
		}
	}
	return ctr, nil
}

// atomsSpirePath is where the lane pod's SPIRE socket sits in the binary's
// container when the call forwarded one.
const atomsSpirePath = "/run/spire/agent.sock"

// stageHas reports whether the binary grades an atom at the stage.
func stageHas(stage, id string) bool { return slices.Contains(atoms.StageIDs(stage), id) }

// withAtomSpire (the voter's: the dry shadow is mounted none) forwards the lane pod's SPIRE socket into the binary's
// container for the stage that carries fleet:witness, so the binary asks
// narcissus as the same SVID the chain does (through the witnesscall layer).
// Without a socket (a local run, a lane that forwarded none) nothing is mounted,
// the flag is not passed and the atom asks in the clear and says so. The owner is
// root, the tools container's user: the chain's nonroot owner belonged to the
// static base its helper ran on.
func (r *run) withAtomSpire(ctr *dagger.Container, stage string) (*dagger.Container, []string) {
	if r.spire == nil || !stageHas(stage, "fleet:witness") {
		return ctr, nil
	}
	return ctr.WithUnixSocket(atomsSpirePath, r.spire), []string{"-spire", atomsSpirePath}
}

// withAtomNarc mounts narcissus's analyzer for the stage that carries
// orbit:surface: the image flux pins live (narcissusRef), resolved per call and
// mounted on top of the layers, so a bump of that pin rebuilds nothing under it.
// A pin that cannot be read is passed to the binary as the reason (-narc-err),
// so the atom settles 2 in the chain's words.
func (r *run) withAtomNarc(ctx context.Context, ctr *dagger.Container, stage string) (*dagger.Container, []string) {
	if !stageHas(stage, "orbit:surface") {
		return ctr, nil
	}
	ref, err := narcissusRef(ctx, r)
	if err != nil {
		return ctr, []string{"-narc-err", err.Error()}
	}
	return ctr.WithFile("/usr/local/bin/narc", dag.Container().From(ref).File("/narc"), dagger.ContainerWithFileOpts{Permissions: 0o755}), nil
}

// atomsVector is the binary's DRY run: fleet:witness lists what it would ask and
// asks nothing, no socket is mounted. It is the non-voting shadow's side when the
// chains vote. See atomsRun.
func (m *FoundryTools) atomsVector(ctx context.Context, stage, base string) (string, error) {
	return m.atomsRun(ctx, stage, base, false)
}

// atomsBallot is the binary's VOTING run: fleet:witness makes its real asks,
// and the lane's SPIRE socket is mounted for the stage that carries it. See atomsRun.
func (m *FoundryTools) atomsBallot(ctx context.Context, stage, base string) (string, error) {
	return m.atomsRun(ctx, stage, base, true)
}

// atomsRun runs the binary in the tools container over the same tree the
// chains read, and answers its stdout: the vector as JSON. THE ONLY PLACE THE
// BINARY RUNS, and it runs in the tools container: nothing in the module execs
// it on a host, so the local hook (`dagger call check`) and the door grade with
// the same binary built from the same layers.
//
// THE TREE AND THE HISTORY ARE THE CHAINS' OWN (r.gitReady, r.withBase), so the
// binary sees what the chains see, including a linked worktree's rebuilt
// repository and the base fetched by sha; only the container differs. A
// non-zero exit is an error here, not a vector: the binary exits 0 whatever its
// atoms found.
func (m *FoundryTools) atomsRun(ctx context.Context, stage, base string, voting bool) (string, error) {
	// REASKED, ALWAYS. The binary's exec has identical inputs on every run of the
	// same tree, so the engine would serve a cached answer, and the atoms here
	// that read the network (the door, opa's build, kubeconform's schemas) would
	// be frozen at the first read. The chains read the door fresh and re-ask a
	// could-not-run; CA_REASK keys every lane exec afresh, so the binary does too.
	r := newRun(m.Source, m.Repo, base).fromOrigin(m.Origin).withSpire(m.spire).reasked(reaskNonce())
	// THE BASE IS FETCHED ONCE HERE (withBase), for the whole run: the binary's
	// change set (Collect) is computed from it once and feeds every atom that
	// judges the change, fleet:witness and ops:immutable among them. Neither
	// fetches it again; TestAtomsVectorFetchesTheBaseOnce holds the count.
	ctr := r.gitReady(ctx, r.withBase(r.onTools(atomsTools(ctx))))
	ctr, flags := r.withAtomDies(ctx, ctr, stage)
	switch {
	case voting:
		// A VOTING WITNESS ASKS FOR REAL, as the SVID the lane's socket issues.
		var spire []string
		ctr, spire = r.withAtomSpire(ctr, stage)
		flags = append(flags, spire...)
	case stageHas(stage, "fleet:witness"):
		// THE SHADOW'S WITNESS IS DRY: it lists what it would ask and asks
		// nothing, so no socket is mounted for it either. A real ask would double
		// the load on narcissus for every pull that touches .go/.py, and
		// narcissus saturated on 2026-10-07.
		flags = append(flags, "-witness-dry")
	}
	ctr, narcFlags := r.withAtomNarc(ctx, ctr, stage)
	flags = append(flags, narcFlags...)
	// -timeout: the tool atoms scan, build and lint whole trees, which the
	// default minute (sized for atoms that read a few files) does not allow.
	// fleet:witness sizes its own deadline from the change (Atom.Deadline).
	args := append([]string{atomsBinPath, "-root", "/src", "-base", base, "-origin", r.repo, "-stage", stage, "-timeout", atomTimeout}, flags...)
	out, code, err := output(ctx, ctr.WithExec(args, anyExit))
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("the atoms binary exited %d: %.300s", code, out)
	}
	return out, nil
}

// atomTimeout is each atom's deadline in the shadow, inside the shadow's own
// five minutes (shadowTimeout).
const atomTimeout = "4m"

// renderShadow is the report from the two sides' answers. It is pure so the
// ways either side can fail are tested without an engine.
func renderShadow(today []checks.Verdict, todayErr error, raw string, rawErr error, elapsed time.Duration) string {
	return renderShadowAs("", today, todayErr, raw, rawErr, elapsed)
}

// renderShadowAs is renderShadow naming the side that voted ("" names none).
func renderShadowAs(voted voter, today []checks.Verdict, todayErr error, raw string, rawErr error, elapsed time.Duration) string {
	if todayErr != nil {
		return "shadow atoms: not compared - the chains did not answer: " + todayErr.Error()
	}
	if rawErr != nil {
		return "shadow atoms: not compared - the binary did not answer: " + rawErr.Error()
	}
	shadow, err := checks.ParseVector(raw)
	if err != nil {
		return "shadow atoms: not compared - " + err.Error()
	}
	rep := atoms.Compare(today, shadow)
	rep.Elapsed, rep.Voter = elapsed, string(voted)
	return rep.Render()
}

// THE SHADOW'S PACKAGE STATE. Each variable below is a seam the tests replace
// (atoms_shadow_test.go's init sets shadowRun and shadowOut to inert values so
// no gate test runs the real shadow by accident; the shadow tests set their
// own and restore them). startShadow COPIES every one of them before it starts
// a goroutine, because an abandoned shadow outlives the call that started it
// and must never read a variable the next call, or the next test, is writing.

// shadowLanes are the lanes that start a shadow, by laneOf. Each compares the
// atoms of the stage it grades (ShadowAtoms), and an orbit lane's gate-file run
// goes through the same startShadow as the gate's, non-voting in the same way.
var shadowLanes = map[string]bool{"gate": true, "orbit": true}

// shadowTimeout bounds the shadow run's own context, apart from the gate's.
var shadowTimeout = 5 * time.Minute

// shadowGrace is how long the gate waits for the shadow AFTER its own record is
// settled (and posted). The shadow starts BEFORE the gate grades, so on a
// normal run it has had the gate's whole duration; the grace is only the tail.
// Past it the shadow is abandoned and the gate returns. Latency is a vote: a
// gate that waits for a non-voting check has let it decide when the record
// lands, and GateFile's volume fallback for a failed post must not sit behind it.
var shadowGrace = 30 * time.Second

// shadowRun is the shadow itself (defaultShadow: the reverse shadow while the
// binary votes, the dry shadow once the chains do), so the tests can make it hang,
// panic or answer garbage and prove the gate does not notice.
var shadowRun = defaultShadow

// shadowOut is where the report goes: stderr. Never the record, never the exit.
var shadowOut = func() io.Writer { return os.Stderr }

// shadowHandle is a started shadow. A nil handle (a lane with no shadow) is
// valid and finish on it does nothing.
type shadowHandle struct {
	done   chan string
	cancel context.CancelFunc
	grace  time.Duration
	out    io.Writer
	// box is the reverse shadow's ballot box; nil when the chains vote.
	box *ballotBox
}

// startShadow starts the shadow in a goroutine and answers at once. IT CANNOT
// VOTE: it returns no state, runs under its own deadline, recovers a panic into
// a line of the report, and only the gate and orbit lanes have one (shadowLanes;
// the mutation and visual lanes have no atoms in the binary). Call it before
// grading, so the shadow overlaps the gate instead of following it, and call
// finish after the record is settled. When the binary votes it also gives the
// lane a ballot box, which is how the shadow reads the votes without running
// the binary a second time.
func (m *FoundryTools) startShadow(ctx context.Context, stage, base string) *shadowHandle {
	if !shadowLanes[laneOf(stage)] {
		return nil
	}
	// Every package variable the goroutine would read, copied HERE.
	run, timeout, voting := shadowRun, shadowTimeout, atomsVoter == voterBinary
	h := &shadowHandle{done: make(chan string, 1), grace: shadowGrace, out: shadowOut()}
	if voting {
		// The vote reaches the shadow through the box, set BEFORE the goroutine
		// starts and before the lane grades.
		h.box = newBallotBox(shadowToday, reverseShadowWitness)
		m.box = h.box
	}
	ctx, h.cancel = context.WithTimeout(ctx, timeout)
	// Buffered: an abandoned shadow can still return and its goroutine end.
	go func() {
		defer func() {
			if p := recover(); p != nil {
				h.done <- fmt.Sprintf("shadow atoms: not compared - the shadow panicked: %v", p)
			}
		}()
		h.done <- run(ctx, m, stage, base)
	}()
	return h
}

// finish waits at most the grace for the report, prints it to stderr, and
// abandons the shadow if it has not answered. The goroutine never writes to
// out: only the caller does, so an abandoned shadow cannot print later.
func (h *shadowHandle) finish() {
	if h == nil {
		return
	}
	defer h.cancel()
	// The record is settled: a vote that has not been cast now never will be, and
	// the shadow stops waiting for it instead of sitting out the grace.
	h.box.put(nil)
	timer := time.NewTimer(h.grace)
	defer timer.Stop()
	select {
	case report := <-h.done:
		fmt.Fprintln(h.out, report)
	case <-timer.C:
		fmt.Fprintf(h.out, "shadow atoms: not compared - no answer within the %s grace after the gate settled\n", h.grace)
	}
}
