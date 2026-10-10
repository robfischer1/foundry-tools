package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
	"dagger/foundry-tools/internal/devlane"
)

// Dev is the interactive build/test surface: `dagger call dev --source=. test`
// runs the repository's own toolchain on the cluster's engine, so a workstation
// needs neither Go nor Rust.
//
// IT IS THE GATE'S LANES, NOT A SECOND SET. The containers are run.lane /
// laneCode / cargoFresh (the same pinned images, proxy coordinates and cache
// volumes), so a verb's answer agrees with the gate's. What differs is only what
// an interactive loop wants: the tool's own output, passthrough arguments, and
// the files a rewriting verb changed.
//
// THE CARGO TARGET IS KEYED ON REPOSITORY AND TREE, AND MOUNTED LOCKED
// (checks.CachesForDev). GOCACHE, GOMODCACHE and cargo's registry are
// content-addressed volumes shared fleet-wide; cargo's target is not, because
// every tree mounts at /src and two trees of one crate share artifact paths.
// The tree (--tree, the worktree path) is in the key so worktrees of one repo
// each keep a warm target and do not queue on each other; LOCKED holds the
// volume for the whole exec, test binaries included, so a same-key collision
// waits rather than running the other tree's binary. A source whose identity
// cannot be found is refused, never given the fleet-shared target.
type Dev struct {
	// +private
	Source *dagger.Directory
	// +private
	Lang string
	// +private
	Repo string
	// +private
	Entries []string
	// +private
	Tree string
}

// Dev binds the interactive surface to a source tree.
func (m *FoundryTools) Dev(
	ctx context.Context,
	// The repository to drive: pass --source=. from inside it. (The default is
	// the module's own checkout, which is never what you meant.)
	//
	// target/, node_modules and the rest of the gate's upload ignore never leave
	// the host (a 6GB target/ would be the whole latency), and .git stays home
	// too: the loop reads no history, and a linked worktree's .git is a dangling
	// pointer in here anyway. devlane.Ignore is this list, held to it by a test.
	//
	// +defaultPath="/"
	// +ignore=["**/node_modules","**/.venv","**/target","**/__pycache__","**/.pytest_cache","**/.mypy_cache","**/.ruff_cache","**/dist",".melt",".claude",".specify",".furnace",".git"]
	source *dagger.Directory,
	// go or rust. Read off go.mod / Cargo.toml when empty.
	// +optional
	lang string,
	// The repository's identity, which the cargo target cache is keyed on.
	// Defaults to the Go module path or the Cargo package name, which every
	// worktree of one repo shares.
	// +optional
	repo string,
	// The worktree this call runs for (the justfile passes its own directory).
	// Hashed into the cargo target's cache key, so the worktrees of one repo
	// each keep a warm target and never queue on or overwrite each other's.
	// +optional
	tree string,
) (*Dev, error) {
	entries, err := source.Entries(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not read the source root: %w", err)
	}
	lang, err = devlane.Lang(entries, lang)
	if err != nil {
		return nil, err
	}
	var manifest, lock string
	if repo == "" {
		// A manifest that will not read leaves the shared key, not an error:
		// the verbs below fail loudly on a tree that is really broken.
		if lang == devlane.Go {
			manifest, _, _ = fileIn(ctx, source, "go.mod")
		} else {
			manifest, _, _ = fileIn(ctx, source, "Cargo.toml")
			lock, _, _ = fileIn(ctx, source, "Cargo.lock")
		}
	}
	repo = devlane.RepoKey(repo, lang, manifest, lock)
	if err := devlane.Refuse(repo); err != nil {
		return nil, err
	}
	return &Dev{Source: source, Lang: lang, Repo: repo, Entries: entries, Tree: tree}, nil
}

// Build compiles. Go: go build (./... by default). Rust: cargo build
// (--workspace by default). The tool's own output; the call exits non-zero when
// the tool does.
//
// +cache="never"
func (d *Dev) Build(ctx context.Context,
	// Passed through to the tool, e.g. ./cmd/x or -p crate.
	// +optional
	args []string,
	// Run past the engine's cache even on an unchanged tree.
	// +optional
	fresh bool,
) (string, error) {
	return d.text(ctx, "build", args, false, fresh)
}

// Test runs the suite. Go: go test -race (./... by default; --race=false or
// -race=false in args turns the detector off). Rust: cargo test (--workspace by
// default; `-p crate name` narrows). The tool's own output; a failing test
// makes the call exit non-zero.
//
// +cache="never"
func (d *Dev) Test(ctx context.Context,
	// Passed through to the tool, e.g. -run TestX ./internal/foo/... or -p crate name.
	// +optional
	args []string,
	// Go only: the race detector.
	// +default=true
	race bool,
	// Run past the engine's cache even on an unchanged tree.
	// +optional
	fresh bool,
) (string, error) {
	return d.text(ctx, "test", args, race, fresh)
}

