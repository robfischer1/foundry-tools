package basesrelease

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/atomtest"
	"dagger/foundry-tools/internal/checks"
)

// layers is a repository of bases: two layers that carry their own release, a
// base that does not, and a stale release/ a local build left behind.
func layers() map[string]string {
	return map[string]string{
		"bases/layer-cc/Dockerfile":                "FROM docker.io/oven/bun:1@sha256:" + strings.Repeat("d", 64) + " AS release\nFROM scratch\nCOPY release/layer-cc/ /\n",
		"bases/layer-cc/package.json":              `{"scripts":{"release":"sh release.sh"}}`,
		"bases/layer-cc/bun.lock":                  "",
		"bases/layer-pi/Dockerfile":                "FROM scratch\nCOPY release/layer-pi/ /\n",
		"bases/layer-pi/package.json":              `{"scripts":{"release":"sh release.sh"}}`,
		"bases/bun/Dockerfile":                     "FROM scratch\n",
		"bases/layer-cc/release/old":               "stale",
		"bases/layer-pi/node_modules/dep/index.js": "x",
	}
}

func line(c atoms.Cmd) string { return strings.Join(append([]string{c.Name}, c.Args...), " ") }

// bun is a fake bun: `bun run release` leaves bin/tool under release/ in the
// directory it ran in, unless told otherwise.
type bun struct {
	t     *testing.T
	exit  map[string]int
	out   map[string]string
	leave bool
	dirs  []string
}

func (b *bun) answer(c atoms.Cmd) (string, int) {
	l := line(c) + " @" + filepath.Base(c.Dir)
	b.dirs = append(b.dirs, l)
	if code := b.exit[l]; code != 0 {
		return b.out[l], code
	}
	if strings.HasPrefix(l, "bun run release") && !b.leave {
		p := filepath.Join(c.Dir, "release", "bin", "tool-"+filepath.Base(c.Dir))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			b.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("//"), 0o755); err != nil {
			b.t.Fatal(err)
		}
	}
	return "", 0
}

func run(t *testing.T, files map[string]string, b *bun) (checks.Verdict, *atomtest.Fake, string, string) {
	t.Helper()
	work := t.TempDir()
	t.Setenv("ATOM_WORK", work)
	t.Setenv("ATOM_RELEASE_OUT", "")
	t.Setenv("ATOM_RELEASE_SRC", "")
	if b == nil {
		b = &bun{}
	}
	b.t = t
	in, f := atomtest.ToolTree(t, files, b.answer)
	f.Free = []string{"bun"}
	return check(context.Background(), in), f, filepath.Join(work, "release"), in.Root
}

func TestTheDeclarationValidates(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StageBases || Def.Toolchain != "bun" || Def.Surface != "any:bases/*/package.json" ||
		Def.Group != checks.GroupOf(id) || Def.Timeout != 45*time.Minute || Def.WritesTree || !slices.Equal(Def.Needs, nil) {
		t.Errorf("%+v", Def)
	}
}

// EACH BASE WITH A RELEASE ROOT is released in its own directory, the frozen
// install then its own release script, in a copy that leaves out release/ and
// node_modules, and what it leaves is carried to <out>/<base>/.
func TestEachBaseIsReleasedInItsOwnDirectory(t *testing.T) {
	b := &bun{}
	v, f, out, root := run(t, layers(), b)
	atomtest.Expect(t, v, checks.StatePass, atomtest.Pass, "2 base(s)", "release/layer-cc/ holds bin/", "release/layer-pi/ holds bin/")
	want := []string{"bun install --frozen-lockfile @layer-cc", "bun run release @layer-cc", "bun install --frozen-lockfile @layer-pi", "bun run release @layer-pi"}
	if !slices.Equal(b.dirs, want) {
		t.Errorf("ran %v", b.dirs)
	}
	if f.Calls[0].Dir == filepath.Join(root, "bases", "layer-cc") {
		t.Error("the steps ran in the checkout and not in a copy")
	}
	for _, base := range []string{"layer-cc", "layer-pi"} {
		got, err := os.ReadFile(filepath.Join(out, base, "bin", "tool-"+base))
		if err != nil || string(got) != "//" {
			t.Errorf("%s: %q %v", base, got, err)
		}
	}
	// the stale release/ did not ride along, the checkout is as it was
	if _, err := os.Stat(filepath.Join(out, "layer-cc", "old")); err == nil {
		t.Error("a release/ the tree carried leaked into the artefacts")
	}
	if _, err := os.Stat(filepath.Join(root, "bases", "layer-cc", "release", "old")); err != nil {
		t.Errorf("the checkout was touched: %v", err)
	}
}

