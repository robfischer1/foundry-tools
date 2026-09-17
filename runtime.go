package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE ATOMS ARE DAGGER CHAINS, NOT SHELL. Rob, 2026-09-12: "It's dagger. It
// has an SDK with native integration with nearly every language we use.
// Atoms should be authored as dagger modules." Until then every atom was a
// POSIX script in a Go string, run through ONE `sh -c` — which handed the
// engine an opaque step it could neither cache in parts nor schedule, and
// re-provisioned every toolchain on every run (measured: `go mod download`
// five times per gate, zero cache volumes, 48 atoms strictly serial).
//
// The shell is gone. The last 48 scripts, AtomDef.Script and the legacyVerdict
// bridge that ran them left together once registry.go named every atom.
//
// This file is the runtime every typed atom is built from. The rules it sets,
// and that atoms_*.go follow:
//
//  1. PROVISIONING IS ITS OWN WithExec WITH THE DEFAULT Expect. A failure is a
//     Dagger error, and verdict() files it as state 2 — could not run — with
//     the error text. There is no guard() and there is no `|| exit 2`.
//  2. THE TOOL RUN IS THE LAST EXEC, WITH anyExit. Its exit code reaches the
//     verdict: 0 pass, 1 findings, anything else could-not-run
//     (checks.StateFor). A tool whose own codes mean something else is
//     remapped by a pure function in internal/checks, never by a case
//     statement in shell.
//  3. ABSENCE IS DECIDED IN GO FROM THE DIRECTORY, before any container runs.
//  4. FILE LISTS ARE COMPUTED IN GO (population) AND PASSED AS ARGUMENTS.
//  5. FETCHED TOOLS COME THROUGH dag.HTTP, mirror first, upstream second.
//  6. A SCRIPT THAT IS THE TOOL (foundry-stocks' python and bash) stays the
//     tool: one exec per script, or one per phase.
//  7. NO `sh -c` ANYWHERE. A pipe is a sign the rest belongs in Go. Asserted
//     over the source, not left as prose: registry_test.go's
//     TestNoLaneFileExecsAShell reads every atoms_*.go for the composite.
//  8. GATE_BASE REACHES ONLY THE ATOMS THAT READ IT, so every other atom's
//     cache key is a function of the tree alone, not of the pull.

// anyExit lets the tool's own exit code reach the verdict instead of failing
// the chain. Provisioning steps do NOT take it.
var anyExit = dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny}

// run is one Verdicts call — or one `+check` call — over one tree: the
// repository under check, the change set's base the door named, and the two
// shared trees (foundry-stocks, foundry-dies) built ONCE so every atom that
// mounts them shares the fetch.
type run struct {
	src  *dagger.Directory
	repo string
	base string
	// reask, when set, is written into every lane container past its toolchain
	// layers, so every exec after it is keyed afresh and nothing the engine
	// cached answers it (verdictFor's re-ask of a could-not-run).
	reask string

	stocks  *dagger.Directory
	dies    *dagger.Directory
	testkit *dagger.Directory

	// goMods is the tree's Go modules, read once per run: the lane check and
	// every go atom ask, and they run concurrently.
	goModsOnce sync.Once
	goMods     []string
	goModsErr  error
}

// reasked is r keyed afresh: every lane exec carries the nonce.
func (r *run) reasked(nonce string) *run {
	r.reask = nonce
	return r
}

func newRun(src *dagger.Directory, repo, base string) *run {
	return &run{
		src:  src,
		repo: repo,
		base: base,
		// The canonical scripts and rulesets, READ AT THEIR ONE HOME rather than
		// vendored (checks.StocksRepo says why). Lazy: nothing is fetched until
		// an atom mounts it.
		stocks: dag.Git(checks.StocksRepo).Ref(checks.StocksRef).Tree(),
		// The fleet's record tree, for the atoms that grade the fleet rather
		// than the repo under test.
		dies: dag.Git(checks.DiesRepo).Ref(checks.DiesRef).Tree(),
		// The mutation gate's knobs, at their one home (checks.TestkitRepo says
		// why). Lazy too: only go:mutation reads it.
		testkit: dag.Git(checks.TestkitRepo).Ref(checks.TestkitRef).Tree(),
	}
}