// Vet runs go vet (Go only).
//
// +cache="never"
func (d *Dev) Vet(ctx context.Context,
	// +optional
	args []string,
	// +optional
	fresh bool,
) (string, error) {
	return d.text(ctx, "vet", args, false, fresh)
}

// Check runs cargo check (Rust only).
//
// +cache="never"
func (d *Dev) Check(ctx context.Context,
	// +optional
	args []string,
	// +optional
	fresh bool,
) (string, error) {
	return d.text(ctx, "check", args, false, fresh)
}

// Clippy runs cargo clippy under the fleet's lint set, warnings denied (Rust
// only). An `--` in args supplies your own lints in place of the fleet's.
//
// +cache="never"
func (d *Dev) Clippy(ctx context.Context,
	// +optional
	args []string,
	// +optional
	fresh bool,
) (string, error) {
	return d.text(ctx, "clippy", args, false, fresh)
}

// Fmt formats in place and answers the files it changed, for
// `dagger call dev fmt export --path=.`. Go: gofmt -w. Rust: cargo fmt --all.
// Only changed files come back, so the export touches nothing else.
//
// +cache="never"
func (d *Dev) Fmt(ctx context.Context,
	// Rust only: passed to cargo fmt.
	// +optional
	args []string,
) (*dagger.Directory, error) {
	return d.changed(ctx, "fmt", args)
}

// Tidy runs go mod tidy (Go only) and answers the files it changed. It leaves
// vendor/ alone: on a vendored repo follow it with
// `dagger call dev --source=. vendor export --path=vendor --wipe`.
//
// +cache="never"
func (d *Dev) Tidy(ctx context.Context) (*dagger.Directory, error) {
	return d.changed(ctx, "tidy", nil)
}

// Vendor runs go mod vendor (Go only, on a tree that has vendor/) and answers
// the whole vendor directory, EMPTY if the tool removed it. Export it with
// `export --path=vendor --wipe`: the wipe is confined to the path, so stale
// files (a leftover modules.txt) are removed from vendor/ and nothing else is
// touched. A plain export adds and rewrites but never deletes, which is why
// this is a verb of its own rather than part of tidy.
//
// +cache="never"
func (d *Dev) Vendor(ctx context.Context) (*dagger.Directory, error) {
	if err := devlane.Applies(d.Lang, "vendor"); err != nil {
		return nil, err
	}
	if !devlane.HasVendor(d.Entries) {
		return nil, fmt.Errorf("this tree has no vendor/ directory: nothing to vendor")
	}
	res, err := d.ran(ctx, "vendor", nil, false, false)
	if err != nil {
		return nil, err
	}
	if res.code != 0 {
		return nil, res.settle(ctx)
	}
	ctr := res.ctr
	after, err := ctr.Directory("/src").Entries(ctx)
	if err != nil {
		return nil, fmt.Errorf("vendor could not read the tree back: %w", err)
	}
	if !devlane.HasVendor(after) {
		return dag.Directory(), nil
	}
	return ctr.Directory("/src/vendor"), nil
}

// Lock runs cargo generate-lockfile (Rust only) and answers the files it changed.
//
// +cache="never"
func (d *Dev) Lock(ctx context.Context,
	// +optional
	args []string,
) (*dagger.Directory, error) {
	return d.changed(ctx, "lock", args)
}

// Update runs cargo update (Rust only; `-p serde` narrows it) and answers the
// files it changed.
//
// +cache="never"
func (d *Dev) Update(ctx context.Context,
	// +optional
	args []string,
) (*dagger.Directory, error) {
	return d.changed(ctx, "update", args)
}

// text runs a verb whose product is the tool's output.
func (d *Dev) text(ctx context.Context, verb string, args []string, race, fresh bool) (string, error) {
	res, err := d.ran(ctx, verb, args, race, fresh)
	if err != nil {
		return "", err
	}
	if res.code != 0 {
		return "", res.settle(ctx)
	}
	return res.stdout + res.stderr, nil
}

// changed runs a verb whose product is the files it rewrote: the source diffed
// against the tree after the tool, so only what moved is exported.
func (d *Dev) changed(ctx context.Context, verb string, args []string) (*dagger.Directory, error) {
	res, err := d.ran(ctx, verb, args, false, false)
	if err != nil {
		return nil, err
	}
	if res.code != 0 {
		return nil, res.settle(ctx)
	}
	return d.Source.Diff(res.ctr.Directory("/src")), nil
}

