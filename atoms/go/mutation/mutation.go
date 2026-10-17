// Package gomutation is the atom go:mutation: every mutant gomutants makes of
// the Go this pull changed is killed by the tests. It is the mutation lane's Go
// atom; checks/gomutation*.go carries the judgement (what a score means), this
// package asks the questions of the tree and runs the tools.
package gomutation

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. NeedsDies FOR THE SAME REASON go:test-race CARRIES IT:
// gomutants gathers coverage by running `go test`, and an atom that runs a
// repo's `go test` runs its armed goldens (hephaestus' internal/slag refuse in a
// container with no /dies: "failed to gather coverage: exit status 1", measured
// 2026-09-11, hephaestus #53). It judges the change, so its environment hands it
// the base, and it needs the servers the tree's suites name (the union over the
// modules, DEF-B3-046) and the two tools it drives.
//
// GO NEEDS NO critical_modules. The python, rust and ts mutation atoms read that
// declaration off .copier-answers.yml; gomutants scopes to the diff itself.
//
// THE DEADLINE IS THE LANE'S: the mutation Job runs against 75 minutes and ends
// the pod there; the atom's own is the same, so the Job's cause (deadline, which
// the door's cap counts) is the one that is read.
var Def = atomkit.Def{
	ID: "go:mutation", Stage: checks.StageMutation, Lane: checks.LaneGo,
	Group:      checks.GroupLanguage,
	Desc:       "Every mutant gremlins makes of this pull's changed Go is killed by the tests.",
	Surface:    "any:**/go.mod",
	Toolchain:  "go",
	Needs:      []string{"dies", "service:postgres", "service:pgvector", "service:redpanda", "tool:gomutants", "tool:mutation-gate"},
	NotOnForks: checks.ForkChangeBar,
	History:    "full",
	Timeout:    75 * time.Minute,
	Check:      check,
}

const id = "go:mutation"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StageMutation, Lane: checks.LaneGo}

// mutationDBLane names the mutation gate's own test-server binding. Any
// non-empty word works; this one reads in a service alias (db-mutation) and in
// the DSN the suite is handed. The environment binds the servers under it.
const mutationDBLane = "mutation"

// noBase is the stand-down for a run with no change set.
const noBase = "no usable PR base sha — the diff-scoped gate did not run"

// check grades every module the tree carries, one after another, and folds the
// answers. A module that could not run is asked once more, on its own.
//
// THE WHOLE-ATOM RE-ASK IS THE WRONG GRAIN FOR A MUTATION RUN. A mutation module
// that graded is the costliest answer in the fleet to throw away: could-not-run
// runs were 44% of the mutation lane's wall time (tesla38-011). So the module
// that could not run is asked again, with the lookup it was given, and the ones
// that graded are kept (atom_adapter.go's reasksItsOwnModules says the same of
// the Dagger runner).
//
// AN OVER-BUDGET TIMEOUT IS NOT ASKED TWICE (foundry-tools#16081, Rob's ruling).
// It is a deterministic result of the tree and the lane's config, so a second
// ask for the same commit is the same answer. The chain also kept the verdict in
// a memo keyed on the commit, for a later ask in the same process; an atom's
// process is one ask, so only the skipped re-ask remains.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	g := newRun(in)
	return atoms.EachGoModule(in, row, func(dir string) checks.Verdict {
		first := g.ask(ctx, dir)
		if checks.GoMutationOverBudget(first.State, first.Reason) {
			return first
		}
		return reasked(first, func() checks.Verdict { return g.ask(ctx, dir) })
	})
}

// reasked settles a could-not-run's second ask: the second answer is the one
// reported, and a second could-not-run says it was asked twice.
func reasked(first checks.Verdict, again func() checks.Verdict) checks.Verdict {
	if first.State != int(checks.StateCannotRun) {
		return first
	}
	v := again()
	if v.State != int(checks.StateCannotRun) {
		return v
	}
	v.Reason += "\n(asked twice, the second time past the engine's cache: it could not run both times)"
	return v
}

// session is what one ask of the atom holds across its modules: the input, and
// how it asks the store what is already graded.
type session struct {
	in atomkit.In
	// lookup is nil when the lane does not reuse gradings: every unit is graded.
	lookup gradingLookup
	// audit says this reuse run is one of the sampled few that grade cold anyway
	// and audit what the lookup would have answered.
	audit bool
}

// newRun arms the lookup when the run was handed the credential for it. The
// credential is taken out of the environment here, before any tool is started.
func newRun(in atomkit.In) *session {
	g := &session{in: in}
	token := atoms.TakeToken()
	if token == "" {
		return g
	}
	g.lookup = lookupVia(in.Origin, token)
	g.audit = auditSampled(commit(in))
	return g
}

// commit is the commit under grade: the run's ATOM_SHA, else the tree's HEAD.
func commit(in atomkit.In) string {
	if sha := os.Getenv(atoms.EnvSha); sha != "" {
		return sha
	}
	out, code := atoms.Git(context.Background(), in.Root, "rev-parse", "HEAD")
	if code != 0 {
		return ""
	}
	return strings.TrimSpace(out)
}

