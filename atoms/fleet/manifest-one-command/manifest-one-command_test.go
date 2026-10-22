package fleetmanifestonecommand

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
)

// vet is the landed atoms/go/vet.toml, the one-command manifest the rows start from.
const vet = `schema = "command-atom@1"
id = "go:vet"
stage = "precommit"
lane = "go"
group = "language"
desc = "go vet ./... reports nothing."
surface = "any:**/go.mod"
toolchain = "go"
argv = ["go", "vet", "./..."]
per_module = true
judge = "exit"
`

// run puts the manifests under a temp root and runs the atom over them, in the
// order given.
func run(t *testing.T, files map[string]string, order ...string) checks.Verdict {
	t.Helper()
	root := t.TempDir()
	for p, body := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return check(context.Background(), atomkit.In{Root: root, Files: order})
}

func TestTheDeclarationValidates(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Group != checks.GroupOf(id) || Def.Surface != "any:atoms/**/*.toml" || Def.Toolchain != "none" {
		t.Errorf("%+v", Def)
	}
}

func TestUT_703_OneCommandPasses(t *testing.T) {
	v := run(t, map[string]string{"atoms/go/vet.toml": vet}, "atoms/go/vet.toml")
	if v.State != 0 || v.Result != "pass" || v.Reason != "fleet:manifest-one-command: PASS" || len(v.Findings) != 0 {
		t.Fatalf("%+v", v)
	}
	if !slices.Equal(v.Logs, []string{"atoms/go/vet.toml: one command"}) {
		t.Errorf("logs %q", v.Logs)
	}
}

// TestUT_704_to_717_EachRuleHasItsLine: one breach per row, each a finding with
// the checks.Finding shape (DEF-B3-007) and the line the design words.
func TestUT_704_to_717_EachRuleHasItsLine(t *testing.T) {
	for _, tc := range []struct {
		name, body, cause string
		line              string
	}{
		{"UT-704 a second step", vet + "prepare = [\"true\"]\n", "second-step", "key prepare declares a second step"},
		{"UT-705 argv empty", strings.Replace(vet, `argv = ["go", "vet", "./..."]`, "argv = []", 1), "argv-shape", "argv must be one non-empty list of strings"},
		{"UT-705 argv a string", strings.Replace(vet, `argv = ["go", "vet", "./..."]`, `argv = "go vet"`, 1), "argv-shape", "argv must be one non-empty list of strings"},
		{"UT-705 argv with a non-string", strings.Replace(vet, `["go", "vet", "./..."]`, `["go", 2]`, 1), "argv-shape", "argv must be one non-empty list of strings"},
		{"UT-706 sh -c", strings.Replace(vet, `["go", "vet", "./..."]`, `["sh", "-c", "true"]`, 1), "inline-script", "argv runs sh with an inline script (-c)"},
		{"UT-706 uv run python -c", strings.Replace(vet, `["go", "vet", "./..."]`, `["uv", "run", "python", "-c", "x"]`, 1), "inline-script", "argv runs python with an inline script (-c)"},
		{"UT-706 env node -e", strings.Replace(vet, `["go", "vet", "./..."]`, `["env", "node", "-e", "x"]`, 1), "inline-script", "argv runs node with an inline script (-e)"},
		{"UT-707 multi-line", strings.Replace(vet, `["go", "vet", "./..."]`, "[\"go\", \"vet\", \"a\\nb\"]", 1), "multi-line-argument", "argv[2] is a multi-line argument"},
		{"UT-708 heredoc", strings.Replace(vet, `["go", "vet", "./..."]`, `["cat", "<<EOF"]`, 1), "heredoc-marker", "argv[1] carries a heredoc marker"},
		{"UT-709 chain", strings.Replace(vet, `["go", "vet", "./..."]`, `["go", "vet", "&&", "go", "build"]`, 1), "chained-command", `argv[2] "&&" chains a second command`},
		{"UT-715 shell as command", strings.Replace(vet, `["go", "vet", "./..."]`, `["sh", "x.sh"]`, 1), "shell-as-command", "argv runs sh (R33)"},
		{"UT-717 multi-line env", vet + "[env]\nX = \"a\\nb\"\n", "multi-line-argument", "env X is a multi-line argument"},
		{"UT-710 not TOML", "argv = [\n", "not-toml", "not TOML: "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := run(t, map[string]string{"atoms/st/a.toml": tc.body}, "atoms/st/a.toml")
			if v.State != 1 || v.Result != "findings" || v.Reason != "fleet:manifest-one-command: FINDINGS (exit 1)" {
				t.Fatalf("%+v", v)
			}
			f := v.Findings[0]
			if f.Verdict != "violated" || f.Subject != "atoms/st/a.toml" || f.Cause != tc.cause || f.Probe != id ||
				!strings.HasPrefix(f.Detail, "atoms/st/a.toml: "+tc.line) || !strings.HasSuffix(f.Detail, ending) {
				t.Errorf("finding %+v", f)
			}
			if v.Logs[len(v.Logs)-1] != f.Detail {
				t.Errorf("logs %q do not end with the finding", v.Logs)
			}
		})
	}
}

