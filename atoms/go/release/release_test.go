package gorelease

import (
	"context"
	"errors"
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

const copiesHades = "FROM x\nCOPY release/hades /hades\n"

// star is a Go star whose Dockerfile copies the binaries named from release/.
func star(name, dockerfile string, extra map[string]string) map[string]string {
	files := atomtest.Star(name, dockerfile, map[string]string{"go.mod": "module git.notusmi.com/rob/" + name + "\n"})
	for k, v := range extra {
		files[k] = v
	}
	return files
}

// cmds is a main package for each name.
func cmds(names ...string) map[string]string {
	out := map[string]string{}
	for _, n := range names {
		out["cmd/"+n+"/main.go"] = "package main\n"
	}
	return out
}

func merge(maps ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, m := range maps {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

// run lays the tree, points the atom's work directory at a temp one and runs it
// over a recording fake that answers every program with exit 0 unless told.
func run(t *testing.T, files map[string]string, answer func(atoms.Cmd) (string, int)) (checks.Verdict, *atomtest.Fake, string) {
	t.Helper()
	work := t.TempDir()
	t.Setenv("ATOM_WORK", work)
	t.Setenv("ATOM_RELEASE_OUT", "")
	in, f := atomtest.ToolTree(t, files, answer)
	f.Free = []string{"go"}
	return check(context.Background(), in), f, filepath.Join(work, "release")
}

func line(c atoms.Cmd) string { return strings.Join(append([]string{c.Name}, c.Args...), " ") }

func TestTheDeclarationValidatesAndKeepsTheRowItReplaces(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrepush || Def.Lane != checks.LaneGo || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "go" || Def.NotOnForks != "" || Def.NeedsManifest || Def.Surface != "any:**/go.mod" ||
		!slices.Equal(Def.Needs, []string{"dies"}) || Def.Timeout != 45*time.Minute || Def.WritesTree ||
		!strings.HasPrefix(Def.Desc, "The release binaries the image will carry compile, with the image's own flags") {
		t.Errorf("%+v", Def)
	}
}

// THE RELEASE BUILD IS THE IMAGE'S COMPILE, derived: the binaries the
// Dockerfile copies out of release/, each from ./cmd/<name>, with the fleet's
// flags, into the atom's release directory.
func TestGoReleaseBuildsWhatTheImageWillCarry(t *testing.T) {
	v, f, out := run(t, merge(star("hades", copiesHades, nil), cmds("hades")), nil)
	atomtest.Expect(t, v, checks.StatePass, atomtest.Pass, "release build: hades", "the Dockerfile's COPY lines", "the module resolves its dependencies")
	if got := f.Ran(); !slices.Equal(got, []string{"go", "go"}) {
		t.Fatalf("ran %v", got)
	}
	if line(f.Calls[0]) != "go mod download" {
		t.Errorf("an unvendored release build skipped go mod download: %v", f.Calls)
	}
	build := f.Last()
	if want := "go build -trimpath -ldflags=-s -w -o " + filepath.Join(out, "hades") + " ./cmd/hades"; line(build) != want {
		t.Errorf("compile %q, want %q", line(build), want)
	}
	if !slices.Contains(build.Env, "CGO_ENABLED=0") || !slices.Contains(build.Env, "GOFLAGS=-buildvcs=false") {
		t.Errorf("the compile's environment is %v", build.Env)
	}
	if !build.Both {
		t.Error("the verdict reads stdout then stderr, as the chain's did")
	}
}

// The output directory is the executor's to name.
func TestTheOutputDirectoryIsTheCallersToName(t *testing.T) {
	into := filepath.Join(t.TempDir(), "assembled")
	t.Setenv("ATOM_RELEASE_OUT", into)
	t.Setenv("ATOM_WORK", t.TempDir())
	t.Setenv("ATOM_SHA", "b3db6f1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	in, f := atomtest.ToolTree(t, merge(star("hades", copiesHades, nil), cmds("hades")), committedAt)
	f.Free = []string{"go"}
	atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Pass)
	if !strings.Contains(line(f.Last()), "-o "+filepath.Join(into, "hades")+" ") {
		t.Errorf("compiled to %q", line(f.Last()))
	}
	if fi, err := os.Stat(into); err != nil || !fi.IsDir() {
		t.Errorf("the release directory was not made: %v", err)
	}
}

// VENDOR IS READ OFF THE TREE, not declared: a tracked vendor/ is the flag, and
// a vendored release build reaches no proxy and no door — ourea's image must
// build with the door down.
func TestAVendoredModuleIsBuiltWithModVendorAndDownloadsNothing(t *testing.T) {
	files := merge(star("ourea", "FROM x\nCOPY release/ourea /ourea\n", map[string]string{"vendor/modules.txt": "# x\n"}), cmds("ourea"))
	v, f, out := run(t, files, nil)
	atomtest.Expect(t, v, checks.StatePass, atomtest.Pass, "-mod=vendor")
	if len(f.Calls) != 1 {
		t.Fatalf("a vendored release build ran more than the compile: %v", f.Calls)
	}
	if want := "go build -mod=vendor -trimpath -ldflags=-s -w -o " + filepath.Join(out, "ourea") + " ./cmd/ourea"; line(f.Last()) != want {
		t.Errorf("compile %q, want %q", line(f.Last()), want)
	}
}

// The Dockerfile copies more than one, and each gets its own exec, in name order.
func TestEveryBinaryTheDockerfileCopiesGetsItsOwnCompile(t *testing.T) {
	files := merge(star("blade-runner", "FROM x\nCOPY release/blade-runner /blade-runner\nCOPY release/blade-controller /blade-controller\n", nil), cmds("blade-runner", "blade-controller"))
	v, f, _ := run(t, files, nil)
	atomtest.Expect(t, v, checks.StatePass, atomtest.Pass, "blade-controller, blade-runner")
	var built []string
	for _, c := range f.Calls[1:] {
		built = append(built, c.Args[len(c.Args)-1])
	}
	if !slices.Equal(built, []string{"./cmd/blade-controller", "./cmd/blade-runner"}) {
		t.Errorf("built %v", built)
	}
}

// A BINARY THAT DID NOT COMPILE IS THE TREE'S FINDING, named by the compiler's
// own words, and the binaries after it are not built on top of it. (The chain
// read only the last binary's exit, so an earlier failure under a later pass
// was a green gate.)
func TestABinaryThatDoesNotCompileIsAFindingAndStopsTheChain(t *testing.T) {
	files := merge(star("blade-runner", "FROM x\nCOPY release/blade-runner /blade-runner\nCOPY release/blade-controller /blade-controller\n", nil), cmds("blade-runner", "blade-controller"))
	v, f, _ := run(t, files, func(c atoms.Cmd) (string, int) {
		if strings.Contains(line(c), "./cmd/blade-controller") {
			return "cmd/blade-controller/main.go:3:1: undefined: x", 1
		}
		return "", 0
	})
	atomtest.Expect(t, v, checks.StateFindings, atomtest.Findings, "release build: blade-controller, blade-runner", "undefined: x")
	if len(f.Calls) != 2 {
		t.Errorf("the chain went on past the failure: %v", f.Calls)
	}
}

// A proxy's bad hour is the substrate's, not a finding: `go mod download`
// failing is a could-not-run that names the step.
func TestAFailedDownloadIsCouldNotRunNotAFinding(t *testing.T) {
	v, f, _ := run(t, merge(star("hades", copiesHades, nil), cmds("hades")), func(c atoms.Cmd) (string, int) {
		if line(c) == "go mod download" {
			return "proxy.golang.org: 502 Bad Gateway", 1
		}
		return "", 0
	})
	atomtest.Expect(t, v, checks.StateCannotRun, atomtest.Cannot, "go:release: CANNOT RUN - preflight `go mod download` exited 1", "502 Bad Gateway")
	if len(f.Calls) != 1 {
		t.Errorf("the compile ran on top of a failed download: %v", f.Calls)
	}
}

// A tool that never started, or that the engine's signal range swallowed, never ran.
func TestACompileThatNeverRanIsCouldNotRun(t *testing.T) {
	for name, code := range map[string]int{"would not start": -1, "was killed": 137} {
		t.Run(name, func(t *testing.T) {
			v, _, _ := run(t, merge(star("hades", copiesHades, nil), cmds("hades")), func(c atoms.Cmd) (string, int) {
				if c.Args[0] == "build" {
					return "go: not found", code
				}
				return "", 0
			})
			atomtest.Expect(t, v, checks.StateCannotRun, atomtest.Cannot, "the atom never ran")
		})
	}
}

// A REPOSITORY WITH DOCKERFILES AND NO STAR IS NOT A STAR IMAGE — ABSENT, never
// a could-not-run. foundry-stocks measured it (Wonka17, 2026-09-17, nine gate
// runs on #205): it ships the CI bases and forge-tools, is not copier-templated,
// and the first cut wedged every landing there over "names no star".
func TestGoReleaseIsAbsentForARepoThatNamesNoStar(t *testing.T) {
	v, f, _ := run(t, map[string]string{"go.mod": "module x\n", "Dockerfile": "FROM x\n"}, nil)
	atomtest.Expect(t, v, checks.StatePass, atomtest.Absent, "names no star", "not a star image")
	if strings.Contains(v.Reason, "compiles itself") {
		t.Errorf("a non-star was told it compiles itself: %s", v.Reason)
	}
	if len(f.Calls) != 0 {
		t.Errorf("nothing compiles for a repo that is not a star: %v", f.Calls)
	}
	// An answers file that names nothing is the same absence.
	v, _, _ = run(t, map[string]string{"go.mod": "module x\n", "Dockerfile": "FROM x\n", ".copier-answers.yml": "project: x\n"}, nil)
	atomtest.Expect(t, v, checks.StatePass, atomtest.Absent, "no service_name")
}

// A DOCKERFILE THAT COMPILES ITSELF ASKS FOR NO RELEASE BUILD — ABSENT, the
// build lane's own reading. narcissus is the case (foundry-tools #10307): cgo,
// compiled statically in its own build stage, so the CGO_ENABLED=0 compile here
// would red a binary its image never carries.
func TestGoReleaseIsAbsentForADockerfileThatCompilesItself(t *testing.T) {
	self := "FROM docker.notusmi.com/library/golang:1.27 AS build\nRUN CGO_ENABLED=1 go build -o /out/hades ./cmd/hades\nFROM x\nCOPY --from=build /out/hades /hades\n"
	v, f, _ := run(t, merge(star("hades", self, nil), cmds("hades")), nil)
	atomtest.Expect(t, v, checks.StatePass, atomtest.Absent, "compiles itself", "release/")
	if len(f.Calls) != 0 {
		t.Errorf("nothing compiles for a Dockerfile that compiles itself: %v", f.Calls)
	}
	// A second Dockerfile that does copy from release/ asks on the repo's behalf.
	v, f, _ = run(t, merge(star("hades", self, map[string]string{"docker/Dockerfile.hades": copiesHades}), cmds("hades")), nil)
	atomtest.Expect(t, v, checks.StatePass, atomtest.Pass, "release build: hades")
	if len(f.Calls) == 0 {
		t.Error("a Dockerfile that copies from release/ asked and nothing compiled")
	}
}

// A Dockerfile that cannot be read is a could-not-run that names it, never an
// absence read off a fault.
func TestADockerfileThatCannotBeReadIsCouldNotRun(t *testing.T) {
	work := t.TempDir()
	t.Setenv("ATOM_WORK", work)
	in, f := atomtest.ToolTree(t, merge(star("hades", copiesHades, nil), cmds("hades")), nil)
	if err := os.Remove(filepath.Join(in.Root, "Dockerfile")); err != nil {
		t.Fatal(err)
	}
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "Dockerfile could not be read")
	if len(f.Calls) != 0 {
		t.Errorf("the compile ran on a Dockerfile it could not read: %v", f.Calls)
	}
}

// A REPO THAT SHIPS NO IMAGE IS ABSENT, NEVER A COULD-NOT-RUN. foundry-tools is
// the case that measured it: no Dockerfile, no image, no release build.
func TestGoReleaseIsAbsentWhereThereIsNoImage(t *testing.T) {
	v, f, _ := run(t, map[string]string{"go.mod": "module x\n", "main.go": "package main\n"}, nil)
	atomtest.Expect(t, v, checks.StatePass, atomtest.Absent, "tracks no Dockerfile")
	if len(f.Calls) != 0 {
		t.Errorf("nothing compiles for a repo that ships no image: %v", f.Calls)
	}
}

// EVERY WAY THE RELEASE BUILD CANNOT ANSWER IS A COULD-NOT-RUN THAT SAYS WHY,
// and none of them is a pass.
func TestGoReleaseSaysWhyItCouldNotRun(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		why   []string
	}{
		{"a module somewhere but not at the root", map[string]string{
			"Dockerfile": copiesHades, ".copier-answers.yml": "service_name: hades\n", "tools/go.mod": "module x\n", "cmd/hades/main.go": "package main\n",
		}, []string{"no go.mod at the repository root"}},
		{"a COPY that is not a binary name", map[string]string{
			"go.mod": "module x\n", "Dockerfile": "FROM x\nCOPY release/-flag /hades\n", ".copier-answers.yml": "service_name: hades\n",
		}, []string{"is not a binary name"}},
		{"a COPY with no main package behind it", merge(star("hades", "FROM x\nCOPY release/hades /hades\nCOPY release/ghost /ghost\n", nil), cmds("hades")),
			[]string{"copies release/ghost", "no cmd/ghost"}},
		{"a Dockerfile that asks and copies nothing out of it", star("hades", "FROM x\nCOPY release/ /app\n", nil),
			[]string{"copies no file out of it"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, f, _ := run(t, c.files, nil)
			atomtest.Expect(t, v, checks.StateCannotRun, atomtest.Cannot, append([]string{"go:release: CANNOT RUN"}, c.why...)...)
			if len(f.Calls) != 0 {
				t.Errorf("a build that could not be planned ran: %v", f.Calls)
			}
		})
	}
}

// The tree itself could not be read: the population is the first thing it asks for.
func TestATreeThatCannotBeEnumeratedIsCouldNotRun(t *testing.T) {
	t.Setenv("ATOM_WORK", t.TempDir())
	in, _ := atomtest.ToolTree(t, merge(star("hades", copiesHades, nil), cmds("hades")), nil)
	in.FilesErr = errors.New("the tree went away")
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "the tree could not be read", "the tree went away")
}