// Where the pod gives the container a tree of its own, the steps run in it.
func TestInThePodsOwnTreeTheStepsRunThereWithNothingCopied(t *testing.T) {
	own := t.TempDir()
	files := layers()
	t.Setenv("ATOM_RELEASE_SRC", own)
	b := &bun{t: t}
	in, f := atomtest.ToolTree(t, files, b.answer)
	f.Free = []string{"bun"}
	// the pod's tree is the checkout: lay the files there
	for p, body := range files {
		full := filepath.Join(own, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	work := t.TempDir()
	t.Setenv("ATOM_WORK", work)
	t.Setenv("ATOM_RELEASE_OUT", "")
	t.Setenv("ATOM_RELEASE_SRC", own)
	v := check(context.Background(), in)
	atomtest.Expect(t, v, checks.StatePass, atomtest.Pass, "2 base(s)")
	if f.Calls[0].Dir != filepath.Join(own, "bases", "layer-cc") {
		t.Errorf("ran in %s", f.Calls[0].Dir)
	}
	// a release/ an earlier run left in that tree is cleared before the script runs
	if _, err := os.Stat(filepath.Join(work, "release", "layer-cc", "old")); err == nil {
		t.Error("a stale release/ was carried")
	}
}

// A STEP THAT FAILS NAMES THE BASE, and the bases after it do not run.
func TestAFailedStepNamesTheBaseAndStopsTheChain(t *testing.T) {
	b := &bun{exit: map[string]int{"bun run release @layer-cc": 2}, out: map[string]string{"bun run release @layer-cc": "error: claude --version printed nothing"}}
	v, f, out, _ := run(t, layers(), b)
	atomtest.Expect(t, v, checks.StateFindings, atomtest.Findings, "bases/layer-cc: ", "`bun run release` exited 2", "printed nothing")
	if len(f.Calls) != 2 {
		t.Errorf("ran %d steps", len(f.Calls))
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("a failed run carried something")
	}
	// a network fault is the substrate's
	b = &bun{exit: map[string]int{"bun install --frozen-lockfile @layer-pi": 1}, out: map[string]string{"bun install --frozen-lockfile @layer-pi": "error: ECONNRESET fetching the tarball"}}
	v, _, _, _ = run(t, layers(), b)
	atomtest.Expect(t, v, checks.StateCannotRun, atomtest.Cannot, "bases/layer-pi: ", "ECONNRESET")
}

func TestAReleaseThatLeavesNothingIsAFinding(t *testing.T) {
	v, _, _, _ := run(t, layers(), &bun{leave: true})
	atomtest.Expect(t, v, checks.StateFindings, atomtest.Findings, "bases/layer-cc's release steps ran clean and left nothing under its release/", "bases/layer-cc/Dockerfile would have nothing to copy")
}

func TestABaseThatCopiesFromTheWrongPlaceIsRefused(t *testing.T) {
	files := layers()
	files["bases/layer-pi/Dockerfile"] = "FROM scratch\nCOPY release/ /\n"
	v, f, _, _ := run(t, files, nil)
	atomtest.Expect(t, v, checks.StateFindings, atomtest.Findings, "bases/layer-pi/Dockerfile copies from release/ but not from release/layer-pi/")
	if len(f.Calls) != 0 {
		t.Errorf("a refusal built something: %v", f.Calls)
	}
}

// ABSENT where no base carries a release of its own; CANNOT RUN where the tree
// or the carrying failed.
func TestAbsentAndCannotRun(t *testing.T) {
	v, f, _, _ := run(t, map[string]string{"bases/bun/Dockerfile": "FROM scratch\n", "bases/bun/package.json": "{}"}, nil)
	atomtest.Expect(t, v, checks.StatePass, atomtest.Absent, "no base carries a package.json")
	if len(f.Calls) != 0 {
		t.Error("an absence built something")
	}

	t.Setenv("ATOM_WORK", t.TempDir())
	in, _ := atomtest.ToolTree(t, layers(), nil)
	in.FilesErr = os.ErrPermission
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "the tree could not be read")

	// a release directory that cannot be written into
	work := t.TempDir()
	t.Setenv("ATOM_WORK", work)
	t.Setenv("ATOM_RELEASE_OUT", filepath.Join(work, "file"))
	if err := os.WriteFile(filepath.Join(work, "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := &bun{t: t}
	in, f2 := atomtest.ToolTree(t, layers(), b.answer)
	f2.Free = []string{"bun"}
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "release could not be carried out")
}

// A tree that cannot be copied to build in is the substrate's: the atom's work
// directory is a file.
func TestATreeThatCannotBeCopiedCannotRun(t *testing.T) {
	work := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(work, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATOM_WORK", work)
	t.Setenv("ATOM_RELEASE_OUT", "")
	t.Setenv("ATOM_RELEASE_SRC", "")
	b := &bun{t: t}
	in, f := atomtest.ToolTree(t, layers(), b.answer)
	f.Free = []string{"bun"}
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "the tree could not be copied")
	if len(f.Calls) != 0 {
		t.Errorf("a tree that could not be copied still built: %v", f.Calls)
	}
}
