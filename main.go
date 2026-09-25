// The fleet's checks, as code.
//
// One zero-argument `// +check` function per catalogued atom, three-state
// (0 pass · 1 findings · 2 could not run), run against the repository the
// caller is standing in. Nothing here takes a directory: the tree under check
// is bound once, at construction, from the caller's own context — so no atom
// can be pointed at a tree other than the one being gated.
package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// FoundryTools is the module root. The lanes hang off it, and the atoms hang
// off the lanes, so an atom's id reads as its namespace: `go:staticcheck`,
// `python:mypy`, `fleet:opengrep-sast`.
type FoundryTools struct {
	// Source is the repository under check.
	//
	// It is bound HERE and nowhere else. The dispatch's rule — "each running
	// against the current repository, the module's own source dir argument is
	// fixed, nothing lets a caller point it at another tree" — is enforced by
	// there being no other place to put a directory: every check function
	// below takes a context and nothing more.
	// +private
	Source *dagger.Directory
	// Repo is where Source was fetched FROM when New was given a repo and a
	// sha, and "" when the caller handed over its own tree. The atoms that
	// read the change set's base need it: a tree the engine fetched carries
	// one ref's history, and the base the door names is a commit on ANOTHER
	// branch (the pull's base at dispatch), which is not in it.
	// +private
	Repo string
	// Sha is the commit Source was fetched at, beside Repo, and "" with it.
	// The build lane labels, tags and permits the image under it.
	// +private
	Sha string
	// Origin is where a snapshot's base is fetched from — the star's origin
	// remote, as Push's --origin names it — for the atoms that grade a change
	// (run.gitReadyOn). "" for a fetched tree and for a hook that named none.
	// +private
	Origin string
}

// New binds the module to the caller's repository — the tree it is standing
// in, or, named by `repo` and `sha`, a commit the ENGINE fetches itself.
func New(
	// The repository under check. Defaults to the caller's context directory —
	// the repo you are standing in when you run `dagger check`.
	//
	// BUILD ARTIFACTS NEVER LEAVE THE HOST. A developer's node_modules, .venv
	// or target/ is not part of the repository, cannot be committed, and was
	// being uploaded to the engine on every run — where it made every cache
	// key unique and the fleet atoms grade files no commit could carry
	// (measured 2026-09-09 on tongs: 40+ large-file findings, all under
	// target/). The ignore is the fleet's, applied before upload. `.git` STAYS:
	// fleet:witness, the mutation lane and sweep:template-render-matrix read
	// history.
	//
	// AND THE AGENT DIRECTORIES GO, for a reason a gitignore filter cannot
	// reach. `.claude/worktrees/` holds a full checkout of the repo per live
	// session, and it is ignored by Rob's GLOBAL gitignore
	// (~/.gitignore_global), which a Dagger `Gitignore` filter does not read —
	// it reads the .gitignore files in the tree. So on any repo whose own
	// .gitignore does not name `.claude/`, every sibling session's worktree was
	// uploaded and graded: measured 2026-09-17 on infra from the developer's
	// checkout, ops:dup reported 36 duplicate fleet facts across 18 facts, every
	// one of them a second copy inside .claude/worktrees/Edison3-zfsprov and
	// .claude/worktrees/Fox5-prom-sd. The gate is green in CI, where the clone
	// carries none of it — which is exactly the shape of a local hook that
	// refuses a commit for somebody else's tree.
	//
	// checks.GateExclude already says nothing under .claude/, .specify/ or
	// .furnace/ is graded, so no atom loses a file it was reading; this moves
	// that rule to the boundary, where the atoms that walk the filesystem
	// rather than the population obey it too — and stops another session's
	// checkout from changing this one's cache key.
	//
	// +defaultPath="/"
	// +ignore=["**/node_modules","**/.venv","**/target","**/__pycache__","**/.pytest_cache","**/.mypy_cache","**/.ruff_cache","**/dist",".melt",".claude",".specify",".furnace"]
	source *dagger.Directory,
	// A git URL the ENGINE fetches the tree from, instead of `source` — the
	// door's own clone URL for the runner (`http://ourea…:8215/<repo>.git`).
	// Names `sha` with it. THE FETCH IS THE ENGINE'S AND SO IS THE CACHE:
	// the runner Job used to clone the tree onto its own filesystem and
	// upload it into the engine on every run (15s of a 59s infra gate,
	// measured 2026-09-13); a git-sourced Directory is cached by commit and
	// the second gate on a repo fetches only what moved. Full history, not
	// the CLI's shallow `url#ref` (depth 1, measured): fleet:witness and the
	// mutation lane diff against the pull's base, which a depth-1 tree
	// cannot reach.
	// +optional
	repo string,
	// The commit to fetch when `repo` is named. A commit, not a ref: the
	// receipt is keyed on the tree the door named, and a branch name would
	// grade whatever the branch pointed at by the time the engine looked.
	// +optional
	sha string,
) (*FoundryTools, error) {
	if repo == "" {
		if sha != "" {
			return nil, fmt.Errorf("--sha names a commit to fetch, and needs --repo to say from where")
		}
		return &FoundryTools{Source: source}, nil
	}
	if sha == "" {
		return nil, fmt.Errorf("--repo=%s names where to fetch from, and needs --sha to say which commit", repo)
	}
	// Depth -1 is "all of it" to the SDK (the zero value is dropped from
	// the query and the engine's default is 1). Measured 2026-09-13 against
	// the cluster engine: default 1 commit, -1 the whole 135.
	return &FoundryTools{Source: dag.Git(repo).Ref(sha).Tree(dagger.GitRefTreeOpts{Depth: -1}), Repo: repo, Sha: sha}, nil
}

