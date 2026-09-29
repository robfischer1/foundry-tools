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
//  5. FETCHED TOOLS COME THROUGH dag.HTTP, from their own public URL.
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
	// origin is where a LOCAL run's base can be fetched from — the star's own
	// origin remote, which the pre-push hook hands over — for a tree that
	// arrived as a linked-worktree snapshot and carries no history of its own.
	// "" for the door's Jobs (their tree is fetched whole, with repo above)
	// and for a hook that named none.
	origin string
	// reask, when set, is written into every lane container past its toolchain
	// layers, so every exec after it is keyed afresh and nothing the engine
	// cached answers it (verdictFor's re-ask of a could-not-run).
	reask string

	dies *dagger.Directory

	// goMods is the tree's Go modules, read once per run: the lane check and
	// every go atom ask, and they run concurrently.
	goModsOnce sync.Once
	goMods     []string
	goModsErr  error

	// pyFiles is the tree's own .py files, read once per run for the same
	// reason and by the same discipline — the planner asks, and asking the
	// engine for a glob over the whole tree is not free.
	pyFilesOnce sync.Once
	pyFiles     []string
	pyFilesErr  error
}

// fromOrigin names where a snapshot's history is fetched from (gitReadyOn).
func (r *run) fromOrigin(url string) *run {
	r.origin = url
	return r
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
		// The fleet's record tree, for the atoms that grade the fleet rather
		// than the repo under test.
		dies: dag.Git(checks.DiesRepo).Ref(checks.DiesRef).Tree(),
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
		// A LANE IS NOT A TELEMETRY PRODUCER. The engine injects
		// OTEL_EXPORTER_OTLP_ENDPOINT — an http://127.0.0.1:<port> proxy into
		// this lane's trace — into every exec, and it overrides a value set
		// here at exec time (measured 2026-09-18: WithEnvVariable to "" and
		// `env` still printed the engine's port). A star booted by its own
		// test suite reads that endpoint as a collector and pushes its
		// bridged Prometheus registry at it; the engine accepts only Gauges,
		// and every push was an engine error line — 219 in 6h across 50 lane
		// sessions, "unknown aggregation from pb: *v1.Metric_Summary"
		// (infra#10314; nyx and arachne confirmed against a local sink). The
		// engine does not inject THIS key, so it survives, and it is the
		// spec's own switch: stellar-core-go's telemetry.Init (#66), the
		// Python SDK's opentelemetry-instrument and the JS SDK all return a
		// no-op on it. What a lane's tools say about themselves goes in the
		// verdict, not in a metric stream nothing reads.
		WithEnvVariable("OTEL_SDK_DISABLED", "true").
		// The go command's coordinates, on every lane rather than only the go
		// lane's: three variables that mean nothing to a non-Go toolchain cost
		// nothing, and a conditional here would be a second place for this
		// file and the atom table to disagree (checks.GoProxy has the
		// measurement).
		WithEnvVariable("GOPROXY", checks.GoProxy).
		WithEnvVariable("GONOSUMDB", checks.GoNoSumDB).
		WithEnvVariable("GOPRIVATE", checks.GoPrivate).
		// THE CLIENTS THAT DO NOT READ THE SYSTEM POOL. The engine installs
		// its custom CA — cache-ca, the fleet's transparent cache's signer
		// (infra cache-ca.yaml) — into every container's system store, which
		// is where go, cargo, curl, apt and pip (>= 24.2, truststore) look.
		// uv verifies against webpki's roots unless told to use the platform
		// store, and node and bun carry their own bundle unless handed extra
		// certificates. Both on every lane: a variable a toolchain does not
		// read costs nothing, and a conditional here would be a second place
		// for this file and the atom table to disagree. WITHOUT THESE, the
		// day the engine names the intercept face (infra dagger-engine.yaml,
		// dnsPolicy None -> 10.43.0.53) every `uv sync` and `bun install` on
		// the fleet refuses the cache's certificate — the F4 incident of
		// 2026-09-18, which was Go codegen doing exactly that before the CA
		// reached the engine.
		WithEnvVariable("UV_NATIVE_TLS", "1").
		WithEnvVariable("NODE_EXTRA_CA_CERTS", "/etc/ssl/certs/ca-certificates.crt").
		// AND THE certifi READERS. pip (as a client, not the truststore
		// feature), requests and urllib3 verify against certifi's bundled
		// roots, not the system store — measured 2026-09-19 ~01:05Z, the
		// first hour of the engine on the intercept face: iris's python
		// lane, `python:pip-audit` (pip-audit -> requests -> pypi.org)
		// answered CERTIFICATE_VERIFY_FAILED "unable to get local issuer"
		// while uv on the same lane resolved fine under UV_NATIVE_TLS
		// (Turing21's record on the bus). Every python star's gate was red
		// on that one atom until these landed. The three names the
		// ecosystem reads, all pointing at the same system bundle the
		// engine writes the CA into.
		WithEnvVariable("SSL_CERT_FILE", "/etc/ssl/certs/ca-certificates.crt").
		WithEnvVariable("REQUESTS_CA_BUNDLE", "/etc/ssl/certs/ca-certificates.crt").
		WithEnvVariable("PIP_CERT", "/etc/ssl/certs/ca-certificates.crt")
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

// code is the tree under check less what no toolchain reads
// (checks.InertPaths): the README, the changelog, the governance, the hooks,
// the justfiles, pre-commit's and copier's files. An exec keyed on this
// mount is keyed on the code, so an edit to any of those re-runs nothing.
func (r *run) code() *dagger.Directory {
	return r.src.Filter(dagger.DirectoryFilterOpts{Exclude: checks.InertPaths})
}

// laneCode is lane on the narrowed tree — for the atoms that compile, vet,
// lint or test and never run git against the mount. The git-reading atoms
// (the mutation lane, the witness) stay on lane: an excluded file reads as
// deleted to a working-tree diff, and their cache key already carries the
// pull.
func (r *run) laneCode(image string) *dagger.Container {
	return r.laneBase(image).
		WithMountedDirectory("/src", r.code()).
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
			WithFile("/usr/local/bin/opengrep", dag.HTTP(checks.OpengrepURL), dagger.ContainerWithFileOpts{Permissions: 0o755}).
			WithExec([]string{"opengrep", "--version"}).
			WithExec([]string{"go", "install", checks.GomutantsModule}).
			WithExec([]string{"go", "install", checks.MutationGateModule}).
			WithExec([]string{"go", "install", checks.StaticcheckModule}).
			WithExec([]string{"go", "install", checks.GovulncheckModule}).
			WithExec([]string{"staticcheck", "-version"}).
			WithExec([]string{"govulncheck", "-version"})
	case checks.ImagePython:
		// python:slim carries python3 and tar and nothing else the atoms
		// exec: git and curl come from apt, uv/uvx out of their own image,
		// opengrep from its release URL. opa and oras the dies and sweep
		// atoms fetch themselves.
		uv := dag.Container().From(checks.ImageUV)
		return ctr.
			WithExec([]string{"apt-get", "update"}).
			WithExec([]string{"apt-get", "install", "-y", "--no-install-recommends", "git", "curl", "ca-certificates"}).
			WithExec([]string{"rm", "-rf", "/var/lib/apt/lists"}).
			WithFile("/usr/local/bin/uv", uv.File("/uv")).
			WithFile("/usr/local/bin/uvx", uv.File("/uvx")).
			WithFile("/usr/local/bin/opengrep", dag.HTTP(checks.OpengrepURL), dagger.ContainerWithFileOpts{Permissions: 0o755}).
			WithExec([]string{"uv", "--version"}).
			WithExec([]string{"opengrep", "--version"})
	case checks.ImageRust:
		// AND A PYTHON THE FLEET CAN ACTUALLY RUN. rust:bookworm ships 3.11.2
		// and the fleet standardised on 3.14; a repo whose cargo tests spawn
		// its own python hooks (cerberus' memory_plugin.rs) ran them through
		// that 3.11 and met a SyntaxError on syntax the fleet's own formatter
		// had emitted. Installed with uv rather than apt because no Debian
		// suite packages 3.14 yet, and symlinked over `python3` because the
		// spawning code names the interpreter and not a path — see
		// checks.FleetPython for the measurement.
		//
		// rust:bookworm carries cargo, git, curl and bash. rustfmt and
		// clippy are rustup components; cargo-audit, cargo-mutants and
		// cargo-nextest are built from source at their pins — minutes on the
		// first run, a cached layer on every later one. mold is bookworm's
		// package: the mutation atom links every mutant's test binaries
		// through it (`mold -run`), and linking was the larger half of a
		// mutant's build.
		rustUV := dag.Container().From(checks.ImageUV)
		return ctr.
			WithExec([]string{"apt-get", "update"}).
			WithExec([]string{"apt-get", "install", "-y", "--no-install-recommends", "mold"}).
			WithExec([]string{"rm", "-rf", "/var/lib/apt/lists"}).
			WithFile("/usr/local/bin/uv", rustUV.File("/uv")).
			// --default WRITES THE SHIMS, so no `ln -s` and so no shell: uv
			// lays python/python3/python3.14 into UV_PYTHON_BIN_DIR itself.
			// TestNoAtomExecsAShell is right to refuse the alternative, and
			// this repo spent a lot of effort getting off `sh -c`.
			WithEnvVariable("UV_PYTHON_BIN_DIR", "/usr/local/bin").
			WithExec([]string{"uv", "python", "install", "--default", checks.FleetPython}).
			WithExec([]string{"python3", "--version"}).
			WithExec([]string{"rustup", "component", "add", "rustfmt", "clippy"}).
			WithFile("/usr/local/bin/opengrep", dag.HTTP(checks.OpengrepURL), dagger.ContainerWithFileOpts{Permissions: 0o755}).
			WithExec([]string{"cargo", "install", "cargo-audit", "--locked", "--version", checks.CargoAuditVersion}).
			WithExec([]string{"cargo", "install", "cargo-mutants", "--locked", "--version", checks.CargoMutantsVersion}).
			WithExec([]string{"cargo", "install", "cargo-nextest", "--locked", "--version", checks.CargoNextestVersion}).
			WithExec([]string{"cargo", "fmt", "--version"}).
			WithExec([]string{"cargo", "clippy", "--version"}).
			WithExec([]string{"cargo", "mutants", "--version"}).
			WithExec([]string{"cargo", "nextest", "--version"}).
			WithExec([]string{"mold", "--version"})
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
			WithFile("/usr/local/bin/opengrep", dag.HTTP(checks.OpengrepURL), dagger.ContainerWithFileOpts{Permissions: 0o755}).
			WithExec([]string{"node", "--version"}).
			WithExec([]string{"opengrep", "--version"})
	}
	return ctr
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

// changeBase answers the commit a pull's change set is measured FROM: the
// merge base of the door's base sha and HEAD. "" with a nil error is a base
// the history does not reach, and the atom stands down as it does with no
// base at all.
//
// THE BASE IS MAIN'S TIP AT DISPATCH, NOT THE BRANCH POINT. `git diff base
// HEAD` compares two trees, so once main has moved past where the pull
// branched, every landing since reads as the pull's own change, reversed.
// MEASURED urania #63 (2026-09-18 00:52Z): the pull added hooks/ and an
// answers file; main had retired src/urania/*.py in the meantime; the ten
// deleted modules diffed as ADDED, python:mutation mutated the vestigial
// package against its stale suite and settled FINDINGS at 68.9% on a pull
// that changed no Python. go:mutation already diffed --merge-base; this is
// the one resolve every diff-scoped atom reads, and the sha it answers is
// what the tools that diff for themselves (gremlins --diff, forge-testkit
// scope --base) are handed too, because they take a two-tree diff as well.
//
// rev-parse --verify --quiet first, not merge-base alone: a missing object is
// exit 128 from merge-base, which the engine reports as its own error even
// under Expect ANY (foundry-tools#63); rev-parse answers 1. A merge-base that
// exits non-zero (unrelated histories) or answers nothing is an error the
// atom settles as could-not-run — a change set with no origin is not a
// change set nothing touched.
func (r *run) changeBase(ctx context.Context, ctr *dagger.Container) (string, error) {
	_, code, err := output(ctx, ctr.WithExec([]string{"git", "rev-parse", "--verify", "--quiet", r.base + "^{commit}"}, anyExit))
	if err != nil {
		return "", fmt.Errorf("the atom never ran: %w", err)
	}
	if code != 0 {
		return "", nil
	}
	out, code, err := output(ctx, ctr.WithExec([]string{"git", "merge-base", r.base, "HEAD"}, anyExit))
	if err != nil {
		return "", fmt.Errorf("the atom never ran: %w", err)
	}
	since := strings.TrimSpace(out)
	if code != 0 || since == "" {
		return "", fmt.Errorf("git found no merge base between the base %s and HEAD (exit %d): %s", r.base, code, out)
	}
	return since, nil
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

// audit is verdict for the four dependency audits, which ask a live advisory
// source and so can fail for the source's reasons rather than the tree's:
// checks.AuditVerdict reads a network fault off a non-zero exit as could-not-run,
// which is what makes verdictFor ask it again past the cache.
func audit(ctx context.Context, a checks.AtomDef, ctr *dagger.Container) checks.Verdict {
	code, err := ctr.ExitCode(ctx)
	if err != nil {
		return checks.VerdictOf(a, 2, fmt.Sprintf("the atom never ran: %v", err))
	}
	stdout, _ := ctr.Stdout(ctx)
	stderr, _ := ctr.Stderr(ctx)
	return checks.AuditVerdict(a, code, stdout+stderr)
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
	return r.gitReadyOn(ctx, ctr, r.src)
}

// gitReadyOn is gitReady for a container whose /src is tree — r.src, or the
// narrowed r.code() — so the mount swap below re-mounts the same tree the
// caller chose, less the dangling .git file.
//
// THE HISTORY IS THE DOOR'S WHEN THE HOOK SAYS WHERE IT LIVES (CA F16, Rob
// 2026-09-19: "let dagger grab the tree"). A snapshot with no history made
// every diff-scoped atom stand down: go:mutation answered 0 with "no usable PR
// base sha" on the hook while the door's Job found 244 missed mutants on the
// same sha (terpsichore 1c28f38, foundry-tools#10253) — blind exactly where
// it runs. With --origin and --base named, the ENGINE fetches the base commit
// from the door (a git-sourced tree, cached by commit, its .git a real
// repository at that commit), the snapshot's files replace the checkout, and
// one commit on top makes HEAD the tree as pushed with the base as its parent.
// `git diff <base>` is then the same change set the door's clone gives —
// deletions included, because the snapshot REPLACES the checkout rather than
// being laid over it. The snapshot marker stays: one synthetic commit is not
// a change set fleet:witness can grade, and the door's Job still grades the
// real commits.
func (r *run) gitReadyOn(ctx context.Context, ctr *dagger.Container, tree *dagger.Directory) *dagger.Container {
	gitdir, err := r.src.File(".git").Contents(ctx)
	if err == nil && r.origin != "" && r.base != "" {
		history := dag.Git(r.origin).Ref(r.base).Tree().Directory(".git")
		return ctr.
			WithMountedDirectory("/src", tree.WithoutFile(".git").WithDirectory(".git", history)).
			WithExec([]string{"git", "config", "--global", "--add", "safe.directory", "*"}).
			WithExec([]string{"git", "config", "--local", "ca.snapshot", "linked-worktree"}).
			WithExec([]string{"git", "add", "-A"}).
			WithExec([]string{"git", "-c", "user.name=ca", "-c", "user.email=ca@notusmi.com", "commit", "-q", "--allow-empty", "-m", "snapshot: the working tree as pushed, on " + r.base})
	}
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
		ctr = ctr.WithMountedDirectory("/src", tree.WithoutFile(".git"))
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

// fetchTool resolves a pinned binary the lane images do not carry, from the
// tool's own public URL. THE ENGINE FETCHES IT, and the engine is where the
// fleet's caching lives: inside the cluster the asset hosts resolve to the
// transparent cache once the engine names the intercept face
// (infra coredns-custom.yaml), the engine trusts cache-ca, and the same URL
// outside the cluster is simply the upstream. There is no mirror address
// to try first — Nexus's github-raw route retired with Nexus (master-plan
// "Transparent Cache — Nexus Retired", F8), and a second address here would
// be the fleet-specific name the plan exists to remove.
//
// A FAILED FETCH IS THE ERROR — the caller files state 2 — never a
// fallthrough: a spec that was never parsed and a policy suite that never ran
// are not a clean tree.
func fetchTool(ctx context.Context, url string) (*dagger.File, error) {
	f := dag.HTTP(url)
	if _, err := f.Sync(ctx); err != nil {
		return nil, fmt.Errorf("could not fetch %s: %w", url, err)
	}
	return f, nil
}