// A REPOSITORY WITH NO ANSWERS FILE IS NAMED BY ITS RECORD (CA F17, Rob
// 2026-09-19). hephaestus is not copier-templated and carries no
// .copier-answers.yml, but foundry-dies holds fleet/repos/hephaestus.json
// saying repo rob/hephaestus — the record the door routes its lanes by.
func TestGoReleaseNamesAStarWithNoAnswersFileByItsRecord(t *testing.T) {
	records := map[string]string{
		"hephaestus":  `{"name":"hephaestus","repo":"rob/hephaestus"}`,
		"tron":        `{"name":"tron","repo":"rob/tron"}`,
		"base-images": `{"name":"base-images","repo":"foundry/base-images"}`,
	}
	bare := func(dockerfile string, names ...string) map[string]string {
		return merge(map[string]string{"go.mod": "module x\n", "Dockerfile": dockerfile}, cmds(names...))
	}
	cases := []struct {
		name, origin string
		files        map[string]string
		state        checks.State
		result       string
		why          string
	}{
		{"the door's clone", "http://ourea.default.svc.cluster.local:8215/hephaestus.git", bare("FROM x\nCOPY release/hephaestus /hephaestus\n", "hephaestus"), checks.StatePass, atomtest.Pass, "release build: hephaestus"},
		{"an owner-qualified clone", "https://git.notusmi.com/foundry/base-images.git", bare("FROM x\nCOPY release/base-images /base-images\n", "base-images"), checks.StatePass, atomtest.Pass, "release build: base-images"},
		{"a clone no record names", "http://ourea.default.svc.cluster.local:8215/nobody.git", bare("FROM x\n"), checks.StatePass, atomtest.Absent, "no record in foundry-dies names repo rob/nobody"},
		{"no clone at all", "", bare("FROM x\n"), checks.StatePass, atomtest.Absent, "no clone URL to find a record by"},
		{"an answers file with no service_name", "http://ourea:8215/hephaestus.git", merge(bare("FROM x\nCOPY release/hephaestus /hephaestus\n", "hephaestus"), map[string]string{".copier-answers.yml": "repo_name: hephaestus-mcp\n"}), checks.StatePass, atomtest.Pass, "release build: hephaestus"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("ATOM_WORK", t.TempDir())
			in, f := atomtest.ToolTree(t, c.files, nil)
			f.Free = []string{"go"}
			in = atomtest.WithDies(t, in, c.origin, records)
			atomtest.Expect(t, check(context.Background(), in), c.state, c.result, c.why)
		})
	}
}

