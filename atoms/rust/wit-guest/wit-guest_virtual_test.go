package rustwitguest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// THE VIRTUAL WORKSPACE (runbook D-B3). A root Cargo.toml with a [workspace]
// and no [package] declares no feature, so the atom used to read an empty table
// and report ABSENT: a pass that checked nothing. These hold it to finding the
// facade through cargo metadata and to being ABSENT only when there is none.

const virtualRoot = "[workspace]\nmembers = [\"rust\", \"rust/crates/*\"]\nresolver = \"3\"\n"

// metadata is `cargo metadata --no-deps` for a workspace at root whose members
// are dir -> feature names.
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

// virtualTree lays a virtual root over a facade at rust/ and answers an input
// whose exec records each run's directory and answers cargo metadata.
func virtualTree(t *testing.T, f *fake, members map[string][]string, dirs map[string]string) atomkit.In {
	t.Helper()
	in := input(t, f, virtualRoot)
	for name, body := range map[string]string{"rust/Cargo.toml": two} {
		p := filepath.Join(in.Root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
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

var facadeMembers = map[string][]string{
	"rust":                 {"default", "wit-guest", "wit-guest-identity", "wit-guest-reader"},
	"rust/crates/identity": {"default", "wit-guest-identity"},
}

func TestAVirtualWorkspaceIsGradedThroughItsFacade(t *testing.T) {
	f := &fake{rules: []rule{{"component wit", "world w", 0}}}
	dirs := map[string]string{}
	in := virtualTree(t, f, facadeMembers, dirs)
	v := check(t.Context(), in)
	if v.State != 0 || v.Result == "absent" {
		t.Fatalf("%+v", v)
	}
	facade := filepath.Join(in.Root, "rust")
	for _, w := range []string{"wit-guest-identity", "wit-guest-reader"} {
		line := build + " --no-default-features --features " + w + " --target " + checks.WitGuestTarget + " --target-dir " + os.Getenv("CARGO_TARGET_DIR")
		if dirs["cargo "+strings.TrimPrefix(line, "cargo ")] != facade {
			t.Errorf("%s did not build from the facade: %q", w, dirs[line])
		}
	}
	tests := 0
	for line, dir := range dirs {
		if strings.HasPrefix(line, "cargo test") {
			tests++
			if dir != facade {
				t.Errorf("the native test ran in %s, not the facade", dir)
			}
		}
		if strings.HasPrefix(line, "cargo metadata") && dir != in.Root {
			t.Errorf("cargo metadata ran in %s, not the root", dir)
		}
	}
	if tests != 1 {
		t.Errorf("one combined native test, got %d: %v", tests, f.runs)
	}
	// every world graded, found from the facade and not from the root's empty table
	if !f.ran("--features wit-guest-identity ") || !f.ran("--features wit-guest-reader ") {
		t.Errorf("worlds not built: %v", f.runs)
	}
}

func TestAVirtualWorkspaceHoldsTheFacadesBudgetFile(t *testing.T) {
	f := &fake{}
	dirs := map[string]string{}
	in := virtualTree(t, f, facadeMembers, dirs)
	// at the ROOT a budget file would be the wrong place; the facade's is read
	if err := os.WriteFile(filepath.Join(in.Root, checks.WitGuestBudgetsFile), []byte("[budget]\nwit-guest-identity = 100\nwit-guest-reader = 100\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(in.Root, "rust", checks.WitGuestBudgetsFile), []byte("[budget]\nwit-guest-identity = 3\nwit-guest-reader = 100\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v := check(t.Context(), in)
	if v.State != 1 || !strings.Contains(v.Reason, "wit-guest-identity: 4 bytes, budget 3, OVER BY 1 bytes") {
		t.Fatalf("the facade's budget file was not the one read: %+v", v)
	}
}

func TestAVirtualWorkspaceWithNoFacadeIsAbsentAndOnlyThen(t *testing.T) {
	f := &fake{}
	dirs := map[string]string{}
	in := virtualTree(t, f, map[string][]string{"rust": {"default", "wit"}, "rust/crates/a": {"default"}}, dirs)
	v := check(t.Context(), in)
	if v.State != 0 || v.Result != "absent" || !strings.Contains(v.Reason, "rust:wit-guest: ABSENT - Cargo.toml is a virtual workspace and no member declares a wit-guest-<world> feature") {
		t.Fatalf("%+v", v)
	}
	if len(f.runs) != 1 || !strings.HasPrefix(f.runs[0], "cargo metadata") {
		t.Errorf("ABSENT is decided from cargo metadata alone: %v", f.runs)
	}
}

// A virtual root whose facade cannot be found out is a could-not-run: the lane
// never passes on a subject it could not find.
func TestAVirtualWorkspaceWhoseFacadeCannotBeFoundIsNeverAPass(t *testing.T) {
	for _, tc := range []struct {
		name string
		rule rule
		want string
	}{
		{"cargo metadata fails", rule{"cargo metadata", "error: failed to parse manifest", 101}, "cargo metadata exited 101"},
		{"cargo cannot run", rule{"cargo metadata", "exec: cargo: not found", -1}, "the atom never ran"},
		{"the answer is not JSON", rule{"cargo metadata", "not json", 0}, "cargo metadata did not parse"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{}
			in := input(t, f, virtualRoot)
			in.Exec = func(ctx context.Context, c atoms.Cmd) (string, int) {
				if c.Name == "cargo" && c.Args[0] == "metadata" {
					f.runs = append(f.runs, "cargo metadata")
					return tc.rule.out, tc.rule.code
				}
				return f.exec(ctx, c)
			}
			v := check(t.Context(), in)
			if v.State != 2 || v.Result == "absent" || v.Result == "pass" || !strings.Contains(v.Reason, tc.want) {
				t.Errorf("%+v", v)
			}
			if f.ran(build) {
				t.Errorf("nothing builds without a facade: %v", f.runs)
			}
		})
	}
}

func TestAMemberWhoseManifestWillNotReadIsACouldNotRun(t *testing.T) {
	f := &fake{}
	dirs := map[string]string{}
	in := virtualTree(t, f, facadeMembers, dirs)
	if err := os.Remove(filepath.Join(in.Root, "rust", "Cargo.toml")); err != nil {
		t.Fatal(err)
	}
	if v := check(t.Context(), in); v.State != 2 || !strings.Contains(v.Reason, "facade's Cargo.toml would not read") {
		t.Errorf("%+v", v)
	}
}

// A root that is a package never asks cargo for its metadata: today's layout
// runs exactly the commands it always ran.
func TestAPackageRootNeverAsksCargoMetadata(t *testing.T) {
	f := &fake{}
	in := input(t, f, strings.Replace(two, "[features]", "[workspace]\nmembers = [\"crates/*\"]\n\n[features]", 1))
	if v := check(t.Context(), in); v.State != 0 {
		t.Fatalf("%+v", v)
	}
	if f.ran("cargo metadata") {
		t.Errorf("a package root is its own facade: %v", f.runs)
	}
	if !slices.ContainsFunc(f.runs, func(r string) bool { return strings.HasPrefix(r, "cargo build") }) {
		t.Errorf("%v", f.runs)
	}
}
