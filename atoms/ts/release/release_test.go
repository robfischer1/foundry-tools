package tsrelease

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
	"dagger/foundry-tools/internal/releasekit"
)

// bunStar is a bun star on the release path: its Dockerfile is the base and a
// COPY, its package.json defines the release script.
func bunStar() map[string]string {
	return atomtest.Star("calliope", "FROM "+atomtest.BunBase+"\nCOPY release/ /app/\nCMD [\"bun\", \"server.js\"]\n", map[string]string{
		"package.json": `{"scripts":{"release":"bun x.ts release"}}`,
		"bun.lock":     "",
		"src/app.ts":   "export {}\n",
		// what the copy leaves out
		"release/stale.js":             "old",
		"node_modules/dep/index.js":    "x",
		"packages/a/node_modules/b.js": "x",
	})
}

func line(c atoms.Cmd) string { return strings.Join(append([]string{c.Name}, c.Args...), " ") }

// bun is a fake bun: `bun run release` leaves server.js under release/ in the
// directory it ran in, unless told otherwise.
type bun struct {
	t     *testing.T
	exit  map[string]int
	out   map[string]string
	leave bool // the release script leaves nothing
	seen  map[string][]string
}

func (b *bun) answer(c atoms.Cmd) (string, int) {
	l := line(c)
	if b.seen == nil {
		b.seen = map[string][]string{}
	}
	b.seen[l] = atomsSorted(c.Dir)
	if code := b.exit[l]; code != 0 {
		return b.out[l], code
	}
	if l == "bun run release" && !b.leave {
		p := filepath.Join(c.Dir, "release", "server.js")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			b.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("//"), 0o644); err != nil {
			b.t.Fatal(err)
		}
	}
	return "", 0
}

// atomsSorted lists what is in a directory (files, recursively), for the copy's assertions.
func atomsSorted(dir string) []string {
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			r, _ := filepath.Rel(dir, p)
			out = append(out, r)
		}
		return nil
	})
	slices.Sort(out)
	return out
}

func run(t *testing.T, files map[string]string, b *bun) (checks.Verdict, *atomtest.Fake, string, string) {
	t.Helper()
	work := t.TempDir()
	t.Setenv("ATOM_WORK", work)
	t.Setenv("ATOM_RELEASE_OUT", "")
	if b == nil {
		b = &bun{}
	}
	b.t = t
	in, f := atomtest.ToolTree(t, files, b.answer)
	f.Free = []string{"bun"}
	return check(context.Background(), in), f, filepath.Join(work, "release"), in.Root
}

func TestTheDeclarationValidatesAndKeepsTheRowItReplaces(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrepush || Def.Lane != checks.LaneTS || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "bun" || Def.NotOnForks != "" || Def.NeedsManifest || Def.Surface != "root:package.json" ||
		!slices.Equal(Def.Needs, []string{"dies"}) || Def.Timeout != 45*time.Minute || Def.WritesTree ||
		!strings.HasPrefix(Def.Desc, "The record's release steps leave the image's files under release/") {
		t.Errorf("%+v", Def)
	}
}

