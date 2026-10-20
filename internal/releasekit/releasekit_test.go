package releasekit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// mem is a Tree over a map, with the faults the callers must word.
type mem struct {
	files   map[string]string
	records map[string]string
	fail    map[string]error // by method name, or "File:<path>"
	dirs    []string
	origin  string
}

// at names the clone the tree came from.
func (m mem) at(origin string) mem { m.origin = origin; return m }

func (m mem) Population(_ context.Context, patterns ...string) ([]string, error) {
	if err := m.fail["Population"]; err != nil {
		return nil, err
	}
	var all []string
	for f := range m.files {
		all = append(all, f)
	}
	slices.Sort(all)
	if len(patterns) == 0 {
		return all, nil
	}
	var out []string
	for _, p := range patterns {
		out = append(out, atoms.Matching(all, p)...)
	}
	return out, nil
}
func (m mem) File(_ context.Context, rel string) (string, error) {
	if err := m.fail["File:"+rel]; err != nil {
		return "", err
	}
	b, ok := m.files[rel]
	if !ok {
		return "", os.ErrNotExist
	}
	return b, nil
}
func (m mem) Present(_ context.Context, rel string) (string, bool) {
	b, ok := m.files[rel]
	return b, ok
}
func (m mem) Entries(context.Context) ([]string, error) {
	if err := m.fail["Entries"]; err != nil {
		return nil, err
	}
	return m.dirs, nil
}
func (m mem) IsDir(_ context.Context, rel string) (bool, error) {
	if err := m.fail["IsDir"]; err != nil {
		return false, err
	}
	return slices.Contains(m.dirs, rel+"/"), nil
}
func (m mem) RecordPaths(context.Context) ([]string, error) {
	if err := m.fail["RecordPaths"]; err != nil {
		return nil, err
	}
	var out []string
	for p := range m.records {
		out = append(out, p)
	}
	slices.Sort(out)
	return out, nil
}
func (m mem) Record(_ context.Context, rel string) (string, error) {
	if err := m.fail["Record:"+rel]; err != nil {
		return "", err
	}
	return m.records[rel], nil
}

var errBoom = errors.New("boom")

func row(id string) checks.AtomDef {
	return checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LaneGo}
}

const goBase = "registry.notusmi.com/foundry/base-images/go:stable@sha256:dddd"

func TestUT_ResolveStandsDownInTheOrderTheChainsAsked(t *testing.T) {
	ctx := context.Background()
	star := map[string]string{".copier-answers.yml": "service_name: hades\n"}
	with := func(extra map[string]string) mem {
		files := map[string]string{}
		for k, v := range star {
			files[k] = v
		}
		for k, v := range extra {
			files[k] = v
		}
		return mem{files: files}
	}
	cases := []struct {
		name  string
		tree  mem
		state int
		said  string
	}{
		{"the tree cannot be read", mem{fail: map[string]error{"Population": errBoom}}, 2, "go:release: CANNOT RUN - the tree could not be read: boom"},
		{"no Dockerfile", with(nil), 0, "ABSENT - this repository tracks no Dockerfile or Containerfile"},
		{"no star", mem{files: map[string]string{"Dockerfile": "FROM x\n"}}, 0, "ABSENT - no .copier-answers.yml and no clone URL to find a record by, so the repository names no star, so this is not a star image"},
		{"records that cannot be listed", mem{files: map[string]string{"Dockerfile": "FROM x\n"}, fail: map[string]error{"RecordPaths": errBoom}}.at("http://o:8215/x.git"), 2, "CANNOT RUN - the fleet's records could not be listed: boom"},
		{"a Dockerfile that cannot be read", with(map[string]string{"Dockerfile": "x"}).failing("File:Dockerfile"), 2, "CANNOT RUN - Dockerfile could not be read: boom"},
		{"a Dockerfile that builds itself", with(map[string]string{"Dockerfile": "FROM x\nRUN go build\n"}), 0, "ABSENT - no tracked Dockerfile copies from release/, so this image compiles itself"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sub, v := Resolve(ctx, c.tree, row("go:release"), c.tree.origin, Compiles)
			if v == nil || v.State != c.state || !strings.Contains(v.Reason, c.said) {
				t.Fatalf("want state %d saying %q, got %+v (%+v)", c.state, c.said, v, sub)
			}
		})
	}
	sub, v := Resolve(ctx, with(map[string]string{"Dockerfile": "FROM x\nCOPY release/hades /hades\n"}), row("go:release"), "", Compiles)
	if _, v := Resolve(ctx, with(map[string]string{"Dockerfile": "FROM x\nRUN y\n"}), row("ts:release"), "", Builds); v == nil || !strings.Contains(v.Reason, "so this image builds itself") {
		t.Errorf("the bun and python releases say builds: %+v", v)
	}
	if v != nil || sub.Star != "hades" || sub.Dockerfile != "Dockerfile" || !strings.Contains(sub.Body, "release/hades") {
		t.Errorf("an image that asks: %+v %v", sub, v)
	}
}

