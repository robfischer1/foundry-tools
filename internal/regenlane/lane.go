// Package regenlane is the work of the atom ts:regen (atoms/ts/regen): each
// core the star's justfile names regenerates byte-identical from its pinned WIT,
// its pinned stellar-core-rust commit and its pinned tools. The atom's file
// holds its declaration; the steps live here, shared by nothing else, because
// an atom's directory holds its own file alone.
//
// A TypeScript core's generated files are a function of a pinned WIT, a pinned
// core commit and three pinned tools (F6, foundry-tools#15441). This regenerates
// them and fails on any byte difference. It is scripts/regen-check.sh run by the
// module instead of by a person, and it is absent unless the star's justfile
// calls that script (checks.RegenWorlds): that recipe is the opt-in.
package regenlane

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/regenkit"
)

const id = "ts:regen"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LaneTS, NeedsManifest: true}

// worldsParallel is how many worlds build at once: the toolchain container's
// own width (plan@1 max_parallel), since each is a cargo build.
const worldsParallel = 2

// Check regenerates every world the justfile names and folds the answers.
func Check(ctx context.Context, in atomkit.In) checks.Verdict {
	body, err := os.ReadFile(filepath.Join(in.Root, "justfile"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return checks.VerdictOf(row, 2, fmt.Sprintf("%s: CANNOT RUN - the justfile would not read: %v", id, err))
	}
	worlds := checks.RegenWorlds(string(body))
	if len(worlds) == 0 {
		return checks.VerdictOf(row, 0, id+": ABSENT - the justfile has no `scripts/regen-check.sh <world>` line, so the star declares no core to regenerate")
	}
	p := newProvision(in)
	results := make([]checks.RegenWorldResult, len(worlds))
	var g errgroup.Group
	g.SetLimit(worldsParallel)
	for i, w := range worlds {
		g.Go(func() error {
			results[i] = regenWorld(ctx, in, p, w)
			return nil
		})
	}
	_ = g.Wait()
	code, out := checks.RegenFold(results)
	return checks.VerdictOf(row, code, out)
}

// world is one world's run: its directory, its tools and its exec.
type world struct {
	ctx  context.Context
	in   atomkit.In
	p    *provision
	name string
	dir  string
	prov checks.RegenProv
}

// exec runs a program. A script written a moment ago can still be open in a
// sibling goroutine's fork (golang/go#22315), which the kernel answers "text
// file busy"; that is retried, and nothing else is.
func (w *world) exec(c atoms.Cmd) (string, int) {
	for range 5 {
		out, code := w.in.Run(w.ctx, c)
		if code >= 0 || !strings.Contains(out, "text file busy") {
			return out, code
		}
		select {
		case <-w.ctx.Done():
			return out, code
		case <-time.After(100 * time.Millisecond):
		}
	}
	return w.in.Run(w.ctx, c)
}

// run is exec with a failure as an error: the program, its exit and what it said.
func (w *world) run(c atoms.Cmd) (string, error) {
	out, code := w.exec(c)
	if code == 0 {
		return out, nil
	}
	return out, fmt.Errorf("%s exited %d: %s", strings.Join(append([]string{c.Name}, c.Args...), " "), code, tail(out))
}

// tail is the last lines of a program's output: enough to say why, one line.
func tail(s string) string { return regenkit.Tail(s) }

// do runs argv in dir with env; argv[0] is the program.
func (w *world) do(dir string, env []string, argv []string, stdoutOnly bool) (string, error) {
	return w.run(atoms.Cmd{Dir: dir, Name: argv[0], Args: argv[1:], Env: env, StdoutOnly: stdoutOnly})
}

// regenWorld is regen-check.sh for one world.
func regenWorld(ctx context.Context, in atomkit.In, p *provision, name string) checks.RegenWorldResult {
	res := checks.RegenWorldResult{World: name}
	gen := "src/" + name + "core-gen"
	body, err := os.ReadFile(filepath.Join(in.Root, gen, "provenance.json"))
	if errors.Is(err, os.ErrNotExist) {
		res.Problems = []string{"no " + gen + "/provenance.json"}
		return res
	}
	if err != nil {
		res.Err = err.Error()
		return res
	}
	prov, err := checks.ParseRegenProv(string(body))
	if err != nil {
		res.Problems = []string{err.Error()}
		return res
	}
	w := &world{ctx: ctx, in: in, p: p, name: name, dir: filepath.Join(atoms.WorkDir(), "ts-regen", name), prov: prov}
	defer os.RemoveAll(w.dir)
	probes, err := w.measure(gen)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	shipped := filepath.Join(in.Root, gen)
	diffOut, code := w.exec(atoms.Cmd{Dir: in.Root, Name: "diff", Args: checks.RegenDiff(filepath.Join(w.dir, "gen"), shipped)[1:]})
	if code < 0 {
		res.Err = "diff: " + diffOut
		return res
	}
	res.Lines, res.Problems = checks.RegenCompare(probes)
	switch {
	case code != 0:
		res.Problems = append(res.Problems, "regenerated output differs from "+gen+":\n"+checks.RegenRelabelDiff(diffOut, filepath.Join(w.dir, "gen"), shipped))
	case len(res.Problems) == 0:
		res.Lines = append(res.Lines, fmt.Sprintf("PASS - %d pins held and every generated file byte-identical to %s", len(probes), gen))
	}
	return res
}

// measure reads every pin, builds the guest, transpiles it and answers every
// probe: a measurement and the pin it is held to.
func (w *world) measure(gen string) ([]checks.RegenProbe, error) {
	var probes []checks.RegenProbe
	pins, err := w.pins()
	if err != nil {
		return nil, err
	}
	probes = append(probes, pins...)
	core, err := w.core()
	if err != nil {
		return nil, err
	}
	for _, c := range w.prov.CoreCopies() {
		sum, err := checks.SHA256File(filepath.Join(core, c.Path))
		if err != nil {
			return nil, fmt.Errorf("core %s@%s: %v", c.Path, w.prov.Core.Commit, err)
		}
		probes = append(probes, checks.RegenProbe{Label: "core " + c.Path + " (guest's own copy)", Got: sum, Pinned: c.SHA256})
	}
	built, err := w.build(core)
	if err != nil {
		return nil, err
	}
	transpiled, err := w.transpile(built)
	if err != nil {
		return nil, err
	}
	return append(probes,
		checks.RegenProbe{Label: "guest.wasm", Got: built.guest, Pinned: w.prov.GuestWasm.SHA256},
		checks.RegenProbe{Label: w.name + ".component.wasm", Got: built.component, Pinned: w.prov.ComponentWasm.SHA256},
		checks.RegenProbe{Label: "wasm-tools", Got: checks.RegenToolVersion(built.wasmTools), Pinned: w.prov.Tools.WasmTools},
		checks.RegenProbe{Label: "rustc", Got: strings.TrimSpace(built.rustc), Pinned: w.prov.Core.Toolchain},
		checks.RegenProbe{Label: "jco", Got: strings.TrimSpace(transpiled), Pinned: w.prov.Tools.JCO}), nil
}
