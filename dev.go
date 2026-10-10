package main

import (
	"context"
	"fmt"
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
// CACHES ARE KEYED ON THE REPOSITORY, NEVER THE WORKTREE. GOCACHE, GOMODCACHE
// and cargo's registry are content-addressed volumes shared fleet-wide; cargo's
// target directory is per repository (checks.CachesForRepo, keyed on Repo), so
// ten worktrees of one repo share one warm target/. The trade-off: cargo takes a
// lock on the build directory, so concurrent runs against one repo's target
// queue behind each other ("Blocking waiting for file lock") rather than racing.
type Dev struct {
	// +private
	Source *dagger.Directory
	// +private
	Lang string
	// +private
	Repo string
	// +private
	Entries []string
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
	return &Dev{Source: source, Lang: lang, Repo: devlane.RepoKey(repo, lang, manifest, lock), Entries: entries}, nil
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

// Tidy runs go mod tidy, and go mod vendor where vendor/ exists (Go only), and
// answers the files it changed. A file the tool DELETES is not in the answer:
// an export adds and rewrites, it never removes.
//
// +cache="never"
func (d *Dev) Tidy(ctx context.Context) (*dagger.Directory, error) {
	return d.changed(ctx, "tidy", nil)
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
	_, out, code, err := d.ran(ctx, verb, args, race, fresh)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", settle(ctx, devlane.SettleCode(code), checks.LogTail(out, devlane.LogLimit))
	}
	return out, nil
}

// changed runs a verb whose product is the files it rewrote: the source diffed
// against the tree after the tool, so only what moved is exported.
func (d *Dev) changed(ctx context.Context, verb string, args []string) (*dagger.Directory, error) {
	ctr, out, code, err := d.ran(ctx, verb, args, false, false)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, settle(ctx, devlane.SettleCode(code), checks.LogTail(out, devlane.LogLimit))
	}
	return d.Source.Diff(ctr.Directory("/src")), nil
}

// ran builds the verb's container and runs its steps, stopping at the first that
// fails. It answers the container after the last step run, everything the steps
// printed, and the failing step's exit code (0 when all passed).
func (d *Dev) ran(ctx context.Context, verb string, args []string, race, fresh bool) (*dagger.Container, string, int, error) {
	if err := devlane.Applies(d.Lang, verb); err != nil {
		return nil, "", 0, err
	}
	r := newRun(d.Source, d.Repo, "").reasked(devlane.Nonce(fresh, time.Now().UnixNano()))
	ctr, steps, err := d.plan(ctx, r, verb, args, race)
	if err != nil {
		return nil, "", 0, err
	}
	var all string
	for _, argv := range steps {
		ctr = ctr.WithExec(argv, anyExit)
		out, code, err := outputBoth(ctx, ctr)
		if err != nil {
			return nil, "", 0, fmt.Errorf("%s could not run: %w", verb, err)
		}
		all += out
		if code != 0 {
			return ctr, all, code, nil
		}
	}
	return ctr, all, 0, nil
}

// plan is the container a verb starts from and the commands it runs there.
func (d *Dev) plan(ctx context.Context, r *run, verb string, args []string, race bool) (*dagger.Container, [][]string, error) {
	if d.Lang == devlane.Go {
		return d.planGo(ctx, r, verb, args, race)
	}
	argv, err := devlane.RustArgv(verb, args)
	if err != nil {
		return nil, nil, err
	}
	switch verb {
	case "test":
		return devRepository(r.cargoFresh()), [][]string{argv}, nil
	case "check", "build", "clippy":
		return r.cargoFresh(), [][]string{argv}, nil
	}
	// fmt, lock and update rewrite files and read no lock: the gate's fetch
	// is --locked, which would refuse the very lock these exist to repair.
	return r.lane(checks.ImageRust), [][]string{argv}, nil
}

func (d *Dev) planGo(ctx context.Context, r *run, verb string, args []string, race bool) (*dagger.Container, [][]string, error) {
	switch verb {
	case "fmt":
		files, err := r.population(ctx, "**/*.go")
		if err != nil {
			return nil, nil, err
		}
		files = devlane.GoFiles(files)
		if len(files) == 0 {
			return nil, nil, fmt.Errorf("no Go files to format")
		}
		return withFileList(r.lane(checks.ImageGo), files), [][]string{xargsExec("gofmt", "-w")}, nil
	case "tidy":
		steps := [][]string{{"go", "mod", "tidy"}}
		if devlane.HasVendor(d.Entries) {
			steps = append(steps, []string{"go", "mod", "vendor"})
		}
		return r.lane(checks.ImageGo), steps, nil
	}
	argv, err := devlane.GoArgv(verb, args, race)
	if err != nil {
		return nil, nil, err
	}
	if verb == "test" {
		// The suite runs where git can read a repository and the fleet's
		// record tree is mounted, as the gate's does (goTestIn).
		return goDownload(devRepository(r.withDies(r.laneCode(checks.ImageGo))), "."), [][]string{argv}, nil
	}
	return r.goModules("."), [][]string{argv}, nil
}

// devRepository gives /src a throwaway repository, because the source arrives
// without .git: a test that asks git a question (rev-parse, ls-files) finds one.
func devRepository(ctr *dagger.Container) *dagger.Container {
	return ctr.
		WithNewFile(gitSystemConfig, safeDirectoryConfig).
		WithExec([]string{"git", "init", "-q", "."}).
		WithExec([]string{"git", "add", "-A"})
}
