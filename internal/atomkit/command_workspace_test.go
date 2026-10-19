package atomkit

import (
	"slices"
	"testing"
)

// THE BARE WORKSPACE ROOT (runbook D-B4), for the command atoms: `bun run <script>`
// has no script at a root that only declares workspaces, so it runs in the members.
// Everything else about a root stays put.

func runIn(t *testing.T, manifest string, argv []string, files ...string) []string {
	t.Helper()
	root := t.TempDir()
	put(t, root, "package.json", manifest)
	f := &fakeExec{}
	c := vetCommand(f, Manifest{Argv: argv, Toolchain: "bun"})
	c.Facts = Tree{Version: 1, Root: root, Files: files}
	RunCommand(t.Context(), c)
	var dirs []string
	for _, call := range f.calls {
		dirs = append(dirs, call.dir)
	}
	return dirs
}

func TestABareWorkspaceRootRunsScriptsInItsMembers(t *testing.T) {
	bare := `{"private": true, "workspaces": ["ts", "tools/*"]}`
	files := []string{"package.json", "ts/package.json", "tools/a/package.json", "tools/b/package.json"}
	if got := runIn(t, bare, []string{"bun", "run", "test"}, files...); !slices.Equal(got, []string{"/src/tools/a", "/src/tools/b", "/src/ts"}) {
		t.Errorf("%v", got)
	}
}

func TestNothingElseMovesOffTheRoot(t *testing.T) {
	bare := `{"private": true, "workspaces": ["ts"]}`
	files := []string{"package.json", "ts/package.json"}
	// bun audit reads the root lockfile
	if got := runIn(t, bare, []string{"bun", "audit", "--audit-level=high"}, files...); !slices.Equal(got, []string{"/src"}) {
		t.Errorf("audit: %v", got)
	}
	// a root with scripts, a package root, a root nobody can read
	for name, manifest := range map[string]string{
		"theia":   `{"private": true, "scripts": {"test": "turbo test"}, "workspaces": ["ts"]}`,
		"package": `{"name": "x", "scripts": {"test": "x"}}`,
		"broken":  `{`,
	} {
		if got := runIn(t, manifest, []string{"bun", "run", "test"}, files...); !slices.Equal(got, []string{"/src"}) {
			t.Errorf("%s: %v", name, got)
		}
	}
	// a tree with no package.json on disk
	f := &fakeExec{}
	c := vetCommand(f, Manifest{Argv: []string{"bun", "run", "test"}, Toolchain: "bun"})
	c.Facts = Tree{Version: 1, Root: t.TempDir(), Files: files}
	RunCommand(t.Context(), c)
	if len(f.calls) != 1 || f.calls[0].dir != "/src" {
		t.Errorf("%+v", f.calls)
	}
	// the go toolchain never reads package.json
	if got := runIn(t, bare, []string{"bun", "run", "test"}, files...); len(got) == 0 {
		t.Error("sanity")
	}
	g := &fakeExec{}
	cg := vetCommand(g, Manifest{})
	cg.Facts = Tree{Version: 1, Root: t.TempDir(), Files: []string{"go.mod"}}
	RunCommand(t.Context(), cg)
	if len(g.calls) != 1 || g.calls[0].dir != "/src" {
		t.Errorf("%+v", g.calls)
	}
}