// failing marks a method as failing, on a copy.
func (m mem) failing(keys ...string) mem {
	f := map[string]error{}
	for k, v := range m.fail {
		f[k] = v
	}
	for _, k := range keys {
		f[k] = errBoom
	}
	m.fail = f
	return m
}

// DEF-B6-037: a Dockerfile under bases/<b>/ that copies from release/ is
// bases:release's and bases:go-release's, not the star release atoms': go:release
// on base-images was a could-not-run (no go.mod at the root) once a base
// carried a go.mod.
func TestDEF_B6_037_AStarReleaseIgnoresTheDockerfilesOfBases(t *testing.T) {
	ctx := context.Background()
	answers := ".copier-answers.yml"
	ask := "FROM scratch\nCOPY release/chairman/chairman-svid /usr/bin/chairman-svid\n"
	basesOnly := mem{files: map[string]string{answers: "service_name: base-images\n", "bases/chairman/Dockerfile": ask, "bases/layer-pi/Dockerfile": ask}}
	if _, v := Resolve(ctx, basesOnly, row("go:release"), "", Compiles); v == nil || v.State != 0 || !strings.Contains(v.Reason, "ABSENT - no tracked Dockerfile copies from release/") {
		t.Errorf("a repository of bases: %+v", v)
	}
	both := mem{files: map[string]string{answers: "service_name: hades\n", "bases/b/Dockerfile": ask, "Dockerfile": "FROM x\nCOPY release/hades /hades\n"}}
	if sub, v := Resolve(ctx, both, row("go:release"), "", Compiles); v != nil || sub.Dockerfile != "Dockerfile" {
		t.Errorf("a star that also holds bases: %+v %+v", sub, v)
	}
}

func TestUT_OnBaseStandsDownUnlessTheImageIsOnTheAsksBase(t *testing.T) {
	a := row("ts:release")
	for _, c := range []struct{ name, body, want string }{
		{"the go base", "FROM " + goBase + "\nCOPY release/x /x\n", "ABSENT - Dockerfile is built on " + goBase + ", not registry.notusmi.com/foundry/base-images/bun"},
		{"a base no lane names", "FROM ${B}\nCOPY release/x /x\n", "built on a base this lane cannot name"},
	} {
		if ref, v := OnBase(a, Subject{Dockerfile: "Dockerfile", Body: c.body}, "bun"); ref != "" || v == nil || v.State != 0 || !strings.Contains(v.Reason, c.want) {
			t.Errorf("%s: %q %+v", c.name, ref, v)
		}
	}
	ref, v := OnBase(a, Subject{Body: "FROM registry.notusmi.com/foundry/base-images/bun:stable@sha256:aa\nCOPY release/x /x\n"}, "bun")
	if v != nil || !strings.HasPrefix(ref, "registry.notusmi.com/foundry/base-images/bun:") {
		t.Errorf("%q %+v", ref, v)
	}
}

