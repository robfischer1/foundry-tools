package checks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TWO STARS WITH EQUAL-LENGTH NAMES, ONE GOCACHE, ONE /src (foundry-tools#16289).
// The lanes mount every tree at one path with every mtime at one second past
// the epoch over one shared GOCACHE volume; the go command's package index
// keys a directory on file name + mtime + size, so a same-sized door.go from
// another star is served from the index. This runs the real go command the
// way a lane does and holds GoDebug to the one thing it is for.
func TestGoDebugKeepsTwoStarsOfEqualNameLengthApartInASharedGoCache(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain on PATH")
	}
	root := t.TempDir()
	cache := filepath.Join(root, "gocache")
	src := filepath.Join(root, "src") // the lanes' /src: same path for every star

	importsOf := func(star, debug string) string {
		t.Helper()
		if err := os.RemoveAll(src); err != nil {
			t.Fatal(err)
		}
		files := map[string]string{
			"go.mod":                    "module example.com/" + star + "\n\ngo 1.21\n",
			"internal/verbs/verbs.go":   "package verbs\n",
			"internal/admin/door.go":    "package admin\n\nimport _ \"example.com/" + star + "/internal/verbs\"\n",
			"internal/admin/another.go": "package admin\n",
		}
		epoch := time.Unix(1, 0) // sourceTimestamp
		for name, body := range files {
			p := filepath.Join(src, name)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(p, epoch, epoch); err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.Command("go", "list", "-mod=mod", "-f", "{{.Imports}}", "./internal/admin")
		cmd.Dir = src
		cmd.Env = append(os.Environ(), "GOCACHE="+cache, "GOFLAGS=", "GOPROXY=off", "GODEBUG="+debug)
		out, _ := cmd.CombinedOutput()
		return string(out)
	}

	// "nomos" and "midas": five letters each, so door.go is the same size.
	for _, debug := range []string{GoDebug} {
		if got := importsOf("nomos", debug); !strings.Contains(got, "example.com/nomos/internal/verbs") {
			t.Fatalf("nomos: go list answered %q", got)
		}
		if got := importsOf("midas", debug); !strings.Contains(got, "example.com/midas/internal/verbs") || strings.Contains(got, "nomos") {
			t.Errorf("with GODEBUG=%s a second star of equal name length is served the first star's imports: %q", debug, got)
		}
	}

	// THE CONTROL: without it the same sequence collides. If a newer go
	// stops, the premise is gone and this test has nothing to hold.
	_ = importsOf("nomos", "")
	if got := importsOf("midas", ""); !strings.Contains(got, "nomos") {
		t.Skipf("this go no longer serves another tree's imports from a stat-keyed index (%q); GoDebug is now belt-and-braces", got)
	}
}

func TestGoDebugNamesTheIndexOff(t *testing.T) {
	if GoDebug != "goindex=0" {
		t.Errorf("GoDebug = %q; the go command's package index is turned off by goindex=0", GoDebug)
	}
}