// The records could not be listed or read: could-not-run, never "no star".
func TestRecordsThatCannotBeReadAreCouldNotRunNotNoStar(t *testing.T) {
	files := merge(map[string]string{"go.mod": "module x\n", "Dockerfile": "FROM x\nCOPY release/hephaestus /hephaestus\n"}, cmds("hephaestus"))
	t.Setenv("ATOM_WORK", t.TempDir())
	// no checkout at all
	in, _ := atomtest.ToolTree(t, files, nil)
	in.Origin = "http://ourea:8215/hephaestus.git"
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "could not be listed")
	// a record that is a directory reads as an error
	in = atomtest.WithDies(t, in, in.Origin, map[string]string{"a": "{}"})
	if err := os.Mkdir(filepath.Join(in.Dies, "fleet", "repos", "b.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "record fleet/repos/b.json could not be read")
}

// A release directory that cannot be made is the run's could-not-run, and the
// compile does not start.
func TestAReleaseDirectoryThatCannotBeMadeIsCouldNotRun(t *testing.T) {
	file := filepath.Join(t.TempDir(), "out")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATOM_WORK", t.TempDir())
	t.Setenv("ATOM_RELEASE_OUT", filepath.Join(file, "release"))
	in, f := atomtest.ToolTree(t, merge(star("hades", copiesHades, nil), cmds("hades")), nil)
	f.Free = []string{"go"}
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "the release directory could not be made")
	if len(f.Calls) != 0 {
		t.Errorf("the compile ran with nowhere to put its binary: %v", f.Calls)
	}
}

