package atoms

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/checks"
)

// THE STAGE SELECTS WHAT RUNS. The atoms of a stage are the catalogue's answer
// (checks.AtomsForStage), narrowed to what the binary carries.
func TestStageIDs(t *testing.T) {
	orbit := []string{"orbit:contracts", "orbit:sidecars", "orbit:repo"}
	prepush := []string{
		"fleet:orbit-drift", "fleet:dagger-lockstep", "fleet:node-kinds-declared", "fleet:consumed-events-emitted",
		"dies:data-keys", "ops:orbit-composed",
	}
	precommit := []string{
		"fleet:check-yaml", "fleet:check-added-large-files", "fleet:check-merge-conflict", "fleet:stop-justifications",
		"fleet:sast-ruleset-lanes", "fleet:copier-answers-intact", "fleet:ourea-config-retired-keys", "fleet:retired-verbs",
		"compose:no-tracked-secrets", "compose:third-party-pins", "dies:canonical", "dies:refusal-codes", "ops:yaml",
	}
	for _, tc := range []struct {
		stage string
		want  []string
	}{
		{checks.StageOrbit, orbit},
		{checks.StagePrepush, prepush},
		{checks.StagePrecommit, precommit},
		// The pull path is both of its stages, in the registry's order, and never
		// an atom of a lane of its own.
		{"", nil},
		{checks.StageMutation, nil},
		{checks.StageVisual, nil},
		{"no-such-stage", nil},
	} {
		got := StageIDs(tc.stage)
		if tc.stage == "" {
			for _, id := range got {
				if st := checks.AtomByID(id).Stage; st != checks.StagePrecommit && st != checks.StagePrepush {
					t.Errorf("the pull path carries %s, which belongs to stage %s", id, checks.AtomByID(id).Stage)
				}
			}
			if len(got) != len(precommit)+len(prepush) {
				t.Errorf("the pull path has %d atoms, want %d", len(got), len(precommit)+len(prepush))
			}
			continue
		}
		if !reflect.DeepEqual(got, tc.want) && !(len(got) == 0 && len(tc.want) == 0) {
			t.Errorf("stage %q: %v, want %v", tc.stage, got, tc.want)
		}
	}
}

// Every registered atom is at a stage a lane that starts a shadow grades: a
// catalogued atom of the mutation or visual stage would never be compared.
func TestEveryBuiltinAtomIsAtAStageAShadowedLaneGrades(t *testing.T) {
	for _, a := range Builtin() {
		switch checks.AtomByID(a.ID).Stage {
		case checks.StagePrecommit, checks.StagePrepush, checks.StageOrbit:
		default:
			t.Errorf("%s is at stage %s, which no lane that starts a shadow grades", a.ID, checks.AtomByID(a.ID).Stage)
		}
	}
}

func TestRegistryForStage(t *testing.T) {
	reg, err := NewRegistry(Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		stage   string
		want    []string
		wantErr string
	}{
		{checks.StageOrbit, []string{"orbit:contracts", "orbit:sidecars", "orbit:repo"}, ""},
		{checks.StageMutation, nil, `no registered atom belongs to the stage "mutation"`},
		{"typo", nil, `no registered atom belongs to the stage "typo"`},
	} {
		t.Run(tc.stage, func(t *testing.T) {
			got, err := reg.ForStage(tc.stage)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("error %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got.IDs(), tc.want) {
				t.Errorf("got %v, %v; want %v", got.IDs(), err, tc.want)
			}
		})
	}
	t.Run("the pull path leaves the orbit lane's atoms out", func(t *testing.T) {
		got, err := reg.ForStage("")
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range got.IDs() {
			if strings.HasPrefix(id, "orbit:") {
				t.Errorf("the pull path carries %s", id)
			}
		}
	})
}

