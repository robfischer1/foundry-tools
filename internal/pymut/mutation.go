// Package pymut is the atom python:mutation: every mutant cosmic-ray
// makes of this pull's changes to the declared critical modules is killed by the
// tests. checks/pythonmutation.go carries the judgement; this package drives
// cosmic-ray and forge-testkit-mutation.
//
// THE MEASUREMENT IS PLAIN EXECS, SETTLED IN GO. foundry-stocks'
// ci/lib/mutation/python.sh ran as seven bash phases; git, uv, cosmic-ray and
// forge-testkit-mutation run as their own programs and checks.PythonReportVerdict
// reads the report.
//
// A WORKER IS A COPY OF THE TREE. cosmic-ray writes each mutant onto the disk, so
// two workers cannot share a tree, and a worker whose editable install pointed at
// another tree would import the unmutated source and every mutant it ran would
// survive. The chain gave each worker a container filesystem, which one atom's
// process cannot ask for; here each worker is its own copy of the tree under the
// atom's work directory, synced its own environment, run concurrently.
package pymut

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

const id = "python:mutation"

// ID is the atom's identity, for the declaration in atoms/python/mutation.
const ID = id

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StageMutation, Lane: checks.LanePython, NeedsManifest: true}

// noBase is the stand-down for a run with no change set.
const noBase = "no usable PR base sha — the diff-scoped mutation gate did not run"

const (
	// pythonMutationConfig is cosmic-ray's config, at the checkout root where its
	// relative module-path resolves.
	pythonMutationConfig = "cosmic-ray.toml"
	session              = "session.sqlite"
)

// The disk, as the atom touches it: variables so a test can say a write fails.
var (
	removeAll = os.RemoveAll
	copyTree  = atoms.CopyTree
	writeFile = os.WriteFile
	readFile  = os.ReadFile
	mkdirAll  = os.MkdirAll
)

// grading is one ask of the atom.
type grading struct {
	in    atomkit.In
	scope string
	work  string
	// tree is the primary copy: init, scope, plan and the final report are asked
	// there. The workers are copies of it.
	tree string
	// carried is the nightly's carried ranges (atoms.ReadCarried); nil is none.
	carried *atoms.Carried
}

// settle files a verdict, its scope line first.
func (g *grading) settle(state int, reason string) checks.Verdict {
	v := checks.VerdictOf(row, state, id+": "+reason)
	v.Reason = g.scope + "\n" + v.Reason
	return v
}

// step is one program's answer: everything it printed and its exit code.
type step struct {
	out  string
	code int
	cmd  atoms.Cmd
}

// do runs one program, stdout then stderr, with the tool's own exit code.
func (g *grading) do(ctx context.Context, dir string, env []string, name string, args ...string) step {
	c := atoms.Cmd{Dir: dir, Name: name, Args: args, Env: env, Both: true}
	out, code := atoms.RunCmd(ctx, g.in, c)
	return step{out: out, code: code, cmd: c}
}

// ran says the program ran: it started and did not end in the signal range.
func (s step) ran() bool { return atoms.ToolRan(s.code) }

// neverRan is the verdict for a program that did not run.
func (g *grading) neverRan(s step) checks.Verdict {
	return g.settle(2, "CANNOT RUN - "+atoms.NeverRanReason(append([]string{s.cmd.Name}, s.cmd.Args...), s.code, s.out))
}

// cannot is a step that exited non-zero, said as the chain said it.
func (g *grading) cannot(what string, s step) checks.Verdict {
	return g.settle(2, fmt.Sprintf("CANNOT RUN - %s (exit %d)\n%s", what, s.code, lastLines(s.out, 20)))
}

// lastLines is the last n lines of s.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return strings.Join(lines[max(0, len(lines)-n):], "\n")
}

// Check measures the declared critical modules.
func Check(ctx context.Context, in atomkit.In) checks.Verdict {
	// Absent answers file, absent declaration: "" is the whole diff.
	answers, _ := atoms.ReadIfPresent(in.Root, ".copier-answers.yml")
	mods := checks.CriticalModules(answers)
	g := &grading{in: in, scope: checks.MutationScope(id, mods), work: filepath.Join(atoms.WorkDir(), "python")}
	v := g.grade(ctx, strings.Fields(mods))
	return g.carried.Into(v)
}

