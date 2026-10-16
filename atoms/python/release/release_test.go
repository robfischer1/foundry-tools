package pythonrelease

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

// pythonStar is a python star on the release path: the base and a COPY of the venv.
func pythonStar() map[string]string {
	return atomtest.Star("iris", "FROM "+atomtest.PythonBase+"\nCOPY --chown=999:999 release/app /app\nCMD [\"iris\"]\n", map[string]string{
		"pyproject.toml": "[project]\nname = \"iris\"\n",
		"uv.lock":        "",
		"src/iris/x.py":  "x = 1\n",
		// what the copy leaves out
		".venv/bin/python":           "local",
		"release/app/old":            "x",
		"src/iris/__pycache__/x.pyc": "x",
	})
}

func line(c atoms.Cmd) string { return strings.Join(append([]string{c.Name}, c.Args...), " ") }

const sync = "uv sync --locked --no-dev --no-editable"

// uv is a fake uv: the sync leaves a venv with the star's console script in the
// app directory it ran in, unless told otherwise.
type uv struct {
	t     *testing.T
	exit  int
	out   string
	leave bool // the sync leaves no venv
	dir   string
	saw   []string
}

func (u *uv) answer(c atoms.Cmd) (string, int) {
	u.dir = c.Dir
	_ = filepath.WalkDir(c.Dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			r, _ := filepath.Rel(c.Dir, p)
			u.saw = append(u.saw, r)
		}
		return nil
	})
	if u.exit != 0 {
		return u.out, u.exit
	}
	if !u.leave {
		p := filepath.Join(c.Dir, ".venv", "bin", "iris")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			u.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("#!/app/.venv/bin/python\n"), 0o755); err != nil {
			u.t.Fatal(err)
		}
		if err := os.Symlink("/usr/local/bin/python3", filepath.Join(c.Dir, ".venv", "bin", "python")); err != nil {
			u.t.Fatal(err)
		}
	}
	return "", 0
}

func run(t *testing.T, files map[string]string, u *uv) (checks.Verdict, *atomtest.Fake, *uv, string) {
	t.Helper()
	work := t.TempDir()
	t.Setenv("ATOM_WORK", work)
	t.Setenv("ATOM_RELEASE_OUT", "")
	t.Setenv("ATOM_RELEASE_APP", filepath.Join(work, "app"))
	if u == nil {
		u = &uv{}
	}
	u.t = t
	in, f := atomtest.ToolTree(t, files, u.answer)
	f.Free = []string{"uv"}
	return check(context.Background(), in), f, u, work
}

func TestTheDeclarationValidatesAndKeepsTheRowItReplaces(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrepush || Def.Lane != checks.LanePython || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "python" || Def.NotOnForks != "" || !Def.NeedsManifest || Def.Surface != "root:pyproject.toml" ||
		!slices.Equal(Def.Needs, []string{"dies", "tool:uv"}) || Def.Timeout != 45*time.Minute || Def.WritesTree ||
		!strings.HasPrefix(Def.Desc, "The venv the image will carry builds from the star's lock on the image's own python base") {
		t.Errorf("%+v", Def)
	}
}

// THE PYTHON RELEASE IS THE STAR'S VENV, BUILT AT THE IMAGE'S /app ON ITS OWN
// BASE from its own lock, as a wheel, with no extras: the project and its
// required dependencies.
func TestPythonReleaseBuildsTheVenvFromTheLockWithNoExtras(t *testing.T) {
	v, f, u, work := run(t, pythonStar(), nil)
	atomtest.Expect(t, v, checks.StatePass, atomtest.Pass, "with no extras", atomtest.PythonBase)
	if len(f.Calls) != 1 || line(f.Calls[0]) != sync {
		t.Fatalf("the venv was not built from the lock: %v", f.Calls)
	}
	if strings.Contains(line(f.Calls[0]), "--extra") {
		t.Error("an extra the tree never declared was installed")
	}
	if want := filepath.Join(work, "app"); f.Calls[0].Dir != want {
		t.Errorf("synced in %s, want the app directory %s", f.Calls[0].Dir, want)
	}
	// the tree is copied in without a local .venv, release/ or bytecode
	slices.Sort(u.saw)
	if want := []string{".copier-answers.yml", "Dockerfile", "pyproject.toml", "src/iris/x.py", "uv.lock"}; !slices.Equal(u.saw, want) {
		t.Errorf("the app directory held %v, want %v", u.saw, want)
	}
	// and the venv reaches the release directory at app/.venv, links kept
	venv := filepath.Join(work, "release", "app", ".venv", "bin")
	if _, err := os.Stat(filepath.Join(venv, "iris")); err != nil {
		t.Errorf("the venv was not carried: %v", err)
	}
	if target, err := os.Readlink(filepath.Join(venv, "python")); err != nil || target != "/usr/local/bin/python3" {
		t.Errorf("the venv's interpreter link was not kept: %q %v", target, err)
	}
}

