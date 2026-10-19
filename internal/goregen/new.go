package goregen

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/regenkit"
)

// THE CHOICES --new MAKES, AND WHY. A new core has no record to read them from,
// so each is taken from the tree and the request, never guessed:
//
//   - WORLD AND PACKAGE NAMES. `--world aiws:<pkg>/<world>@<ver>` is the WIT's own
//     name for the world (wit/aiws-<pkg>.wit declares `package aiws:<pkg>@<ver>;`
//     and `world <world>`; the atom reads that file at the revision and refuses a
//     request it does not match). <pkg> is the compose.sh world and the
//     wit-guest-<pkg> feature (Cargo.toml and compose.sh's roster both spell it
//     so: topic-contract, observability-api). <world> is what gravity calls the
//     bindings (--name): a composed component's own world is always `root`, which
//     is why every composed core records --name <world> beside its --package. The
//     Go package is the core's directory name, as in every other core.
//   - THE GENERATOR is the gravity commit the other cores of the tree pin (the
//     majority of their provenance.json files; a tie is refused), and its flags
//     are the composed cores': --name, --package, --gofmt, --output.
//   - THE COMPILER is the newest this atom can lay (checks.RegenNewRustTag), which
//     is what the newest cores of the tree record (classescore, observabilityapi).
//   - THE BUILD is one world through the revision's own tools/compose/compose.sh,
//     as classescore's is.

var (
	commitRE   = regexp.MustCompile(`^[0-9a-f]{40}$`)
	packageRE  = regexp.MustCompile(`(?m)^package\s+aiws:([a-z0-9-]+)@([0-9.]+);`)
	worldDeclS = `(?m)^world\s+%s\s*\{`
)

// bootstrap brings a core into being: it builds the guest the compose way,
// generates the bindings, and writes build-guest.sh, provenance.json and the
// three generated files. It writes the blocks it can derive and says which it
// could not.
func (r *runner) bootstrap(nc NewCore) outcome {
	line, f := r.bring(nc)
	return outcome{core: nc.Core, line: line, err: f}
}

func (r *runner) bring(nc NewCore) (string, *failure) {
	world, err := ParseWorld(nc.World)
	if err != nil {
		return "", finding("read", "%v", err)
	}
	dir := filepath.Join(r.in.Root, nc.Core)
	for _, f := range []string{"provenance.json", "build-guest.sh"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return "", finding("read", "%s/%s exists: name the core to regenerate it, or remove it to bootstrap again", nc.Core, f)
		}
	}
	gravity, f := r.gravityRev()
	if f != nil {
		return "", f
	}
	commit, f := r.resolve(nc.Rev)
	if f != nil {
		return "", f
	}
	src, f := r.source(commit)
	if f != nil {
		return "", f
	}
	wit, f := checkWorld(src, nc.Rev, world)
	if f != nil {
		return "", f
	}
	chain, f := r.newToolchain()
	if f != nil {
		return "", f
	}
	guestBytes, f := r.build(nc.Core, BuildGuest{Rev: commit, Compose: []string{world.Pkg}}, src, chain, defaultCargoHome, "")
	if f != nil {
		return "", f
	}
	gen, f := r.generate(nc.Core, r.in.Root, gravity, GeneratorCommand(nc.Core, world), guestBytes)
	if f != nil {
		return "", f
	}
	var module string
	for name := range gen.files {
		if name == "zz_generated.go" {
			continue
		}
		if module != "" {
			return "", cannot("generate", "the generator wrote more than one module (%s and %s): only a one-world composition is bootstrapped", module, name)
		}
		module = name
	}
	if _, ok := gen.files["zz_generated.go"]; !ok || module == "" {
		return "", cannot("generate", "the generator wrote %s: want zz_generated.go and one module", strings.Join(slices.Sorted(maps.Keys(gen.files)), ", "))
	}
	wasmTools, wac := r.toolVersion("", "wasm-tools"), r.toolVersion("", "wac")
	rec := func(file string, b []byte) Rec { return Rec{File: file, SHA256: sum(b), Bytes: len(b)} }
	witSum := ""
	if b, err := os.ReadFile(filepath.Join(src, "wit", "aiws-"+world.Pkg+".wit")); err == nil {
		witSum = sum(b)
	}
	prov := RenderProvenance(NewProv{
		Core: nc.Core, Rev: nc.Rev, Commit: commit, World: world, GenCommit: gravity, WitSHA256: witSum,
		Toolchain: fmt.Sprintf("%s, wasm-tools %s, wac %s", chain.line, wasmTools, wac),
		Guest:     rec("guest.wasm", guestBytes), Embedded: rec(module, gen.files[module]), Bindings: rec("zz_generated.go", gen.files["zz_generated.go"]),
	})
	files := []struct {
		name string
		body []byte
		mode os.FileMode
	}{
		{"guest.wasm", guestBytes, 0o644}, {"zz_generated.go", gen.files["zz_generated.go"], 0o644}, {module, gen.files[module], 0o644},
		{"build-guest.sh", []byte(RenderBuildGuest(world, commit, wasmTools, wac)), 0o755}, {"provenance.json", []byte(prov), 0o644},
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", cannot("write", "%v", err)
	}
	for _, w := range files {
		if err := os.WriteFile(filepath.Join(dir, w.name), w.body, w.mode); err != nil {
			return "", cannot("write", "%v", err)
		}
	}
	return fmt.Sprintf("%s: NEW from stellar-core-rust %s (%s), %s; wrote guest.wasm %s, zz_generated.go %s, %s %s, build-guest.sh, provenance.json; %s (%s); left for a human: %s",
		nc.Core, nc.Rev, commit[:7], nc.World, short(sum(guestBytes)), short(sum(gen.files["zz_generated.go"])), module, short(sum(gen.files[module])),
		chain.line, chain.how, strings.Join(LeftForAHuman(wit, world.Pkg), ", ")), nil
}

