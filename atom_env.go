package main

import (
	"context"
	_ "embed"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/catalogue"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE ENVIRONMENT OF A PORTED ATOM. The question an atom asks is in its own
// file. What stays in this package is the place it is asked in on the Dagger
// engine: the lane container, its mounts and its services, keyed by atom id.
// It holds no logic (R35): an entry says which container, and the adapter
// does the rest. An atom with no entry runs in the lane of its toolchain.

// atomEnv is one atom's container, as its chain built it.
type atomEnv struct {
	// container is the lane container the atom runs in, with /src mounted.
	container func(r *run) *dagger.Container
	// facts says the atom judges the change or the origin: it is handed the
	// base, the origin URL and the fork record (ATOM_BASE, ATOM_ORIGIN,
	// ATOM_FORK) and the base is fetched. Off, the exec's cache key is the
	// tree's alone and a pull's base does not reach it.
	facts bool
	// factsWhen is facts for an atom that judges the change only when the tree
	// opts in (the rust lane's scope script): the base reaches the atom, and its
	// cache key, only for the repos that ask. Nil is never.
	factsWhen func(ctx context.Context, r *run) bool
	// narrow says /src is the tree less what no toolchain reads (r.code()), so
	// the linked-worktree swap re-mounts that same tree and not the whole one.
	narrow bool
	// bind adds, last, what the tree decides the run needs and no layer below
	// knows: the test servers its build tags name, the generator its regen
	// tests ask for. It reads the tree with the same questions the atom asks and
	// judges nothing; nil adds nothing.
	bind func(ctx context.Context, r *run, ctr *dagger.Container) *dagger.Container
	// origin says the atom reads the URL the repository came from
	// (ATOM_ORIGIN) and nothing else of the change: its cache key is the
	// tree's and the origin's, not the pull's base.
	origin bool
	// tools says the atom runs in the tools container the atoms binary votes in
	// (atomsTools), not in a lane: the container that carries the pinned
	// programs and the python venv its question execs. The chain provisioned
	// them one run at a time; the container has them already.
	tools bool
	// fork hands the atom the fork record (ATOM_FORK) and nothing else of facts.
	fork bool
	// nogit leaves the repository alone: the atom reads files off the disk and
	// asks git nothing, and its image runs as a user who cannot make a
	// repository in /src (the Renovate image).
	nogit bool
	// narc says the atom execs narcissus's analyzer: it is mounted from the pin
	// flux holds live, and a pin that could not be read reaches the atom as
	// ATOM_NARC_ERR, which it settles 2 with.
	narc bool
}

// atomEnvironments are the atoms whose chain did not run in the plain lane
// container of their toolchain. The go lane's atoms read the tree less what no
// toolchain reads (laneCode), so an edit to a README re-runs none of them.
var atomEnvironments = map[string]atomEnv{
	"go:vet":         {container: goCodeLane},
	"go:build":       {container: goCodeLane},
	"go:staticcheck": {container: goCodeLane},
	// the go lane over the narrowed tree, the gravity the regen tests need and,
	// for the push's run, the servers the tree's tags name
	"go:govulncheck": {container: goCodeLane, narrow: true},
	"go:test":        {container: goCodeLane, narrow: true, bind: goSuiteLane("", false)},
	"go:test-race":   {container: goCodeLane, narrow: true, bind: goSuiteLane("", true)},
	// The diff-coverage atom reads the change (git diff against the base), so it runs
	// on the whole tree, as the mutation lane's git reads do, and judges it
	// (facts). Its suite is a run of its own beside go:test-race's, on servers
	// of its own: dagger content-addresses services, and two suites that reset
	// one schema per test tear it apart.
	"go:diff-coverage": {facts: true, bind: goSuiteLane("diffcov", true)},
	// the rust lane with the dependency graph fetched, and the base only for a
	// repo that scopes its crates; the tests build binaries in the per-repo
	// target and then run them, so they hold it LOCKED (withLockedCargoTarget)
	"rust:cargo-clippy": {container: (*run).cargoDeps, factsWhen: rustScoped},
	"rust:cargo-test":   {container: rustTestLane, factsWhen: rustScoped},
	// the wit atoms: each its own release volumes and the pinned tools it execs
	// (atom_env_rust.go); neither reads the other's output
	"rust:wit-guest":   {container: witGuestLane, bind: witTools(false)},
	"rust:wit-compose": {container: witComposeLane, bind: witTools(true)},

	// The ops, orbit, compose, wit and template atoms that exec a program: the
	// tools container, as the binary's vote runs them.
	"compose:config":         {tools: true},
	"ops:chezmoi":            {tools: true},
	"ops:flux":               {tools: true},
	"ops:kube-linter":        {tools: true},
	"ops:orbit-sidecars":     {tools: true},
	"ops:shell":              {tools: true},
	"orbit:sidecars":         {tools: true},
	"wit:validate":           {tools: true},
	"ops:ansible":            {tools: true},
	"ops:declaration":        {tools: true},
	"ops:dup":                {tools: true},
	"ops:metrics":            {tools: true},
	"ops:specs":              {tools: true},
	"ops:star-sa":            {tools: true},
	"ops:world-patches":      {tools: true},
	"template:render-matrix": {tools: true},
	// ... and the ones that judge the change or the origin are also handed them.
	"ops:immutable": {tools: true, facts: true},
	"orbit:repo":    {origin: true},
	"orbit:surface": {tools: true, origin: true, narc: true},

	// The fleet atoms. The ones that exec a program or read the tree with the
	// tools container's venv run there, as the binary's vote runs them; the
	// witness's run is keyed so no earlier answer stands in for a later ask.
	"fleet:stop-justifications": {origin: true},
	"fleet:opengrep-sast":       {tools: true, fork: true},
	"fleet:hadolint":            {tools: true},
	"fleet:wit-topics":          {tools: true},
	"fleet:witness":             {tools: true, facts: true, bind: witnessRun},
	"fleet:renovate-config":     {container: renovateLane, nogit: true},
}

// rustTestLane is the fetched rust lane with the per-repo target held LOCKED, the
// remount after the fetch so the fetch itself takes no lock.
func rustTestLane(r *run) *dagger.Container { return r.withLockedCargoTarget(r.cargoDeps()) }

// rustScoped says the tree carries the repo's scope script, the opt-in to being
// graded on the crates a pull reaches (checks.RustScopeScript).
func rustScoped(ctx context.Context, r *run) bool {
	has, err := present(ctx, r, checks.RustScopeScript)
	return err == nil && has
}

// goCodeLane is the go lane over the narrowed tree.
func goCodeLane(r *run) *dagger.Container { return r.laneCode(checks.ImageGo) }

// atomEnvironment is the container a ported atom runs in: its environment, the
// foundry-dies checkout it asks for, the SPIRE socket when it needs an identity
// and, for a logic atom, a repository git can read (the tree facts are
// computed there, as the atoms binary computes them).
func (r *run) atomEnvironment(ctx context.Context, e catalogue.Entry) *dagger.Container {
	env, ok := atomEnvironments[e.ID]
	var ctr *dagger.Container
	switch {
	case ok && env.tools:
		ctr = r.onTools(atomsTools(ctx))
	case ok && env.container != nil:
		ctr = env.container(r)
	default:
		ctr = r.lane(checks.AtomByID(e.ID).Image)
	}
	needs := needsOf(e)
	if slices.Contains(needs, "dies") {
		ctr = r.withDies(ctr).WithEnvVariable("ATOM_DIES", "/dies")
	}
	if slices.Contains(needs, "svid") && r.spire != nil {
		ctr = ctr.WithUnixSocket(atomsSpirePath, r.spire).WithEnvVariable("SPIFFE_ENDPOINT_SOCKET", "unix://"+atomsSpirePath)
	}
	if env.narc {
		var err error
		if ctr, err = r.withNarc(ctx, ctr); err != nil {
			ctr = ctr.WithEnvVariable("ATOM_NARC_ERR", err.Error())
		}
	}
	if e.Def == nil {
		return ctr
	}
	if env.origin {
		ctr = ctr.WithEnvVariable("ATOM_ORIGIN", r.repo)
	}
	if env.fork {
		ctr = ctr.WithEnvVariable("ATOM_FORK", r.forkRecord(ctx))
	}
	if env.facts || (ok && env.factsWhen != nil && env.factsWhen(ctx, r)) {
		ctr = r.withBase(ctr).
			WithEnvVariable("ATOM_BASE", r.base).
			WithEnvVariable("ATOM_ORIGIN", r.repo).
			WithEnvVariable("ATOM_FORK", r.forkRecord(ctx))
	}
	if ok && env.nogit {
		return ctr
	}
	if ok && env.narrow {
		ctr = r.gitReadyOn(ctx, ctr, r.code())
	} else {
		ctr = r.gitReady(ctx, ctr)
	}
	if ok && env.bind != nil {
		ctr = env.bind(ctx, r, ctr)
	}
	return ctr
}

// needsOf is what an atom declares it needs.
func needsOf(e catalogue.Entry) []string {
	if e.Def != nil {
		return e.Def.Needs
	}
	return e.Manifest.Needs
}

// toolsSource is tools.toml, the catalogue of toolchains and tools the atoms
// run on. The module embeds it: a lane reads the toolchain's preflight from it.
//
//go:embed tools.toml
var toolsSource []byte

var (
	toolsOnce sync.Once
	toolsData atomkit.ToolsFile
	toolsErr  error
)

// atomTools is tools.toml, parsed once.
func atomTools() (atomkit.ToolsFile, error) {
	toolsOnce.Do(func() { toolsData, toolsErr = atomkit.ParseTools(toolsSource) })
	return toolsData, toolsErr
}

// runManifest runs a command atom's manifest: in each module directory the
// toolchain's preflight from tools.toml, then the manifest's one command
// (FN-087 step 5). The answer is the verdict the chain it replaced gave,
// word for word (R20): checks.VerdictOf over the output, folded across Go
// modules by eachModule. atomkit.RunCommand is the pod executor's reading of
// the same manifest and words its verdict by FN-053; see DEF-B3-012.
func runManifest(ctx context.Context, r *run, e catalogue.Entry) checks.Verdict {
	a := checks.AtomByID(e.ID)
	m := *e.Manifest
	tools, err := atomTools()
	if err != nil {
		return cannotRun(a, "tools.toml: "+err.Error())
	}
	// the container is built when a module is first run: an absent lane costs no
	// container (building one fetches the tools its image is provisioned with)
	var exec atomkit.ExecFunc
	one := func(dir string) checks.Verdict {
		if exec == nil {
			exec = execIn(manifestContainer(ctx, r, e, m))
		}
		return commandIn(ctx, a, m, tools.Preflights[m.Toolchain], exec, path.Join("/src", dir))
	}
	switch {
	case !m.PerModule:
		return one(".")
	case m.Toolchain != "go":
		return cannotRun(a, "per_module is wired for the go toolchain only, and "+e.ID+" runs on "+m.Toolchain)
	}
	return r.eachModule(ctx, a, one)
}

// manifestContainer is the container a command atom runs in: its environment
// and the manifest's [env].
func manifestContainer(ctx context.Context, r *run, e catalogue.Entry, m atomkit.Manifest) *dagger.Container {
	ctr := r.atomEnvironment(ctx, e)
	keys := make([]string, 0, len(m.Env))
	for k := range m.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		ctr = ctr.WithEnvVariable(k, m.Env[k])
	}
	return ctr
}

