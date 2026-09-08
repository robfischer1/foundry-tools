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

// New binds the module to the caller's repository.
func New(
	// The repository under check. Defaults to the caller's context directory —
	// the repo you are standing in when you run `dagger check`.
	// +defaultPath="/"
	source *dagger.Directory,
) *FoundryTools {
	return &FoundryTools{Source: source}
}

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

// Verdicts runs every atom that has a surface here and answers the VECTOR —
// one element per atom, each preserving its own 0/1/2. The door reads the join,
// not a single exit code, so an atom that could not run stays visible as a 2
// instead of being flattened into the run's overall failure.
func (m *FoundryTools) Verdicts(
	ctx context.Context,
	// Only atoms at this stage: precommit, prepush or sweep. EMPTY MEANS EVERY
	// PULL-PATH STAGE — precommit and prepush — and deliberately NOT sweep: a
	// door that asks for "the vector" is asking about a pull, and CA F9's
	// acceptance is that no sweep atom ever appears in one. Ask for the sweep
	// by name or you do not get it.
	// +optional
	stage string,
) (string, error) {
	selected := checks.AtomsForStage(stage)
	out := make([]checks.Verdict, 0, len(selected))
	for _, a := range selected {
		v, err := m.verdict(ctx, a.ID)
		if err != nil {
			return "", err
		}
		out = append(out, v)
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (m *FoundryTools) verdict(ctx context.Context, id string) (checks.Verdict, error) {
	return verdictFor(ctx, m.Source, id)
}

// verdictFor is the one place an atom actually runs.
func verdictFor(ctx context.Context, src *dagger.Directory, id string) (checks.Verdict, error) {
	a := checks.AtomByID(id)

	if a.Lane != checks.LaneAny {
		entries, err := src.Entries(ctx)
		if err != nil {
			// The tree could not be read. That is a CANNOT RUN about the
			// repository, not a finding about it.
			return checks.VerdictOf(a, 2, fmt.Sprintf("could not read the repository root: %v", err)), nil
		}
		if !checks.DeclaresLane(entries, a.Lane) {
			return checks.AbsentVerdict(a), nil
		}
	}

	ctr := dag.Container().
		From(a.Image).
		// worktree-guard and every other hook that stands down under CI reads
		// this. The engine IS the CI boundary; saying so beats each atom
		// guessing.
		WithEnvVariable("CI", "true").
		// The sweep's fetch coordinates. They live in internal/checks so the
		// digest-pins sweep lands on ONE block (images.go) rather than on a
		// string buried in a shell body, and they are set for every atom
		// rather than only the sweep's because a conditional here would be a
		// second place for the atom table to disagree with itself.
		WithEnvVariable("CRD_SCHEMA_LOCATION", checks.CRDSchemaLocation).
		WithEnvVariable("CRD_SCHEMA_PROBE", checks.CRDSchemaProbe).
		WithEnvVariable("ORAS_MIRROR", checks.OrasMirror).
		WithEnvVariable("ORAS_URL", checks.OrasURL).
		WithMountedDirectory("/src", src).
		WithWorkdir("/src")
	if a.NeedsStocks {
		// The canonical script is READ AT ITS ONE HOME through the door, not
		// vendored here. A second copy is the defect the script itself exists
		// to catch.
		ctr = ctr.WithMountedDirectory("/stocks", dag.Git(checks.StocksRepo).Ref(checks.StocksRef).Tree())
	}
	ctr = ctr.WithExec(
		[]string{"sh", "-c", a.Script},
		dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny},
	)

	code, err := ctr.ExitCode(ctx)
	if err != nil {
		// The exec never completed — an unpullable image, a dead engine, a
		// cancelled run. None of those is a clean scan.
		return checks.VerdictOf(a, 2, fmt.Sprintf("the atom never ran: %v", err)), nil
	}
	stdout, _ := ctr.Stdout(ctx)
	stderr, _ := ctr.Stderr(ctx)
	return checks.VerdictOf(a, code, stdout+stderr), nil
}

// run is what every check function calls: one atom, one verdict, answered the
// way `dagger check` reads it.
func run(ctx context.Context, src *dagger.Directory, id string) (string, error) {
	v, err := verdictFor(ctx, src, id)
	if err != nil {
		return "", err
	}
	return v.Answer()
}