func (g *grading) grade(ctx context.Context, declared []string) checks.Verdict {
	in := g.in
	if in.Base == "" {
		return g.settle(0, noBase)
	}
	// Provisioning, under the default Expect: an image without uv or prlimit is
	// the atom's could-not-run.
	for _, tool := range [][]string{{"uv", "--version"}, {"prlimit", "--version"}} {
		if s := g.do(ctx, in.Root, nil, tool[0], tool[1:]...); s.code != 0 {
			return g.neverRan(s)
		}
	}
	// The change set starts at the merge base, not at the base the door named:
	// main's tip moves under an open pull.
	since, err := atoms.ChangeBase(ctx, in, in.Root)
	if err != nil {
		return g.settle(2, "CANNOT RUN - "+err.Error())
	}
	if since == "" {
		return g.settle(atoms.MissingBase(in, noBase))
	}
	if g.carried, err = atoms.ReadCarried(ctx, in, since, id, func(f string) bool { return strings.HasSuffix(f, ".py") }); err != nil {
		return g.settle(2, "CANNOT RUN - the carried ranges: "+err.Error())
	}
	// A TEST-ONLY CHANGE IS GRADED BY WHAT ITS TESTS RUN (covered.go): the
	// source lines join the carried ones, so a pull that changed no source still
	// has a scope, and they ride every verdict's scope line from here.
	covered, note, failed := g.testCovered(ctx, since, declared)
	if failed != nil {
		return *failed
	}
	g.carried = g.carried.Plus(covered)
	if note != "" {
		g.scope += "\n" + note
	}

	// RESOLVE: the declared modules, or every python source the pull added or
	// changed outside the tests; less the generated ones.
	modules := declared
	if len(modules) == 0 {
		s := g.do(ctx, in.Root, nil, "git", append([]string{"diff", "--name-only", "--diff-filter=AM", since, "HEAD", "--"}, checks.PythonWholeDiffSpecs...)...)
		if !s.ran() {
			return g.neverRan(s)
		}
		if s.code != 0 {
			return g.settle(2, "CANNOT RUN - git could not list the python the pull changed: "+strings.TrimSpace(s.out))
		}
		modules = strings.Fields(s.out)
		// A night's carried survivors are in scope though nothing new touched them.
		for _, f := range g.carried.Files() {
			if !slices.Contains(modules, f) {
				modules = append(modules, f)
			}
		}
	}
	var kept []string
	for _, m := range modules {
		// A directory or an absent path does not read, and is kept.
		if src, err := readFile(filepath.Join(in.Root, m)); err != nil || !checks.PythonGenerated(string(src)) {
			kept = append(kept, m)
		}
	}
	if len(kept) == 0 {
		return g.settle(0, "no hand-written python in scope — nothing to mutate")
	}
	// The whole tree, with renames paired, narrowed to the kept modules in Go: a
	// pathspec of the kept modules alone cannot see a file's old path.
	s := g.do(ctx, in.Root, nil, "git", "diff", "-M", "--unified=0", since, "HEAD")
	if !s.ran() {
		return g.neverRan(s)
	}
	if s.code != 0 {
		return g.settle(2, "CANNOT RUN - git could not diff the pull against its base "+since+": "+strings.TrimSpace(s.out))
	}
	diff := g.carried.WithCarried(checks.PythonDiffFor(s.out, kept), in.Root)
	// A PURE-DELETION PULL touches the module and adds no mutable line, and scope
	// leaves the session intact for it: exec would run every site init enumerated
	// (oceanus: a two-line deletion became a 43-minute gate).
	if !checks.DiffAddsLines(diff) {
		return g.settle(0, "this pull added no line to the critical modules — nothing to mutate")
	}
	return g.measure(ctx, since, diff, kept)
}

// scopeRefusal is the text forge-testkit's scope verb prints when the diff's
// paths appear in none of the session's mutation sites (mutation.py
// scope_session).
const scopeRefusal = "scope matched none of"

// testkit is one forge-testkit-mutation call.
//
// THE GATE MUST NOT INHERIT THE CONSUMER'S LOCK. Every call is
// `uv run --no-project --isolated --refresh-package forge-testkit --with <floor>`:
// --no-project detaches the resolution from the project, the floor selects a
// testkit that has the CLI (eros#112 resolved 0.3.1 without it), --isolated
// keeps the star's synced .venv out (forge-testkit#3 ran its own tree), and
// --refresh-package makes uv re-read the testkit's index page on every call: a
// cached page that predates a release makes `>=` unsatisfiable until the cache
// expires (2.1.0 sat unresolvable ~15h after it reached devpi). Do not tidy any
// of the four away.
func (g *grading) testkit(ctx context.Context, env []string, args ...string) step {
	return g.do(ctx, g.tree, env, "uv", append([]string{"run", "--no-project", "--isolated", "--refresh-package", "forge-testkit",
		"--with", checks.PythonMutationTestkit, "forge-testkit-mutation"}, args...)...)
}
