package regenlane

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// writeStar is star with a provenance that records the bytes too, as the real
// ones do, and the args the dev request carries.
func writeStar(t *testing.T, ver string, worlds ...string) atomkit.In {
	t.Helper()
	in := star(t, ver, worlds...)
	for _, w := range worlds {
		p := filepath.Join(in.Root, "src", w+"core-gen", "provenance.json")
		b, _ := os.ReadFile(p)
		s := strings.Replace(string(b), `"guest_wasm":{"sha256":"`+sum(guestBytes)+`"}`, `"guest_wasm":{"sha256":"`+sum(guestBytes)+`","bytes":`+strconv.Itoa(len(guestBytes))+`}`, 1)
		s = strings.Replace(s, `"component_wasm":{"sha256":"`+sum(compBytes)+`"}`, `"component_wasm":{"sha256":"`+sum(compBytes)+`","bytes":`+strconv.Itoa(len(compBytes))+`}`, 1)
		put(t, in.Root, "src/"+w+"core-gen/provenance.json", s)
	}
	in.Args = worlds
	return in
}

// treeOf is every file of the tree, by path, as the delivery would diff it.
func treeOf(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			b, _ := os.ReadFile(p)
			rel, _ := filepath.Rel(root, p)
			out[rel] = string(b)
		}
		return nil
	})
	return out
}

func wantWrite(t *testing.T, v checks.Verdict, state int, says ...string) {
	t.Helper()
	if v.State != state {
		t.Errorf("state %d, want %d: %s", v.State, state, v.Reason)
	}
	for _, s := range says {
		if !strings.Contains(v.Reason, s) {
			t.Errorf("the reason does not say %q:\n%s", s, v.Reason)
		}
	}
}

func TestDevTSRegenOfAWorldThatHoldsLeavesTheTreeAsItWas(t *testing.T) {
	_, in, _ := withFake(t, writeStar(t, "1.250.0", "stamp"))
	before := treeOf(t, in.Root)
	v := CheckWrite(t.Context(), in)
	wantWrite(t, v, 0, "dev:ts-regen: PASS - 1 world(s) regenerated", "dev:ts-regen(stamp): guest.wasm unchanged", "stamp.component.wasm unchanged", "0 file(s) written", "provenance.json unchanged")
	after := treeOf(t, in.Root)
	if len(after) != len(before) {
		t.Fatalf("the tree grew: %v", after)
	}
	for f, b := range before {
		if after[f] != b {
			t.Errorf("%s moved", f)
		}
	}
}

// DEF-B5D-018: nothing but generated files reaches the tree, and the work stays under ATOM_WORK.
func TestDevTSRegenWritesOnlyGeneratedFilesAndProvenanceIntoTheTree(t *testing.T) {
	_, in, _ := withFake(t, writeStar(t, "1.250.0", "stamp"))
	// the shipped copy is stale: a differing file, a missing one, and one nothing makes
	put(t, in.Root, "src/stampcore-gen/stamp-core.config.js", "old js")
	put(t, in.Root, "src/stampcore-gen/retired.d.ts", "gone")
	prov := filepath.Join(in.Root, "src/stampcore-gen/provenance.json")
	b, _ := os.ReadFile(prov)
	put(t, in.Root, "src/stampcore-gen/provenance.json", strings.Replace(string(b), sum(guestBytes), strings.Repeat("0", 64), 1))
	before := treeOf(t, in.Root)
	wantWrite(t, CheckWrite(t.Context(), in), 0, "1 file(s) written (0 added, 1 rewritten)", "guest.wasm 0000000000->", "provenance.json rewritten",
		"1 shipped file(s) the regeneration no longer makes were NOT deleted and need removing: retired.d.ts")
	after := treeOf(t, in.Root)
	changed := []string{}
	for f, v := range after {
		if before[f] != v {
			changed = append(changed, f)
		}
	}
	for f := range before {
		if _, ok := after[f]; !ok {
			t.Errorf("%s was deleted", f)
		}
	}
	if len(after) != len(before) {
		t.Errorf("files appeared in the tree: %v", after)
	}
	if strings.Join(sorted(changed), ",") != "src/stampcore-gen/provenance.json,src/stampcore-gen/stamp-core.config.js" {
		t.Errorf("changed %v", changed)
	}
	if after["src/stampcore-gen/stamp-core.config.js"] != "js" || !strings.Contains(after["src/stampcore-gen/provenance.json"], sum(guestBytes)) {
		t.Error("the regenerated file and the record were not written")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("ATOM_WORK"), "ts-regen", "stamp")); !errors.Is(err, os.ErrNotExist) {
		t.Error("the world's work directory was left behind")
	}
}

