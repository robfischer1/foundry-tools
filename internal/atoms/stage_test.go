package atoms

import (
	"bytes"
	"context"
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
	prepush := []string{
		"fleet:orbit-drift", "fleet:dagger-lockstep", "fleet:renovate-lockstep", "fleet:node-kinds-declared", "fleet:consumed-events-emitted", "fleet:witness",
	}
	precommit := []string{
		"fleet:check-yaml", "fleet:check-added-large-files", "fleet:stop-justifications",
		"fleet:sast-ruleset-lanes", "fleet:copier-answers-intact", "fleet:ourea-config-retired-keys", "fleet:retired-verbs",
		"fleet:opengrep-sast", "fleet:hadolint", "fleet:wit-topics", "fleet:pdp-wired", "fleet:kafka-flows",
	}
	for _, tc := range []struct {
		stage string
		want  []string
	}{
		{checks.StageOrbit, nil},
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

// Atoms that exec a tool no container provisions say so, so a voting or
// local-hook set can leave them out mechanically. The tools container carries
// every program the atoms exec (the pinned tools, and the python layers' venv),
// so no atom is marked, and the mechanism keeps all of them.
func TestToolMarker(t *testing.T) {
	for _, a := range Builtin() {
		if a.Tool != "" {
			t.Errorf("%s is marked as needing %s, which the tools container provisions", a.ID, a.Tool)
		}
	}
	if free := WithoutTools(Builtin()); len(free) != len(Builtin()) {
		t.Errorf("WithoutTools kept %d of %d", len(free), len(Builtin()))
	}
	marked := []Atom{{ID: "a", Tool: "x"}, {ID: "b"}}
	if got := WithoutTools(marked); len(got) != 1 || got[0].ID != "b" {
		t.Errorf("WithoutTools kept %v, want only the atom with no tool", got)
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

// A stage no registered atom belongs to is a bad flag (2), not an empty vector.
func TestRunRefusesAStageWithNoAtom(t *testing.T) {
	dir := newRepo(t)
	put(t, dir, "a.txt", "a\n")
	commitAll(t, dir, "c1")
	now := func() time.Time { return time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC) }
	var out, errb bytes.Buffer
	if code := Run(context.Background(), []string{"-root", dir, "-stage", "mutation"}, &out, &errb, now); code != 2 ||
		out.Len() != 0 || !strings.Contains(errb.String(), `no registered atom belongs to the stage "mutation"`) {
		t.Errorf("a stage with no atom: exit %d, stdout %q, stderr %q", code, out.String(), errb.String())
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
	// A .git that is a FILE (a linked worktree's) is a file like any other.
	linked := treeIn(t, map[string]string{".git": "gitdir: /elsewhere", "a.txt": "a"}).tree()
	files, err = linked.files()
	if want := []string{".git", "a.txt"}; err != nil || !reflect.DeepEqual(files, want) {
		t.Errorf("files with a .git file %v, %v; want %v", files, err, want)
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
		{"both keeps the exit code of a failure", Cmd{Name: "sh", Args: []string{"-c", script + "3"}, Both: true}, "out\nerr\n", 3},
		{"stdout only is stdout alone, untrimmed, on success", Cmd{Name: "sh", Args: []string{"-c", script + "0"}, StdoutOnly: true}, "out\n", 0},
		{"stdout only leaves stderr out of a failure", Cmd{Name: "sh", Args: []string{"-c", script + "3"}, StdoutOnly: true}, "out\n", 3},
		{"stdout only outranks both", Cmd{Name: "sh", Args: []string{"-c", script + "0"}, StdoutOnly: true, Both: true}, "out\n", 0},
		{"a program that will not start is exit -1", Cmd{Name: "no-such-program-here"}, "no-such-program-here: ", -1},
		{"Env is added to the environment", Cmd{Name: "sh", Args: []string{"-c", "echo $ATOMS_T"}, Env: []string{"ATOMS_T=set"}}, "set", 0},
		{"without Env the variable is unset", Cmd{Name: "sh", Args: []string{"-c", "echo ${ATOMS_T-unset}"}}, "unset", 0},
		{"Env leaves the rest of the environment", Cmd{Name: "sh", Args: []string{"-c", "echo ${PATH:+path}"}, Env: []string{"ATOMS_T=set"}}, "path", 0},
		{"Stdin is what the program reads", Cmd{Name: "cat", Stdin: "hello"}, "hello", 0},
		{"with no Stdin the program reads nothing", Cmd{Name: "cat"}, "", 0},
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

// -spire and -narc-err reach the atoms that read them.
func TestRunHandsTheSocketAndTheNarcReasonToTheAtoms(t *testing.T) {
	dir := newRepo(t)
	put(t, dir, "a.txt", "a\n")
	commitAll(t, dir, "c1")
	build := func() (*Registry, error) {
		return NewRegistry(Atom{ID: "fleet:check-yaml", Run: func(_ context.Context, a checks.AtomDef, in Input) checks.Verdict {
			return checks.VerdictOf(a, 0, "seen "+in.Spire+"|"+in.NarcErr)
		}})
	}
	var out, errb bytes.Buffer
	args := []string{"-root", dir, "-spire", "/run/spire/agent.sock", "-narc-err", "the pin"}
	if code := run(context.Background(), args, &out, &errb, time.Now, build); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "seen /run/spire/agent.sock|the pin") {
		t.Errorf("the atom did not see the flags:\n%s", out.String())
	}
}