// Tree answers the git tree hash of the bound repository — the key the
// door's receipt join is written under (CA_GATE_TREE), so a runner that
// let the engine fetch the commit can still PROVE it is grading the tree
// the door named before it asks for a vector. A tree with no commit behind
// it (a linked worktree's snapshot, which gitReady rebuilds without history)
// is an error, not an empty string: nothing keyed on "" may be attested.
func (m *FoundryTools) Tree(ctx context.Context) (string, error) {
	r := newRun(m.Source, m.Repo, "")
	out, code, err := output(ctx, r.gitReady(ctx, r.lane(checks.ImageFleet)).
		WithExec([]string{"git", "rev-parse", "HEAD^{tree}"}, anyExit))
	if err != nil {
		return "", fmt.Errorf("could not read the tree: %w", err)
	}
	if code != 0 || out == "" {
		return "", fmt.Errorf("the repository has no HEAD to read a tree off (exit %d): %s", code, out)
	}
	return out, nil
}

// Compose holds the host-stacks atoms. They report ABSENT on a repo that
// tracks no compose spec, which is most of the fleet.
func (m *FoundryTools) Compose() *Compose { return &Compose{Source: m.Source} }

// Dies holds the policy die's atoms. They report ABSENT on any tree that is not
// foundry-dies-shaped — policy/.manifest and fleet/stars/ together.
func (m *FoundryTools) Dies() *Dies { return &Dies{Source: m.Source} }

// Fleet holds the atoms that run in every repository, whatever it is written in.
func (m *FoundryTools) Fleet() *Fleet { return &Fleet{Source: m.Source} }

// Go holds the Go lane's atoms. They report ABSENT on a repo with no go.mod.
func (m *FoundryTools) Go() *GoLane { return &GoLane{Source: m.Source} }

// Python holds the Python lane's atoms. They report ABSENT on a repo with no
// pyproject.toml.
func (m *FoundryTools) Python() *PythonLane { return &PythonLane{Source: m.Source} }

// Rust holds the Rust lane's atoms. They report ABSENT on a repo with no
// Cargo.toml.
func (m *FoundryTools) Rust() *RustLane { return &RustLane{Source: m.Source} }

// Ts holds the TypeScript lane's atoms. They report ABSENT on a repo with no
// package.json.
func (m *FoundryTools) Ts() *TSLane { return &TSLane{Source: m.Source} }

// Lanes reports which lanes this repository actually builds, read off the root
// manifests rather than off a label anyone attached to the repo.
func (m *FoundryTools) Lanes(ctx context.Context) (string, error) {
	entries, err := m.Source.Entries(ctx)
	if err != nil {
		return "", fmt.Errorf("could not read the repository root: %w", err)
	}
	// THE PLANNER'S OWN ANSWER, not a second reading of the tree. Go and
	// python are both declared by their FILES — a go.mod or a .py anywhere the
	// repository owns — so a verb that re-derived the lanes off the root
	// entries would tell a session something the gate does not believe.
	r := newRun(m.Source, "", "")
	dirs, err := r.goModuleDirs(ctx)
	if err != nil {
		return "", fmt.Errorf("could not enumerate the tree's Go modules: %w", err)
	}
	pys, err := r.pythonFiles(ctx)
	if err != nil {
		return "", fmt.Errorf("could not enumerate the tree's python: %w", err)
	}
	out := ""
	for _, l := range checks.LanesOfTree(checks.Tree{Entries: entries, GoModules: len(dirs), PythonFiles: len(pys)}) {
		switch {
		case l == checks.LaneGo && len(dirs) == 1 && dirs[0] == ".":
			out += fmt.Sprintf("%s (go.mod)\n", l)
		case l == checks.LaneGo:
			out += fmt.Sprintf("%s (go.mod in %s)\n", l, strings.Join(dirs, ", "))
		case l == checks.LanePython && len(pys) > 0:
			// The count, not the list: infra carries 617 of them.
			out += fmt.Sprintf("%s (%d .py file(s), and %s)\n", l, len(pys), manifestState(entries, l))
		default:
			out += fmt.Sprintf("%s (%s)\n", l, checks.ManifestFor(l))
		}
	}
	if out == "" {
		return "this repository declares no lane (no go.mod and no .py anywhere, and no pyproject.toml, Cargo.toml or package.json at its root)", nil
	}
	return out, nil
}