// The binary takes a stage: -stage orbit runs the orbit lane's atoms and no
// others, in the registry's order, and a stage with no atom is a bad flag (2).
func TestRunTakesAStage(t *testing.T) {
	dir := newRepo(t)
	put(t, dir, "a.txt", "a\n")
	commitAll(t, dir, "c1")
	now := func() time.Time { return time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC) }

	var out, errb bytes.Buffer
	if code := Run(context.Background(), []string{"-root", dir, "-stage", "orbit"}, &out, &errb, now); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	var vector []checks.Verdict
	if err := json.Unmarshal(out.Bytes(), &vector); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, v := range vector {
		ids = append(ids, v.Atom)
		if v.Stage != checks.StageOrbit || v.Result != absent {
			t.Errorf("%s: stage %s result %s; a tree with no orbit surface is absent in every orbit atom", v.Atom, v.Stage, v.Result)
		}
	}
	if want := []string{"orbit:contracts", "orbit:sidecars", "orbit:repo"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("ran %v, want %v", ids, want)
	}

	out.Reset()
	errb.Reset()
	if code := Run(context.Background(), []string{"-root", dir, "-stage", "mutation"}, &out, &errb, now); code != 2 ||
		out.Len() != 0 || !strings.Contains(errb.String(), `no registered atom belongs to the stage "mutation"`) {
		t.Errorf("a stage with no atom: exit %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
}

// -dies reaches the atoms that grade against foundry-dies.
func TestRunHandsTheDiesCheckoutToTheAtoms(t *testing.T) {
	dir := newRepo(t)
	put(t, dir, "prime/orbits/a.orbit.toml", "x")
	commitAll(t, dir, "c1")
	var out, errb bytes.Buffer
	args := []string{"-root", dir, "-stage", "orbit", "-dies", t.TempDir()}
	if code := Run(context.Background(), args, &out, &errb, time.Now); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	var vector []checks.Verdict
	if err := json.Unmarshal(out.Bytes(), &vector); err != nil {
		t.Fatal(err)
	}
	for _, v := range vector {
		if v.Atom == "orbit:sidecars" && strings.Contains(v.Reason, "was not supplied") {
			t.Errorf("the checkout was supplied and the atom still says it was not: %s", v.Reason)
		}
	}
}

func TestEntriesGlobAndFiles(t *testing.T) {
	in := treeIn(t, map[string]string{"a.txt": "a", "d/b.txt": "b", "d/e/c.txt": "c", ".git/HEAD": "ref", "d/.git/inner": "i"})
	tr := in.tree()
	entries, err := tr.entries(".")
	if err != nil || !reflect.DeepEqual(entries, []string{".git/", "a.txt", "d/"}) {
		t.Errorf("entries %v, %v: want directories with a trailing slash", entries, err)
	}
	if _, err := tr.entries("nope"); err == nil {
		t.Error("a directory that is not there listed")
	}
	if got := tr.glob("d/*"); !reflect.DeepEqual(got, []string{"d/.git/", "d/b.txt", "d/e/"}) {
		t.Errorf("glob %v: want one segment, directories with a slash", got)
	}
	if got := tr.glob("a.txt"); !reflect.DeepEqual(got, []string{"a.txt"}) {
		t.Errorf("a literal pattern: %v", got)
	}
	if got := tr.glob("nope/*"); len(got) != 0 {
		t.Errorf("a directory that is not there matches %v", got)
	}
	// The root's .git is not walked; a .git deeper in is a directory like any other.
	files, err := tr.files()
	if want := []string{"a.txt", "d/.git/inner", "d/b.txt", "d/e/c.txt"}; err != nil || !reflect.DeepEqual(files, want) {
		t.Errorf("files %v, %v; want %v", files, err, want)
	}
	if _, err := (tree{root: filepath.Join(in.Root, "nope")}).files(); err == nil {
		t.Error("a root that is not there walked")
	}
}

func TestRunProgram(t *testing.T) {
	ctx := context.Background()
	script := "echo out; echo err >&2; exit "
	for _, tc := range []struct {
		name string
		cmd  Cmd
		out  string
		code int
	}{
		{"success keeps stdout only, trimmed", Cmd{Name: "sh", Args: []string{"-c", script + "0"}}, "out", 0},
		{"failure adds stderr", Cmd{Name: "sh", Args: []string{"-c", script + "3"}}, "out\nerr", 3},
		{"both is both streams, untrimmed, on success", Cmd{Name: "sh", Args: []string{"-c", script + "0"}, Both: true}, "out\nerr\n", 0},
		{"a program that will not start is exit -1", Cmd{Name: "no-such-program-here"}, "no-such-program-here: ", -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.cmd.Dir = t.TempDir()
			out, code := RunProgram(ctx, tc.cmd)
			if code != tc.code || (tc.code == -1 && !strings.HasPrefix(out, tc.out)) || (tc.code != -1 && out != tc.out) {
				t.Errorf("got %q, %d; want %q, %d", out, code, tc.out, tc.code)
			}
		})
	}
	t.Run("the program runs in its directory", func(t *testing.T) {
		dir := t.TempDir()
		out, _ := RunProgram(ctx, Cmd{Dir: dir, Name: "pwd"})
		if real, _ := filepath.EvalSymlinks(dir); out != real && out != dir {
			t.Errorf("ran in %q, want %q", out, dir)
		}
	})
	t.Run("an input's seam answers instead", func(t *testing.T) {
		in := Input{Exec: func(context.Context, Cmd) (string, int) { return "seam", 9 }}
		if out, code := in.run(ctx, Cmd{Name: "sh"}); out != "seam" || code != 9 {
			t.Errorf("got %q, %d", out, code)
		}
		if out, code := (Input{}).run(ctx, Cmd{Name: "sh", Args: []string{"-c", "exit 4"}}); out != "" || code != 4 {
			t.Errorf("the real thing: %q, %d", out, code)
		}
	})
}

