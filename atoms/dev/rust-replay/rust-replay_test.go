package devrustreplay

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

const two = "[package]\nname = \"stellar-core\"\n\n[features]\ndefault = []\nwit-guest = [\"wit-guest-identity\"]\nwit-guest-identity = [\"dep:wit-bindgen\"]\nwit-guest-reader = [\"dep:wit-bindgen\"]\n"

// replayOut is what the replay host prints for two tapes and a red one.
const replayOut = "component x: imports []\nexports []\ntests/tapes/identity.json: 3/3 pass, 0 fail\ntests/tapes/reader.json: 5/5 pass, 0 fail\ntests/red/tail.json: 2/2 pass, 0 fail\nBENCH all: 10 ns/call over 3 calls\nTOTAL 10 pass, 0 fail\n"

// exec answers the replay host with out and code, and leaves the host where cargo would.
func exec(out string, code int) atoms.Exec {
	return func(_ context.Context, c atoms.Cmd) (string, int) {
		if i := slices.Index(c.Args, "--target-dir"); c.Name == "cargo" && i >= 0 {
			host := filepath.Join(c.Args[i+1], "release", "replay")
			_ = os.MkdirAll(filepath.Dir(host), 0o755)
			_ = os.WriteFile(host, []byte("host"), 0o644)
		}
		if strings.HasSuffix(c.Name, "replay") {
			return out, code
		}
		return "", 0
	}
}

func input(t *testing.T, files map[string]string, ex atoms.Exec) atomkit.In {
	t.Helper()
	t.Setenv("CARGO_TARGET_DIR", filepath.Join(t.TempDir(), "target"))
	t.Setenv("ATOM_WORK", t.TempDir())
	root := t.TempDir()
	var names []string
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return atomkit.In{Root: root, Files: names, Exec: ex}
}

func tree() map[string]string {
	return map[string]string{
		"Cargo.toml":                two,
		"tools/compose/compose.sh":  "#!/usr/bin/env bash\n",
		"tools/replay/Cargo.toml":   "[package]\nname = \"replay\"\n",
		"tests/tapes/identity.json": "{}",
		"tests/tapes/reader.json":   "{}",
		"tests/red/tail.json":       "{}",
	}
}

func TestTheDeclarationValidatesAndIsTheDevVerb(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != "dev:rust-replay" || Def.Stage != "dev" || Def.Lane != checks.LaneRust || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "rust" || Def.Surface != "root:Cargo.toml" || Def.NotOnForks != "" || Def.SubsumedBy != "" ||
		!Def.WritesTree || !Def.NeedsManifest || !slices.Equal(Def.Needs, []string{"tool:wasm-tools", "tool:wac"}) {
		t.Errorf("%+v", Def)
	}
}

func TestAPassCarriesTheTallyAndTheTapeCount(t *testing.T) {
	v := check(t.Context(), input(t, tree(), exec(replayOut, 0)))
	if v.State != 0 || v.Atom != id || v.Stage != "dev" || v.Reason != "dev:rust-replay: TOTAL 10 pass, 0 fail over 3 tapes" {
		t.Fatalf("%+v", v)
	}
	logs := strings.Join(v.Logs, "\n")
	for _, ln := range []string{"tests/tapes/identity.json: 3/3 pass, 0 fail", "tests/tapes/reader.json: 5/5 pass, 0 fail", "tests/red/tail.json: 2/2 pass, 0 fail", "TOTAL 10 pass, 0 fail"} {
		if !strings.Contains(logs, ln) {
			t.Errorf("the logs lack %q: %v", ln, v.Logs)
		}
	}
}

func TestAFailingReplayFollowsItsExitAndLeadsWithTheTally(t *testing.T) {
	out := strings.Replace(replayOut, "identity.json: 3/3 pass, 0 fail", "identity.json: 2/3 pass, 1 fail", 1)
	out = strings.Replace(out, "TOTAL 10 pass, 0 fail", "TOTAL 9 pass, 1 fail", 1)
	v := check(t.Context(), input(t, tree(), exec(out, 1)))
	if v.State != 1 || !strings.HasPrefix(v.Reason, "dev:rust-replay: TOTAL 9 pass, 1 fail over 3 tapes\n") || !strings.Contains(v.Reason, "[replay]") {
		t.Fatalf("%+v", v)
	}
}

func TestWithoutATallyTheSharedVerdictStands(t *testing.T) {
	// the replay died before printing one: the shared verdict is the answer
	v := check(t.Context(), input(t, tree(), exec("boom\n", 1)))
	if v.State != 1 || strings.Contains(v.Reason, "over") || !strings.Contains(v.Reason, "dev:rust-replay [replay]") {
		t.Fatalf("%+v", v)
	}
}

func TestAbsentIsLeftAlone(t *testing.T) {
	v := check(t.Context(), input(t, map[string]string{"Cargo.toml": "[package]\nname = \"x\"\n\n[features]\ndefault = []\n"}, exec("", 0)))
	if v.State != 0 || v.Result != "absent" || !strings.HasPrefix(v.Reason, "dev:rust-replay: ABSENT") {
		t.Fatalf("%+v", v)
	}
}