// resolve answers the commit a tag or a commit names in stellar-core-rust: a
// commit is itself, and a tag is looked up on the remote, its peeled commit
// when it is annotated.
func (r *runner) resolve(rev string) (string, *failure) {
	if commitRE.MatchString(rev) {
		return rev, nil
	}
	if err := os.MkdirAll(r.work, 0o755); err != nil {
		return "", cannot("resolve", "%v", err)
	}
	out, err := r.run(r.work, regenkit.GitEnv, "git", "ls-remote", checks.TSRegenCoreGit, "refs/tags/"+rev+"^{}", "refs/tags/"+rev)
	if err != nil {
		return "", cannot("resolve", "stellar-core-rust: %v", err)
	}
	var plain string
	for _, l := range strings.Split(out, "\n") {
		sha, ref, ok := strings.Cut(strings.TrimSpace(l), "\t")
		switch {
		case !ok || !commitRE.MatchString(sha):
		case ref == "refs/tags/"+rev+"^{}":
			return sha, nil
		case ref == "refs/tags/"+rev:
			plain = sha
		}
	}
	if plain == "" {
		return "", cannot("resolve", "stellar-core-rust has no tag %s", rev)
	}
	return plain, nil
}

// checkWorld holds the request to the revision's own tree: the crate has the
// wit-guest-<pkg> feature, and wit/aiws-<pkg>.wit declares the package at the
// version and the world by the name asked for. It answers the WIT's text.
func checkWorld(src, rev string, w World) (string, *failure) {
	manifest, err := os.ReadFile(filepath.Join(src, "Cargo.toml"))
	if err != nil {
		return "", cannot("resolve", "stellar-core-rust %s: Cargo.toml would not read: %v", rev, err)
	}
	feature := checks.WitGuestPrefix + w.Pkg
	if worlds := checks.WitGuestWorlds(string(manifest)); !slices.Contains(worlds, feature) {
		return "", finding("resolve", "stellar-core-rust %s has no %s feature (it builds: %s)", rev, feature, strings.Join(worlds, ", "))
	}
	text, err := os.ReadFile(filepath.Join(src, "wit", "aiws-"+w.Pkg+".wit"))
	if err != nil {
		return "", finding("resolve", "stellar-core-rust %s has no wit/aiws-%s.wit: %v", rev, w.Pkg, err)
	}
	m := packageRE.FindStringSubmatch(string(text))
	if m == nil || m[1] != w.Pkg || m[2] != w.Version {
		got := "no package line"
		if m != nil {
			got = "aiws:" + m[1] + "@" + m[2]
		}
		return "", finding("resolve", "wit/aiws-%s.wit at stellar-core-rust %s declares %s, not aiws:%s@%s", w.Pkg, rev, got, w.Pkg, w.Version)
	}
	if !regexp.MustCompile(fmt.Sprintf(worldDeclS, regexp.QuoteMeta(w.Name))).MatchString(string(text)) {
		return "", finding("resolve", "wit/aiws-%s.wit at stellar-core-rust %s declares no world %s", w.Pkg, rev, w.Name)
	}
	return string(text), nil
}

// newToolchain is the compiler a new core is built with: the newest this atom
// lays, or the container's own when that is the same release.
func (r *runner) newToolchain() (toolchain, *failure) {
	native, f := r.nativeRustc()
	if f != nil {
		return toolchain{}, f
	}
	if _, v, _ := checks.RegenRustLine(native); v == checks.RegenNewRustTag {
		return r.toolchain(native)
	}
	_, nativeVersion, _ := checks.RegenRustLine(native)
	return r.lay(checks.RegenNewRustTag, "", nativeVersion)
}

// gravityRev is the gravity commit the tree's cores pin: the one most of them
// agree on. A tree with none to read, or a tie, has no answer.
func (r *runner) gravityRev() (string, *failure) {
	count := map[string]int{}
	for _, p := range regenerableProvs(r.in.Root) {
		count[p.GenCommit]++
	}
	revs := slices.Sorted(maps.Keys(count))
	if len(revs) == 0 {
		return "", cannot("generate", "no core of the tree records a gravity commit to pin the new one to")
	}
	sort.SliceStable(revs, func(i, j int) bool { return count[revs[i]] > count[revs[j]] })
	if len(revs) > 1 && count[revs[0]] == count[revs[1]] {
		return "", cannot("generate", "the tree's cores pin gravity %s and %s equally often: the new core's generator is not decided by them", revs[0], revs[1])
	}
	return revs[0], nil
}
