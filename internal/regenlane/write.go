package regenlane

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/sync/errgroup"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/regenkit"
)

// THE WRITE TWIN OF ts:regen (atoms/dev/ts-regen). ts:regen rebuilds a world's
// guest, transpiles it and FAILS on any byte difference from src/<world>core-gen;
// this runs the very same steps (measure: the pins, the core at its commit, the
// build under the pinned rustc and wasm-tools, the transpile under the pinned
// node and jco) and, where every PIN holds, writes what it regenerated into the
// tree and the new guest and component hashes into provenance.json.
//
// A PIN THAT DOES NOT HOLD IS A FINDING AND WRITES NOTHING: the WIT, the result
// WIT, the tape, the core's own copy of the WIT, the wasm-tools, rustc and jco
// versions are what the output is a function of, and a regeneration under a pin
// that moved is not the regeneration the record describes. The two hashes the
// regeneration exists to change (guest.wasm, <world>.component.wasm) are not pins
// here; they are what is written.
//
// NOTHING BUT GENERATED FILES REACHES THE TREE (DEF-B5D-018: the executor's
// delivery diffs the WHOLE tree copy). The core, the build, node, jco, its npm
// prefix and cache and the cargo target all live under ATOM_WORK (the world's
// directory and the provision's), and the only writes into the tree are the
// files of src/<world>core-gen and its provenance.json.

const writeID = "dev:ts-regen"

// writeRow is the part of the catalogue row a verdict reads: its identity.
var writeRow = checks.AtomDef{ID: writeID, Stage: "dev", Lane: checks.LaneTS}

// writeUsage is the argument syntax, in every refusal about arguments.
const writeUsage = "usage: <world>... (a world is the <world> of src/<world>core-gen)"

// Seams a test replaces: the files the write twin reads and writes.
var (
	statFile  = os.Stat
	readFile  = os.ReadFile
	writeFile = os.WriteFile
)

var worldRE = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

// ParseWorlds reads the dev request's arguments: world names, each once.
func ParseWorlds(args []string) ([]string, error) {
	var out []string
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "-"):
			return nil, fmt.Errorf("unknown option %s", a)
		case !worldRE.MatchString(a):
			return nil, fmt.Errorf("%q is not a world name (lower-case letters and digits)", a)
		case slices.Contains(out, a):
			return nil, fmt.Errorf("%s is named twice", a)
		}
		out = append(out, a)
	}
	return out, nil
}

// KnownWorlds are the worlds a star can be asked to regenerate: the ones its
// justfile's regen-check names, and every other src/<world>core-gen that holds a
// provenance.json.
func KnownWorlds(root string) (named, others []string) {
	body, _ := os.ReadFile(filepath.Join(root, "justfile")) // no justfile names none
	named = checks.RegenWorlds(string(body))
	ents, _ := os.ReadDir(filepath.Join(root, "src")) // no src lists none
	for _, e := range ents {
		w, ok := strings.CutSuffix(e.Name(), "core-gen")
		if !ok || !e.IsDir() || slices.Contains(named, w) {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, "src", e.Name(), "provenance.json")); err == nil {
			others = append(others, w)
		}
	}
	slices.Sort(others)
	return named, others
}

// CheckWrite regenerates the worlds named and writes them into the tree.
func CheckWrite(ctx context.Context, in atomkit.In) checks.Verdict {
	in = writeScope(in)
	worlds, err := ParseWorlds(in.Args)
	if err != nil {
		return checks.VerdictOf(writeRow, 2, fmt.Sprintf("%s: CANNOT RUN - the arguments do not read: %v (%s)", writeID, err, writeUsage))
	}
	if len(worlds) == 0 {
		named, others := KnownWorlds(in.Root)
		return checks.VerdictOf(writeRow, 2, fmt.Sprintf("%s: CANNOT RUN - no world named (%s); the justfile's regen-check names: %s; other worlds with a provenance.json: %s",
			writeID, writeUsage, strings.Join(named, ", "), strings.Join(others, ", ")))
	}
	p := newProvision(in)
	results := make([]checks.RegenWorldResult, len(worlds))
	var g errgroup.Group
	g.SetLimit(worldsParallel)
	for i, w := range worlds {
		g.Go(func() error {
			results[i] = writeWorld(ctx, in, p, w)
			return nil
		})
	}
	_ = g.Wait()
	v := foldWrite(results)
	// the lines are the run's report: the engine and the pod keep an atom's stderr
	fmt.Fprintln(os.Stderr, v.Reason)
	return v
}

// writeScope is the tree the worlds live in. A bare workspace root (the monorepo's:
// checks.TSBareWorkspaceMembers) has no justfile, scripts or cores of its own, they
// are its one member's, as ts:regen reads them; the dev lane hands the atom the
// repo root whatever subtree the request came from. A root with no single member
// is the tree as it is.
func writeScope(in atomkit.In) atomkit.In {
	manifest, err := os.ReadFile(filepath.Join(in.Root, "package.json"))
	if err != nil {
		return in
	}
	members, bare := checks.TSBareWorkspaceMembers(string(manifest), in.Files)
	if !bare || len(members) != 1 {
		return in
	}
	return memberInput(in, members[0])
}