// module is go:mutation in one module: the working state of one ask.
type module struct {
	*session
	dir, cwd string
	// work is where this module's report, cache and controls are written.
	work string
	// scope is the tree's server reads, which ride every verdict of the module.
	scope string
}

func (g *session) ask(ctx context.Context, dir string) checks.Verdict {
	m := &module{session: g, dir: dir, cwd: atoms.GoModuleRoot(g.in, dir)}
	m.work = filepath.Join(atoms.WorkDir(), "go", slug(dir))
	return m.grade(ctx)
}

// slug names a module directory as one path element.
func slug(dir string) string {
	if dir == "." {
		return "root"
	}
	return strings.ReplaceAll(dir, "/", "_")
}

// settle files a verdict: the scope line first, then the reason. The scope line
// is set on the verdict, not folded into the output: a pass keeps no output
// (checks.VerdictOf), and the line is printed either way.
//
// A RUN THAT GRADED NOTHING RIDES BESIDE IT, for the same reason and only for
// that case. It settles 0, so its reason would be discarded exactly where it
// matters: a green that verified nothing, printed as an unqualified pass. The
// reason's first line is the headline (GoMutationVerdict builds it that way) and
// checks.GoMutationNothingGraded is the one string both ends agree on. A
// PARTIALLY ungraded run is not lifted - its viable mutants were measured, and
// the exclusion is in the summary the logs keep.
func (m *module) settle(state int, reason string) checks.Verdict {
	v := checks.VerdictOf(row, state, id+": "+reason)
	note := m.scope
	if state == 0 && strings.HasPrefix(reason, checks.GoMutationNothingGraded) {
		note += "\n" + id + ": " + strings.SplitN(reason, "\n", 2)[0]
	}
	v.Reason = note + "\n" + v.Reason
	return v
}

// neverRan is the verdict for a program that did not run: the machine would not
// start it, or it ended in the signal range the engine refuses to read an exit
// from (atoms.ToolRan).
func (m *module) neverRan(c atoms.Cmd, code int, out string) checks.Verdict {
	return m.settle(2, "CANNOT RUN - "+atoms.NeverRanReason(append([]string{c.Name}, c.Args...), code, out))
}

// grade is the atom in one module. The change set is known before anything is
// provisioned: the base, the merge base and the diff are git questions about the
// tree; the module download is only for a run that will mutate something.
// MEASURED 2026-10-09: 20 of 107 mutation runs graded nothing and still paid
// ~110 CPU-s each for a Postgres, a broker and a `go mod download` they never
// used.
func (m *module) grade(ctx context.Context) checks.Verdict {
	// THE SERVERS' SCOPE LINE is read here and rides every verdict, an empty
	// change set's included. The servers themselves are the environment's.
	dbs, brokers, scope := atoms.GoTestServers(ctx, m.in, m.cwd)
	m.scope = scope
	if m.in.Base == "" {
		return m.settle(0, noBase)
	}
	// The change set starts at the merge base, not at the base the door named:
	// main's tip moves under an open pull.
	since, err := atoms.ChangeBase(ctx, m.in, m.cwd)
	if err != nil {
		return m.settle(2, "CANNOT RUN - "+err.Error())
	}
	if since == "" {
		return m.settle(atoms.MissingBase(m.in, noBase))
	}
	// AN EMPTY DIFF MEANS "MUTATE EVERYTHING" TO GOMUTANTS, written for "no
	// --diff was given". A pull that changes no Go produces exactly that, so it
	// stands down here instead of mutating the whole module.
	diff := atoms.Cmd{Dir: m.cwd, Name: "git", Args: []string{"diff", "--relative", since, "--name-only", "--", "*.go"}}
	changed, code := m.run(ctx, diff)
	if !atoms.ToolRan(code) {
		return m.neverRan(diff, code, changed)
	}
	if code != 0 {
		return m.settle(2, "CANNOT RUN - git could not diff the pull against its base "+since+": "+changed)
	}
	if strings.TrimSpace(changed) == "" {
		return m.settle(0, "this pull changes no Go file — nothing to mutate")
	}
	// SOMETHING WILL BE MUTATED: download the module.
	if out, code := m.run(ctx, atoms.Cmd{Dir: m.cwd, Name: "go", Args: []string{"mod", "download"}}); code != 0 {
		return m.settle(2, fmt.Sprintf("CANNOT RUN - preflight `go mod download` exited %d: %s", code, atoms.LastLine(out)))
	}
	var tags []string
	if len(dbs) > 0 || len(brokers) > 0 {
		tags = []string{"-tags", checks.BuildTags(dbs, brokers)}
	}
	return m.measure(ctx, since, changed, tags)
}

// run is the atom's one way to start a program, through the input's seam.
func (m *module) run(ctx context.Context, c atoms.Cmd) (string, int) {
	return atoms.RunCmd(ctx, m.in, c)
}
