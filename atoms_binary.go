package main

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/dagger"
)

// THE ATOMS BINARY. internal/atoms runs the cheap fleet atoms in one process:
// the binary is built here from the filtered source and run once for a lane in
// the tools container (atomsRun), and its verdicts are the lane's votes
// (atoms_vote.go).

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
	"internal/atomkit/**",
	"internal/atomlib/**",
	"internal/catalogue/**",
	"internal/checks/**",
	"internal/execmem/**",
	"internal/pgroupps/**",
	"internal/gitextra/**",
	"internal/gomut/**",
	"internal/pymut/**",
	"internal/rustmut/**",
	"internal/tsmut/**",
	"internal/govlaw/**",
	"internal/hadescall/**",
	"internal/unitkey/**",
	"internal/witnesscall/**",
	"internal/regenlane/**",
	"internal/regenkit/**",
	"internal/goregen/**",
	// rust:wit-compose and dev:rust-replay share one body
	"internal/witreplay/**",
	// the question a gate atom and its dev verb both ask: go:gofmt and
	// dev:go-fmt-check, go:govulncheck and dev:go-vuln, fleet:opengrep-sast and dev:go-sast
	"internal/gofmtcheck/**",
	"internal/vulncheck/**",
	"internal/sastcheck/**",
	// dev:python-typecheck and dev:python-check run one type check
	"internal/devpython/**",
	"internal/retiredverbs/**",
	"internal/releasekit/**",
	// the bundle lane's build (bundle:policy, :fleet) and its decisions
	"internal/bundlekit/**",
	"internal/bundlelane/**",
	// the chairman base's validation (bases:chairman)
	"internal/chairmanfixed/**",
	// the cast lane's compile (rust:cast-release, go:cast-release) and its decisions
	"internal/publishpack/**",
	"internal/publishlane/**",
	"internal/castrelease/**",
	"internal/castlane/**",
	"internal/buildlane/**",
	"internal/copyout/**",
	// ts:visual's: the artifact's push and the login it pushes with
	"internal/visuallane/**",
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

// reaskNonce keys the binary's exec afresh on every call; a variable so a test
// can read what each call carried.
var reaskNonce = func() string { return strconv.FormatInt(time.Now().UnixNano(), 10) }

// atomsSpirePath is where the lane pod's SPIRE socket sits in the binary's
// container when the call forwarded one.
const atomsSpirePath = "/run/spire/agent.sock"

// stageHas reports whether the binary grades an atom at the stage.
func stageHas(stage, id string) bool { return slices.Contains(atoms.StageIDs(stage), id) }

// withAtomSpire forwards the lane pod's SPIRE socket into the binary's
// container for the stage that carries fleet:witness, so the binary asks
// narcissus as the same SVID the chain does (through the witnesscall layer).
// Without a socket (a local run, a lane that forwarded none) nothing is mounted,
// the flag is not passed and the atom settles could-not-run, loudly. The owner is
// root, the tools container's user: the chain's nonroot owner belonged to the
// static base its helper ran on.
func (r *run) withAtomSpire(ctr *dagger.Container, stage string) (*dagger.Container, []string) {
	if r.spire == nil || !stageHas(stage, "fleet:witness") {
		return ctr, nil
	}
	return ctr.WithUnixSocket(atomsSpirePath, r.spire), []string{"-spire", atomsSpirePath}
}

// atomsBallot is the binary's run for a lane: fleet:witness makes its real asks,
// and the lane's SPIRE socket is mounted for the stage that carries it. See atomsRun.
func (m *FoundryTools) atomsBallot(ctx context.Context, stage, base string) (string, error) {
	return m.atomsRun(ctx, stage, base)
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
func (m *FoundryTools) atomsRun(ctx context.Context, stage, base string) (string, error) {
	// REASKED, ALWAYS. The binary's exec has identical inputs on every run of the
	// same tree, so the engine would serve a cached answer, and the atoms here
	// that read the network (the door, opa's build, kubeconform's schemas) would
	// be frozen at the first read. The chains read the door fresh and re-ask a
	// could-not-run; CA_REASK keys every lane exec afresh, so the binary does too.
	r := newRun(m.Source, m.Repo, base).fromOrigin(m.Origin).withSpire(m.spire).reasked(reaskNonce())
	// THE BASE IS FETCHED ONCE HERE (withBase), for the whole run: the binary's
	// change set (Collect) is computed from it once and feeds every atom that
	// judges the change, fleet:witness and ops:immutable among them. Neither
	// fetches it again; TestAtomsRunFetchesTheBaseOnce holds the count.
	ctr := r.gitReady(ctx, r.withBase(r.onTools(atomsTools(ctx))))
	// THE WITNESS ASKS FOR REAL, as the SVID the lane's socket issues.
	ctr, flags := r.withAtomSpire(ctr, stage)
	// A FORK IS NAMED TO THE BINARY BY ITS RECORD (-fork), read here as the
	// planner reads it: the binary has no door to foundry-dies for it. Fleet
	// atoms that scan a fork differently (fleet:opengrep-sast) read it off
	// Input.Fork; "" is not a fork.
	if fork := r.forkRecord(ctx); fork != "" {
		flags = append(flags, "-fork", fork)
	}
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

// atomTimeout is each atom's deadline in the binary's run.
const atomTimeout = "4m"