// TestUT_704_BreachesFollowFileThenRuleOrder: several breaches in one file keep
// rule order, and a clean file beside it still logs "one command".
func TestUT_712_SeveralBreachesInRuleOrder(t *testing.T) {
	bad := strings.Replace(vet, `["go", "vet", "./..."]`, `["cat", "<<EOF", "|", "sh"]`, 1) + "then = [\"true\"]\nprepare = [\"true\"]\n"
	v := run(t, map[string]string{"atoms/go/vet.toml": vet, "atoms/st/a.toml": bad}, "atoms/go/vet.toml", "atoms/st/a.toml")
	want := []string{
		"atoms/go/vet.toml: one command",
		"atoms/st/a.toml: key prepare declares a second step" + ending,
		"atoms/st/a.toml: key then declares a second step" + ending,
		"atoms/st/a.toml: argv[1] carries a heredoc marker" + ending,
		`atoms/st/a.toml: argv[2] "|" chains a second command` + ending,
	}
	if v.State != 1 || !slices.Equal(v.Logs, want) {
		t.Fatalf("state %d logs:\n%s", v.State, strings.Join(v.Logs, "\n"))
	}
	if len(v.Findings) != 4 {
		t.Errorf("%d findings, want 4", len(v.Findings))
	}
}

func TestUT_711_AnUnreadableManifestIsACannotRun(t *testing.T) {
	v := check(context.Background(), atomkit.In{Root: t.TempDir(), Files: []string{"atoms/st/a.toml"}})
	if v.State != 2 || v.Result != "cannot-run" || !strings.HasPrefix(v.Reason, "atoms/st/a.toml: unreadable: ") {
		t.Fatalf("%+v", v)
	}
}

func TestUT_718_NoManifestIsAbsent(t *testing.T) {
	v := check(context.Background(), atomkit.In{Root: t.TempDir(), Files: []string{"README.md", "atoms/go/vet.md"}})
	if v.State != 0 || v.Result != "absent" || !strings.HasPrefix(v.Reason, id+": ABSENT") {
		t.Fatalf("%+v", v)
	}
}

// TestIT_349_TheLandedManifestsAreOneCommand: every manifest the module lands
// passes, one "<path>: one command" line each (R46).
func TestIT_349_TheLandedManifestsAreOneCommand(t *testing.T) {
	root := "../../.."
	paths, _ := filepath.Glob(filepath.Join(root, "atoms", "*", "*.toml"))
	if len(paths) != 43 {
		t.Fatalf("%d manifests, want 43", len(paths))
	}
	var files, want []string
	for _, p := range paths {
		rel, _ := filepath.Rel(root, p)
		files = append(files, filepath.ToSlash(rel))
		want = append(want, filepath.ToSlash(rel)+": one command")
	}
	v := check(context.Background(), atomkit.In{Root: root, Files: files})
	if v.State != 0 || !slices.Equal(v.Logs, want) {
		t.Fatalf("state %d\n%s", v.State, strings.Join(v.Logs, "\n"))
	}
}

// withDefaults is the vet manifest with the argument rules and the preflight
// opt-out the dev manifests use: still one command.
const withDefaults = vet + `args = "append"
preflight = false

[[default]]
args = ["--workspace"]
unless = ["-p", "--workspace"]
until = "--"

[[default]]
args = ["--", "-W", "clippy::all"]
unless = ["--"]
tail = true
`

func TestDefaultTablesAndPreflightAreOneCommand(t *testing.T) {
	v := run(t, map[string]string{"atoms/dev/a.toml": withDefaults}, "atoms/dev/a.toml")
	if v.State != 0 || len(v.Findings) != 0 || !slices.Equal(v.Logs, []string{"atoms/dev/a.toml: one command"}) {
		t.Fatalf("%+v", v)
	}
}

// TestADefaultCannotSmuggleASecondCommand: the rules read the composed argv, so
// a chain, a heredoc or a multi-line argument in a default is a breach.
func TestADefaultCannotSmuggleASecondCommand(t *testing.T) {
	for _, tc := range []struct{ name, extra, cause, line string }{
		{"chain", "[[default]]\nargs = [\"&&\", \"rm\"]\n", "chained-command", `chains a second command`},
		{"heredoc", "[[default]]\nargs = [\"<<EOF\"]\n", "heredoc-marker", "carries a heredoc marker"},
		{"multi-line", "[[default]]\nargs = [\"a\\nb\"]\n", "multi-line-argument", "is a multi-line argument"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := run(t, map[string]string{"atoms/dev/a.toml": vet + "args = \"append\"\n" + tc.extra}, "atoms/dev/a.toml")
			if v.State != 1 || len(v.Findings) != 1 || v.Findings[0].Cause != tc.cause || !strings.Contains(v.Findings[0].Detail, tc.line) {
				t.Fatalf("%+v", v)
			}
		})
	}
}
