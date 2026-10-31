package basesgorelease

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

// bases is a repository of bases: chairman keeps its Go module in svid/, vend
// vendors and keeps its module in its own directory, and plain has no release.
func bases() map[string]string {
	return map[string]string{
		"bases/chairman/Dockerfile":     "FROM scratch\nCOPY release/chairman/chairman-svid /usr/local/bin/chairman-svid\n",
		"bases/chairman/release.toml":   "module = \"svid\"\n[[binary]]\npackage = \".\"\nout = \"release/chairman-svid\"\n",
		"bases/chairman/svid/go.mod":    "module chairman-svid\n",
		"bases/chairman/svid/main.go":   "package main\n",
		"bases/vend/Dockerfile":         "FROM scratch\nCOPY release/vend/ /\n",
		"bases/vend/release.toml":       "[[binary]]\npackage = \"./cmd/v\"\nout = \"release/v\"\ncgo = true\ntags = [\"netgo\"]\nldflags = \"-s\"\n",
		"bases/vend/go.mod":             "module v\n",
		"bases/vend/vendor/modules.txt": "# v\n",
		"bases/plain/Dockerfile":        "FROM scratch\nCOPY bases/plain/staged/ /\n",
		"go.mod":                        "module repo\n",
	}
}

func line(c atoms.Cmd) string { return strings.Join(append([]string{c.Name}, c.Args...), " ") }

func run(t *testing.T, files map[string]string, answer func(atoms.Cmd) (string, int)) (checks.Verdict, *atomtest.Fake, string, string) {
	t.Helper()
	work := t.TempDir()
	t.Setenv("ATOM_WORK", work)
	t.Setenv("ATOM_RELEASE_OUT", "")
	in, f := atomtest.ToolTree(t, files, answer)
	f.Free = []string{"go"}
	return check(context.Background(), in), f, filepath.Join(work, "release"), in.Root
}

func TestTheDeclarationValidates(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StageBases || Def.Toolchain != "go" || Def.Surface != "any:bases/*/release.toml" ||
		Def.Group != checks.GroupOf(id) || Def.Timeout != 45*time.Minute || Def.WritesTree || !slices.Equal(Def.Needs, nil) {
		t.Errorf("%+v", Def)
	}
}

// EACH BASE WITH A GO RELEASE is compiled in its module directory with the
// fleet's flags into release/<base>/, an unvendored module after its download.
func TestEachBaseIsCompiledInItsModuleWithTheFleetsFlags(t *testing.T) {
	v, f, out, root := run(t, bases(), nil)
	atomtest.Expect(t, v, checks.StatePass, atomtest.Pass, "2 base(s) compiled", "release/chairman/chairman-svid", "release/vend/v")
	var got []string
	for _, c := range f.Calls {
		got = append(got, filepath.Base(c.Dir)+": "+line(c))
	}
	want := []string{
		"svid: go mod download",
		"svid: go build -trimpath -ldflags=-s -w -o " + filepath.Join(out, "chairman", "chairman-svid") + " .",
		"vend: go build -mod=vendor -trimpath -ldflags=-s -tags=netgo -o " + filepath.Join(out, "vend", "v") + " ./cmd/v",
	}
	if len(got) != len(want) {
		t.Fatalf("ran %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("call %d = %q, want %q", i, got[i], want[i])
		}
	}
	if f.Calls[0].Dir != filepath.Join(root, "bases", "chairman", "svid") {
		t.Errorf("ran in %s", f.Calls[0].Dir)
	}
	build := f.Calls[1]
	if !slices.Contains(build.Env, "CGO_ENABLED=0") || !slices.Contains(build.Env, "GOFLAGS=-buildvcs=false") || !build.Both {
		t.Errorf("env %v both %v", build.Env, build.Both)
	}
	if !slices.Contains(f.Calls[2].Env, "CGO_ENABLED=1") {
		t.Errorf("a cgo binary compiled with %v", f.Calls[2].Env)
	}
	for _, dir := range []string{"chairman", "vend"} {
		if fi, err := os.Stat(filepath.Join(out, dir)); err != nil || !fi.IsDir() {
			t.Errorf("release/%s was not made: %v", dir, err)
		}
	}
}

func TestTheOutputDirectoryIsTheExecutorsToName(t *testing.T) {
	into := filepath.Join(t.TempDir(), "assembled")
	t.Setenv("ATOM_RELEASE_OUT", into)
	t.Setenv("ATOM_WORK", t.TempDir())
	t.Setenv("ATOM_SHA", "b3db6f1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	in, f := atomtest.ToolTree(t, bases(), func(c atoms.Cmd) (string, int) {
		if c.Name == "git" {
			return "1792086104\n", 0
		}
		return "", 0
	})
	f.Free = []string{"go"}
	atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Pass)
	var compiled []string
	for _, c := range f.Calls {
		if c.Name == "go" && c.Args[0] == "build" {
			compiled = append(compiled, line(c))
		}
	}
	if len(compiled) == 0 || !strings.Contains(compiled[0], "-o "+filepath.Join(into, "chairman", "chairman-svid")+" ") {
		t.Errorf("compiled to %q", compiled)
	}
}

