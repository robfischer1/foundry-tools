package publishpack

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/atomtest"
	"dagger/foundry-tools/internal/checks"
)

var (
	pyRow = checks.AtomDef{ID: "python:artifacts", Stage: checks.StagePublish, Lane: checks.LanePython}
	tsRow = checks.AtomDef{ID: "ts:pack", Stage: checks.StagePublish, Lane: checks.LaneTS}
)

// shop is a fake uv and bun. exit and out are keyed by the argv's joined words
// up to "--destination" (so a key is the step); wrote lists what uv build writes.
type shop struct {
	t     *testing.T
	exit  map[string]int
	out   map[string]string
	wrote []string // files uv build writes into its --out-dir
	skip  bool     // bun pm pack writes no tarball
	calls []atoms.Cmd
}

func (s *shop) key(c atoms.Cmd) string {
	args := c.Args
	if i := slices.Index(args, "--destination"); i >= 0 {
		args = args[:i]
	}
	if i := slices.Index(args, "--out-dir"); i >= 0 {
		args = args[:i]
	}
	return strings.Join(append([]string{c.Name}, args...), " ")
}

func (s *shop) answer(c atoms.Cmd) (string, int) {
	s.calls = append(s.calls, c)
	k := s.key(c)
	if code := s.exit[k]; code != 0 {
		return s.out[k], code
	}
	switch k {
	case "uv build --python python3":
		d := c.Args[slices.Index(c.Args, "--out-dir")+1]
		for _, f := range s.wrote {
			s.put(filepath.Join(d, f), "x")
		}
	case "bun pm pack", "bun pm pack --ignore-scripts":
		if !s.skip {
			d := c.Args[slices.Index(c.Args, "--destination")+1]
			s.put(filepath.Join(d, "pkg-1.tgz"), "TGZ "+c.Dir)
		}
	}
	return "", 0
}

func (s *shop) put(p, body string) {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		s.t.Fatal(err)
	}
}

func (s *shop) ran() []string {
	var out []string
	for _, c := range s.calls {
		out = append(out, strings.Join(append([]string{c.Name}, c.Args...), " "))
	}
	return out
}

func start(t *testing.T, files map[string]string, s *shop) (atomkit.In, string) {
	t.Helper()
	work := t.TempDir()
	t.Setenv("ATOM_WORK", work)
	t.Setenv("ATOM_RELEASE_OUT", "")
	s.t = t
	in, f := atomtest.ToolTree(t, files, s.answer)
	f.Free = []string{"uv", "bun", "bunx"}
	return in, filepath.Join(work, "release")
}

const pyproject = "[project]\nname = \"svid-auth\"\n"

func TestDEF_B6_publish_PythonBuildsEveryWheelAndSdistIntoDist(t *testing.T) {
	s := &shop{wrote: []string{"svid_auth-0.3.0-py3-none-any.whl", "svid_auth-0.3.0.tar.gz", ".gitignore"}}
	in, out := start(t, map[string]string{"pyproject.toml": pyproject}, s)
	v := Python(context.Background(), in, pyRow)
	atomtest.Expect(t, v, checks.StatePass, atomtest.Pass, "svid_auth-0.3.0-py3-none-any.whl", "svid_auth-0.3.0.tar.gz")
	if got := s.ran(); len(got) != 1 || got[0] != "uv build --python python3 --out-dir "+filepath.Join(out, "dist") {
		t.Errorf("%v", got)
	}
	if !s.calls[0].Both {
		t.Error("uv build is read stdout and stderr both, as Dagger reads it")
	}
	if _, err := os.Stat(filepath.Join(out, "dist", "svid_auth-0.3.0.tar.gz")); err != nil {
		t.Error(err)
	}
}

func TestDEF_B6_publish_PythonTakesASetupPyTreeAndClearsAStaleDist(t *testing.T) {
	s := &shop{wrote: []string{"a-1.tar.gz"}}
	in, out := start(t, map[string]string{"setup.py": "x"}, s)
	stale := filepath.Join(out, "dist", "old-0.tar.gz")
	s.put(stale, "old")
	atomtest.Expect(t, Python(context.Background(), in, pyRow), checks.StatePass, atomtest.Pass)
	if _, err := os.Stat(stale); err == nil {
		t.Error("an earlier build's file was left in dist")
	}
}