const narcToml = `[[binary]]
package = "./cmd/narc"
out = "release/narc"
cgo = true
tags = ["netgo", "osusergo"]
ldflags = '-s -w -extldflags "-static"'
`

// A TREE WITH A release.toml IS BUILT AS IT DECLARES: cgo on, its tags, its
// ldflags, and the declared binary rather than a guess from the COPY line.
func TestAReleaseTomlDeclaresTheCompile(t *testing.T) {
	files := merge(star("narcissus", "FROM x\nCOPY release/narc /narc\n", map[string]string{"release.toml": narcToml}), cmds("narc"))
	v, f, out := run(t, files, nil)
	atomtest.Expect(t, v, checks.StatePass, atomtest.Pass, "release build: narc", "release.toml")
	build := f.Last()
	want := `go build -trimpath -ldflags=-s -w -extldflags "-static" -tags=netgo,osusergo -o ` + filepath.Join(out, "narc") + " ./cmd/narc"
	if line(build) != want {
		t.Errorf("compile %q, want %q", line(build), want)
	}
	if !slices.Contains(build.Env, "CGO_ENABLED=1") || slices.Contains(build.Env, "CGO_ENABLED=0") || !slices.Contains(build.Env, "GOFLAGS=-buildvcs=false") {
		t.Errorf("the compile's environment is %v", build.Env)
	}
}

