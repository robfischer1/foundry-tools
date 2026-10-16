package atomkit

import (
	"reflect"
	"strings"
	"testing"
)

const notOnForks = "it holds code to the fleet's own lint set, style and conventions, which upstream-owned code does not follow"

const manVet = `schema = "command-atom@1"
id = "go:vet"
stage = "precommit"
lane = "go"
group = "language"
desc = "go vet"
surface = "any:**/go.mod"
toolchain = "go"
argv = ["go", "vet", "./..."]
per_module = true
judge = "exit"
not_on_forks = "` + notOnForks + `"
`

const manMin = `schema = "command-atom@1"
id = "go:vet"
stage = "precommit"
group = "language"
desc = "go vet"
surface = "root:go.mod"
toolchain = "go"
argv = ["go", "vet", "./..."]
`

// manWith swaps the argv line of manMin and appends extra lines.
func manWith(argv, extra string) string {
	s := strings.Replace(manMin, `argv = ["go", "vet", "./..."]`, argv, 1)
	return s + extra
}

func TestUT_339_ParseManifest(t *testing.T) {
	got, err := ParseManifest([]byte(manVet))
	if err != nil {
		t.Fatal(err)
	}
	want := Manifest{Schema: "command-atom@1", ID: "go:vet", Stage: "precommit", Lane: "go", Group: "language",
		Desc: "go vet", Surface: "any:**/go.mod", Toolchain: "go", Argv: []string{"go", "vet", "./..."},
		PerModule: true, Judge: "exit", Args: "none", Timeout: "10m", NotOnForks: notOnForks}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if _, has := reflect.TypeOf(got).FieldByName("Prepare"); has {
		t.Fatal("Manifest must not have a Prepare field (DEF-5-009)")
	}
}

func TestUT_340_ManifestDefaults(t *testing.T) {
	got, err := ParseManifest([]byte(manMin))
	if err != nil {
		t.Fatal(err)
	}
	if got.PerModule || got.Judge != "exit" || got.Args != "none" || got.Timeout != "10m" {
		t.Fatalf("defaults: %+v", got)
	}
}

func TestManifestErrors(t *testing.T) {
	for _, c := range []struct{ row, src, want string }{
		{"UT-341", manVet + "shell = true\n", "unknown key shell"},
		{"UT-342", manVet + "zz = 1\naa = 1\n", "unknown key zz"},
		{"UT-694", manVet + "prepare = [\"go\",\"mod\",\"download\"]\n", "unknown key prepare"},
		{"UT-343", strings.Replace(manVet, "command-atom@1", "command-atom@2", 1), "schema is command-atom@2, want command-atom@1"},
		{"UT-343", strings.Replace(manVet, "schema = \"command-atom@1\"\n", "", 1), "schema is missing, want command-atom@1"},
		{"UT-344", strings.Replace(manMin, "id = \"go:vet\"\n", "", 1), "missing id"},
		{"UT-344", strings.Replace(manMin, "argv = [\"go\", \"vet\", \"./...\"]\n", "", 1), "missing argv"},
		{"UT-344", manWith(`argv = []`, ""), "missing argv"},
		{"UT-344", strings.Replace(manMin, "toolchain = \"go\"\n", "", 1), "missing toolchain"},
		{"UT-345", manMin + "judge = \"regex\"\n", "judge regex is not exit or output"},
		{"UT-345", manMin + "args = \"all\"\n", "args all is not none or append"},
		{"UT-346", manWith(`argv = ["bash","x.sh"]`, ""), "argv[0] bash is not allowed"},
		{"UT-346", manWith(`argv = ["sh","-c","true"]`, ""), "argv runs sh with an inline script (-c)"},
		{"UT-346", manWith(`argv = ["uv","run","python","-c","print(1)"]`, ""), "argv runs python with an inline script (-c)"},
		{"UT-714", manWith(`argv = ["sh","x.sh"]`, ""), "argv[0] sh is not allowed"},
		{"UT-716", manWith("argv = [\"go\",\"vet\",\"a\\nb\"]", ""), "argv[2] is a multi-line argument"},
		{"UT-716", manWith(`argv = ["cat","<<EOF"]`, ""), "argv[1] carries a heredoc marker"},
		{"UT-716", manWith(`argv = ["go","vet","&&","go","build"]`, ""), `argv[2] "&&" chains a second command`},
		{"FN-055 · 4", manMin + "[env]\nA = \"x\\ny\"\n", "env A is a multi-line argument"},
		{"FN-055 · 1", "this is = not toml [", "not TOML: "},
	} {
		_, err := ParseManifest([]byte(c.src))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want error %q, got %v", c.row, c.want, err)
		} else if c.row != "FN-055 · 1" && err.Error() != c.want {
			t.Errorf("%s: want exactly %q (no path prefix), got %q", c.row, c.want, err)
		}
	}
}

func TestManifestAcceptsAnInterpreterWithoutAnInlineFlag(t *testing.T) {
	// CONF-5-018: an interpreter as argv[0] with no inline flag passes.
	if _, err := ParseManifest([]byte(manWith(`argv = ["bun","audit","--audit-level=high"]`, ""))); err != nil {
		t.Fatal(err)
	}
}

func TestManifestEnvAndAppend(t *testing.T) {
	m, err := ParseManifest([]byte(manMin + "args = \"append\"\nwrites_tree = true\n[env]\nCGO_ENABLED = \"0\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Args != "append" || !m.WritesTree || m.Env["CGO_ENABLED"] != "0" {
		t.Fatalf("%+v", m)
	}
}

func TestOneCommandShellAsCommand(t *testing.T) {
	// IF-050 rule 3 words the shell-as-command breach differently from FN-055.
	got := OneCommand([]string{"sh", "x.sh"}, nil)
	if len(got) != 1 || got[0] != "argv runs sh (R33)" {
		t.Fatalf("got %v", got)
	}
	if got := OneCommand([]string{"go", "vet"}, nil); len(got) != 0 {
		t.Fatalf("clean argv: %v", got)
	}
}

func TestOneCommandSkipsLauncherFlags(t *testing.T) {
	got := OneCommand([]string{"env", "-i", "python", "-c", "x"}, nil)
	if len(got) != 1 || got[0] != "argv runs python with an inline script (-c)" {
		t.Fatalf("got %v", got)
	}
}