func TestDEF_B6_publish_PythonAbsentWithoutAPythonProject(t *testing.T) {
	s := &shop{}
	in, _ := start(t, map[string]string{"package.json": "{}"}, s)
	atomtest.Expect(t, Python(context.Background(), in, pyRow), checks.StatePass, "absent", "python:artifacts: ABSENT")
	if len(s.calls) != 0 {
		t.Error("nothing runs")
	}
}

// [tool.uv] package = false is the declared not-a-package marker: a tooling-only
// project (python-repo-template's tooling_only render) has nothing to build, and
// uv build on it fails in the backend (hatchling) or, bypassed, would publish an
// empty wheel.
func TestPythonArtifactsAbsentOnAToolingOnlyPyproject(t *testing.T) {
	s := &shop{wrote: []string{"x-0.0.0-py3-none-any.whl"}}
	tooling := "[project]\nname = \"paneless-scripts\"\nversion = \"0.0.0\"\n\n[build-system]\nrequires = [\"hatchling\"]\nbuild-backend = \"hatchling.build\"\n\n[tool.uv]\npackage = false\n"
	in, out := start(t, map[string]string{"pyproject.toml": tooling}, s)
	atomtest.Expect(t, Python(context.Background(), in, pyRow), checks.StatePass, "absent", "python:artifacts: ABSENT", "[tool.uv] package = false", "nothing to build")
	if len(s.calls) != 0 {
		t.Errorf("uv build ran on a project that declares there is nothing to build: %v", s.ran())
	}
	if _, err := os.Stat(filepath.Join(out, "dist")); err == nil {
		t.Error("dist was staged for a project that publishes nothing")
	}
}

// The neighbours still build: package = true, no [tool.uv] table, and a
// pyproject too broken to read (uv build owns that error).
func TestPythonArtifactsStillBuildsWhenPackageIsNotFalse(t *testing.T) {
	for name, body := range map[string]string{
		"package true": pyproject + "\n[tool.uv]\npackage = true\n",
		"other uv key": pyproject + "\n[tool.uv]\ndev-dependencies = []\n",
		"no uv table":  pyproject,
		"unreadable":   "[project\nname = ",
		"comment only": pyproject + "\n# package = false\n",
	} {
		t.Run(name, func(t *testing.T) {
			s := &shop{wrote: []string{"svid_auth-0.3.0-py3-none-any.whl"}}
			in, _ := start(t, map[string]string{"pyproject.toml": body}, s)
			atomtest.Expect(t, Python(context.Background(), in, pyRow), checks.StatePass, atomtest.Pass, "svid_auth-0.3.0-py3-none-any.whl")
			if len(s.ran()) != 1 {
				t.Errorf("uv build did not run: %v", s.ran())
			}
		})
	}
}

func TestDEF_B6_publish_PythonBuildFailuresAreClassifiedLikeDagger(t *testing.T) {
	s := &shop{exit: map[string]int{"uv build --python python3": 2}, out: map[string]string{"uv build --python python3": "error: Failed to build `x`\n  Caused by: No module named hatchling"}}
	in, _ := start(t, map[string]string{"pyproject.toml": pyproject}, s)
	atomtest.Expect(t, Python(context.Background(), in, pyRow), checks.StateFindings, atomtest.Findings, "findings in uv build", "hatchling")

	s = &shop{exit: map[string]int{"uv build --python python3": 2}, out: map[string]string{"uv build --python python3": "error: request failed: connection refused"}}
	in, _ = start(t, map[string]string{"pyproject.toml": pyproject}, s)
	atomtest.Expect(t, Python(context.Background(), in, pyRow), checks.StateCannotRun, atomtest.Cannot, "uv build failed on a network fault")

	s = &shop{exit: map[string]int{"uv build --python python3": -1}, out: map[string]string{"uv build --python python3": "no uv"}}
	in, _ = start(t, map[string]string{"pyproject.toml": pyproject}, s)
	atomtest.Expect(t, Python(context.Background(), in, pyRow), checks.StateCannotRun, atomtest.Cannot, "could not run: uv build never ran: no uv")
}