// THE BUN RELEASE IS THE REPO'S OWN `release` SCRIPT, ON THE IMAGE'S OWN BASE:
// the tree copied without release/ or node_modules, the frozen install, then
// `bun run release` — and what it left under release/ is what the COPY gets.
func TestTSReleaseRunsTheReleaseScriptOnTheImagesBase(t *testing.T) {
	b := &bun{}
	v, f, out, root := run(t, bunStar(), b)
	atomtest.Expect(t, v, checks.StatePass, atomtest.Pass, "`bun run release`", atomtest.BunBase, "server.js")
	if got := f.Ran(); !slices.Equal(got, []string{"bun", "bun"}) {
		t.Fatalf("ran %v", got)
	}
	if line(f.Calls[0]) != "bun install --frozen-lockfile" || line(f.Calls[1]) != "bun run release" {
		t.Errorf("the frozen install, then the release script: %v", f.Calls)
	}
	// built in a copy that leaves out release/ and every node_modules
	if f.Calls[0].Dir == root {
		t.Error("the steps ran in the checkout and not in a copy")
	}
	files := b.seen["bun install --frozen-lockfile"]
	if want := []string{".copier-answers.yml", "Dockerfile", "bun.lock", "package.json", "src/app.ts"}; !slices.Equal(files, want) {
		t.Errorf("the copy holds %v, want %v", files, want)
	}
	// and the checkout is as it was found
	if _, err := os.Stat(filepath.Join(root, "release", "stale.js")); err != nil {
		t.Errorf("the checkout was touched: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(out, "server.js")); err != nil || string(got) != "//" {
		t.Errorf("release/ was not carried into the release directory: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(out, "stale.js")); err == nil {
		t.Error("a release/ the tree carried leaked into the artefacts")
	}
}

// A RELEASE THAT FAILS IS THE TREE'S FINDING, NAMED; one that could not run at
// all, or a network the substrate lost, is a could-not-run. A package.json with
// no release script fails in bun, which is the tree's finding too.
func TestTSReleaseSettlesAFailedStepByWhoseFaultItIs(t *testing.T) {
	const install, release = "bun install --frozen-lockfile", "bun run release"
	cases := []struct {
		name  string
		exit  map[string]int
		out   map[string]string
		state checks.State
		why   []string
	}{
		{"a failing release", map[string]int{release: 2}, map[string]string{release: "error TS2322: Type 'string' is not assignable"}, checks.StateFindings, []string{"`bun run release` exited 2", "TS2322"}},
		{"no release script", map[string]int{release: 1}, map[string]string{release: `error: Script not found "release"`}, checks.StateFindings, []string{"Script not found"}},
		{"a frozen lockfile that moved", map[string]int{install: 1}, map[string]string{install: "error: lockfile had changes, but lockfile is frozen"}, checks.StateFindings, []string{"`bun install --frozen-lockfile` exited 1", "lockfile is frozen"}},
		{"a command that is not there", map[string]int{release: 127}, map[string]string{release: "bun: not found"}, checks.StateCannotRun, []string{"bun: not found"}},
		{"a network fault", map[string]int{release: 1}, map[string]string{release: "error: ECONNRESET fetching the tarball"}, checks.StateCannotRun, []string{"ECONNRESET"}},
		{"a step that never started", map[string]int{release: -1}, map[string]string{release: "bun: not found"}, checks.StateCannotRun, []string{"the atom never ran", "bun: not found"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, f, _, _ := run(t, bunStar(), &bun{exit: c.exit, out: c.out})
			atomtest.Expect(t, v, c.state, v.Result, c.why...)
			if v.State != int(c.state) {
				t.Errorf("state %d", v.State)
			}
			if _, failedInstall := c.exit[install]; failedInstall && len(f.Calls) != 1 {
				t.Errorf("the release script ran on top of a failed install: %v", f.Calls)
			}
		})
	}
}

// A clean release that left nothing under release/ gave the COPY nothing.
func TestAReleaseThatLeavesNothingIsAFinding(t *testing.T) {
	v, _, out, _ := run(t, bunStar(), &bun{leave: true})
	atomtest.Expect(t, v, checks.StateFindings, atomtest.Findings, "left nothing under release/")
	if _, err := os.Stat(out); err == nil {
		t.Error("an empty release was carried")
	}
}

// THE ABSENCES, EACH A 0 THAT SAYS WHY AND BUILDS NOTHING: no image, not a
// star, an image that builds itself, and an image on another lane's base — the
// one that matters for a tree declaring two toolchains.
func TestTheBunReleaseIsAbsentWhereTheImageIsNotItsOwn(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		why   string
	}{
		{"no Dockerfile", map[string]string{"package.json": "{}", ".copier-answers.yml": "service_name: x\n"}, "tracks no Dockerfile"},
		{"not a star", map[string]string{"package.json": "{}", "Dockerfile": "FROM " + atomtest.BunBase + "\nCOPY release/ /app\n"}, "not a star image"},
		{"builds itself", map[string]string{"package.json": "{}", ".copier-answers.yml": "service_name: x\n", "Dockerfile": "FROM " + atomtest.BunBase + " AS b\nRUN bun build\nFROM " + atomtest.BunBase + "\nCOPY --from=b /deploy /app\n"}, "builds itself"},
		{"bun tree on the python base", atomtest.Star("iris", "FROM "+atomtest.PythonBase+"\nCOPY release/app /app\n", map[string]string{"package.json": "{}"}), "not registry.notusmi.com/foundry/base-images/bun"},
		{"a base no lane names", atomtest.Star("x", "FROM ${BASE}\nCOPY release/app /app\n", map[string]string{"package.json": "{}"}), "a base this lane cannot name"},
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

// A FRONTEND STAR IS NAMED BY ITS RECORD. The frontend template asks for
// repo_name, not service_name, and repo_name is not the star (demeter's is
// demeter-mcp): the record whose meta.repo is the clone URL's key names it, the
// way it names a repository with no answers file at all (calliope #46).
func TestAFrontendStarWithNoServiceNameIsNamedByItsRecord(t *testing.T) {
	files := bunStar()
	files[".copier-answers.yml"] = "repo_name: calliope-mcp\nframework: mcp\n"
	records := map[string]string{"calliope": `{"repo":"rob/calliope"}`}
	for name, c := range map[string]struct {
		origin string
		state  checks.State
		result string
		why    string
	}{
		"named by the door's clone":    {"http://ourea.default.svc.cluster.local:8215/calliope.git", checks.StatePass, atomtest.Pass, "`bun run release`"},
		"with no clone to find it by":  {"", checks.StatePass, atomtest.Absent, "no service_name in .copier-answers.yml and no clone URL"},
		"with a clone no record names": {"http://ourea.default.svc.cluster.local:8215/elsewhere.git", checks.StatePass, atomtest.Absent, "no service_name in .copier-answers.yml, and no record in foundry-dies names repo rob/elsewhere"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("ATOM_WORK", t.TempDir())
			b := &bun{t: t}
			in, f := atomtest.ToolTree(t, files, b.answer)
			f.Free = []string{"bun"}
			in = atomtest.WithDies(t, in, c.origin, records)
			atomtest.Expect(t, check(context.Background(), in), c.state, c.result, c.why)
		})
	}
}

// THE QUESTION ITSELF FAILING IS A COULD-NOT-RUN: a tree that cannot be read, a
// Dockerfile that cannot be read, records that cannot be listed.
func TestTheBunReleaseSaysWhyItCouldNotAsk(t *testing.T) {
	t.Setenv("ATOM_WORK", t.TempDir())
	in, _ := atomtest.ToolTree(t, bunStar(), nil)
	in.FilesErr = os.ErrPermission
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "the tree could not be read")

	in, f := atomtest.ToolTree(t, bunStar(), nil)
	if err := os.Remove(filepath.Join(in.Root, "Dockerfile")); err != nil {
		t.Fatal(err)
	}
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "Dockerfile could not be read")
	if len(f.Calls) != 0 {
		t.Errorf("a Dockerfile that could not be read still built: %v", f.Calls)
	}

	// A star named by its record, whose records cannot be listed: the name is
	// unknown, not absent — never "not a star".
	files := bunStar()
	delete(files, ".copier-answers.yml")
	in, _ = atomtest.ToolTree(t, files, nil)
	in.Origin = "http://ourea:8215/calliope.git"
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "ts:release: CANNOT RUN", "could not be listed")
}