func TestUT_StarNameReadsTheAnswersThenTheRecord(t *testing.T) {
	ctx := context.Background()
	records := map[string]string{"fleet/repos/hephaestus.json": `{"repo":"rob/hephaestus"}`}
	for _, c := range []struct {
		name, origin string
		files        map[string]string
		fail         map[string]error
		want, err    string
		notAStar     bool
	}{
		{"service_name", "", map[string]string{".copier-answers.yml": "service_name: tron\n"}, nil, "tron", "", false},
		{"a record by the clone", "http://o:8215/hephaestus.git", nil, nil, "hephaestus", "", false},
		{"a clone no record names", "http://o:8215/nobody.git", nil, nil, "", "no .copier-answers.yml, and no record in foundry-dies names repo rob/nobody, so the repository names no star", true},
		{"an answers file without the name and no clone", "", map[string]string{".copier-answers.yml": "x: y\n"}, nil, "", "no service_name in .copier-answers.yml and no clone URL to find a record by, so the repository names no star", true},
		{"a record that cannot be read", "http://o:8215/other.git", nil, map[string]error{"Record:fleet/repos/hephaestus.json": errBoom}, "", "record fleet/repos/hephaestus.json could not be read: boom", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := StarName(ctx, mem{files: c.files, records: records, fail: c.fail}, c.origin)
			if got != c.want || (err == nil) != (c.err == "") || (err != nil && (err.Error() != c.err || IsNotAStar(err) != c.notAStar)) {
				t.Errorf("%q %v", got, err)
			}
		})
	}
	name, body, err := RecordOf(ctx, mem{records: records}, "rob/hephaestus")
	if name != "hephaestus" || !strings.Contains(body, "rob/hephaestus") || err != nil {
		t.Errorf("%q %q %v", name, body, err)
	}
	if Origin("http://o:8215/a.git", "http://o:8215/b.git") != "http://o:8215/a.git" || Origin("", "http://o:8215/b.git") != "http://o:8215/b.git" {
		t.Error("the door's clone is read before the hook's")
	}
}

func TestUT_GoPlanDerivesTheBinariesAndRefusesWhatHasNoMain(t *testing.T) {
	ctx := context.Background()
	sub := Subject{Star: "hades", Body: "FROM x\nCOPY release/hades /hades\n"}
	plan, why := GoPlan(ctx, mem{dirs: []string{"cmd/hades/", "vendor/"}}, sub)
	if why != "" || !plan.Vendored || len(plan.Binaries) != 1 || plan.Binaries[0].Package != "./cmd/hades" {
		t.Errorf("%+v %q", plan, why)
	}
	// a tree whose root cannot be listed is not vendored, and the plan stands
	plan, why = GoPlan(ctx, mem{dirs: []string{"cmd/hades/"}, fail: map[string]error{"Entries": errBoom}}, sub)
	if why != "" || plan.Vendored {
		t.Errorf("%+v %q", plan, why)
	}
	if _, why = GoPlan(ctx, mem{}, sub); !strings.Contains(why, "copies release/hades and the tree carries no cmd/hades") {
		t.Errorf("%q", why)
	}
	if _, why = GoPlan(ctx, mem{dirs: []string{"cmd/hades/"}, fail: map[string]error{"IsDir": errBoom}}, sub); !strings.Contains(why, "the tree could not be read for cmd/hades: boom") {
		t.Errorf("%q", why)
	}
	if _, why = GoPlan(ctx, mem{}, Subject{Star: "hades", Body: "FROM x\nCOPY release/ /a\n"}); !strings.Contains(why, "copies no file out of it") {
		t.Errorf("%q", why)
	}
}

func TestUT_RustPlanIsThePackageOfTheSameName(t *testing.T) {
	plan, why := RustPlan(Subject{Star: "tron", Body: "FROM x\nCOPY release/tron /tron\n"})
	if why != "" || plan.Lane != checks.LaneRust || plan.Binaries[0].Package != "tron" {
		t.Errorf("%+v %q", plan, why)
	}
	if _, why = RustPlan(Subject{Star: "tron", Body: "FROM x\nCOPY release/-x /tron\n"}); !strings.Contains(why, "is not a binary name") {
		t.Errorf("%q", why)
	}
}

