package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"golang.org/x/sync/errgroup"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE TS REGEN ATOM (F6, foundry-tools#15441). A TypeScript core's generated
// files are a function of a pinned WIT, a pinned stellar-core-rust commit and
// three pinned tools; this regenerates them and fails on any byte difference.
// It is scripts/regen-check.sh run by the module instead of by a person.
//
// WHY NOT GIVE THE LANE AN ENGINE (option a) OR A LANE OF ITS OWN (option c).
// The script reaches an engine because it was written for a workstation: it
// shells out to `dagger core container from rust:1.90 ...`. This module IS
// already running on an engine, so the same two containers are expressed here
// directly — no engine socket handed to a lane, no nested client, no second
// lane for the door to schedule and settle. And the build is the star's OWN
// inner scripts (scripts/<world>-core-inner.sh and transpile-<world>-inner.sh),
// mounted and run verbatim, so what a person's `just regen-check` builds and
// what the lane builds are one script. The image, the pinned tool versions and
// the byte comparison are what this atom adds; nothing in it can make a build
// pass that the script would fail, because the verdict is byte equality with
// the recorded hashes.
//
// IT IS ABSENT UNLESS THE STAR'S justfile CALLS scripts/regen-check.sh: that
// recipe is the opt-in (checks.RegenWorlds).
//
// THE ENGINE CACHES IT. Every exec here is keyed on its inputs — the core's
// commit, the image, the inner script, the tool version — so a run whose pins
// did not move rebuilds nothing, and what stays live on every run is the
// comparison with the tree's committed files. The shared base (rustup target,
// wasm-tools compiled once at its pin) is its own layer, so four worlds compile
// wasm-tools once.

func init() {
	register("ts:regen", tsRegen)
}

func tsRegen(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("ts:regen")
	just, _, err := fileIfPresent(ctx, r.src, "justfile")
	if err != nil {
		return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - the justfile would not read: %v", a.ID, err))
	}
	worlds := checks.RegenWorlds(just)
	if len(worlds) == 0 {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - the justfile has no `scripts/regen-check.sh <world>` line, so the star declares no core to regenerate")
	}
	results := make([]checks.RegenWorldResult, len(worlds))
	var g errgroup.Group
	for i, w := range worlds {
		g.Go(func() error {
			results[i] = r.regenWorld(ctx, w)
			return nil
		})
	}
	_ = g.Wait()
	code, out := checks.RegenFold(results)
	return checks.VerdictOf(a, code, out)
}

// reads collects the first error of a run of reads. Every read below is lazy
// until it is made, and none depends on another's answer, so they are all made
// and the error is looked at once.
type reads struct {
	ctx context.Context
	err error
}

func (rd *reads) text(f *dagger.File, what string) string {
	s, err := f.Contents(rd.ctx)
	if err != nil && rd.err == nil {
		rd.err = fmt.Errorf("%s: %v", what, err)
	}
	return s
}

// regenWorld is regen-check.sh for one world.
func (r *run) regenWorld(ctx context.Context, world string) checks.RegenWorldResult {
	res := checks.RegenWorldResult{World: world}
	gen := "src/" + world + "core-gen"
	body, present, err := fileIfPresent(ctx, r.src, gen+"/provenance.json")
	if err != nil {
		res.Err = err.Error()
		return res
	}
	if !present {
		res.Problems = []string{"no " + gen + "/provenance.json"}
		return res
	}
	p, err := checks.ParseRegenProv(body)
	if err != nil {
		res.Problems = []string{err.Error()}
		return res
	}

	rd := &reads{ctx: ctx}
	var probes []checks.RegenProbe

	// 1. The WIT, the result WIT and the tape at their recorded commits.
	for _, lp := range p.Pins() {
		pin := lp.Pin
		text := rd.text(dag.Git(checks.TSRegenWitGit).Commit(pin.Commit).Tree().File(pin.Path), pin.Path+"@"+pin.Commit)
		probes = append(probes, checks.RegenProbe{
			Label: fmt.Sprintf("%s %s@%s", lp.Label, pin.Path, pin.Commit), Got: checks.SHA256Hex(text), Pinned: pin.SHA256})
	}

	// 2. The core at its recorded commit carries the very same WIT.
	core := dag.Git(checks.TSRegenCoreGit).Commit(p.Core.Commit).Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
	for _, c := range p.CoreCopies(world) {
		text := rd.text(core.File(c.Path), "core "+c.Path+"@"+p.Core.Commit)
		probes = append(probes, checks.RegenProbe{
			Label: "core " + c.Path + " (guest's own copy)", Got: checks.SHA256Hex(text), Pinned: c.SHA256})
	}

	// 3. Build the guest with the star's own inner script, and measure.
	built := regenRustBase(p.Tools.WasmTools).
		WithMountedDirectory("/src", core).
		WithMountedFile("/inner.sh", r.src.File("scripts/"+world+"-core-inner.sh")).
		WithWorkdir("/src").
		WithEnvVariable("WASM_TOOLS_VERSION", p.Tools.WasmTools).
		WithEnvVariable("CARGO_TARGET_DIR", "/tmp/target").
		WithExec([]string{"sh", "/inner.sh"}).
		Directory("/out")
	wasmTools := rd.text(built.File("wasm-tools.version"), "the build's wasm-tools.version")
	rustc := rd.text(built.File("rustc.version"), "the build's rustc.version")
	// THE WASM IS HASHED IN THE ENGINE, never read as a string: a binary
	// through the SDK's string comes back as UTF-8 and hashes as a different
	// file (measured on this atom's first run: every guest "differed").
	sums, err := wasmSums(ctx, map[string]*dagger.File{
		"guest":     built.File("guest.wasm"),
		"component": built.File(world + ".component.wasm"),
	})

	// 4. Transpile the component with the star's own inner script.
	transpiled := dag.Container().From(checks.ImageNode).
		WithEnvVariable("NODE_EXTRA_CA_CERTS", "/etc/ssl/certs/ca-certificates.crt").
		WithEnvVariable("JCO_VERSION", p.Tools.JCO).
		WithMountedFile("/in/"+world+".component.wasm", built.File(world+".component.wasm")).
		WithMountedFile("/inner.sh", r.src.File("scripts/transpile-"+world+"-inner.sh")).
		WithExec([]string{"sh", "/inner.sh"}).
		Directory("/out")
	jco := rd.text(transpiled.File(".jco-version"), "the transpile's .jco-version")

	// 5. Every generated file, byte for byte, against what the tree ships.
	diff, code, derr := output(ctx, dag.Container().From(checks.ImageFleet).
		WithMountedDirectory("/gen", transpiled).
		WithMountedDirectory("/shipped", r.src.Directory(gen)).
		WithExec([]string{"diff", "-r", "-q", "-x", "provenance.json", "-x", ".jco-version", "/gen", "/shipped"}, anyExit))

	if err = errors.Join(rd.err, err, derr); err != nil {
		res.Err = err.Error()
		return res
	}
	probes = append(probes,
		checks.RegenProbe{Label: "guest.wasm", Got: sums["guest"], Pinned: p.GuestWasm.SHA256},
		checks.RegenProbe{Label: world + ".component.wasm", Got: sums["component"], Pinned: p.ComponentWasm.SHA256},
		checks.RegenProbe{Label: "wasm-tools", Got: checks.RegenToolVersion(wasmTools), Pinned: p.Tools.WasmTools},
		checks.RegenProbe{Label: "rustc", Got: strings.TrimSpace(rustc), Pinned: p.Core.Toolchain},
		checks.RegenProbe{Label: "jco", Got: strings.TrimSpace(jco), Pinned: p.Tools.JCO})
	res.Lines, res.Problems = checks.RegenCompare(probes)
	if code != 0 {
		res.Problems = append(res.Problems, "regenerated output differs from "+gen+":\n"+diff)
	} else {
		res.Lines = append(res.Lines, fmt.Sprintf("PASS - %d pins held and every generated file byte-identical to %s", len(probes), gen))
	}
	return res
}

// wasmSums is sha256sum of files, run in the engine: name -> digest.
func wasmSums(ctx context.Context, files map[string]*dagger.File) (map[string]string, error) {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	ctr := dag.Container().From(checks.ImageFleet)
	args := []string{"sha256sum"}
	for _, n := range names {
		ctr = ctr.WithFile("/f/"+n, files[n])
		args = append(args, "/f/"+n)
	}
	out, code, err := output(ctx, ctr.WithExec(args, anyExit))
	if err == nil && code != 0 {
		err = fmt.Errorf("sha256sum exited %d: %s", code, out)
	}
	if err != nil {
		return nil, err
	}
	byPath, err := checks.ParseSha256sum(out)
	sums := map[string]string{}
	for _, n := range names {
		sums[n] = byPath["/f/"+n]
	}
	return sums, err
}

// regenRustBase is the rust container the guests are built in: the toolchain
// the provenance records (checks.ImageRustWasm), the wasm32 target, and
// wasm-tools compiled once at its pinned version. The inner scripts run
// `rustup target add` and `cargo install wasm-tools --locked --version` again;
// both find them present and do nothing, so the layer is shared by every world.
func regenRustBase(wasmTools string) *dagger.Container {
	return dag.Container().From(checks.ImageRustWasm).
		WithExec([]string{"rustup", "target", "add", "wasm32-unknown-unknown"}).
		WithExec([]string{"cargo", "install", "wasm-tools", "--locked", "--version", wasmTools})
}