func TestDEF_B6_publish_PythonBuildThatWroteNothingIsAFinding(t *testing.T) {
	s := &shop{wrote: []string{".gitignore"}}
	in, out := start(t, map[string]string{"pyproject.toml": pyproject}, s)
	v := Python(context.Background(), in, pyRow)
	atomtest.Expect(t, v, checks.StateFindings, atomtest.Findings, "findings: uv build exited clean and wrote no wheel or sdist to "+filepath.Join(out, "dist")+" (it wrote: .gitignore)")
}

func TestDEF_B6_publish_PythonSetupFailuresAreCouldNotRun(t *testing.T) {
	s := &shop{}
	in, _ := start(t, map[string]string{"pyproject.toml": pyproject}, s)
	blocker := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATOM_RELEASE_OUT", filepath.Join(blocker, "x"))
	atomtest.Expect(t, Python(context.Background(), in, pyRow), checks.StateCannotRun, atomtest.Cannot, "the release directory could not be made")

	in2 := atomtest.MissingRoot(t)
	atomtest.Expect(t, Python(context.Background(), in2, pyRow), checks.StateCannotRun, atomtest.Cannot, "the tree's root could not be listed")
}

func TestDEF_B6_publish_PythonBuildThatCannotBeReadIsCouldNotRun(t *testing.T) {
	s := &shop{wrote: []string{"a-1.tar.gz"}}
	in, out := start(t, map[string]string{"pyproject.toml": pyproject}, s)
	// uv "wrote" a file where dist should be a directory: the listing fails
	in.Exec = func(_ context.Context, c atoms.Cmd) (string, int) {
		if err := os.RemoveAll(filepath.Join(out, "dist")); err != nil {
			t.Fatal(err)
		}
		s.put(filepath.Join(out, "dist"), "not a dir")
		return "", 0
	}
	atomtest.Expect(t, Python(context.Background(), in, pyRow), checks.StateCannotRun, atomtest.Cannot, "what uv build wrote could not be read")
}

func theia(extra map[string]string) map[string]string {
	files := map[string]string{
		"package.json":            `{"name":"theia","private":true,"workspaces":["packages/*"]}`,
		"packages/a/package.json": `{"name":"@f/a","version":"1","publishConfig":{"registry":"https://r/"},"scripts":{"prepublishOnly":"pnpm run build"}}`,
		"packages/b/package.json": `{"name":"@f/b","version":"2","publishConfig":{"registry":"https://r/"}}`,
		"bun.lock":                "x",
	}
	for k, v := range extra {
		files[k] = v
	}
	return files
}