// devRan is one verb's exec: the container after it, what it printed on each
// stream, and its exit code.
type devRan struct {
	ctr            *dagger.Container
	stdout, stderr string
	code           int
}

// settle ends the call on the tool's own failure. The reason travels as a FILE,
// not an argument, so the engine does not echo it a second time into the exec's
// span title; the two streams are tailed on their own (devlane.Reason).
func (r devRan) settle(ctx context.Context) error {
	_, err := dag.Container().From(checks.ImageStatic).
		WithFile("/usr/local/bin/verdict", helperBinary("verdict")).
		WithEnvVariable("LANE_SETTLED_AT", strconv.FormatInt(time.Now().UnixNano(), 10)).
		WithNewFile("/reason", devlane.Reason(r.stdout, r.stderr)).
		WithExec([]string{"/usr/local/bin/verdict", strconv.Itoa(devlane.SettleCode(r.code)), "@/reason"}).
		Sync(ctx)
	return err
}

// ran builds the verb's container and runs its command there.
func (d *Dev) ran(ctx context.Context, verb string, args []string, race, fresh bool) (devRan, error) {
	if err := devlane.Applies(d.Lang, verb); err != nil {
		return devRan{}, err
	}
	r := newRun(d.Source, d.Repo, "").reasked(devlane.Nonce(fresh, time.Now().UnixNano()))
	r.dev, r.tree = true, d.Tree
	ctr, argv, err := d.plan(ctx, r, verb, args, race)
	if err != nil {
		return devRan{}, err
	}
	ctr = ctr.WithExec(argv, anyExit)
	code, err := exitCodeOf(ctx, ctr)
	if err != nil {
		return devRan{}, fmt.Errorf("%s could not run: %w", verb, err)
	}
	stdout, err := ctr.Stdout(ctx)
	if err != nil {
		return devRan{}, fmt.Errorf("%s ran, and its output could not be read: %w", verb, err)
	}
	stderr, err := ctr.Stderr(ctx)
	if err != nil {
		return devRan{}, fmt.Errorf("%s ran, and its output could not be read: %w", verb, err)
	}
	return devRan{ctr: ctr, stdout: stdout, stderr: stderr, code: code}, nil
}

// plan is the container a verb starts from and the command it runs there.
func (d *Dev) plan(ctx context.Context, r *run, verb string, args []string, race bool) (*dagger.Container, []string, error) {
	if d.Lang == devlane.Go {
		return d.planGo(ctx, r, verb, args, race)
	}
	argv, err := devlane.RustArgv(verb, args)
	if err != nil {
		return nil, nil, err
	}
	switch verb {
	case "test":
		return devRepository(r.cargoFresh()), argv, nil
	case "check", "build", "clippy":
		return r.cargoFresh(), argv, nil
	}
	// fmt, lock and update rewrite files and read no lock: the gate's fetch
	// is --locked, which would refuse the very lock these exist to repair.
	return r.lane(checks.ImageRust), argv, nil
}

func (d *Dev) planGo(ctx context.Context, r *run, verb string, args []string, race bool) (*dagger.Container, []string, error) {
	switch verb {
	case "fmt":
		files, err := r.population(ctx, "**/*.go")
		if err != nil {
			return nil, nil, err
		}
		if len(files) == 0 {
			return nil, nil, fmt.Errorf("no Go files to format")
		}
		return withFileList(r.lane(checks.ImageGo), files), xargsExec("gofmt", "-w"), nil
	case "tidy":
		return r.lane(checks.ImageGo), []string{"go", "mod", "tidy"}, nil
	case "vendor":
		// -mod=mod: the vendor directory about to be replaced is the one that
		// may be inconsistent with go.mod, and must not be consulted.
		return r.lane(checks.ImageGo).WithEnvVariable("GOFLAGS", "-mod=mod"), []string{"go", "mod", "vendor"}, nil
	}
	argv, err := devlane.GoArgv(verb, args, race)
	if err != nil {
		return nil, nil, err
	}
	if verb == "test" {
		// The suite runs where git can read a repository and the fleet's
		// record tree is mounted, as the gate's does (goTestIn).
		return goDownload(devRepository(r.withDies(r.laneCode(checks.ImageGo))), "."), argv, nil
	}
	return r.goModules("."), argv, nil
}

// devRepository gives /src a throwaway repository, because the source arrives
// without .git: a test that asks git a question (rev-parse, ls-files) finds one.
func devRepository(ctr *dagger.Container) *dagger.Container {
	return ctr.
		WithNewFile(gitSystemConfig, safeDirectoryConfig).
		WithExec([]string{"git", "init", "-q", "."}).
		WithExec([]string{"git", "add", "-A"})
}