// commandIn runs the preflight and then the manifest's argv in dir, and judges
// the exit.
func commandIn(ctx context.Context, a checks.AtomDef, m atomkit.Manifest, preflight []string, exec atomkit.ExecFunc, dir string) checks.Verdict {
	if len(preflight) > 0 {
		if res := exec(ctx, preflight, dir); res.Code != 0 {
			return cannotRun(a, fmt.Sprintf("%s: CANNOT RUN - preflight `%s` exited %d: %s",
				a.ID, strings.Join(preflight, " "), res.Code, lastLine(res.Stderr)))
		}
	}
	res := exec(ctx, m.Argv, dir)
	if res.NotRun {
		return checks.VerdictOf(a, int(checks.StateCannotRun), res.Stderr)
	}
	if judge, ok := judges[a.ID]; ok {
		return judge(a, res)
	}
	return checks.VerdictOf(a, res.Code, res.Stdout+res.Stderr)
}

// judges are the atoms whose exit code the chain read in a vocabulary of its
// own before it became a state: cargo answers 101, not 1, for a command that
// failed (checks.CargoExit), and an audit's network fault is the substrate's,
// not the tree's (checks.AuditVerdict). The manifest's judge is exit and
// nothing more (CONF-5-008); until DEF-B3-012 is settled these keep the wording
// and the re-ask the Dagger gates have.
var judges = map[string]func(a checks.AtomDef, res atomkit.ExecResult) checks.Verdict{
	"rust:cargo-fmt":   cargoJudge,
	"rust:cargo-audit": auditJudge,
	"python:pip-audit": auditJudge,
	"ts:bun-audit":     auditJudge,
}