// A manifest that cannot be read is the repository's fault, said before any compile.
func TestABrokenReleaseTomlIsCouldNotRun(t *testing.T) {
	files := merge(star("narcissus", "FROM x\nCOPY release/narc /narc\n", map[string]string{"release.toml": "[[binary"}), cmds("narc"))
	v, f, _ := run(t, files, nil)
	atomtest.Expect(t, v, checks.StateCannotRun, atomtest.Cannot, "release.toml")
	if len(f.Calls) != 0 {
		t.Errorf("ran %v", f.Calls)
	}
}

// THE BUILD LANE'S BINARIES CARRY THE STAMP (foundry-tools#18676): the landed
// commit's committer time and sha7, linked into every convention the fleet keeps
// a version var in. The executor names a release directory only in the lanes that
// ship the binaries, so that is where the compile is stamped; the gate's is not
// (TestGoReleaseBuildsWhatTheImageWillCarry pins its argv without a -X).
func TestADeliveryLanesCompileIsStamped(t *testing.T) {
	into := filepath.Join(t.TempDir(), "assembled")
	t.Setenv("ATOM_RELEASE_OUT", into)
	t.Setenv("ATOM_WORK", t.TempDir())
	t.Setenv("ATOM_SHA", "b3db6f1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	in, f := atomtest.ToolTree(t, merge(star("hades", copiesHades, nil), cmds("hades")), func(c atoms.Cmd) (string, int) {
		if c.Name == "git" {
			return "1792086104\n", 0
		}
		return "", 0
	})
	f.Free = []string{"go"}
	atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Pass)
	mod := "git.notusmi.com/rob/hades"
	want := "go build -trimpath -ldflags=-s -w" +
		" -X main.Version=1792086104-b3db6f1 -X main.version=1792086104-b3db6f1" +
		" -X " + mod + "/internal/config.Version=1792086104-b3db6f1" +
		" -X " + mod + "/internal/verbs.Version=1792086104-b3db6f1" +
		" -X " + mod + "/verbs.Version=1792086104-b3db6f1" +
		" -o " + filepath.Join(into, "hades") + " ./cmd/hades"
	if got := line(f.Last()); got != want {
		t.Errorf("compile %q, want %q", got, want)
	}
}

// A delivery lane that cannot say what it stamps does not ship an unstamped
// binary: it could not run.
func TestADeliveryLaneThatCannotStampCouldNotRun(t *testing.T) {
	t.Setenv("ATOM_RELEASE_OUT", filepath.Join(t.TempDir(), "assembled"))
	t.Setenv("ATOM_WORK", t.TempDir())
	t.Setenv("ATOM_SHA", "")
	in, f := atomtest.ToolTree(t, merge(star("hades", copiesHades, nil), cmds("hades")), nil)
	f.Free = []string{"go"}
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "ATOM_SHA")
	for _, c := range f.Calls {
		if c.Name == "go" && len(c.Args) > 0 && c.Args[0] == "build" {
			t.Errorf("compiled anyway: %s", line(c))
		}
	}
}

// committedAt answers git's committer-time question for a delivery lane's stamp.
func committedAt(c atoms.Cmd) (string, int) {
	if c.Name == "git" {
		return "1792086104\n", 0
	}
	return "", 0
}