func TestUT_GoRootWantsTheRootModuleAndAWalkThatWorks(t *testing.T) {
	ctx := context.Background()
	if err := GoRoot(ctx, mem{files: map[string]string{"go.mod": ""}}); err != nil {
		t.Error(err)
	}
	if err := GoRoot(ctx, mem{files: map[string]string{"tools/go.mod": ""}}); err == nil || !strings.Contains(err.Error(), "no go.mod at the repository root") {
		t.Error(err)
	}
	if err := GoRoot(ctx, mem{fail: map[string]error{"Population": errBoom}}); err == nil || !strings.Contains(err.Error(), "could not enumerate the tree's Go modules: boom") {
		t.Error(err)
	}
}

func TestUT_StepVerdictSettlesAnExitByWhoseFaultItIs(t *testing.T) {
	a := row("ts:release")
	argv := []string{"bun", "run", "release"}
	for _, c := range []struct {
		code  int
		out   string
		state int
		ok    bool
		said  string
	}{
		{0, "built", 0, true, "ts:release: PASS"},
		{1, "error TS2322", 1, false, "`bun run release` exited 1\nerror TS2322"},
		{2, "error: Failed to fetch: i/o timeout dial tcp", 2, false, "i/o timeout"},
		{127, "not found", 2, false, "exited 127"},
		{-1, "bun: not found", 2, false, "the atom never ran: bun: not found"},
	} {
		v, ok := StepVerdict(a, argv, c.code, c.out)
		if v.State != c.state || ok != c.ok || !strings.Contains(v.Reason, c.said) {
			t.Errorf("exit %d: %+v ok=%v", c.code, v, ok)
		}
	}
}

func TestUT_MadeVerdictsNameWhatWasBuiltAndFindWhatIsMissing(t *testing.T) {
	steps := checks.VerdictOf(row("ts:release"), 0, "out")
	if v := TSMade(row("ts:release"), "ref", nil, steps); v.State != 1 || !strings.Contains(v.Reason, "left nothing under release/") {
		t.Errorf("%+v", v)
	}
	if v := TSMade(row("ts:release"), "ref", []string{"a.js", "b/"}, steps); v.State != 0 || !strings.Contains(v.Reason, "`bun run release` on ref; release/ holds a.js, b/\nts:release: PASS") || !slices.Equal(v.Logs, []string{"out"}) {
		t.Errorf("%+v", v)
	}
	if v := PythonMade(row("python:release"), "ref", nil, steps); v.State != 1 || !strings.Contains(v.Reason, "left no /app/.venv") {
		t.Errorf("%+v", v)
	}
	if v := PythonMade(row("python:release"), "ref", []string{"iris"}, steps); v.State != 0 || !strings.Contains(v.Reason, "the venv, with no extras, on ref\nts:release: PASS") {
		t.Errorf("%+v", v)
	}
	plan := checks.ReleasePlan{Lane: checks.LaneGo, Binaries: []checks.ReleaseBinary{{Name: "a"}}}
	if v := Scoped(plan, steps); !strings.HasPrefix(v.Reason, "release build: a, from the Dockerfile's COPY lines") || !strings.HasSuffix(v.Reason, "\nts:release: PASS") {
		t.Errorf("%+v", v)
	}
}

func TestUT_TheDirectoriesAreTheCallersToNameAndDefaultToTheAtomsOwn(t *testing.T) {
	t.Setenv("ATOM_WORK", "/work/go__release")
	t.Setenv(OutEnv, "")
	t.Setenv(AppEnv, "")
	if OutDir() != "/work/go__release/release" || AppDir() != checks.PythonReleaseApp || BuildDir() != "/work/go__release/release-src" {
		t.Errorf("%s %s %s", OutDir(), AppDir(), BuildDir())
	}
	t.Setenv(OutEnv, "/work/release")
	t.Setenv(AppEnv, "/tmp/app")
	if OutDir() != "/work/release" || AppDir() != "/tmp/app" {
		t.Errorf("%s %s", OutDir(), AppDir())
	}
}