// laneBase is the container every atom starts from: the pinned lane image, the
// environment the fleet's toolchains need and the toolchain caches for that
// image — everything but the tree, for a step that mounts only the files it
// reads.
//
// THE CACHES ARE THE POINT. checks.CachesFor names, per image, the directories
// its toolchain writes to — go's module and build caches, uv's cache, cargo's
// registry, bun's install cache — and each is a Dagger cache volume that
// persists on the engine across runs, seeded on first creation from the
// image's own warm layer. A gate's second run downloads nothing it downloaded
// on its first.
func (r *run) laneBase(image string) *dagger.Container {
	ctr := dag.Container().From(image).
		// worktree-guard and every other hook that stands down under CI reads
		// this. The engine IS the CI boundary; saying so beats each atom
		// guessing.
		WithEnvVariable("CI", "true").
		// The go command's coordinates, on every lane rather than only the go
		// lane's: three variables that mean nothing to a non-Go toolchain cost
		// nothing, and a conditional here would be a second place for this
		// file and the atom table to disagree (checks.GoProxy has the
		// measurement).
		WithEnvVariable("GOPROXY", checks.GoProxy).
		WithEnvVariable("GONOSUMDB", checks.GoNoSumDB).
		WithEnvVariable("GOPRIVATE", checks.GoPrivate)
	ctr = provision(ctr, image)
	for _, c := range checks.CachesFor(image) {
		opts := dagger.ContainerWithMountedCacheOpts{}
		if c.Seed {
			opts.Source = dag.Container().From(image).Directory(c.Path)
		}
		ctr = ctr.WithMountedCache(c.Path, dag.CacheVolume(c.Key), opts)
		if c.EnvVar != "" {
			ctr = ctr.WithEnvVariable(c.EnvVar, c.Path)
		}
	}
	if r.reask != "" {
		ctr = ctr.WithEnvVariable("CA_REASK", r.reask)
	}
	return ctr
}

// lane is laneBase with the tree under check mounted at /src.
func (r *run) lane(image string) *dagger.Container {
	return r.laneBase(image).
		WithMountedDirectory("/src", r.src).
		WithWorkdir("/src")
}

// provision installs what a lane's atoms exec that the upstream toolchain
// image does not carry — the work the fleet's own CI images used to bake
// (Rob, 2026-09-12: "We're going to stop maintaining CI images. We'll
// leverage dagger's caching instead."). ONE EXEC PER TOOL, EVERY VERSION
// PINNED (checks/images.go), IN VOLATILITY ORDER: the distro packages the
// image lacks, then the stable binaries copied out of their own images, then
// the pinned scanners, then the tools built from source at the versions that
// move most. The engine caches each exec by its inputs, so a tool is fetched
// or compiled once per pin and a cache hit survives everything but the last
// layer moving. Runs BEFORE the cache volumes are mounted, so a layer never
// depends on what a volume happens to hold.
//
// NO SHELL. Every step is an argv the engine runs directly, exactly as the
// atoms are; a download is dag.HTTP into the container, never `curl | sh`.
func provision(ctr *dagger.Container, image string) *dagger.Container {
	switch image {
	case checks.ImageGo:
		// golang:bookworm carries git and curl, which is all the go atoms exec
		// besides the tools below; the mutation gate scores in Go now, so the
		// python3 go_score.py needed is not installed.
		return ctr.
			WithFile("/usr/local/bin/opengrep", dag.HTTP(checks.OpengrepMirror), dagger.ContainerWithFileOpts{Permissions: 0o755}).
			WithExec([]string{"opengrep", "--version"}).
			WithExec([]string{"go", "install", checks.GremlinsModule}).
			WithExec([]string{"go", "install", checks.MutationGateModule}).
			WithExec([]string{"go", "install", checks.StaticcheckModule}).
			WithExec([]string{"go", "install", checks.GovulncheckModule}).
			WithExec([]string{"staticcheck", "-version"}).
			WithExec([]string{"govulncheck", "-version"})
	case checks.ImagePython:
		// python:slim carries python3 and tar and nothing else the atoms
		// exec: git and curl come from apt, uv/uvx out of their own image,
		// opengrep from the mirror. opa and oras the dies and sweep atoms
		// fetch themselves.
		uv := dag.Container().From(checks.ImageUV)
		return ctr.
			WithExec([]string{"apt-get", "update"}).
			WithExec([]string{"apt-get", "install", "-y", "--no-install-recommends", "git", "curl", "ca-certificates"}).
			WithExec([]string{"rm", "-rf", "/var/lib/apt/lists"}).
			WithFile("/usr/local/bin/uv", uv.File("/uv")).
			WithFile("/usr/local/bin/uvx", uv.File("/uvx")).
			WithFile("/usr/local/bin/opengrep", dag.HTTP(checks.OpengrepMirror), dagger.ContainerWithFileOpts{Permissions: 0o755}).
			WithExec([]string{"uv", "--version"}).
			WithExec([]string{"opengrep", "--version"})
	case checks.ImageRust:
		// rust:bookworm carries cargo, git, curl and bash. rustfmt and
		// clippy are rustup components; cargo-audit and cargo-mutants are
		// built from source at their pins — minutes on the first run, a
		// cached layer on every later one.
		return ctr.
			WithExec([]string{"rustup", "component", "add", "rustfmt", "clippy"}).
			WithFile("/usr/local/bin/opengrep", dag.HTTP(checks.OpengrepMirror), dagger.ContainerWithFileOpts{Permissions: 0o755}).
			WithExec([]string{"cargo", "install", "cargo-audit", "--locked", "--version", checks.CargoAuditVersion}).
			WithExec([]string{"cargo", "install", "cargo-mutants", "--locked", "--version", checks.CargoMutantsVersion}).
			WithExec([]string{"cargo", "fmt", "--version"}).
			WithExec([]string{"cargo", "clippy", "--version"}).
			WithExec([]string{"cargo", "mutants", "--version"})
	case checks.ImageTS:
		// bun:slim runs as the bun user and carries neither git nor node;
		// ts:mutation runs stryker under node. Root for the installs, then back.
		// procps, for ps: Stryker tears its test-runner processes down with
		// tree-kill, which spawns `ps -o pid --no-headers --ppid <pid>`. With no
		// ps on PATH the spawn's unhandled ENOENT kills Stryker right after the
		// dry run, so every ts:mutation run read CANNOT RUN with no report
		// (theia #57, 2026-09-13).
		node := dag.Container().From(checks.ImageNode)
		return ctr.
			WithUser("root").
			WithExec([]string{"apt-get", "update"}).
			WithExec([]string{"apt-get", "install", "-y", "--no-install-recommends", "git", "curl", "ca-certificates", "procps"}).
			WithExec([]string{"rm", "-rf", "/var/lib/apt/lists"}).
			WithFile("/usr/local/bin/node", node.File("/usr/local/bin/node")).
			WithFile("/usr/local/bin/opengrep", dag.HTTP(checks.OpengrepMirror), dagger.ContainerWithFileOpts{Permissions: 0o755}).
			WithExec([]string{"node", "--version"}).
			WithExec([]string{"opengrep", "--version"})
	}
	return ctr
}

