package atomkit

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// manDefaults is a go-test-shaped manifest: -race ahead of the caller's
// arguments unless the caller spells it, ./... after them unless they name a
// package, and always before -args.
const manDefaults = manMin + `args = "append"

[[default]]
args = ["-race"]
unless = ["-race", "--race"]
until = "-args"

[[default]]
args = ["./..."]
unless = ["@package"]
tail = true
until = "-args"
`

func TestParseManifestDefaultTables(t *testing.T) {
	m, err := ParseManifest([]byte(manDefaults))
	if err != nil {
		t.Fatal(err)
	}
	want := []Default{
		{Args: []string{"-race"}, Unless: []string{"-race", "--race"}, Until: "-args"},
		{Args: []string{"./..."}, Unless: []string{"@package"}, Tail: true, Until: "-args"},
	}
	if !reflect.DeepEqual(m.Default, want) {
		t.Fatalf("got %+v\nwant %+v", m.Default, want)
	}
	if !m.RunsPreflight() {
		t.Fatal("preflight defaults to true")
	}
}

func TestParseManifestPreflight(t *testing.T) {
	m, err := ParseManifest([]byte(manMin + "preflight = false\n"))
	if err != nil {
		t.Fatal(err)
	}
	if m.RunsPreflight() {
		t.Fatal("preflight = false must opt out")
	}
	m, err = ParseManifest([]byte(manMin + "preflight = true\n"))
	if err != nil || !m.RunsPreflight() {
		t.Fatalf("preflight = true: %v, %+v", err, m)
	}
}

func TestParseManifestRefusesBadDefaultsAndPreflight(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{"default without args = append", manMin + "[[default]]\nargs = [\"./...\"]\n", "default needs args = \"append\""},
		{"default with args = none", manMin + "args = \"none\"\n[[default]]\nargs = [\"./...\"]\n", "default needs args = \"append\""},
		{"default with no args", manMin + "args = \"append\"\n[[default]]\nunless = [\"-p\"]\n", "default[0] has no args"},
		{"unknown key in a default", manMin + "args = \"append\"\n[[default]]\nargs = [\"x\"]\nshell = true\n", "unknown key default.shell"},
		{"unknown @ token", manMin + "args = \"append\"\n[[default]]\nargs = [\"x\"]\nunless = [\"@pkg\"]\n", "default[0] unless @pkg is not @package"},
		{"empty unless token", manMin + "args = \"append\"\n[[default]]\nargs = [\"x\"]\nunless = [\"\"]\n", "default[0] has an empty unless token"},
		{"preflight not a bool", manMin + "preflight = \"no\"\n", "not TOML"},
		{"chained default", manMin + "args = \"append\"\n[[default]]\nargs = [\"&&\", \"x\"]\n", `chains a second command`},
		{"multi-line default", manMin + "args = \"append\"\n[[default]]\nargs = [\"a\\nb\"]\n", "multi-line argument"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseManifest([]byte(c.src))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err %v, want it to contain %q", err, c.want)
			}
		})
	}
}

