package main

import (
	"context"
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

type regenResult struct {
	world    string
	lines    []string
	problems []string
	err      error
}

func tsRegen(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("ts:regen")
	just, present, err := fileIfPresent(ctx, r.src, "justfile")
	if err != nil {
		return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - the justfile would not read: %v", a.ID, err))
	}
	worlds := checks.RegenWorlds(just)
	if !present || len(worlds) == 0 {
		return checks.VerdictOf(a, 0, a.ID+": ABSENT - the justfile has no `scripts/regen-check.sh <world>` line, so the star declares no core to regenerate")
	}
	results := make([]regenResult, len(worlds))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(len(worlds))
	for i, w := range worlds {
		g.Go(func() error {
			results[i] = r.regenWorld(gctx, w)
			return nil
		})
	}
	_ = g.Wait()
	var out []string
	code := 0
	for _, res := range results {
		out = append(out, res.lines...)
		switch {
		case res.err != nil:
			code = 2
			out = append(out, fmt.Sprintf("regen-check(%s): CANNOT RUN: %v", res.world, res.err))
		case len(res.problems) > 0:
			if code == 0 {
				code = 1
			}
			for _, p := range res.problems {
				out = append(out, fmt.Sprintf("regen-check(%s): FAIL: %s", res.world, p))
			}
		}
	}
	if code == 1 {
		out = append([]string{a.ID + ": FINDINGS - a core does not regenerate byte-identical from its pins"}, out...)
	}
	return checks.VerdictOf(a, code, strings.Join(out, "\n"))
}

// regenWorld is regen-check.sh for one world, in the script's order.
func (r *run) regenWorld(ctx context.Context, world string) (res regenResult) {
	res.world = world
	gen := "src/" + world + "core-gen"
	body, ok, err := fileIfPresent(ctx, r.src, gen+"/provenance.json")
	if err != nil {
		res.err = err
		return res
	}
	if !ok {
		res.problems = append(res.problems, "no "+gen+"/provenance.json")
		return res
	}
	p, err := checks.ParseRegenProv(body)
	if err != nil {
		res.problems = append(res.problems, err.Error())
		return res
	}
	checked := 0
	expect := func(label, got, pinned string) {
		ok, problem := checks.RegenExpect(label, got, pinned)
		checked++
		if problem != "" {
			res.problems = append(res.problems, problem)
			return
		}
		res.lines = append(res.lines, "regen-check("+world+"): "+ok)
	}

	// 1. The WIT, the result WIT and the tape at their recorded commits.
	pinned := func(label string, pin checks.RegenPin) {
		tree := dag.Git(checks.TSRegenWitGit).Commit(pin.Commit).Tree()
		text, err := tree.File(pin.Path).Contents(ctx)
		if err != nil {
			res.err = fmt.Errorf("%s@%s: %v", pin.Path, pin.Commit, err)
			return
		}
		expect(fmt.Sprintf("%s %s@%s", label, pin.Path, pin.Commit), checks.SHA256Hex(text), pin.SHA256)
	}
	pinned("wit", p.WIT)
	if p.WITResult != nil {
		pinned("wit", *p.WITResult)
	}
	pinned("tape", p.Tape)
	if res.err != nil {
		return res
	}

	// 2. The core at its recorded commit carries the very same WIT.
	core := dag.Git(checks.TSRegenCoreGit).Commit(p.Core.Commit).Tree(dagger.GitRefTreeOpts{DiscardGitDir: true})
	copyOf := func(path, pinnedSum string) {
		text, err := core.File(path).Contents(ctx)
		if err != nil {
			res.err = fmt.Errorf("core %s@%s: %v", path, p.Core.Commit, err)
			return
		}
		expect("core "+path+" (guest's own copy)", checks.SHA256Hex(text), pinnedSum)
	}
	copyOf("wit/aiws-"+world+".wit", p.WIT.SHA256)
	if p.WITResult != nil {
		copyOf("wit/aiws-result.wit", p.WITResult.SHA256)
	}
	if res.err != nil {
		return res
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
	read := func(name string) string {
		text, err := built.File(name).Contents(ctx)
		if err != nil {
			res.err = fmt.Errorf("the build left no %s: %v", name, err)
		}
		return text
	}
	wasmTools, rustc := read("wasm-tools.version"), read("rustc.version")
	// THE WASM IS HASHED IN THE ENGINE, never read as a string: a binary
	// through the SDK's string comes back as UTF-8 and hashes as a different
	// file (measured on this atom's first run: every guest "differed").
	sums, err := wasmSums(ctx, map[string]*dagger.File{
		"guest":     built.File("guest.wasm"),
		"component": built.File(world + ".component.wasm"),
	})
	if res.err != nil || err != nil {
		if res.err == nil {
			res.err = err
		}
		return res
	}
	expect("guest.wasm", sums["guest"], p.GuestWasm.SHA256)
	expect(world+".component.wasm", sums["component"], p.ComponentWasm.SHA256)
	expect("wasm-tools", checks.RegenToolVersion(wasmTools), p.Tools.WasmTools)
	expect("rustc", strings.TrimSpace(rustc), p.Core.Toolchain)

	// 4. Transpile the component with the star's own inner script.
	transpiled := dag.Container().From(checks.ImageNode).
		WithEnvVariable("NODE_EXTRA_CA_CERTS", "/etc/ssl/certs/ca-certificates.crt").
		WithEnvVariable("JCO_VERSION", p.Tools.JCO).
		WithMountedFile("/in/"+world+".component.wasm", built.File(world+".component.wasm")).
		WithMountedFile("/inner.sh", r.src.File("scripts/transpile-"+world+"-inner.sh")).
		WithExec([]string{"sh", "/inner.sh"}).
		Directory("/out")
	jco, err := transpiled.File(".jco-version").Contents(ctx)
	if err != nil {
		res.err = fmt.Errorf("the transpile left no .jco-version: %v", err)
		return res
	}
	expect("jco", strings.TrimSpace(jco), p.Tools.JCO)

	// 5. Every generated file, byte for byte, against what the tree ships.
	diff, code, err := output(ctx, dag.Container().From(checks.ImageFleet).
		WithMountedDirectory("/gen", transpiled).
		WithMountedDirectory("/shipped", r.src.Directory(gen)).
		WithExec([]string{"diff", "-r", "-q", "-x", "provenance.json", "-x", ".jco-version", "/gen", "/shipped"}, anyExit))
	switch {
	case err != nil:
		res.err = err
	case code != 0:
		res.problems = append(res.problems, "regenerated output differs from "+gen+":\n"+diff)
	default:
		res.lines = append(res.lines, checks.RegenWorldEnvelope(world, checked))
	}
	return res
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
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("sha256sum exited %d: %s", code, out)
	}
	byPath, err := checks.ParseSha256sum(out)
	if err != nil {
		return nil, err
	}
	sums := map[string]string{}
	for _, n := range names {
		sums[n] = byPath["/f/"+n]
	}
	return sums, nil
}