// withStocks mounts foundry-stocks at /stocks — the rulesets and the scripts
// that ARE some atoms' tool. checks.RulesetsDir and the script paths are
// spelled against this mount.
func (r *run) withStocks(ctr *dagger.Container) *dagger.Container {
	return ctr.WithMountedDirectory("/stocks", r.stocks)
}

// withDies mounts foundry-dies at /dies and names it in the environment: the
// tests that read it live in other repositories, and two spellings of one
// path is the shape this helper exists to end.
func (r *run) withDies(ctr *dagger.Container) *dagger.Container {
	return ctr.
		WithMountedDirectory("/dies", r.dies).
		WithEnvVariable("FOUNDRY_DIES", "/dies")
}

// withBase hands an atom the change set's base. ONLY the atoms that judge the
// change (fleet:witness, the mutation lane) call this — on every other atom
// it would make the cache key a function of the pull rather than of the tree.
func (r *run) withBase(ctr *dagger.Container) *dagger.Container {
	ctr = ctr.WithEnvVariable("GATE_BASE", r.base)
	// THE BASE RIDES IN WITH A FETCHED TREE. When the engine fetched Source
	// (New --repo/--sha) it fetched ONE ref's history, and the base the door
	// names is the pull's base branch at dispatch — a commit that history does
	// not reach. The runner's old clone had every head; the fetched tree has
	// its own. MEASURED 2026-09-14 02:10Z, the first gate under the fetch
	// (gate-infra-8b2ea83): `git diff 049baaf6..HEAD` exit 128, fleet:witness
	// CANNOT RUN on every pull, tips untouched (an empty base reads HEAD^).
	// So the base is fetched here, from the same door, by sha — the door
	// serves a raw-sha want (verified) — and ONLY here: this is the one seam
	// the base crosses (rule 8), so the fetch lands only in the atoms whose
	// cache key already carries the pull. `-c safe.directory=*` because this
	// exec precedes gitReady's global config in every chain that wraps it.
	if r.repo != "" && r.base != "" {
		ctr = ctr.WithExec([]string{"git", "-c", "safe.directory=*", "-C", "/src", "fetch", "--quiet", "--no-tags", r.repo, r.base})
	}
	return ctr
}