// foldWrite settles on the worst world: could-not-run beats a finding beats a pass.
func foldWrite(results []checks.RegenWorldResult) checks.Verdict {
	code := 0
	var out []string
	for _, r := range results {
		for _, l := range r.Lines {
			out = append(out, writeID+"("+r.World+"): "+l)
		}
		for _, p := range r.Problems {
			out = append(out, writeID+"("+r.World+"): FAIL: "+p)
		}
		if r.Err != "" {
			out = append(out, writeID+"("+r.World+"): CANNOT RUN: "+r.Err)
			code = 2
		} else if len(r.Problems) > 0 && code == 0 {
			code = 1
		}
	}
	v := checks.VerdictOf(writeRow, code, strings.Join(out, "\n"))
	if code == 0 {
		v.Reason = fmt.Sprintf("%s: PASS - %d world(s) regenerated\n%s", writeID, len(results), strings.Join(out, "\n"))
	}
	return v
}

// writeWorld is one world: measure it, hold the pins, and write the output.
func writeWorld(ctx context.Context, in atomkit.In, p *provision, name string) checks.RegenWorldResult {
	res := checks.RegenWorldResult{World: name}
	gen := "src/" + name + "core-gen"
	body, err := os.ReadFile(filepath.Join(in.Root, gen, "provenance.json"))
	if err != nil {
		res.Problems = []string{"no " + gen + "/provenance.json (" + err.Error() + ")"}
		return res
	}
	prov, err := checks.ParseRegenProv(string(body))
	if err != nil {
		res.Problems = []string{err.Error()}
		return res
	}
	w := &world{ctx: ctx, in: in, p: p, name: name, dir: filepath.Join(atoms.WorkDir(), "ts-regen", name), prov: prov}
	defer os.RemoveAll(w.dir)
	probes, b, err := w.measureBuilt(gen)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	// the two hashes this exists to move are written; every other probe is a pin
	var pins []checks.RegenProbe
	for _, pr := range probes {
		if pr.Label != "guest.wasm" && pr.Label != name+".component.wasm" {
			pins = append(pins, pr)
		}
	}
	res.Lines, res.Problems = checks.RegenCompare(pins)
	if len(res.Problems) > 0 {
		res.Problems = append(res.Problems, "nothing was written: a pin that moved is not the regeneration the record describes")
		return res
	}
	shipped := filepath.Join(in.Root, gen)
	moved, err := w.install(filepath.Join(w.dir, "gen"), shipped)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	guestSize, err1 := fileSize(filepath.Join(b.out, "guest.wasm"))
	compSize, err2 := fileSize(filepath.Join(b.out, name+".component.wasm"))
	if err := errors.Join(err1, err2); err != nil {
		res.Err = err.Error()
		return res
	}
	rewritten, err := regenkit.Rewrite(string(body), []regenkit.Rec{
		{Key: "guest_wasm", SHA256: b.guest, Bytes: guestSize},
		{Key: "component_wasm", SHA256: b.component, Bytes: compSize},
	})
	if err != nil {
		res.Problems = []string{err.Error()}
		return res
	}
	provNote := "provenance.json unchanged"
	if rewritten != string(body) {
		if err := writeFile(filepath.Join(shipped, "provenance.json"), []byte(rewritten), 0o644); err != nil {
			res.Err = err.Error()
			return res
		}
		provNote = "provenance.json rewritten"
	}
	res.Lines = append(res.Lines, fmt.Sprintf("guest.wasm %s; %s.component.wasm %s; %s; %s", delta(prov.GuestWasm.SHA256, b.guest),
		name, delta(prov.ComponentWasm.SHA256, b.component), moved, provNote))
	return res
}

// delta words a hash: unchanged, or old->new.
func delta(old, now string) string {
	if old == now {
		return "unchanged " + now[:10]
	}
	return old[:10] + "->" + now[:10]
}

// fileSize is a file's length in bytes.
func fileSize(path string) (int, error) {
	fi, err := statFile(path)
	if err != nil {
		return 0, err
	}
	return int(fi.Size()), nil
}

// install puts every regenerated file into the shipped directory: a file that
// differs is rewritten, one that is not there is added, one that is the same is
// left alone. A shipped file the regeneration did not make is NOT deleted: the
// tree copy's delivery carries adds and rewrites, and the words say which files
// a person must remove. provenance.json is the record, not an output, and the
// transpile's .jco-version is the tool's stamp, not a shipped file.
func (w *world) install(from, to string) (string, error) {
	var added, rewrote, same int
	made := map[string]bool{}
	err := filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		if rel == ".jco-version" {
			return nil
		}
		made[rel] = true
		body, err := readFile(path)
		if err != nil {
			return err
		}
		dst := filepath.Join(to, rel)
		old, oerr := os.ReadFile(dst)
		switch {
		case oerr == nil && bytes.Equal(old, body):
			same++
			return nil
		case oerr == nil:
			rewrote++
		default:
			added++
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return writeFile(dst, body, 0o644)
	})
	if err != nil {
		return "", err
	}
	var stale []string
	_ = filepath.WalkDir(to, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		if rel, _ := filepath.Rel(to, path); rel != "provenance.json" && !made[rel] {
			stale = append(stale, rel)
		}
		return nil
	})
	words := fmt.Sprintf("%d file(s) written (%d added, %d rewritten), %d unchanged", added+rewrote, added, rewrote, same)
	if len(stale) > 0 {
		slices.Sort(stale)
		words += fmt.Sprintf("; %d shipped file(s) the regeneration no longer makes were NOT deleted and need removing: %s", len(stale), strings.Join(stale, ", "))
	}
	return words, nil
}
