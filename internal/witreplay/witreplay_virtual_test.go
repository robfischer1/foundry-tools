package witreplay

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
)

// THE VIRTUAL WORKSPACE (runbook D-B3), for the replay body rust:wit-compose and
// dev:rust-replay share: the composer, the replay host and the tapes are the
// facade's, so they are found and run from beside it, and ABSENT is the answer
// only when there is no facade.

const virtualRoot = "[workspace]\nmembers = [\"rust\"]\n"

func metadata(root string, members map[string][]string) string {
	type pkg struct {
		ID           string              `json:"id"`
		ManifestPath string              `json:"manifest_path"`
		Features     map[string][]string `json:"features"`
	}
	var pkgs []pkg
	var ids []string
	for dir, names := range members {
		f := map[string][]string{}
		for _, n := range names {
			f[n] = []string{}
		}
		manifest := filepath.Join(root, dir, "Cargo.toml")
		pkgs = append(pkgs, pkg{ID: "path+file://" + manifest, ManifestPath: manifest, Features: f})
		ids = append(ids, "path+file://"+manifest)
	}
	out, _ := json.Marshal(map[string]any{"packages": pkgs, "workspace_members": ids, "workspace_root": root})
	return string(out)
}

// virtual lays a virtual root over a facade at rust/ carrying the crate's files.
func virtual(t *testing.T, f *fake, members map[string][]string, facade map[string]string, dirs map[string]string) atomkit.In {
	t.Helper()
	files := map[string]string{"Cargo.toml": virtualRoot}
	for name, body := range facade {
		files["rust/"+name] = body
	}
	in := input(t, f, files)
	meta := metadata(in.Root, members)
	in.Exec = func(ctx context.Context, c atoms.Cmd) (string, int) {
		line := strings.Join(append([]string{c.Name}, c.Args...), " ")
		dirs[line] = c.Dir
		if strings.HasPrefix(line, "cargo metadata") {
			f.runs = append(f.runs, line)
			return meta, 0
		}
		return f.exec(ctx, c)
	}
	return in
}

var rustFiles = map[string]string{
	"Cargo.toml": two, "tools/compose/compose.sh": "x", "tools/replay/Cargo.toml": "x",
	"tests/tapes/identity.json": "{}", "tests/red/a.json": "{}",
}

var members = map[string][]string{"rust": {"default", "wit-guest", "wit-guest-identity"}}

func TestAVirtualWorkspaceComposesAndReplaysFromItsFacade(t *testing.T) {
	f := &fake{}
	dirs := map[string]string{}
	in := virtual(t, f, members, rustFiles, dirs)
	v := Run(t.Context(), in, row)
	if v.State != 0 || v.Result == "absent" {
		t.Fatalf("%+v", v)
	}
	facade := filepath.Join(in.Root, "rust")
	var composed, replayed, built bool
	for line, dir := range dirs {
		switch {
		case strings.HasPrefix(line, "cargo metadata"):
			if dir != in.Root {
				t.Errorf("cargo metadata ran in %s, not the root", dir)
			}
		case strings.HasPrefix(line, "cargo build --locked --release --manifest-path tools/replay/Cargo.toml"):
			built = true
			if dir != facade {
				t.Errorf("the replay host built in %s, not the facade", dir)
			}
		case strings.HasPrefix(line, "tools/compose/compose.sh "):
			composed = true
			if dir != facade {
				t.Errorf("the composer ran in %s, not the facade", dir)
			}
		case strings.Contains(line, "replay-host/replay"):
			replayed = true
			if dir != facade || !strings.Contains(line, " tests/tapes/identity.json tests/red/a.json") {
				t.Errorf("the replay ran in %s with %q; tapes are the facade's, named relative to it", dir, line)
			}
		}
	}
	if !built || !composed || !replayed {
		t.Errorf("built %v composed %v replayed %v: %v", built, composed, replayed, f.runs)
	}
}

// The root's own tools and tapes are not the facade's: a virtual workspace whose
// facade carries no composer is absent, and says where it looked.
func TestAVirtualWorkspaceWhoseFacadeHasNoComposerIsAbsent(t *testing.T) {
	f := &fake{}
	in := virtual(t, f, members, map[string]string{"Cargo.toml": two}, map[string]string{})
	// a composer at the ROOT must not satisfy the facade
	in = withRootFiles(t, in, "tools/compose/compose.sh", "tools/replay/Cargo.toml")
	v := Run(t.Context(), in, row)
	if v.Result != "absent" || !strings.Contains(v.Reason, "the tree carries no rust/tools/compose/compose.sh and rust/tools/replay/Cargo.toml") {
		t.Errorf("%+v", v)
	}
}

func withRootFiles(t *testing.T, in atomkit.In, names ...string) atomkit.In {
	t.Helper()
	in.Files = append(in.Files, names...)
	return in
}

func TestAVirtualWorkspaceWithNoFacadeIsAbsentAndOnlyThen(t *testing.T) {
	f := &fake{}
	in := virtual(t, f, map[string][]string{"rust": {"default"}}, rustFiles, map[string]string{})
	v := Run(t.Context(), in, row)
	if v.Result != "absent" || !strings.Contains(v.Reason, "x:replay: ABSENT - Cargo.toml is a virtual workspace and no member declares a wit-guest-<world> feature") {
		t.Errorf("%+v", v)
	}
	if len(f.runs) != 1 || !strings.HasPrefix(f.runs[0], "cargo metadata") {
		t.Errorf("%v", f.runs)
	}
}

func TestAVirtualWorkspaceWhoseFacadeCannotBeFoundIsNeverAPass(t *testing.T) {
	for name, answer := range map[string]struct {
		out  string
		code int
	}{"exit": {"error", 101}, "garbage": {"nope", 0}, "never ran": {"cargo: not found", -1}} {
		in := input(t, &fake{}, map[string]string{"Cargo.toml": virtualRoot})
		in.Exec = func(context.Context, atoms.Cmd) (string, int) { return answer.out, answer.code }
		if v := Run(t.Context(), in, row); v.State != 2 || v.Result == "absent" {
			t.Errorf("%s: %+v", name, v)
		}
	}
}

// A package root asks cargo for nothing.
func TestAPackageRootNeverAsksCargoMetadata(t *testing.T) {
	f := &fake{}
	if v := Run(t.Context(), input(t, f, tree(nil)), row); v.State != 0 {
		t.Fatalf("%+v", v)
	}
	for _, r := range f.runs {
		if strings.HasPrefix(r, "cargo metadata") {
			t.Errorf("a package root is its own facade: %v", f.runs)
		}
	}
}