// uv EXITS 2 ON EVERY ERROR, so the code cannot say whose fault it was: a lock
// behind its pyproject is the tree's finding, a network the substrate lost is a
// could-not-run, and a sync that left no venv gave the COPY nothing.
func TestPythonReleaseReadsUvsFailuresByTheirWords(t *testing.T) {
	v, _, _, _ := run(t, pythonStar(), &uv{exit: 2, out: "error: The lockfile at `uv.lock` needs to be updated, but `--locked` was provided."})
	atomtest.Expect(t, v, checks.StateFindings, atomtest.Findings, "exited 2", "needs to be updated")

	v, _, _, _ = run(t, pythonStar(), &uv{exit: 2, out: "error: Failed to fetch: `https://pypi.org/simple/mcp/`\n  Caused by: dial tcp: i/o timeout"})
	atomtest.Expect(t, v, checks.StateCannotRun, atomtest.Cannot, "i/o timeout")

	v, _, _, _ = run(t, pythonStar(), &uv{exit: -1, out: "uv: not found"})
	atomtest.Expect(t, v, checks.StateCannotRun, atomtest.Cannot, "the atom never ran", "uv: not found")

	v, _, _, work := run(t, pythonStar(), &uv{leave: true})
	atomtest.Expect(t, v, checks.StateFindings, atomtest.Findings, "left no /app/.venv")
	if _, err := os.Stat(filepath.Join(work, "release")); err == nil {
		t.Error("a venv that was not there was carried")
	}
}

// THE ABSENCES, EACH A 0 THAT SAYS WHY AND BUILDS NOTHING.
func TestPythonReleaseIsAbsentWhereTheImageIsNotItsOwn(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		why   string
	}{
		{"not a star", map[string]string{"pyproject.toml": "", "Dockerfile": "FROM " + atomtest.PythonBase + "\nCOPY release/app /app\n"}, "not a star image"},
		// mnemosyne's shape: a go.mod and the pyproject its port left behind,
		// the image on the go base. python:release is not its release.
		{"python tree on the go base", atomtest.Star("mnemosyne", "FROM "+atomtest.GoBase+"\nCOPY release/mnemosyne /mnemosyne\n", map[string]string{"go.mod": "module x\n", "pyproject.toml": ""}), "is built on " + atomtest.GoBase},
		{"a base no lane names", atomtest.Star("x", "FROM ${BASE}\nCOPY release/app /app\n", map[string]string{"pyproject.toml": ""}), "a base this lane cannot name"},
		{"a Dockerfile that builds itself", atomtest.Star("x", "FROM "+atomtest.PythonBase+" AS b\nRUN uv sync\nFROM "+atomtest.PythonBase+"\nCOPY --from=b /app /app\n", map[string]string{"pyproject.toml": ""}), "builds itself"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, f, _, _ := run(t, c.files, nil)
			atomtest.Expect(t, v, checks.StatePass, atomtest.Absent, c.why)
			if len(f.Calls) != 0 {
				t.Errorf("an absence built something: %v", f.Calls)
			}
		})
	}
}

// THE QUESTION ITSELF FAILING IS A COULD-NOT-RUN: a tree that cannot be read, a
// Dockerfile that cannot be read, a star whose records cannot be listed.
func TestPythonReleaseSaysWhyItCouldNotAsk(t *testing.T) {
	t.Setenv("ATOM_WORK", t.TempDir())
	in, _ := atomtest.ToolTree(t, pythonStar(), nil)
	in.FilesErr = os.ErrPermission
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "the tree could not be read")

	in, f := atomtest.ToolTree(t, pythonStar(), nil)
	if err := os.Remove(filepath.Join(in.Root, "Dockerfile")); err != nil {
		t.Fatal(err)
	}
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "Dockerfile could not be read")
	if len(f.Calls) != 0 {
		t.Errorf("a Dockerfile that could not be read still synced: %v", f.Calls)
	}

	files := pythonStar()
	delete(files, ".copier-answers.yml")
	in, _ = atomtest.ToolTree(t, files, nil)
	in.Origin = "http://ourea:8215/iris.git"
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "python:release: CANNOT RUN", "could not be listed")
}

// A tree that cannot be copied to the app directory is the run's could-not-run.
func TestATreeThatCannotBeCopiedIsCouldNotRun(t *testing.T) {
	file := filepath.Join(t.TempDir(), "app")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATOM_RELEASE_APP", file)
	t.Setenv("ATOM_WORK", t.TempDir())
	in, f := atomtest.ToolTree(t, pythonStar(), nil)
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "python:release: CANNOT RUN")
	if len(f.Calls) != 0 {
		t.Errorf("uv ran with nowhere to build: %v", f.Calls)
	}
}
