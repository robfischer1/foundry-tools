package atoms

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

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
	out, code, never := RunTool(ctx, in, a, Cmd{Dir: in.Root, Name: argv[0], Args: argv[1:], Env: env})
	if never != nil {
		return *never
	}
	return CargoVerdict(a, out, code)
}
