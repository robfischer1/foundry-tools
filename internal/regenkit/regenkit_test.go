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

// fakeUnpack answers every program, and the tar it is asked to unpack leaves the
// kit's directory (as an archive of a commit holding it does) unless bare.
func fakeUnpack(t *testing.T, lines, dirs *[]string, bare bool) Runner {
	return func(dir string, env, argv []string) (string, error) {
		*lines = append(*lines, strings.Join(argv, " "))
		*dirs = append(*dirs, dir)
		if len(argv) > 1 && argv[1] == "fetch" && (len(env) != 1 || env[0] != "GIT_TERMINAL_PROMPT=0") {
			t.Errorf("fetch env %v", env)
		}
		if argv[0] == "tar" && !bare {
			if err := os.MkdirAll(filepath.Join(argv[4], checks.RustKit.Subdir), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		return "", nil
	}
}

// THE WHOLE COMMIT IS ARCHIVED and the answer is the kit's directory in it:
// stellar-core's workspace manifest and Cargo.lock are at the root, above rust/.
func TestUnpackCoreArchivesTheWholeCommitAndAnswersTheKitsDirectory(t *testing.T) {
	base := t.TempDir()
	var lines, dirs []string
	core, err := UnpackCore(fakeUnpack(t, &lines, &dirs, false), base, "c0ffee")
	if err != nil || core != filepath.Join(base, "core", "rust") {
		t.Fatalf("%q %v", core, err)
	}
	want := []string{
		"git init --quiet " + filepath.Join(base, "kit"),
		"git fetch --quiet --depth 1 https://git.notusmi.com/stellar-core.git c0ffee",
		"git archive --format=tar --output=" + filepath.Join(base, "core.tar") + " c0ffee",
		"tar -xf " + filepath.Join(base, "core.tar") + " -C " + filepath.Join(base, "core"),
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") || dirs[1] != filepath.Join(base, "kit") {
		t.Errorf("%q", lines)
	}

	failing := func(string, []string, []string) (string, error) { return "", errors.New("no such commit") }
	if _, err := UnpackCore(failing, base, "c0ffee"); err == nil || err.Error() != "core @c0ffee: no such commit" {
		t.Errorf("%v", err)
	}
	blocked := filepath.Join(base, "file")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := UnpackCore(fakeUnpack(t, &lines, &dirs, false), blocked, "c0ffee"); err == nil {
		t.Error("a base that is a file was unpacked into")
	}
}

// A commit with no rust/ is no kit: said, not built from an empty directory.
func TestUnpackCoreRefusesACommitWithoutTheKitsDirectory(t *testing.T) {
	var lines, dirs []string
	_, err := UnpackCore(fakeUnpack(t, &lines, &dirs, true), t.TempDir(), "c0ffee")
	if err == nil || err.Error() != "core @c0ffee: stellar-core c0ffee has no rust/" {
		t.Errorf("%v", err)
	}
}

// A PIN NAMING stellar-core-rust is fetched and archived as the commit it became.
func TestUnpackCoreFetchesAStellarCoreRustPinAsTheCommitItBecame(t *testing.T) {
	const old, moved = "01337847eb0e8e3ffd1caac7764d69f097e5983d", "c735c1f792013022e651986ca34e7ab8af0ef7f3"
	var lines, dirs []string
	if _, err := UnpackCore(fakeUnpack(t, &lines, &dirs, false), t.TempDir(), old); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(lines[1], "stellar-core.git "+moved) || !strings.HasSuffix(lines[2], " "+moved) {
		t.Errorf("%q", lines)
	}
	failing := func(string, []string, []string) (string, error) { return "", errors.New("no such commit") }
	if _, err := UnpackCore(failing, t.TempDir(), old); err == nil || err.Error() != "core @"+old+" (stellar-core "+moved+"): no such commit" {
		t.Errorf("%v", err)
	}
}

// A KIT WITH A REPOSITORY OF ITS OWN is the archive's root.
func TestUnpackCoreOfAKitWithItsOwnRepositoryAnswersTheRoot(t *testing.T) {
	old := checks.RustKit
	checks.RustKit = checks.KitSource{Repo: "rob/stellar-core-rust"}
	t.Cleanup(func() { checks.RustKit = old })
	var lines, dirs []string
	base := t.TempDir()
	core, err := UnpackCore(fakeUnpack(t, &lines, &dirs, false), base, "c0ffee")
	if err != nil || core != filepath.Join(base, "core") || !strings.Contains(lines[1], "stellar-core-rust.git c0ffee") {
		t.Errorf("%q %v %q", core, err, lines)
	}
}
