package regenkit

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

func TestTailIsTheLastSixLinesOnOneLine(t *testing.T) {
	lines := strings.Fields("l1 l2 l3 l4 l5 l6 l7 l8")
	if got := Tail(strings.Join(lines, "\n") + "\n"); got != "l3 | l4 | l5 | l6 | l7 | l8" {
		t.Errorf("%q", got)
	}
	if got := Tail("one"); got != "one" {
		t.Errorf("%q", got)
	}
}

func TestStepNamesTheProgramItsExitAndWhatItSaid(t *testing.T) {
	var got atoms.Cmd
	ok := func(c atoms.Cmd) (string, int) { got = c; return "", 0 }
	if err := Step(ok, []string{"A=1"}, "tar", "-xf", "x"); err != nil || got.Name != "tar" || got.Dir != os.TempDir() || got.Env[0] != "A=1" || len(got.Args) != 2 {
		t.Errorf("%+v %v", got, err)
	}
	bad := func(atoms.Cmd) (string, int) { return "boom\n", 3 }
	err := Step(bad, nil, "tar", "-xf", "x")
	if err == nil || err.Error() != "tar -xf x exited 3: boom" {
		t.Errorf("%v", err)
	}
}

func TestFetchAsksTheAssetsOwnURLAndHoldsThePinnedSum(t *testing.T) {
	var sources []string
	var sum string
	fake := func(_ context.Context, c *http.Client, s []string, want, dest string) error {
		sources, sum = s, want
		if c != nil || dest != "/x/dest" {
			t.Errorf("%v %q", c, dest)
		}
		return nil
	}
	if err := Fetch(t.Context(), fake, checks.GofmtURL, "/x/dest"); err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0] != checks.GofmtURL || sum != checks.ToolSHA256[checks.GofmtURL] {
		t.Errorf("%v %s", sources, sum)
	}
	if err := Fetch(t.Context(), fake, "https://example.com/unpinned.tar.gz", "/x/dest"); err == nil {
		t.Error("an unpinned URL was fetched")
	}
	fail := func(context.Context, *http.Client, []string, string, string) error { return errors.New("down") }
	if err := Fetch(t.Context(), fail, checks.GofmtURL, "/x/dest"); err == nil || err.Error() != "down" {
		t.Errorf("%v", err)
	}
}

func TestWrapNamesWhatTheReadWasFor(t *testing.T) {
	if Wrap("x", nil) != nil || Wrap("the build's x", errors.New("gone")).Error() != "the build's x: gone" {
		t.Error("wrap")
	}
}

func TestPathWithAndCargoBinReadTheEnvironment(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("CARGO_HOME", "")
	if CargoBin() != "" {
		t.Errorf("%q", CargoBin())
	}
	t.Setenv("CARGO_HOME", "/c")
	if CargoBin() != "/c/bin" {
		t.Errorf("%q", CargoBin())
	}
	if got := PathWith("", "/a", "", "/b"); got != "/a:/b:/usr/bin" {
		t.Errorf("%q", got)
	}
}

func TestUnpackCoreFetchesOneCommitAndUnpacksItsArchive(t *testing.T) {
	base := t.TempDir()
	var lines []string
	var dirs []string
	run := func(dir string, env, argv []string) (string, error) {
		lines = append(lines, strings.Join(argv, " "))
		dirs = append(dirs, dir)
		if len(argv) > 1 && argv[1] == "fetch" && (len(env) != 1 || env[0] != "GIT_TERMINAL_PROMPT=0") {
			t.Errorf("fetch env %v", env)
		}
		return "", nil
	}
	core, err := UnpackCore(run, base, "c0ffee")
	if err != nil || core != filepath.Join(base, "core") {
		t.Fatalf("%q %v", core, err)
	}
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "git init") || !strings.Contains(lines[1], checks.RustKit.GitURL()+" c0ffee") ||
		!strings.HasPrefix(lines[2], "git archive") || !strings.HasPrefix(lines[3], "tar -xf ") || dirs[1] != filepath.Join(base, "stellar-core-rust") {
		t.Errorf("%q", lines)
	}
	if st, err := os.Stat(core); err != nil || !st.IsDir() {
		t.Error("the core directory was not made")
	}

	failing := func(string, []string, []string) (string, error) { return "", errors.New("no such commit") }
	if _, err := UnpackCore(failing, base, "c0ffee"); err == nil || err.Error() != "core @c0ffee: no such commit" {
		t.Errorf("%v", err)
	}
	blocked := filepath.Join(base, "file")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := UnpackCore(run, blocked, "c0ffee"); err == nil {
		t.Error("a base that is a file was unpacked into")
	}
}

// A kit in a subdirectory is archived from that subdirectory, so the core
// unpacks with its crate at the root, as it does today.
func TestUnpackCoreArchivesTheKitsSubdirectory(t *testing.T) {
	old := checks.RustKit
	checks.RustKit = checks.KitSource{Repo: "rob/stellar-core", Subdir: "rust"}
	t.Cleanup(func() { checks.RustKit = old })
	var lines []string
	run := func(_ string, _, argv []string) (string, error) {
		lines = append(lines, strings.Join(argv, " "))
		return "", nil
	}
	if _, err := UnpackCore(run, t.TempDir(), "c0ffee"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lines[1], "https://git.notusmi.com/stellar-core.git c0ffee") || !strings.HasSuffix(lines[2], " c0ffee:rust") {
		t.Errorf("%q", lines)
	}
}