// population is THE GATE'S OWN POPULATION: the files the repository would
// commit, minus what the fleet excludes, matching the patterns given (every
// file when none is given). Patterns are Dagger globs — "**/*.yml".
//
// NO git IS INVOLVED. The engine's own gitignore filter yields the same set
// `git ls-files -o --exclude-standard` did, without the forty-line prelude
// that used to build a throwaway repository so git could be asked. The fleet
// exclude (checks.GatePopulation) is applied in Go, where it is tested.
func (r *run) population(ctx context.Context, patterns ...string) ([]string, error) {
	tracked := r.src.Filter(dagger.DirectoryFilterOpts{
		Gitignore: true,
		Exclude:   []string{".git"},
	})
	if len(patterns) == 0 {
		patterns = []string{"**"}
	}
	seen := map[string]bool{}
	var files []string
	for _, p := range patterns {
		matches, err := tracked.Glob(ctx, p)
		if err != nil {
			return nil, fmt.Errorf("could not enumerate the tree for %q: %w", p, err)
		}
		for _, m := range matches {
			if !seen[m] {
				seen[m] = true
				files = append(files, m)
			}
		}
	}
	sort.Strings(files)
	return checks.GatePopulation(files), nil
}

// verdict is the one place a chain becomes a verdict. It evaluates the chain:
// a provisioning exec that failed, an image that would not pull, an engine
// that went away all surface as the error — state 2, could not run, never a
// pass. Otherwise the last exec's exit code is the state and its output is
// the result.
func verdict(ctx context.Context, a checks.AtomDef, ctr *dagger.Container) checks.Verdict {
	code, err := ctr.ExitCode(ctx)
	if err != nil {
		return checks.VerdictOf(a, 2, fmt.Sprintf("the atom never ran: %v", err))
	}
	stdout, _ := ctr.Stdout(ctx)
	stderr, _ := ctr.Stderr(ctx)
	return checks.VerdictOf(a, code, stdout+stderr)
}

// output evaluates a chain whose last exec is a QUESTION rather than the
// verdict — a count, a listing — and answers what it printed and how it
// exited. The last exec must carry anyExit. THE TWO FAILURES ARE DISTINCT:
// an error is the engine's (a mount that would not evaluate, an image that
// would not pull) and is the caller's state 2; a non-zero code is the
// tool's, and what it means is the caller's to decide.
func output(ctx context.Context, ctr *dagger.Container) (stdout string, code int, err error) {
	code, err = ctr.ExitCode(ctx)
	if err != nil {
		return "", 0, err
	}
	stdout, err = ctr.Stdout(ctx)
	if err != nil {
		return "", 0, err
	}
	if code != 0 {
		stderr, _ := ctr.Stderr(ctx)
		stdout += stderr
	}
	return strings.TrimSpace(stdout), code, nil
}

// outputBoth evaluates a chain whose last exec carries anyExit and answers
// EVERYTHING it printed — stdout then stderr — with the tool's own exit code.
//
// It is output()'s sibling, and the difference is the whole reason it exists:
// output() folds stderr in only when the code is non-zero, which is right when
// stderr is an error report and wrong when it is half the measurement.
// kubeconform prints its summary to either stream and the summary is the count
// this atom refuses a zero of; kube-linter prints its zero-population refusal
// to stderr with the same exit code it uses for findings. Both have to read
// both on a clean exit.
//
// THE TWO FAILURES STAY DISTINCT, as in output(): err is the engine's (state 2
// for the caller) and code is the tool's (the caller's to interpret).
func outputBoth(ctx context.Context, ctr *dagger.Container) (out string, code int, err error) {
	code, err = ctr.ExitCode(ctx)
	if err != nil {
		return "", 0, err
	}
	stdout, err := ctr.Stdout(ctx)
	if err != nil {
		return "", 0, err
	}
	stderr, err := ctr.Stderr(ctx)
	if err != nil {
		return "", 0, err
	}
	return stdout + stderr, code, nil
}

// fileList is where a population too long for an argv is handed to the tool.
//
// RULE 4 SAYS THE LIST IS COMPUTED IN GO AND PASSED AS ARGUMENTS, and for most
// atoms the argument vector is where it ends. The fleet lane is the exception:
// its population is EVERY file in the repository, and infra's YAML alone runs
// to thousands of paths. A NUL-joined file read by `xargs -0 -a` is still a
// typed exec of one program with a fixed argument vector — the list is data on
// disk rather than a word-split shell expansion, which is exactly what `$files`
// was not.
const fileList = "/tmp/files0"

