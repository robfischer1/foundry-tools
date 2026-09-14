// The fleet's checks, as code.
//
// One zero-argument `// +check` function per catalogued atom, three-state
// (0 pass · 1 findings · 2 could not run), run against the repository the
// caller is standing in. Nothing here takes a directory: the tree under check
// is bound once, at construction, from the caller's own context — so no atom
// can be pointed at a tree other than the one being gated.
package main

import (
	"context"
	"encoding/json"
	"fmt"

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
	// +defaultPath="/"
	// +ignore=["**/node_modules","**/.venv","**/target","**/__pycache__","**/.pytest_cache","**/.mypy_cache","**/.ruff_cache","**/dist",".melt"]
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
	return &FoundryTools{Source: dag.Git(repo).Ref(sha).Tree(dagger.GitRefTreeOpts{Depth: -1})}, nil
}

// Tree answers the git tree hash of the bound repository — the key the
// door's receipt join is written under (CA_GATE_TREE), so a runner that
// let the engine fetch the commit can still PROVE it is grading the tree
// the door named before it asks for a vector. A tree with no commit behind
// it (a linked worktree's snapshot, which gitReady rebuilds without history)
// is an error, not an empty string: nothing keyed on "" may be attested.
func (m *FoundryTools) Tree(ctx context.Context) (string, error) {
	r := newRun(m.Source, "")
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

// Sweep holds the repo-cadence atoms — the checks that describe a REPOSITORY
// rather than a change. They run on a clock (CA F9's ca-sweep CronJob) and
// never in a pull's path; `Verdicts` with no stage cannot reach them.
func (m *FoundryTools) Sweep() *Sweep { return &Sweep{Source: m.Source} }

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
	lanes := checks.LanesOf(entries)
	if len(lanes) == 0 {
		return "this repository declares no lane (no go.mod, pyproject.toml, Cargo.toml or package.json at its root)", nil
	}
	out := ""
	for _, l := range lanes {
		out += fmt.Sprintf("%s (%s)\n", l, checks.ManifestFor(l))
	}
	return out, nil
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
	// "sweep:digest-pins,sweep:kubeconform". Empty means every atom the stage
	// admits.
	//
	// THIS SELECTS WHAT IS REPORTED BY SELECTING WHAT IS RUN, which is the
	// only honest way to split a cadence: ca-sweep runs digest-pins nightly
	// and the rest weekly, and a nightly that evaluated all five and printed
	// one would be computing findings it then threw away. It names ATOMS, not
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
	selected := checks.AtomsForStage(stage)
	if only != "" {
		var err error
		selected, err = checks.Select(selected, only)
		if err != nil {
			return "", err
		}
	}
	r := newRun(m.Source, base)
	out := make([]checks.Verdict, len(selected))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(atomsInFlight)
	for i, a := range selected {
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
		return "", err
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// verdictFor is the one place an atom is dispatched: the lane's surface is
// checked, then the registered runner builds the atom's chain and answers.
func verdictFor(ctx context.Context, r *run, id string) (checks.Verdict, error) {
	a := checks.AtomByID(id)

	if a.Lane != checks.LaneAny {
		entries, err := r.src.Entries(ctx)
		if err != nil {
			// The tree could not be read. That is a CANNOT RUN about the
			// repository, not a finding about it.
			return checks.VerdictOf(a, 2, fmt.Sprintf("could not read the repository root: %v", err)), nil
		}
		if !checks.DeclaresLane(entries, a.Lane) {
			return checks.AbsentVerdict(a), nil
		}
	}

	fn, ok := registry[id]
	if !ok {
		// A catalogue row with no runner is an AUTHORING ERROR, not a verdict.
		// register() panics at load for a runner naming a row that does not
		// exist; this is the other direction, and TestEveryCatalogueIDIsRegistered
		// catches it in the tests rather than on a gate.
		return checks.Verdict{}, fmt.Errorf("%s has no runner registered", id)
	}
	return fn(ctx, r), nil
}

// check is what every `+check` function calls: one atom, one verdict, answered the
// way `dagger check` reads it.
func check(ctx context.Context, src *dagger.Directory, id string) (string, error) {
	v, err := verdictFor(ctx, newRun(src, ""), id)
	if err != nil {
		return "", err
	}
	return v.Answer()
}