func TestDoorDefault(t *testing.T) {
	if got := (Input{}).door(); got.Base != checks.OureaDoor {
		t.Errorf("the zero input's door is %q, want the production door", got.Base)
	}
	if got := (Input{Door: checks.Door{Base: "http://x"}}).door(); got.Base != "http://x" {
		t.Errorf("a door named is the door used: %q", got.Base)
	}
}

func TestOrbitHelpers(t *testing.T) {
	for in, want := range map[string]string{"http://door/rob/a.git": "a", "git@h:rob/blade-runner": "blade-runner", "": "."} {
		if got := starOf(in); got != want {
			t.Errorf("starOf(%q) = %q, want %q", in, got, want)
		}
	}
	if starSet(nil) != nil || len(starSet(map[string]string{"a": "x"})) != 1 {
		t.Error("an empty roster is nil (the two-part file-name rule alone); a roster is its set")
	}
	tr := treeIn(t, map[string]string{
		"fleet/stars/a/data.json": starData("a"),
		"fleet/stars/b/data.json": "not json at all",
	}).tree()
	got, err := roster(tr)
	if err != nil || !reflect.DeepEqual(got, map[string]string{"a": "a", "b": ""}) {
		t.Errorf("roster %v, %v: a shard that is not JSON still names its star, with no prefix", got, err)
	}
	broken := treeIn(t, map[string]string{"fleet/stars/a/data.json/x": "x"}).tree()
	if _, err := roster(broken); err == nil || !strings.Contains(err.Error(), "fleet/stars/a/data.json/ could not be read") {
		t.Errorf("a shard that will not read: %v", err)
	}
	if _, err := filesIn(treeIn(t, map[string]string{"x/y/z": "z"}).tree(), "x/*"); err != nil {
		t.Errorf("a directory is named, not read: %v", err)
	}
	dir := treeIn(t, map[string]string{"orbits/a.toml": "x", "orbits/sub/f": "f"}).tree()
	byName, err := filesByName(dir, "orbits/*")
	if err != nil || byName["a.toml"] == nil || byName["sub"] != nil || len(byName) != 2 {
		t.Errorf("filesByName %v, %v: a directory is a nil entry", byName, err)
	}
	if _, err := filesIn(treeIn(t, map[string]string{"orbits/a.toml/f": "x"}).tree(), "orbits/*/f"); err != nil {
		t.Errorf("a readable file: %v", err)
	}
	tmp := t.TempDir()
	link := filepath.Join(tmp, "orbits")
	if err := os.MkdirAll(link, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(tmp, "gone"), filepath.Join(link, "x.toml")); err != nil {
		t.Fatal(err)
	}
	if _, err := filesIn(tree{root: tmp}, "orbits/*.toml"); err == nil || !strings.Contains(err.Error(), "orbits/*.toml could not be read") {
		t.Errorf("a file that will not read names its pattern: %v", err)
	}
}