func TestComposeTable(t *testing.T) {
	m, err := ParseManifest([]byte(strings.Replace(manDefaults, `argv = ["go", "vet", "./..."]`, `argv = ["go", "test"]`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		args []string
		want []string
	}{
		{"no args", nil, []string{"go", "test", "-race", "./..."}},
		{"empty args", []string{}, []string{"go", "test", "-race", "./..."}},
		{"a package replaces the default", []string{"./pkg"}, []string{"go", "test", "-race", "./pkg"}},
		{"a flag keeps the default", []string{"-run", "TestX"}, []string{"go", "test", "-race", "-run", "TestX", "./..."}},
		{"a value shaped like nothing", []string{"-run", "TestX", "./a", "./b"}, []string{"go", "test", "-race", "-run", "TestX", "./a", "./b"}},
		{"race off", []string{"-race=false"}, []string{"go", "test", "-race=false", "./..."}},
		{"args marker", []string{"-args", "-v"}, []string{"go", "test", "-race", "./...", "-args", "-v"}},
		{"package before the args marker", []string{"./x", "-args", "-v"}, []string{"go", "test", "-race", "./x", "-args", "-v"}},
		{"package-shaped word after the args marker", []string{"-args", "./x"}, []string{"go", "test", "-race", "./...", "-args", "./x"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := m.Compose(c.args); !slices.Equal(got, c.want) {
				t.Fatalf("got %v\nwant %v", got, c.want)
			}
		})
	}
	m.Argv = []string{"go", "test"}
	before := slices.Clone(m.Argv)
	_ = m.Compose([]string{"./x"})
	if !slices.Equal(m.Argv, before) {
		t.Fatal("Compose wrote into the manifest's argv")
	}
}

func TestComposeShortFlagAndEqualsForms(t *testing.T) {
	m := Manifest{Argv: []string{"cargo", "test"}, Args: "append", Default: []Default{
		{Args: []string{"--workspace"}, Unless: []string{"-p", "--package", "--workspace", "--manifest-path"}, Until: "--"},
	}}
	for _, c := range []struct{ args, want string }{
		{"", "cargo test --workspace"},
		{"-p foo", "cargo test -p foo"},
		{"-pfoo", "cargo test -pfoo"},
		{"--package=foo", "cargo test --package=foo"},
		{"--profile release", "cargo test --workspace --profile release"},
		{"--lib", "cargo test --workspace --lib"},
		{"-- -p foo", "cargo test --workspace -- -p foo"},
		{"--manifest-path a/Cargo.toml", "cargo test --manifest-path a/Cargo.toml"},
	} {
		if got := strings.Join(m.Compose(strings.Fields(c.args)), " "); got != c.want {
			t.Errorf("%q: got %q, want %q", c.args, got, c.want)
		}
	}
}

func TestComposeTailUnlessDoubleDash(t *testing.T) {
	m := Manifest{Argv: []string{"cargo", "clippy"}, Args: "append", Default: []Default{
		{Args: []string{"--workspace"}, Unless: []string{"--workspace", "-p"}, Until: "--"},
		{Args: []string{"--", "-W", "clippy::all", "-D", "warnings"}, Unless: []string{"--"}, Tail: true},
	}}
	for _, c := range []struct{ args, want string }{
		{"", "cargo clippy --workspace -- -W clippy::all -D warnings"},
		{"--lib", "cargo clippy --workspace --lib -- -W clippy::all -D warnings"},
		{"-p foo", "cargo clippy -p foo -- -W clippy::all -D warnings"},
		{"-- -A dead_code", "cargo clippy --workspace -- -A dead_code"},
	} {
		if got := strings.Join(m.Compose(strings.Fields(c.args)), " "); got != c.want {
			t.Errorf("%q: got %q, want %q", c.args, got, c.want)
		}
	}
}

func TestComposeIgnoresArgsAndDefaultsWhenArgsIsNone(t *testing.T) {
	m := Manifest{Argv: []string{"go", "vet", "./..."}, Args: "none"}
	if got := m.Compose([]string{"-x"}); !slices.Equal(got, []string{"go", "vet", "./..."}) {
		t.Fatalf("%v", got)
	}
}

func TestUT_PreflightOptOutSkipsThePreflight(t *testing.T) {
	no := false
	for _, c := range []struct {
		name      string
		preflight *bool
		wantCalls int
	}{{"default", nil, 2}, {"opted out", &no, 1}} {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeExec{}
			cmd := vetCommand(f, Manifest{Argv: []string{"cargo", "generate-lockfile"}, Preflight: c.preflight})
			cmd.Preflight = []string{"cargo", "fetch", "--locked"}
			RunCommand(context.Background(), cmd)
			if len(f.calls) != c.wantCalls {
				t.Fatalf("%d calls, want %d: %+v", len(f.calls), c.wantCalls, f.calls)
			}
			if got := f.calls[len(f.calls)-1].argv; got[0] != "cargo" || got[1] != "generate-lockfile" {
				t.Fatalf("last call %v", got)
			}
		})
	}
}

func TestRunCommandComposesDefaults(t *testing.T) {
	f := &fakeExec{}
	cmd := vetCommand(f, Manifest{Argv: []string{"go", "test"}, Args: "append", Default: []Default{
		{Args: []string{"./..."}, Unless: []string{"@package"}, Tail: true},
	}})
	cmd.Args = []string{"./pkg"}
	RunCommand(context.Background(), cmd)
	if got := f.calls[0].argv; !slices.Equal(got, []string{"go", "test", "./pkg"}) {
		t.Fatalf("%v", got)
	}
}

// TestTheNewFieldsRoundTripAndStayOutOfTheGolden: default and preflight are
// omitted when unset, so command-atom.json and its checksum are unchanged, and
// they survive a JSON round trip when set.
func TestTheNewFieldsRoundTripAndStayOutOfTheGolden(t *testing.T) {
	plain, err := Marshal(Manifest{Schema: ManifestSchema, ID: "a:b"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "default") || strings.Contains(string(plain), "preflight") {
		t.Fatalf("unset fields were encoded: %s", plain)
	}
	no := false
	in := Manifest{Schema: ManifestSchema, ID: "a:b", Preflight: &no, Default: []Default{{Args: []string{"x"}, Unless: []string{"-p"}, Tail: true, Until: "--"}}}
	b, err := Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Manifest
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("got %+v\nwant %+v", out, in)
	}
}