func TestUT_RunStepsStopsAtTheFirstFailureAndAnswersTheLast(t *testing.T) {
	var ran []string
	in := atoms.Input{Exec: func(_ context.Context, c atoms.Cmd) (string, int) {
		ran = append(ran, strings.Join(append([]string{c.Name}, c.Args...), " ")+" in "+c.Dir)
		if c.Name == "two" {
			return "bad", 1
		}
		return c.Name + " done", 0
	}}
	steps := [][]string{{"one"}, {"two", "x"}, {"three"}}
	if v := RunSteps(context.Background(), in, row("a:b"), "/d", nil, steps[:1]); v.State != 0 || !slices.Equal(v.Logs, []string{"one done"}) {
		t.Errorf("%+v", v)
	}
	ran = nil
	v := RunSteps(context.Background(), in, row("a:b"), "/d", nil, steps)
	if v.State != 1 || !strings.Contains(v.Reason, "`two x` exited 1") || !slices.Equal(ran, []string{"one in /d", "two x in /d"}) {
		t.Errorf("%+v %v", v, ran)
	}
}

func TestUT_DiskIsATreeOverACheckout(t *testing.T) {
	ctx := context.Background()
	root, dies := t.TempDir(), t.TempDir()
	put := func(base, rel, body string) {
		p := filepath.Join(base, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put(root, "Dockerfile", "FROM x")
	put(root, "cmd/a/main.go", "package main")
	put(root, "go.mod", "module x")
	put(root, "tools/go.mod", "module y")
	put(dies, "fleet/repos/b.json", `{"repo":"rob/b"}`)
	put(dies, "fleet/repos/a.json", `{"repo":"rob/a"}`)
	d := Disk{Root: root, Files: []string{"go.mod", "tools/go.mod", "Dockerfile", "cmd/a/main.go"}, Dies: dies}

	all, err := d.Population(ctx)
	if err != nil || len(all) != 4 {
		t.Errorf("%v %v", all, err)
	}
	mods, _ := d.Population(ctx, "**/go.mod", "go.mod")
	if !slices.Equal(mods, []string{"go.mod", "tools/go.mod"}) {
		t.Errorf("a pattern given twice is read once, in order: %v", mods)
	}
	if _, err := (Disk{FilesErr: errBoom}).Population(ctx); err != errBoom {
		t.Errorf("%v", err)
	}
	if b, err := d.File(ctx, "Dockerfile"); b != "FROM x" || err != nil {
		t.Errorf("%q %v", b, err)
	}
	if _, ok := d.Present(ctx, "cmd"); ok {
		t.Error("a directory is not a present file")
	}
	if b, ok := d.Present(ctx, "go.mod"); !ok || b != "module x" {
		t.Errorf("%q %v", b, ok)
	}
	if e, _ := d.Entries(ctx); !slices.Contains(e, "cmd/") || !slices.Contains(e, "Dockerfile") {
		t.Errorf("%v", e)
	}
	for rel, want := range map[string]bool{"cmd/a": true, "Dockerfile": false, "nowhere": false} {
		if got, err := d.IsDir(ctx, rel); got != want || err != nil {
			t.Errorf("%s: %v %v", rel, got, err)
		}
	}
	// a path under a file is an error, not an absence
	if _, err := d.IsDir(ctx, "Dockerfile/x"); err == nil {
		t.Error("a path below a file is unreadable, not absent")
	}
	paths, err := d.RecordPaths(ctx)
	if err != nil || !slices.Equal(paths, []string{"fleet/repos/a.json", "fleet/repos/b.json"}) {
		t.Errorf("%v %v", paths, err)
	}
	if b, err := d.Record(ctx, paths[0]); err != nil || !strings.Contains(b, "rob/a") {
		t.Errorf("%q %v", b, err)
	}
	if _, err := (Disk{}).RecordPaths(ctx); err == nil {
		t.Error("no checkout is an error, not no records")
	}
	if got := DiskOf(atoms.Input{Root: root, Files: []string{"x"}, Dies: dies}); got.Root != root || got.Dies != dies || got.Files[0] != "x" {
		t.Errorf("%+v", got)
	}
}

func TestUT_CopyTreeLeavesOutWhatTheGlobsExcludeAndKeepsLinksAndModes(t *testing.T) {
	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "copy")
	put := func(rel, body string, mode os.FileMode) {
		p := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	put("package.json", "{}", 0o644)
	put("bin/run", "#!/bin/sh", 0o755)
	put("release/old", "x", 0o644)
	put("node_modules/a/i.js", "x", 0o644)
	put("pkg/node_modules/b.js", "x", 0o644)
	put("pkg/src.ts", "x", 0o644)
	put("release.md", "not release/", 0o644)
	if err := os.Symlink("bin/run", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	if err := CopyTree(src, dst, TSExcludes); err != nil {
		t.Fatal(err)
	}
	var got []string
	_ = filepath.WalkDir(dst, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			r, _ := filepath.Rel(dst, p)
			got = append(got, r)
		}
		return nil
	})
	slices.Sort(got)
	if want := []string{"bin/run", "link", "package.json", "pkg/src.ts", "release.md"}; !slices.Equal(got, want) {
		t.Errorf("copied %v, want %v", got, want)
	}
	if fi, _ := os.Stat(filepath.Join(dst, "bin", "run")); fi == nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("the mode was not kept: %v", fi)
	}
	if target, err := os.Readlink(filepath.Join(dst, "link")); err != nil || target != "bin/run" {
		t.Errorf("the link was not kept: %q %v", target, err)
	}
	// the python list
	for rel, want := range map[string]bool{".venv": true, ".venv/bin/python": true, "release": true, "a/__pycache__/x.pyc": true, "src/x.py": false, "venv": false} {
		if excluded(rel, PythonExcludes) != want {
			t.Errorf("python excludes %s: want %v", rel, want)
		}
	}
	// a source that is not there, and a destination that cannot be made
	if err := CopyTree(filepath.Join(src, "nowhere"), dst, nil); err == nil {
		t.Error("a missing source copied")
	}
	if err := CopyTree(src, filepath.Join(src, "package.json", "x"), nil); err == nil {
		t.Error("a destination under a file was made")
	}
}