// withFileList writes the population where xargs will read it. NUL-joined
// because a path may contain anything but a NUL, which is the whole reason
// `-print0`/`-0` exists; the shell's `tr '\n' '\0'` was the same idea one
// process later.
func withFileList(ctr *dagger.Container, files []string) *dagger.Container {
	return ctr.WithNewFile(fileList, strings.Join(files, "\x00"))
}

// xargsExec is the tool run over that population: one program, its fixed
// arguments, and the file list appended by xargs in as many invocations as the
// argv takes. NOT a pipe and not a shell — xargs is the exec'd binary.
func xargsExec(tool ...string) []string {
	return append([]string{"xargs", "-0", "-a", fileList}, tool...)
}

// gitReady makes /src readable BY git, for the atoms that read the repository
// through it — history (fleet:witness, the mutation lane), the origin URL
// (stop_justifications' repo_name), a revision (the dies bundle). Most atoms
// never need it: population() answers the file list without git.
//
// A LINKED WORKTREE'S `.git` IS A FILE, AND IT DANGLES IN HERE. It holds
// `gitdir: <primary>/.git/worktrees/<name>`, an absolute host path that does
// not exist inside the container, so every git command answers "fatal: not a
// git repository". The pre-push hook hands the engine a worktree, and this
// fleet works in worktrees, so it is the COMMON case for a local run (the
// door's Job clones whole, and its `.git` is a directory — untouched here).
// The tree is given a throwaway repository whose index holds the committable
// files, and origin is reconstructed from the gitdir path because an
// exemption keyed on the repository must not evaporate because the push came
// from a worktree.
func (r *run) gitReady(ctx context.Context, ctr *dagger.Container) *dagger.Container {
	gitdir, err := r.src.File(".git").Contents(ctx)
	if err == nil {
		// THE MOUNT SWAP COMES FIRST, before ANY git command — including the
		// --global config below, which needs no repository. git discovers the
		// repository at startup regardless of the subcommand, and a `.git` file
		// whose gitdir does not exist is a hard error there, not a "no repo":
		// `git config --global` in /src answered "fatal: not a git repository:
		// /home/rob/Forge/Outputs/eros/.git/worktrees/<name>" and exited 128,
		// so every atom behind this helper never ran (foundry-tools#8736,
		// measured 2026-09-12 on eros and ares worktrees against the cluster
		// engine and a local one; the f0566e9b shell prelude removed the file
		// first and passed). The rebuild below never got to run.
		ctr = ctr.WithMountedDirectory("/src", r.src.WithoutFile(".git"))
	}
	// The clone is owned by whoever made it; git refuses a repository it does
	// not own (exit 128, "dubious ownership") and the process here is root.
	ctr = ctr.WithExec([]string{"git", "config", "--global", "--add", "safe.directory", "*"})
	if err != nil {
		// `.git` is a directory (a primary checkout) or absent: nothing to rebuild.
		return ctr
	}
	// The snapshot is MARKED, in the one place every atom that reads the
	// repository can see it: the throwaway's own config. A snapshot has no
	// commits, so an atom that needs a change set (fleet:witness reads HEAD)
	// cannot answer here and must say so instead of exiting 128 — the landing's
	// Job clones whole and grades the real commits.
	ctr = ctr.
		WithExec([]string{"git", "init", "-q", "."}).
		WithExec([]string{"git", "config", "--local", "ca.snapshot", "linked-worktree"}).
		WithExec([]string{"git", "add", "-A"})
	if primary := checks.WorktreePrimary(gitdir); primary != "" {
		ctr = ctr.WithExec([]string{"git", "remote", "add", "origin", primary + ".git"})
	}
	return ctr
}

// fetchTool resolves a pinned binary the lane images do not carry: the Nexus
// mirror first, upstream second. BOTH FAILING IS THE ERROR — the caller files
// state 2 — never a fallthrough: a spec that was never parsed and a policy
// suite that never ran are not a clean tree.
func fetchTool(ctx context.Context, mirror, upstream string) (*dagger.File, error) {
	var failures []string
	for _, url := range []string{mirror, upstream} {
		f := dag.HTTP(url)
		if _, err := f.Sync(ctx); err == nil {
			return f, nil
		} else {
			failures = append(failures, fmt.Sprintf("%s: %v", url, err))
		}
	}
	return nil, fmt.Errorf("could not fetch from the mirror or from upstream: %s", strings.Join(failures, "; "))
}