func sorted(s []string) []string {
	for i := range s {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
	return s
}

func TestDevTSRegenAddsAGeneratedFileTheTreeLacks(t *testing.T) {
	_, in, _ := withFake(t, writeStar(t, "1.250.0", "stamp"))
	if err := os.Remove(filepath.Join(in.Root, "src/stampcore-gen/stamp-core.config.js")); err != nil {
		t.Fatal(err)
	}
	wantWrite(t, CheckWrite(t.Context(), in), 0, "1 file(s) written (1 added, 0 rewritten)")
	if treeOf(t, in.Root)["src/stampcore-gen/stamp-core.config.js"] != "js" {
		t.Error("not added")
	}
}

func TestDevTSRegenWritesNothingWhenAPinMoved(t *testing.T) {
	f, in, _ := withFake(t, writeStar(t, "1.250.0", "stamp"))
	f.wasmVer = "wasm-tools 1.251.0\n"
	put(t, in.Root, "src/stampcore-gen/stamp-core.config.js", "old js")
	before := treeOf(t, in.Root)
	wantWrite(t, CheckWrite(t.Context(), in), 1, "wasm-tools: got 1.251.0, pinned 1.250.0", "nothing was written")
	for f, b := range treeOf(t, in.Root) {
		if before[f] != b {
			t.Errorf("%s was written under a moved pin", f)
		}
	}
}

func TestDevTSRegenRunsEveryWorldNamedAndFoldsThem(t *testing.T) {
	_, in, _ := withFake(t, writeStar(t, "1.250.0", "stamp", "tail"))
	wantWrite(t, CheckWrite(t.Context(), in), 0, "2 world(s) regenerated", "dev:ts-regen(stamp)", "dev:ts-regen(tail)")
	f, in2, _ := withFake(t, writeStar(t, "1.250.0", "stamp", "tail"))
	f.fail["tail-core-inner.sh"] = "cargo exploded"
	wantWrite(t, CheckWrite(t.Context(), in2), 2, "dev:ts-regen(tail): CANNOT RUN", "cargo exploded", "dev:ts-regen(stamp): guest.wasm")
}

func TestDevTSRegenRefusesWhatItCannotReadAndListsTheWorlds(t *testing.T) {
	_, in, _ := withFake(t, writeStar(t, "1.250.0", "stamp", "tail"))
	put(t, in.Root, "src/alarmcore-gen/provenance.json", "{}")
	put(t, in.Root, "src/emptycore-gen/other.txt", "x")
	put(t, in.Root, "src/notgen/provenance.json", "{}")
	put(t, in.Root, "src/loose-file", "x")
	in.Args = nil
	wantWrite(t, CheckWrite(t.Context(), in), 2, "no world named", "usage:", "regen-check names: stamp, tail", "provenance.json: alarm")
	for args, says := range map[string]string{"--all": "unknown option --all", "Stamp": "is not a world name", "stamp stamp": "stamp is named twice"} {
		in.Args = strings.Fields(args)
		wantWrite(t, CheckWrite(t.Context(), in), 2, "the arguments do not read", says)
	}
	in.Args = []string{"nosuch"}
	wantWrite(t, CheckWrite(t.Context(), in), 1, "no src/nosuchcore-gen/provenance.json")
	in.Args = []string{"alarm"}
	wantWrite(t, CheckWrite(t.Context(), in), 1, "provenance.json pins no")
}

func TestDevTSRegenCannotRunWhenTheRecordCannotBeRewritten(t *testing.T) {
	// a provenance with no bytes to rewrite is a finding, and nothing is written for it
	_, in, _ := withFake(t, star(t, "1.250.0", "stamp"))
	in.Args = []string{"stamp"}
	wantWrite(t, CheckWrite(t.Context(), in), 1, "guest_wasm has no bytes to rewrite")
}

func TestDevTSRegenCannotRunWhenAFileCannotBeReadOrWritten(t *testing.T) {
	for name, tc := range map[string]func(){
		"a size that cannot be read": func() { statFile = func(string) (os.FileInfo, error) { return nil, errors.New("no stat") } },
		"a record that cannot be written": func() {
			writeFile = func(p string, b []byte, m os.FileMode) error {
				if strings.HasSuffix(p, "provenance.json") {
					return errors.New("read-only")
				}
				return os.WriteFile(p, b, m)
			}
		},
		"a generated file that cannot be written": func() { writeFile = func(string, []byte, os.FileMode) error { return errors.New("disk full") } },
		"a generated file that cannot be read":    func() { readFile = func(string) ([]byte, error) { return nil, errors.New("unreadable") } },
	} {
		t.Run(name, func(t *testing.T) {
			stat, rd, wr := statFile, readFile, writeFile
			t.Cleanup(func() { statFile, readFile, writeFile = stat, rd, wr })
			_, in, _ := withFake(t, writeStar(t, "1.250.0", "stamp"))
			put(t, in.Root, "src/stampcore-gen/stamp-core.config.js", "old js")
			prov := filepath.Join(in.Root, "src/stampcore-gen/provenance.json")
			b, _ := os.ReadFile(prov)
			put(t, in.Root, "src/stampcore-gen/provenance.json", strings.Replace(string(b), sum(guestBytes), strings.Repeat("0", 64), 1))
			tc()
			wantWrite(t, CheckWrite(t.Context(), in), 2, "dev:ts-regen(stamp): CANNOT RUN")
		})
	}
}

func TestDevTSRegenCannotRunWhenAGeneratedFileHasNoPlaceInTheTree(t *testing.T) {
	f, in, _ := withFake(t, writeStar(t, "1.250.0", "stamp"))
	// the transpile also leaves interfaces/x.d.ts, and the tree has a FILE named interfaces
	inner := in.Exec
	in.Exec = func(ctx context.Context, c atoms.Cmd) (string, int) {
		out, code := inner(ctx, c)
		if strings.HasSuffix(c.Name, "transpile-stamp-inner.sh") {
			gen := filepath.Dir(c.Name) + "/gen"
			put(t, gen, "interfaces/x.d.ts", "d")
		}
		return out, code
	}
	_ = f
	put(t, in.Root, "src/stampcore-gen/interfaces", "a file")
	wantWrite(t, CheckWrite(t.Context(), in), 2, "dev:ts-regen(stamp): CANNOT RUN")
}

func TestKnownWorldsOfATreeWithNoJustfileOrSrc(t *testing.T) {
	named, others := KnownWorlds(t.TempDir())
	if len(named) != 0 || len(others) != 0 {
		t.Errorf("%v %v", named, others)
	}
}

// withFilesRecord adds a `files` object to the stamp world's provenance.json,
// as the real ones carry, laid out the way they are (one entry per file).
func withFilesRecord(t *testing.T, in atomkit.In, entries string) {
	t.Helper()
	p := filepath.Join(in.Root, "src/stampcore-gen/provenance.json")
	b, _ := os.ReadFile(p)
	s := strings.TrimSuffix(strings.TrimSpace(string(b)), "}") + `,
 "files": {
` + entries + `
 },
 "substrate": "aiws:stamp@0.1.0"}
`
	put(t, in.Root, "src/stampcore-gen/provenance.json", s)
}

func fileEntry(name, sha string, bytes int) string {
	return `  "` + name + `": {
   "sha256": "` + sha + `",
   "bytes": ` + strconv.Itoa(bytes) + `
  }`
}

// absorb-adult-erode: ts-regen rewrote the shipped files and the two wasm
// hashes but left provenance.json's `files` entries at the old hashes.
func TestDevTSRegenRewritesTheFilesRecordOfWhatItRegenerated(t *testing.T) {
	_, in, _ := withFake(t, writeStar(t, "1.250.0", "stamp"))
	put(t, in.Root, "src/stampcore-gen/stamp-core.config.js", "old js")
	withFilesRecord(t, in, fileEntry("stamp-core.config.js", sum([]byte("old js")), 6)+",\n"+fileEntry("retired.d.ts", strings.Repeat("1", 64), 7))
	wantWrite(t, CheckWrite(t.Context(), in), 0, "provenance.json rewritten",
		"1 entr(ies) in its files record name a file the regeneration no longer makes and need removing: retired.d.ts")
	got := mustRead(t, in.Root, "src/stampcore-gen/provenance.json")
	want := `"stamp-core.config.js": {
   "sha256": "` + sum([]byte("js")) + `",
   "bytes": 2
  }`
	if !strings.Contains(got, want) {
		t.Errorf("the regenerated file's entry was not moved:\n%s", got)
	}
	if !strings.Contains(got, fileEntry("retired.d.ts", strings.Repeat("1", 64), 7)) || !strings.Contains(got, `"substrate": "aiws:stamp@0.1.0"`) {
		t.Errorf("an entry the regeneration did not make, or a neighbour, was touched:\n%s", got)
	}
}

// The neighbouring case: a files record that already matches comes back byte
// for byte, and a regenerated file with no entry is named, not invented.
func TestDevTSRegenLeavesAFilesRecordThatHoldsAndNamesAnEntryItLacks(t *testing.T) {
	_, in, _ := withFake(t, writeStar(t, "1.250.0", "stamp"))
	withFilesRecord(t, in, fileEntry("stamp-core.config.js", sum([]byte("js")), 2))
	before := treeOf(t, in.Root)
	wantWrite(t, CheckWrite(t.Context(), in), 0, "provenance.json unchanged")
	if after := treeOf(t, in.Root); after["src/stampcore-gen/provenance.json"] != before["src/stampcore-gen/provenance.json"] {
		t.Errorf("a record that held was rewritten:\n%s", after["src/stampcore-gen/provenance.json"])
	}

	_, in, _ = withFake(t, writeStar(t, "1.250.0", "stamp"))
	withFilesRecord(t, in, fileEntry("other.js", strings.Repeat("2", 64), 3))
	before = treeOf(t, in.Root)
	wantWrite(t, CheckWrite(t.Context(), in), 0, "1 regenerated file(s) have no entry in its files record and need adding: stamp-core.config.js")
	if after := treeOf(t, in.Root); after["src/stampcore-gen/provenance.json"] != before["src/stampcore-gen/provenance.json"] {
		t.Error("an entry the author must add was invented or another was changed")
	}
}