// manifestState says whether the lane's build is declared too, because the
// files and the manifest now answer different halves of the question and a
// session asking `Lanes` wants both.
func manifestState(entries []string, l checks.Lane) string {
	manifest := checks.ManifestFor(l)
	if checks.DeclaresManifest(entries, manifest) {
		return manifest
	}
	return "no " + manifest + " — lint and test, no build"
}

// Catalogue lists every atom this module carries — id, stage, lane and
// description — as the catalogue rows nereus holds for them.
func (m *FoundryTools) Catalogue(ctx context.Context) (string, error) {
	rows := make([]map[string]string, 0, len(checks.Atoms))
	for _, a := range checks.Atoms {
		rows = append(rows, map[string]string{
			"atom":        a.ID,
			"stage":       a.Stage,
			"lane":        string(a.Lane),
			"manifest":    checks.ManifestFor(a.Lane),
			"description": a.Desc,
		})
	}
	b, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// atomsInFlight bounds how many atoms run at once. The engine schedules each
// atom's chain as its own DAG, so independent atoms overlap by default once
// they are asked for concurrently; the bound keeps a lane's heaviest atoms
// (`go test -race`, `cargo test`, a mutation run) from stacking their memory
// on one 8Gi engine.
const atomsInFlight = 4

// Verdicts runs every atom that has a surface here and answers the VECTOR —
// one element per atom, each preserving its own 0/1/2. The door reads the join,
// not a single exit code, so an atom that could not run stays visible as a 2
// instead of being flattened into the run's overall failure.
//
// NEVER CACHED AS A WHOLE. A vector is a successful return even when an atom
// in it could not run, and a function's result is cached for seven days by
// default — so a re-push of the same tree would be answered a stale
// could-not-run without looking. The atoms' own execs stay cached; only the
// vector is recomputed.
//
// +cache="never"
func (m *FoundryTools) Verdicts(
	ctx context.Context,
	// Only atoms at this stage: precommit, prepush, sweep or mutation. EMPTY
	// MEANS EVERY PULL-PATH STAGE — precommit and prepush — and deliberately
	// NOT sweep: a door that asks for "the vector" is asking about a pull, and
	// CA F9's acceptance is that no sweep atom ever appears in one. Ask for the
	// sweep by name or you do not get it. Mutation is asked for by name too —
	// it blocks the same pull, as its own lane, so the gate's slot stays short.
	// +optional
	stage string,
	// Only these atom ids, comma-separated — e.g.
	// "sweep:kubeconform,sweep:kube-linter". Empty means every atom the stage
	// admits.
	//
	// THIS SELECTS WHAT IS REPORTED BY SELECTING WHAT IS RUN, which is the
	// only honest way to split a cadence: a run that evaluated every atom and
	// printed one would be computing findings it then threw away. It names ATOMS, not
	// directories — the charter is that nothing below New takes a tree, and
	// this takes no tree.
	//
	// An id that is not in the table, or is not admitted by the stage, is an
	// ERROR rather than a silent empty vector: a sweep asked for an atom that
	// does not exist must not answer "nothing to report".
	// +optional
	only string,
	// The change set's base — the pull's merge base, as the door names it
	// (CA_GATE_BASE). Empty means the tip against its parent. Only an atom
	// that judges the CHANGE rather than the tree reads it (fleet:witness,
	// the mutation lane); it reaches those atoms alone, so every other atom's
	// cache key is a function of the tree and not of the pull.
	// +optional
	base string,
) (string, error) {
	out, err := m.vector(ctx, stage, only, base)
	if err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// vector runs the atoms a stage (and an `only` list) selects, concurrently and
// bounded, and answers their verdicts in selection order.
func (m *FoundryTools) vector(ctx context.Context, stage, only, base string) ([]checks.Verdict, error) {
	selected := checks.AtomsForStage(stage)
	if only != "" {
		var err error
		selected, err = checks.Select(selected, only)
		if err != nil {
			return nil, err
		}
	}
	// An atom another SELECTED atom covers stands down here rather than in the
	// runner, so the caller still gets its line (checks.Subsumed says why).
	selected, covered := checks.Subsumed(selected)
	r := newRun(m.Source, m.Repo, base).fromOrigin(m.Origin)
	plan, absent, err := r.plan(ctx, selected)
	if err != nil {
		return nil, err
	}
	out := make([]checks.Verdict, len(plan.Run))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(atomsInFlight)
	for i, a := range plan.Run {
		g.Go(func() error {
			v, err := verdictFor(gctx, r, a.ID)
			if err != nil {
				return err
			}
			out[i] = v
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	out = append(out, absent...)
	for _, a := range covered {
		out = append(out, checks.CoveredVerdict(a))
	}
	return out, nil
}

// plan asks the tree what it contains, ONCE, before any atom runs — Rob's
// "What's in the commit? (Go? Python? Ansible?)" as its own step. It answers
// the plan and the verdicts of the atoms that never start, so an atom whose
// lane is absent costs one map lookup instead of a dispatch that reads the
// repository root again.
//
// A ROOT THAT CANNOT BE READ IS A COULD-NOT-RUN ABOUT THE REPOSITORY, and it
// is one per atom that needed the answer, exactly as it was when each atom
// asked for itself: the atoms that run everywhere are unaffected by it.
func (r *run) plan(ctx context.Context, selected []checks.AtomDef) (checks.Plan, []checks.Verdict, error) {
	entries, entriesErr := r.src.Entries(ctx)
	mods, modsErr := r.goModuleDirs(ctx)
	pys, pysErr := r.pythonFiles(ctx)
	plan := checks.PlanAtoms(selected, checks.Tree{
		Entries:     entries,
		GoModules:   len(mods),
		PythonFiles: len(pys),
	})
	if entriesErr != nil || modsErr != nil || pysErr != nil {
		why := cmp.Or(entriesErr, modsErr, pysErr)
		plan = checks.Plan{}
		var unread []checks.Verdict
		for _, a := range selected {
			if a.Lane == checks.LaneAny {
				plan.Run = append(plan.Run, a)
				continue
			}
			unread = append(unread, checks.VerdictOf(a, 2, fmt.Sprintf("could not read the repository root: %v", why)))
		}
		return plan, unread, nil
	}
	absent := make([]checks.Verdict, 0, len(plan.Absent))
	for _, a := range plan.Absent {
		absent = append(absent, checks.AbsentVerdict(a))
	}
	return plan, absent, nil
}

// verdictFor is the one place an atom is dispatched: the registered runner
// builds the atom's chain and answers.
//
// WHETHER THE LANE HAS A SURFACE IS THE PLANNER'S QUESTION, not this one's.
// `run.plan` asks the tree once, before anything starts, and an atom whose
// lane the tree does not declare never reaches here — where it used to read
// the repository root again, once per atom, to arrive at the same answer.
func verdictFor(ctx context.Context, r *run, id string) (checks.Verdict, error) {
	fn, ok := registry[id]
	if !ok {
		// A catalogue row with no runner is an AUTHORING ERROR, not a verdict.
		// register() panics at load for a runner naming a row that does not
		// exist; this is the other direction, and TestEveryCatalogueIDIsRegistered
		// catches it in the tests rather than on a gate.
		return checks.Verdict{}, fmt.Errorf("%s has no runner registered", id)
	}
	v := fn(ctx, r)
	if v.State != int(checks.StateCannotRun) {
		return v, nil
	}
	// A COULD-NOT-RUN IS ASKED AGAIN, PAST THE CACHE, BEFORE IT IS REPORTED.
	// An exec that expects any exit caches its failure, and since the engine
	// began keeping its cache (infra #617, 2026-09-17) a transient failure — a
	// proxy that answered 404 once, a dropped connection — would come back
	// from cache on every re-ask of the same tree. The second run keys every
	// lane exec afresh, so it looks again. MEASURED on the gate receipts
	// 2026-09-09 -> 17: 759 could-not-run rows against 1,362 findings.
	again := fn(ctx, newRun(r.src, r.repo, r.base).fromOrigin(r.origin).reasked(strconv.FormatInt(time.Now().UnixNano(), 10)))
	if again.State != int(checks.StateCannotRun) {
		return again, nil
	}
	again.Reason += "\n(asked twice, the second time past the engine's cache: it could not run both times)"
	return again, nil
}

// check is what every `+check` function calls: one atom, one verdict, answered the
// way `dagger check` reads it.
func check(ctx context.Context, src *dagger.Directory, id string) (string, error) {
	v, err := verdictFor(ctx, newRun(src, "", ""), id)
	if err != nil {
		return "", err
	}
	return v.Answer()
}