// A tree that cannot be copied is the run's could-not-run.
func TestATreeThatCannotBeCopiedIsCouldNotRun(t *testing.T) {
	// the work directory is a file, so nothing can be built under it
	file := filepath.Join(t.TempDir(), "work")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATOM_WORK", file)
	in, f := atomtest.ToolTree(t, bunStar(), nil)
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "ts:release: CANNOT RUN")
	if len(f.Calls) != 0 {
		t.Errorf("the steps ran with nowhere to build: %v", f.Calls)
	}
}

// A release that cannot be carried out is the run's could-not-run.
func TestAReleaseThatCannotBeCarriedOutIsCouldNotRun(t *testing.T) {
	file := filepath.Join(t.TempDir(), "out")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATOM_WORK", t.TempDir())
	t.Setenv("ATOM_RELEASE_OUT", filepath.Join(file, "release"))
	b := &bun{t: t}
	in, f := atomtest.ToolTree(t, bunStar(), b.answer)
	f.Free = []string{"bun"}
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "ts:release: CANNOT RUN - the release could not be carried out")
}

// BUILT IN PLACE when the executor owns /src for this container (DEF-B6-013): no
// copy, the steps run in the named root, and release/ is read from it.
func TestDEF_B6_bun_TSReleaseBuildsInPlaceWhenToldTheRoot(t *testing.T) {
	b := &bun{}
	root := t.TempDir()
	t.Setenv(releasekit.SrcEnv, root)
	v, f, out, _ := run(t, bunStar(), b)
	atomtest.Expect(t, v, checks.StatePass, atomtest.Pass, "server.js")
	if f.Calls[0].Dir != root || f.Calls[1].Dir != root {
		t.Errorf("the steps ran in %s and %s, not in %s", f.Calls[0].Dir, f.Calls[1].Dir, root)
	}
	if got, err := os.ReadFile(filepath.Join(out, "server.js")); err != nil || string(got) != "//" {
		t.Errorf("%q %v", got, err)
	}
}