// THE FIRST FAILURE STOPS THE CHAIN and names the base and the binary.
func TestAFailedCompileNamesTheBaseAndTheBinary(t *testing.T) {
	answer := func(c atoms.Cmd) (string, int) {
		if c.Args[0] == "build" {
			return "main.go:3: undefined: x", 1
		}
		return "", 0
	}
	v, f, out, _ := run(t, bases(), answer)
	atomtest.Expect(t, v, checks.StateFindings, atomtest.Findings, "chairman: chairman-svid: ", "undefined: x")
	if len(f.Calls) != 2 {
		t.Errorf("the chain went on: %v", f.Ran())
	}
	_ = out
}

func TestAFailedDownloadIsTheSubstratesAndNamesTheBase(t *testing.T) {
	answer := func(c atoms.Cmd) (string, int) {
		if c.Args[0] == "mod" {
			return "dial tcp: lookup proxy", 1
		}
		return "", 0
	}
	v, f, _, _ := run(t, bases(), answer)
	atomtest.Expect(t, v, checks.StateCannotRun, atomtest.Cannot, "chairman: ", "go mod download")
	if len(f.Calls) != 1 {
		t.Errorf("built on a failed download: %v", f.Ran())
	}
}

func TestAProgramThatNeverStartedIsCouldNotRun(t *testing.T) {
	answer := func(c atoms.Cmd) (string, int) {
		if c.Args[0] == "build" {
			return "go: not found", -1
		}
		return "", 0
	}
	v, _, _, _ := run(t, bases(), answer)
	atomtest.Expect(t, v, checks.StateCannotRun, atomtest.Cannot, "go: not found")
}

func TestARefusalIsAFindingAndBuildsNothing(t *testing.T) {
	files := bases()
	files["bases/chairman/release.toml"] = "[[binary"
	v, f, _, _ := run(t, files, nil)
	atomtest.Expect(t, v, checks.StateFindings, atomtest.Findings, "bases/chairman/release.toml: release.toml does not parse")
	if len(f.Calls) != 0 {
		t.Errorf("a refusal built something: %v", f.Ran())
	}
}

func TestAbsentWhereNoBaseHasAGoRelease(t *testing.T) {
	v, f, _, _ := run(t, map[string]string{"bases/plain/Dockerfile": "FROM scratch\n", "go.mod": "module r\n"}, nil)
	atomtest.Expect(t, v, checks.StatePass, atomtest.Absent, "no base carries a release.toml")
	if len(f.Calls) != 0 {
		t.Error("an absence built something")
	}
}

func TestWhatCannotBeReadOrMadeCannotRun(t *testing.T) {
	t.Setenv("ATOM_WORK", t.TempDir())
	t.Setenv("ATOM_RELEASE_OUT", "")
	in, _ := atomtest.ToolTree(t, bases(), nil)
	in.FilesErr = os.ErrPermission
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "the tree could not be read")

	// a release directory that is a file
	work := t.TempDir()
	file := filepath.Join(work, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATOM_RELEASE_OUT", file)
	in, f := atomtest.ToolTree(t, bases(), nil)
	f.Free = []string{"go"}
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "the release directory could not be made")
}

// A DELIVERY LANE'S BASE BINARIES CARRY THE STAMP (foundry-tools#18676): each is
// stamped with its own module's path and the landed commit's stamp.
func TestADeliveryLanesBasesAreStamped(t *testing.T) {
	into := filepath.Join(t.TempDir(), "assembled")
	t.Setenv("ATOM_RELEASE_OUT", into)
	t.Setenv("ATOM_WORK", t.TempDir())
	t.Setenv("ATOM_SHA", "b3db6f1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	in, f := atomtest.ToolTree(t, bases(), func(c atoms.Cmd) (string, int) {
		if c.Name == "git" {
			return "1792086104\n", 0
		}
		return "", 0
	})
	f.Free = []string{"go"}
	atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Pass)
	var builds []string
	for _, c := range f.Calls {
		if c.Name == "go" && c.Args[0] == "build" {
			builds = append(builds, line(c))
		}
	}
	if len(builds) != 2 || !strings.Contains(builds[0], " -X chairman-svid/internal/config.Version=1792086104-b3db6f1 ") ||
		!strings.Contains(builds[1], " -X v/verbs.Version=1792086104-b3db6f1 ") || !strings.Contains(builds[1], "-tags=netgo") {
		t.Errorf("builds: %q", builds)
	}
}
