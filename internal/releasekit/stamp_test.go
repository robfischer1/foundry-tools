package releasekit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atoms"
)

const stampSha = "b3db6f1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// stampIn is an input over a module whose go.mod says mod, with git answering
// what the test says (a commit time, or a failure).
func stampIn(t *testing.T, mod string, git func(atoms.Cmd) (string, int)) (atoms.Input, string, *[]atoms.Cmd) {
	t.Helper()
	dir := t.TempDir()
	if mod != "" {
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var calls []atoms.Cmd
	in := atoms.Input{Root: dir, Exec: func(_ context.Context, c atoms.Cmd) (string, int) {
		calls = append(calls, c)
		return git(c)
	}}
	return in, dir, &calls
}

func committedAt(string) func(atoms.Cmd) (string, int) {
	return func(atoms.Cmd) (string, int) { return "1792086104\n", 0 }
}

// THE GATE'S COMPILE IS NOT STAMPED; A DELIVERY LANE'S IS. The executor names a
// release directory (ATOM_RELEASE_OUT) in the lanes that ship what the release
// atoms stage, and in no other: that is the signal, and with it unset nothing is
// stamped and git is not asked.
func TestStampIsNothingOutsideADeliveryLane(t *testing.T) {
	t.Setenv(OutEnv, "")
	t.Setenv("ATOM_SHA", stampSha)
	in, dir, calls := stampIn(t, "module git.notusmi.com/rob/x\n", committedAt(""))
	mod, ver, fault := Stamp(context.Background(), in, dir)
	if mod != "" || ver != "" || fault != "" || len(*calls) != 0 {
		t.Errorf("got %q %q %q after %v", mod, ver, fault, *calls)
	}
}

// In a delivery lane the stamp is the stamp tag's tail: the landed commit's
// committer time and its first seven characters (buildlane.StampVersion).
func TestStampIsTheStampTagsTailInADeliveryLane(t *testing.T) {
	t.Setenv(OutEnv, t.TempDir())
	t.Setenv("ATOM_SHA", stampSha)
	in, dir, calls := stampIn(t, "// c\nmodule \"git.notusmi.com/rob/x\" // trailing\n\ngo 1.25\n", committedAt(""))
	mod, ver, fault := Stamp(context.Background(), in, dir)
	if mod != "git.notusmi.com/rob/x" || ver != "1792086104-b3db6f1" || fault != "" {
		t.Errorf("got %q %q %q", mod, ver, fault)
	}
	if len(*calls) != 1 || strings.Join((*calls)[0].Args, " ") != "-c safe.directory=* log -1 --format=%ct "+stampSha {
		t.Errorf("git was asked %v", *calls)
	}
}

// A delivery lane that cannot say what it stamps is could-not-run, never an
// unstamped binary shipped as if it were stamped.
func TestStampFaultsAreNamed(t *testing.T) {
	cases := []struct {
		name, mod, sha string
		git            func(atoms.Cmd) (string, int)
		want           string
	}{
		{"no commit", "module x\n", "", committedAt(""), "ATOM_SHA"},
		{"git fails", "module x\n", stampSha, func(atoms.Cmd) (string, int) { return "bad object", 128 }, "committer time"},
		{"git cannot start", "module x\n", stampSha, func(atoms.Cmd) (string, int) { return "no git", -1 }, "committer time"},
		{"not a timestamp", "module x\n", stampSha, func(atoms.Cmd) (string, int) { return "yesterday", 0 }, "unix timestamp"},
		{"no go.mod", "", stampSha, committedAt(""), "go.mod"},
		{"no module line", "go 1.25\n", stampSha, committedAt(""), "module"},
		{"module that splits ldflags", "module a b;c\n", stampSha, committedAt(""), "cannot be named"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(OutEnv, t.TempDir())
			t.Setenv("ATOM_SHA", c.sha)
			in, dir, _ := stampIn(t, c.mod, c.git)
			mod, ver, fault := Stamp(context.Background(), in, dir)
			if fault == "" || !strings.Contains(fault, c.want) || mod != "" || ver != "" {
				t.Errorf("got %q %q %q, want a fault with %q", mod, ver, fault, c.want)
			}
		})
	}
}

// An unquoted module path is the path as written.
func TestStampReadsAPlainModuleLine(t *testing.T) {
	t.Setenv(OutEnv, t.TempDir())
	t.Setenv("ATOM_SHA", stampSha)
	in, dir, _ := stampIn(t, "module git.notusmi.com/rob/plain\n\ngo 1.25\n", committedAt(""))
	mod, ver, fault := Stamp(context.Background(), in, dir)
	if mod != "git.notusmi.com/rob/plain" || ver != "1792086104-b3db6f1" || fault != "" {
		t.Errorf("got %q %q %q", mod, ver, fault)
	}
}

// THE TOOLCHAIN LINE NAMES THE IMAGE AND WHAT GO SAYS IT IS (foundry-tools#25784).
// go's answer is asked as `go env GOVERSION`, and anything that is not one clean
// word, or a non-zero exit, reads "unreadable": the digest is still there, and
// no version is made up.
func TestToolchainLineNamesTheImageAndGoesVersion(t *testing.T) {
	const image = "docker.io/library/golang:1.27.3-bookworm@sha256:abc"
	for _, tc := range []struct {
		name string
		out  string
		code int
		want string
	}{
		{"readable", "go1.27.3\n", 0, "toolchain: go image=" + image + " version=go1.27.3"},
		{"a non-zero exit", "boom", 3, "toolchain: go image=" + image + " version=unreadable"},
		{"nothing said", "\n", 0, "toolchain: go image=" + image + " version=unreadable"},
		{"more than one word", "go version go1.27.3\n", 0, "toolchain: go image=" + image + " version=unreadable"},
	} {
		in, _, calls := stampIn(t, "", func(atoms.Cmd) (string, int) { return tc.out, tc.code })
		got := ToolchainLine(context.Background(), in, image)
		if got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
		if len(*calls) != 1 || (*calls)[0].Name != "go" || strings.Join((*calls)[0].Args, " ") != "env GOVERSION" {
			t.Errorf("%s: asked %v", tc.name, *calls)
		}
	}
}
