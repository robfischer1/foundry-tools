package atoms

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

// THE WIT ATOMS' SHARED PARTS: what rust:wit-guest and rust:wit-compose each need
// before they build. Neither reads what the other wrote. Each builds into its
// own directory under the cargo target dir it is given, so there is no volume,
// directory or file one of them leaves for the other (DEF-B4-wit-1).

// CargoTargetDir is the cargo target dir the atom builds in: CARGO_TARGET_DIR
// when the environment names one (the executor gives each pod its own under
// /work, and the Dagger adapter mounts the atom's release volume there), and a
// directory of the atom's own work area otherwise.
func CargoTargetDir() string {
	if d := os.Getenv("CARGO_TARGET_DIR"); d != "" {
		return d
	}
	return filepath.Join(WorkDir(), "cargo-target")
}

// WitProvision is the chains' provisioning for a wasm32 guest build: the target
// is added to the toolchain, and each tool answers its version. A step that
// fails means the atom never ran; it is never a finding about the tree. It
// answers nil when everything is there.
func WitProvision(ctx context.Context, in Input, a checks.AtomDef, target string, tools ...string) *checks.Verdict {
	out, code := in.run(ctx, Cmd{Dir: in.Root, Name: "rustup", Args: []string{"target", "add", target}, Both: true})
	if code != 0 {
		v := NeverRan(a, []string{"rustup", "target", "add", target}, code, out)
		return &v
	}
	for _, tool := range tools {
		if v := Probe(ctx, in, a, tool, "--version"); v != nil {
			return v
		}
	}
	return nil
}

// ClearOutput removes a build's output before the build, so a build that did
// not produce it leaves it absent and not the copy a previous tree made.
func ClearOutput(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("could not clear %s before the build: %w", path, err)
	}
	return nil
}

// CarryOut copies a build's output to dst, executable. A source that is absent
// is skipped, not an error: the next step's own "no such file" is the answer
// (internal/copyout, which this is the in-process form of, says why).
func CarryOut(src, dst string) error {
	in, err := os.Open(src)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// CargoRun is one cargo program run in the tree's root under env, its exit read
// in cargo's vocabulary (CargoVerdict). A program that would not start, or was
// killed by a signal, is the atom's could-not-run.
func CargoRun(ctx context.Context, in Input, a checks.AtomDef, env []string, argv ...string) checks.Verdict {
	return CargoRunIn(ctx, in, in.Root, a, env, argv...)
}

// CargoRunIn is CargoRun in a directory of the tree other than its root: the
// wit facade of a virtual workspace (WitFacade), where cargo finds the
// workspace by walking up and the facade is the package the run is about.
func CargoRunIn(ctx context.Context, in Input, dir string, a checks.AtomDef, env []string, argv ...string) checks.Verdict {
	v, _ := CargoRunInOut(ctx, in, dir, a, env, argv...)
	return v
}

// CargoRunInOut is CargoRunIn that also answers what cargo printed ("" when it
// never ran), for an atom that reads a pass's output (rust:wit-guest's counts).
func CargoRunInOut(ctx context.Context, in Input, dir string, a checks.AtomDef, env []string, argv ...string) (checks.Verdict, string) {
	out, code, never := RunTool(ctx, in, a, Cmd{Dir: dir, Name: argv[0], Args: argv[1:], Env: env})
	if never != nil {
		return *never, ""
	}
	return CargoVerdict(a, out, code), out
}

// WitFacade is where the wit atoms find the crate they grade.
type WitFacade struct {
	// Dir is the facade's directory under the tree's root, slash-separated; ""
	// when the root IS the crate (a package root: every repository today).
	Dir string
	// Manifest is the facade's Cargo.toml as written; "" when a virtual
	// workspace has no facade.
	Manifest string
	// Virtual is true when the root Cargo.toml is a virtual workspace.
	Virtual bool
}

// Abs is a directory of the tree: the facade's, for every cargo run and every
// path the atoms resolve (tests/tapes, wit-guest-budgets.toml, the composer).
func (f WitFacade) Abs(root string) string { return filepath.Join(root, filepath.FromSlash(f.Dir)) }

// Within answers a tree file's path relative to the facade, and whether the
// file is under it. For a package root every file is, unchanged.
func (f WitFacade) Within(file string) (string, bool) {
	if f.Dir == "" {
		return file, true
	}
	rel, ok := strings.CutPrefix(file, f.Dir+"/")
	return rel, ok
}

// Rel is a facade-relative file as the tree's file list names it.
func (f WitFacade) Rel(file string) string { return path.Join(f.Dir, file) }

// ResolveWitFacade finds the crate the wit atoms grade (checks.WitFacadeDir
// says why). A root that is a package answers itself with NO program run, so a
// single-package repository is graded exactly as before. A virtual root asks
// `cargo metadata --no-deps`; a cargo that will not answer, or an answer that
// will not parse, is the atom's could-not-run and never an ABSENT: a lane that
// cannot find its subject must not pass on it. Only an answer with no member
// declaring a wit-guest feature comes back with an empty Manifest, and that is
// the one case the caller may report as absent.
func ResolveWitFacade(ctx context.Context, in Input, a checks.AtomDef) (WitFacade, *checks.Verdict) {
	root, err := os.ReadFile(filepath.Join(in.Root, "Cargo.toml"))
	if err != nil {
		v := checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - Cargo.toml would not read: %v", a.ID, err))
		return WitFacade{}, &v
	}
	if !checks.IsVirtualWorkspace(string(root)) {
		return WitFacade{Manifest: string(root)}, nil
	}
	out, code, never := RunTool(ctx, in, a, Cmd{Dir: in.Root, Name: "cargo", Args: []string{"metadata", "--no-deps", "--format-version", "1"}, StdoutOnly: true})
	if never != nil {
		return WitFacade{}, never
	}
	if code != 0 {
		v := checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - cargo metadata exited %d, so the virtual workspace's facade is unknown", a.ID, code))
		return WitFacade{}, &v
	}
	dir, found, err := checks.WitFacadeDir([]byte(out))
	if err != nil {
		v := checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - %v", a.ID, err))
		return WitFacade{}, &v
	}
	if !found {
		return WitFacade{Virtual: true}, nil
	}
	manifest, err := os.ReadFile(filepath.Join(in.Root, filepath.FromSlash(dir), "Cargo.toml"))
	if err != nil {
		v := checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - the facade's Cargo.toml would not read: %v", a.ID, err))
		return WitFacade{}, &v
	}
	return WitFacade{Dir: dir, Manifest: string(manifest), Virtual: true}, nil
}