func TestDEF_B6_publish_TSInstallsOnceBuildsThroughTurboAndPacksEveryPackage(t *testing.T) {
	s := &shop{}
	in, out := start(t, theia(map[string]string{"turbo.json": "{}"}), s)
	v := TS(context.Background(), in, tsRow)
	atomtest.Expect(t, v, checks.StatePass, atomtest.Pass, "@f/a@1", "@f/b@2")
	want := []string{
		"bun install --frozen-lockfile",
		"bunx turbo run build --filter=@f/a",
		"bun pm pack --ignore-scripts --destination " + filepath.Join(out, "npm", "packages/a"),
		"bun pm pack --destination " + filepath.Join(out, "npm", "packages/b"),
	}
	if got := s.ran(); !slices.Equal(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
	if s.calls[1].Dir != in.Root || s.calls[2].Dir != filepath.Join(in.Root, "packages/a") {
		t.Errorf("dirs %v %v", s.calls[1].Dir, s.calls[2].Dir)
	}
	if _, err := os.Stat(filepath.Join(out, "npm", "packages/a", "pkg-1.tgz")); err != nil {
		t.Error(err)
	}
}

func TestDEF_B6_publish_TSWithoutTurboBuildsInThePackagesOwnDir(t *testing.T) {
	s := &shop{}
	in, _ := start(t, theia(nil), s)
	atomtest.Expect(t, TS(context.Background(), in, tsRow), checks.StatePass, atomtest.Pass)
	if got := s.ran()[1]; got != "bun run build" || s.calls[1].Dir != filepath.Join(in.Root, "packages/a") {
		t.Errorf("%q in %s", got, s.calls[1].Dir)
	}
}

func TestDEF_B6_publish_TSPlainRootIsPackedAtTheRootPlaceholder(t *testing.T) {
	s := &shop{}
	in, out := start(t, map[string]string{"package.json": `{"name":"@f/x","version":"1"}`}, s)
	atomtest.Expect(t, TS(context.Background(), in, tsRow), checks.StatePass, atomtest.Pass)
	// no prepublishOnly: nothing to install, nothing to build; bun runs what packing runs
	if got := s.ran(); !slices.Equal(got, []string{"bun pm pack --destination " + filepath.Join(out, "npm", "_root")}) {
		t.Errorf("%v", got)
	}
}

func TestDEF_B6_publish_TSOtherPrepublishOnlyScriptsRunHereBeforeThePack(t *testing.T) {
	s := &shop{}
	in, _ := start(t, map[string]string{"package.json": `{"name":"@f/x","version":"1","scripts":{"prepublishOnly":"tsc && node gen.js"}}`}, s)
	atomtest.Expect(t, TS(context.Background(), in, tsRow), checks.StatePass, atomtest.Pass)
	got := s.ran()
	if len(got) != 3 || got[0] != "bun install --frozen-lockfile" || got[1] != "bun run prepublishOnly" || !strings.HasPrefix(got[2], "bun pm pack --destination ") {
		t.Errorf("%v", got)
	}
}

func TestDEF_B6_publish_TSAbsentForAPythonTreeOrNoManifest(t *testing.T) {
	s := &shop{}
	in, _ := start(t, map[string]string{"package.json": "{}", "pyproject.toml": pyproject}, s)
	atomtest.Expect(t, TS(context.Background(), in, tsRow), checks.StatePass, "absent", "ts:pack: ABSENT")
	in, _ = start(t, map[string]string{"README.md": "x"}, s)
	atomtest.Expect(t, TS(context.Background(), in, tsRow), checks.StatePass, "absent", "no package.json")
	if len(s.calls) != 0 {
		t.Error("nothing runs")
	}
}

func TestDEF_B6_publish_TSPackageSelectionRefusalsAreDaggersSentences(t *testing.T) {
	s := &shop{}
	in, _ := start(t, map[string]string{"package.json": `{"name":"x","private":true}`}, s)
	atomtest.Expect(t, TS(context.Background(), in, tsRow), checks.StateFindings, atomtest.Findings, "findings: the record says this repo ships a package, and its root package.json (x) is private")
}

func TestDEF_B6_publish_TSInstallBuildAndPackFailuresAreClassified(t *testing.T) {
	// install: a tree finding
	s := &shop{exit: map[string]int{"bun install --frozen-lockfile": 1}, out: map[string]string{"bun install --frozen-lockfile": "error: lockfile had changes, but lockfile is frozen"}}
	in, _ := start(t, theia(nil), s)
	atomtest.Expect(t, TS(context.Background(), in, tsRow), checks.StateFindings, atomtest.Findings, "findings in bun install --frozen-lockfile", "lockfile is frozen")
	// install never started
	s = &shop{exit: map[string]int{"bun install --frozen-lockfile": -1}, out: map[string]string{"bun install --frozen-lockfile": "no bun"}}
	in, _ = start(t, theia(nil), s)
	atomtest.Expect(t, TS(context.Background(), in, tsRow), checks.StateCannotRun, atomtest.Cannot, "could not run: bun install never ran")

	// one build fails (finding), the next package still packs; the verdict is findings
	s = &shop{exit: map[string]int{"bun run build": 1}, out: map[string]string{"bun run build": "error TS2304"}}
	in, _ = start(t, theia(nil), s)
	v := TS(context.Background(), in, tsRow)
	atomtest.Expect(t, v, checks.StateFindings, atomtest.Findings, "findings in the build of @f/a@1", "TS2304")
	if got := s.ran(); got[len(got)-1][:12] != "bun pm pack " {
		t.Errorf("the other package is still packed: %v", got)
	}

	// a network fault is could-not-run
	s = &shop{exit: map[string]int{"bun run build": 1}, out: map[string]string{"bun run build": "error: connection reset"}}
	in, _ = start(t, theia(nil), s)
	atomtest.Expect(t, TS(context.Background(), in, tsRow), checks.StateCannotRun, atomtest.Cannot, "the build of @f/a@1 failed on a network fault")

	// prepublishOnly fails
	s = &shop{exit: map[string]int{"bun run prepublishOnly": 1}, out: map[string]string{"bun run prepublishOnly": "boom"}}
	in, _ = start(t, map[string]string{"package.json": `{"name":"@f/x","version":"1","scripts":{"prepublishOnly":"tsc && x"}}`}, s)
	atomtest.Expect(t, TS(context.Background(), in, tsRow), checks.StateFindings, atomtest.Findings, "findings in the prepublishOnly of @f/x@1")

	// pack fails; pack leaves no tarball
	s = &shop{exit: map[string]int{"bun pm pack": 1}, out: map[string]string{"bun pm pack": "error: no files"}}
	in, _ = start(t, map[string]string{"package.json": `{"name":"@f/x","version":"1"}`}, s)
	atomtest.Expect(t, TS(context.Background(), in, tsRow), checks.StateFindings, atomtest.Findings, "findings in bun pm pack @f/x@1")
	s = &shop{skip: true}
	in, out := start(t, map[string]string{"package.json": `{"name":"@f/x","version":"1"}`}, s)
	atomtest.Expect(t, TS(context.Background(), in, tsRow), checks.StateFindings, atomtest.Findings, "findings: bun pm pack wrote no tarball for @f/x@1 in "+filepath.Join(out, "npm", "_root"))
}

func TestDEF_B6_publish_TSSetupFailuresAreCouldNotRun(t *testing.T) {
	s := &shop{}
	in, _ := start(t, theia(nil), s)
	blocker := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATOM_RELEASE_OUT", filepath.Join(blocker, "x"))
	atomtest.Expect(t, TS(context.Background(), in, tsRow), checks.StateCannotRun, atomtest.Cannot, "the release directory could not be made")
	in2 := atomtest.MissingRoot(t)
	atomtest.Expect(t, TS(context.Background(), in2, tsRow), checks.StateCannotRun, atomtest.Cannot, "the tree's root could not be listed")
}

// BUILT AT THE ROOT THE EXECUTOR NAMES (DEF-B6-013): ts:pack reads, installs, builds
// and packs there and not in the copy the lead made of the tree.
func TestDEF_B6_bun_TSPackBuildsAtTheNamedRoot(t *testing.T) {
	s := &shop{}
	in, out := start(t, map[string]string{"package.json": `{"name":"@f/x","version":"1","scripts":{"prepublishOnly":"bun run build"}}`}, s)
	src := t.TempDir()
	s.put(filepath.Join(src, "package.json"), `{"name":"@f/y","version":"2","scripts":{"prepublishOnly":"bun run build"}}`)
	t.Setenv("ATOM_RELEASE_SRC", src)
	atomtest.Expect(t, TS(context.Background(), in, tsRow), checks.StatePass, atomtest.Pass, "@f/y@2")
	for i, c := range s.calls {
		if c.Dir != src {
			t.Errorf("call %d ran in %s, not in %s", i, c.Dir, src)
		}
	}
	if got := s.ran()[len(s.ran())-1]; got != "bun pm pack --ignore-scripts --destination "+filepath.Join(out, "npm", "_root") {
		t.Error(got)
	}
}