func TestUT_EntriesAndCarry(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "b", "c"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Entries(dir); !slices.Equal(got, []string{"a", "b/"}) {
		t.Errorf("%v", got)
	}
	if Entries(filepath.Join(dir, "nowhere")) != nil {
		t.Error("a directory that is not there lists nothing")
	}
	out := filepath.Join(t.TempDir(), "out", "release")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "stale"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Carry(dir, out); err != nil {
		t.Fatal(err)
	}
	if got := Entries(out); !slices.Equal(got, []string{"a", "b/"}) {
		t.Errorf("the destination is the source and nothing else: %v", got)
	}
	// a destination that cannot be cleared
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Carry(dir, filepath.Join(file, "x", "y")); err == nil {
		t.Error("a destination under a file was filled")
	}
}

func TestUT_TSStepsAreTheFrozenInstallThenTheReleaseScript(t *testing.T) {
	steps := TSSteps()
	if len(steps) != 2 || strings.Join(steps[0], " ") != "bun install --frozen-lockfile" || strings.Join(steps[1], " ") != "bun run release" {
		t.Errorf("%v", steps)
	}
}

func TestUT_AnExcludedFileIsLeftOutAndACopyThatCannotBeMadeIsAnError(t *testing.T) {
	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "copy")
	for rel, body := range map[string]string{"release": "a file, not a directory", "keep": "x"} {
		if err := os.WriteFile(filepath.Join(src, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := CopyTree(src, dst, TSExcludes); err != nil {
		t.Fatal(err)
	}
	if got := Entries(dst); !slices.Equal(got, []string{"keep"}) {
		t.Errorf("copied %v", got)
	}
	// the source file cannot be opened
	if err := copyFile(filepath.Join(src, "nowhere"), filepath.Join(dst, "x")); err == nil {
		t.Error("a missing source copied")
	}
	// the destination is a directory
	if err := copyFile(filepath.Join(src, "keep"), src); err == nil {
		t.Error("a file was copied over a directory")
	}
}

// THE BUN BUILDS RUN AT /src WHEN THE EXECUTOR SAYS THE TREE THEY OWN IS THERE
// (DEF-B6-013): a bun bundle embeds the absolute build directory, and the
// engine's was /src. Unset, the build is the copy in the atom's work directory.
func TestDEF_B6_bun_BuildRootIsTheCallersOrTheCopy(t *testing.T) {
	t.Setenv("ATOM_WORK", "/work/ts__release")
	t.Setenv(SrcEnv, "")
	if dir, inPlace := BuildRoot(); dir != "/work/ts__release/release-src" || inPlace {
		t.Errorf("%s %v", dir, inPlace)
	}
	t.Setenv(SrcEnv, "/src")
	if dir, inPlace := BuildRoot(); dir != "/src" || !inPlace {
		t.Errorf("%s %v", dir, inPlace)
	}
	t.Setenv(SrcEnv, "")
	if got := SrcRoot("/work/ts__pack/src"); got != "/work/ts__pack/src" {
		t.Error(got)
	}
	t.Setenv(SrcEnv, "/src")
	if got := SrcRoot("/work/ts__pack/src"); got != "/src" {
		t.Error(got)
	}
}

// A base with a package.json and a Dockerfile that copies from release/<name>/
// carries its own release root.
func TestBaseRootsAreTheBasesThatCarryTheirOwnRelease(t *testing.T) {
	t.Parallel()
	layer := func(n string) string { return "FROM scratch\nCOPY release/" + n + "/ /\n" }
	tree := mem{files: map[string]string{
		"bases/layer-pi/Dockerfile": layer("layer-pi"), "bases/layer-pi/package.json": "{}", "bases/layer-pi/bun.lock": "",
		"bases/layer-cc/Dockerfile": layer("layer-cc"), "bases/layer-cc/package.json": "{}",
		"bases/layer-bun/Dockerfile": "FROM scratch\nCOPY bases/layer-bun/staged/ /\n", "bases/layer-bun/package.json": "{}",
		"bases/bun/Dockerfile":   "FROM scratch\n",
		"bases/wrong/Dockerfile": "FROM scratch\nCOPY release/other/ /\n", "bases/wrong/package.json": "{}",
		"bases/nodockerfile/package.json": "{}",
		"bases/deep/x/Dockerfile":         layer("x"), "bases/deep/x/package.json": "{}",
		"package.json": "{}", "Dockerfile": "FROM scratch\n",
	}}
	roots, refused, err := BaseRoots(context.Background(), tree)
	if err != nil {
		t.Fatal(err)
	}
	if want := []BaseRoot{{"layer-cc", "bases/layer-cc/Dockerfile"}, {"layer-pi", "bases/layer-pi/Dockerfile"}}; !slices.Equal(roots, want) {
		t.Errorf("roots %v", roots)
	}
	if len(refused) != 1 || !strings.Contains(refused[0], "bases/wrong/Dockerfile copies from release/ but not from release/wrong/") {
		t.Errorf("refused %v", refused)
	}
	if roots[0].Dir() != "bases/layer-cc" {
		t.Errorf("dir %s", roots[0].Dir())
	}
	// no bases, no roots
	if roots, refused, err := BaseRoots(context.Background(), mem{files: map[string]string{"package.json": "{}"}}); err != nil || roots != nil || refused != nil {
		t.Errorf("%v %v %v", roots, refused, err)
	}
}

func TestBaseRootsNameWhatCouldNotBeRead(t *testing.T) {
	t.Parallel()
	if _, _, err := BaseRoots(context.Background(), mem{fail: map[string]error{"Population": errors.New("boom")}}); err == nil || err.Error() != "boom" {
		t.Errorf("population: %v", err)
	}
	tree := mem{files: map[string]string{"bases/a/Dockerfile": "x", "bases/a/package.json": "{}"}, fail: map[string]error{"File:bases/a/Dockerfile": errors.New("gone")}}
	if _, _, err := BaseRoots(context.Background(), tree); err == nil || !strings.Contains(err.Error(), "bases/a/Dockerfile could not be read: gone") {
		t.Errorf("file: %v", err)
	}
}

// A base with a release.toml, a go.mod in its module directory and a Dockerfile
// that copies from release/<name>/ has a Go release root of its own.
func TestBaseGoRootsAreTheBasesThatNameTheirOwnModule(t *testing.T) {
	t.Parallel()
	layer := func(n string) string { return "FROM scratch\nCOPY release/" + n + "/x /x\n" }
	tree := mem{files: map[string]string{
		"bases/chairman/Dockerfile": layer("chairman"), "bases/chairman/release.toml": "module = \"svid\"\n[[binary]]\npackage = \".\"\nout = \"release/chairman-svid\"\n",
		"bases/chairman/svid/go.mod": "module x\n", "bases/chairman/svid/go.sum": "",
		"bases/vend/Dockerfile": layer("vend"), "bases/vend/release.toml": "[[binary]]\npackage = \"./cmd/v\"\nout = \"release/v\"\n",
		"bases/vend/go.mod": "module v\n", "bases/vend/vendor/modules.txt": "# v\n",
		"bases/nomod/Dockerfile": layer("nomod"), "bases/nomod/release.toml": "[[binary]]\npackage = \".\"\nout = \"release/n\"\n",
		"bases/wrong/Dockerfile": "FROM scratch\nCOPY release/other/x /x\n", "bases/wrong/release.toml": "[[binary]]\npackage = \".\"\nout = \"release/w\"\n", "bases/wrong/go.mod": "module w\n",
		"bases/idle/Dockerfile": "FROM scratch\nCOPY bases/idle/staged/ /\n", "bases/idle/release.toml": "[[binary]]\npackage = \".\"\nout = \"release/i\"\n", "bases/idle/go.mod": "module i\n",
		"bases/bad/Dockerfile": layer("bad"), "bases/bad/release.toml": "[[binary", "bases/bad/go.mod": "module b\n",
		"bases/nodockerfile/release.toml": "[[binary]]\npackage = \".\"\nout = \"release/n\"\n", "bases/nodockerfile/go.mod": "module n\n",
		"release.toml": "[[binary]]\npackage = \".\"\nout = \"release/root\"\n", "go.mod": "module root\n",
	}}
	roots, refused, err := BaseGoRoots(context.Background(), tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 || roots[0].Name != "chairman" || roots[0].Module != "bases/chairman/svid" || roots[0].Vendored || roots[0].Binaries[0].Name != "chairman-svid" ||
		roots[1].Name != "vend" || roots[1].Module != "bases/vend" || !roots[1].Vendored || roots[1].Binaries[0].Package != "./cmd/v" {
		t.Errorf("roots %+v", roots)
	}
	if roots[0].Dockerfile != "bases/chairman/Dockerfile" {
		t.Errorf("dockerfile %s", roots[0].Dockerfile)
	}
	want := []string{
		"bases/bad/release.toml: release.toml does not parse",
		"bases/nomod/release.toml: no go.mod at bases/nomod, which the manifest names as its module",
		"bases/wrong/Dockerfile copies from release/ but not from release/wrong/, where its release is staged",
	}
	if len(refused) != 3 {
		t.Fatalf("refused %q", refused)
	}
	for i, w := range want {
		if !strings.Contains(refused[i], w) {
			t.Errorf("refused[%d] = %q, want %q", i, refused[i], w)
		}
	}
	if roots, refused, err := BaseGoRoots(context.Background(), mem{files: map[string]string{"go.mod": "module r\n"}}); err != nil || roots != nil || refused != nil {
		t.Errorf("%v %v %v", roots, refused, err)
	}
}

func TestBaseGoRootsNameWhatCouldNotBeRead(t *testing.T) {
	t.Parallel()
	if _, _, err := BaseGoRoots(context.Background(), mem{fail: map[string]error{"Population": errors.New("boom")}}); err == nil || err.Error() != "boom" {
		t.Errorf("population: %v", err)
	}
	files := map[string]string{"bases/a/Dockerfile": "FROM scratch\nCOPY release/a/x /x\n", "bases/a/release.toml": "[[binary]]\npackage = \".\"\nout = \"release/a\"\n", "bases/a/go.mod": "module a\n"}
	for _, f := range []string{"bases/a/Dockerfile", "bases/a/release.toml"} {
		tree := mem{files: files, fail: map[string]error{"File:" + f: errors.New("gone")}}
		if _, _, err := BaseGoRoots(context.Background(), tree); err == nil || !strings.Contains(err.Error(), f+" could not be read: gone") {
			t.Errorf("%s: %v", f, err)
		}
	}
}
