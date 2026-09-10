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
	// that judges the CHANGE rather than the tree reads it (fleet:witness);
	// it rides into every atom's environment as GATE_BASE so there is one
	// way to learn it and not a conditional here.
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
	out := make([]checks.Verdict, 0, len(selected))
	for _, a := range selected {
		v, err := verdictFor(ctx, m.Source, a.ID, base)
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

// verdictFor is the one place an atom actually runs.
func verdictFor(ctx context.Context, src *dagger.Directory, id string, base string) (checks.Verdict, error) {
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
		// The change set's base, for the atoms that judge the change (see
		// Verdicts). Empty on a tip and on a local run.
		WithEnvVariable("GATE_BASE", base).
		// The sweep's fetch coordinates. They live in internal/checks so the
		// digest-pins sweep lands on ONE block (images.go) rather than on a
		// string buried in a shell body, and they are set for every atom
		// rather than only the sweep's because a conditional here would be a
		// second place for the atom table to disagree with itself.
		WithEnvVariable("CRD_SCHEMA_LOCATION", checks.CRDSchemaLocation).
		WithEnvVariable("CRD_SCHEMA_PROBE", checks.CRDSchemaProbe).
		WithEnvVariable("ORAS_MIRROR", checks.OrasMirror).
		WithEnvVariable("ORAS_URL", checks.OrasURL).
		// The compose: and dies: fetch coordinates, here for the same reason
		// the sweep's are: the pinned version lives in internal/checks so the
		// digest-pins sweep and any future pin audit land on ONE block, and a
		// conditional here would be a second place for this file and the atom
		// table to disagree about which tool an atom provisions.
		WithEnvVariable("COMPOSE_VERSION", checks.ComposeVersion).
		WithEnvVariable("COMPOSE_MIRROR", checks.ComposeMirror).
		WithEnvVariable("COMPOSE_URL", checks.ComposeURL).
		WithEnvVariable("OPA_VERSION", checks.OpaVersion).
		WithEnvVariable("OPA_MIRROR", checks.OpaMirror).
		WithEnvVariable("OPA_URL", checks.OpaURL).
		// The go command's coordinates. Set on EVERY atom's container, not
		// only the go lane's, for the reason the sweep's coordinates above are:
		// a conditional here is a second place for this file and the atom table
		// to disagree, and three variables that mean nothing to a shell script
		// cost nothing. See checks.GoProxy for the measurement — without them
		// every go:* atom died resolving forgejo.notusmi.com against the
		// forge's SSO portal.
		//
		// GOPRIVATE IS SET TO THE EMPTY STRING ON PURPOSE and must be set:
		// go-ci bakes one, GOPRIVATE is GONOPROXY's default, and a GONOPROXY
		// naming the forge sends the fetch direct to it however right GOPROXY
		// is.
		WithEnvVariable("GOPROXY", checks.GoProxy).
		WithEnvVariable("GONOSUMDB", checks.GoNoSumDB).
		WithEnvVariable("GOPRIVATE", checks.GoPrivate).
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
	v, err := verdictFor(ctx, src, id, "")
	if err != nil {
		return "", err
	}
	return v.Answer()
}