// cargoJudge is cargoVerdict over an exec's answer: output() is stdout, plus
// stderr when cargo exited non-zero, trimmed.
func cargoJudge(a checks.AtomDef, res atomkit.ExecResult) checks.Verdict {
	out := res.Stdout
	if res.Code != 0 {
		out += res.Stderr
	}
	return checks.VerdictOf(a, checks.CargoExit(res.Code), strings.TrimSpace(out))
}

// auditJudge is audit() over an exec's answer.
func auditJudge(a checks.AtomDef, res atomkit.ExecResult) checks.Verdict {
	return checks.AuditVerdict(a, res.Code, res.Stdout+res.Stderr)
}

// execIn is atomkit's Exec on the engine: argv in dir of ctr, under expect:ANY,
// answering the exit code and what it printed. An engine fault (an image that
// would not pull, a mount that would not evaluate) is the atom's could-not-run,
// not an exit code.
func execIn(ctr *dagger.Container) atomkit.ExecFunc {
	return func(ctx context.Context, argv []string, dir string) atomkit.ExecResult {
		ran := ctr.WithWorkdir(dir).WithExec(argv, anyExit)
		code, err := exitCodeOf(ctx, ran)
		if err != nil {
			return atomkit.ExecResult{Code: -1, Stderr: fmt.Sprintf("the atom never ran: %v", err), NotRun: true}
		}
		stdout, _ := ran.Stdout(ctx)
		stderr, _ := ran.Stderr(ctx)
		return atomkit.ExecResult{Code: code, Stdout: stdout, Stderr: stderr}
	}
}
