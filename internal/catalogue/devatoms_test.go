package catalogue

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/devlane"
)

// devAtoms are the dev:<lang>-<verb> command atoms: the Dev surface's verbs as
// manifests, for the in-pod executor. The argv of each is what `dagger call dev`
// runs when the caller passes no arguments.
var devAtoms = []struct {
	id, lang, surface, toolchain string
	writes                       bool
}{
	{"dev:go-build", "go", "root:go.mod", "go", true},
	{"dev:go-vet", "go", "root:go.mod", "go", false},
	{"dev:go-check", "go", "root:go.mod", "go", false},
	{"dev:go-lint", "go", "root:go.mod", "go", false},
	{"dev:go-update", "go", "root:go.mod", "go", true},
	{"dev:go-test", "go", "root:go.mod", "go", true},
	{"dev:go-fmt", "go", "root:go.mod", "go", true},
	{"dev:go-tidy", "go", "root:go.mod", "go", true},
	{"dev:go-vendor", "go", "root:go.mod", "go", true},
	{"dev:rust-fmt", "rust", "root:Cargo.toml", "rust", true},
	{"dev:rust-lock", "rust", "root:Cargo.toml", "rust", true},
	{"dev:rust-update", "rust", "root:Cargo.toml", "rust", true},
	{"dev:python-lock", "python", "root:pyproject.toml", "python", true},
	{"dev:python-update", "python", "root:pyproject.toml", "python", true},
	{"dev:python-build", "python", "root:pyproject.toml", "python", true},
	{"dev:ts-build", "ts", "root:package.json", "bun", true},
	{"dev:ts-lock", "ts", "root:package.json", "bun", true},
	{"dev:ts-update", "ts", "root:package.json", "bun", true},
	{"dev:go-typecheck", "go", "root:go.mod", "go", false},
	{"dev:ts-test", "ts", "root:package.json", "bun", true},
	{"dev:ts-fmt", "ts", "root:package.json", "bun", true},
	{"dev:ts-typecheck", "ts", "root:package.json", "bun", true},
	{"dev:ts-check", "ts", "root:package.json", "bun", true},
	{"dev:rust-vuln", "rust", "root:Cargo.toml", "rust", false},
	{"dev:go-lock-apt", "go", "any:**/packages.lock", "go", true},
	{"dev:rust-lock-apt", "rust", "any:**/packages.lock", "go", true},
	{"dev:python-lock-apt", "python", "any:**/packages.lock", "go", true},
	{"dev:ts-lock-apt", "ts", "any:**/packages.lock", "go", true},
}

// TestUT_514_TheDevAtomsKeepTheirShape: all 28 parse, sit in the dev stage with
// the Dev surface's fields, and write the tree for the go build (its -o output is delivered), the go test (it rewrites
// goldens), the go update and the six rewriting go/rust verbs, the python fmt
// and the four ts ones (a ts install lands in node_modules,
// beside the tree, so every ts verb runs on its own copy).
func TestUT_514_TheDevAtomsKeepTheirShape(t *testing.T) {
	for _, tc := range devAtoms {
		e, ok := Lookup(tc.id)
		if !ok || e.Manifest == nil {
			t.Errorf("%s: no manifest in the catalogue", tc.id)
			continue
		}
		m := *e.Manifest
		if m.Stage != "dev" || m.Lane != tc.lang || m.Group != "language" || m.Surface != tc.surface ||
			m.Toolchain != tc.toolchain || m.Args != "append" || m.PerModule || m.Judge != "exit" || m.WritesTree != tc.writes {
			t.Errorf("%s: %+v", tc.id, m)
		}
	}
	if got := len(devAtoms); got != 28 {
		t.Errorf("%d dev atoms in the table, want 28", got)
	}
}

// devRustLogic are the cargo verbs that scope to the change (internal/devrust):
// logic atoms, and the timeouts the manifests they replaced carried.
var devRustLogic = map[string]time.Duration{
	"dev:rust-test": 30 * time.Minute, "dev:rust-build": 30 * time.Minute,
	"dev:rust-check": 10 * time.Minute, "dev:rust-typecheck": 10 * time.Minute,
	"dev:rust-clippy": 10 * time.Minute, "dev:rust-lint": 10 * time.Minute,
}

// RUSTDEV-9: the six scoping cargo verbs are logic atoms in the dev stage with
// the manifests' surface, toolchain and timeout, and the scope script's python.
func TestRUSTDEV_9_TheScopingCargoVerbsAreLogicAtoms(t *testing.T) {
	for id, timeout := range devRustLogic {
		e, ok := Lookup(id)
		if !ok || e.Def == nil || e.Manifest != nil {
			t.Errorf("%s is not a logic atom: %+v", id, e)
			continue
		}
		d := e.Def
		if d.Stage != "dev" || d.Lane != checks.LaneRust || d.Group != checks.GroupLanguage || d.Surface != "root:Cargo.toml" ||
			d.Toolchain != "rust" || d.WritesTree || d.Timeout != timeout || !slices.Contains(d.Needs, "tool:python-standalone") {
			t.Errorf("%s: %+v", id, d)
		}
	}
}

// TestTheDevAtomsDoNotDriftFromDagger: for the text verbs the manifest's argv is
// devlane's own answer for no arguments, so the two surfaces cannot disagree.
func TestTheDevAtomsDoNotDriftFromDagger(t *testing.T) {
	for _, tc := range []struct {
		id, lang, verb string
	}{
		{"dev:go-build", "go", "build"}, {"dev:go-vet", "go", "vet"}, {"dev:go-test", "go", "test"},
		{"dev:rust-fmt", "rust", "fmt"},
		{"dev:rust-lock", "rust", "lock"}, {"dev:rust-update", "rust", "update"},
	} {
		var want []string
		var err error
		if tc.lang == "go" {
			want, err = devlane.GoArgv(tc.verb, nil, true)
		} else {
			want, err = devlane.RustArgv(tc.verb, nil)
		}
		if err != nil {
			t.Fatal(err)
		}
		e, ok := Lookup(tc.id)
		if !ok || !slices.Equal(e.Manifest.Compose(nil), want) {
			t.Errorf("%s: argv %v, devlane says %v", tc.id, e.Manifest.Compose(nil), want)
		}
	}
	// the Go verbs devlane does not spell as argv: fmt and tidy/vendor are
	// planGo's own (dev.go), pinned here.
	for id, want := range map[string][]string{
		"dev:go-fmt": {"go", "fmt", "./..."}, "dev:go-tidy": {"go", "mod", "tidy"}, "dev:go-vendor": {"go", "mod", "vendor"},
	} {
		if e, _ := Lookup(id); !slices.Equal(e.Manifest.Compose(nil), want) {
			t.Errorf("%s: argv %v, want %v", id, e.Manifest.Compose(nil), want)
		}
	}
}

// TestTheDevAtomsCoverEveryDevlaneVerb: each verb devlane answers to has a
// manifest, so a verb added to the Dev surface cannot go without an atom.
func TestTheDevAtomsCoverEveryDevlaneVerb(t *testing.T) {
	for lang, verbs := range map[string][]string{
		devlane.Go:   {"build", "vet", "test", "fmt", "tidy", "vendor"},
		devlane.Rust: {"check", "build", "test", "clippy", "fmt", "lock", "update"},
	} {
		for _, v := range verbs {
			if err := devlane.Applies(lang, v); err != nil {
				t.Fatal(err)
			}
			if _, ok := Lookup("dev:" + lang + "-" + v); !ok {
				t.Errorf("dev:%s-%s has no manifest", lang, v)
			}
		}
	}
}

// devNeeds, devEnv and devTimeout are what each dev atom asks of the pod beyond
// its argv; an id absent from a table has none (needs, env) or the 10m default.
var (
	devNeeds = map[string][]string{
		"dev:go-test":         {"dies", "tool:jq"}, // checks.SuiteJqNeeds: a suite that execs jq (daedalus tools/bg)
		"dev:go-lint":         {"tool:staticcheck"},
		"dev:rust-vuln":       {"tool:cargo-audit"},
		"dev:python-lock":     {"tool:uv"},
		"dev:python-update":   {"tool:uv"},
		"dev:python-build":    {"tool:uv"},
		"dev:ts-build":        {"tool:node", "tool:curl"},
		"dev:ts-lock":         {"tool:node", "tool:curl"},
		"dev:ts-update":       {"tool:node", "tool:curl"},
		"dev:ts-test":         {"tool:node", "tool:curl"},
		"dev:ts-fmt":          {"tool:node", "tool:curl"},
		"dev:ts-typecheck":    {"tool:node", "tool:curl"},
		"dev:ts-check":        {"tool:node", "tool:curl"},
		"dev:go-lock-apt":     {"svid", "tool:deblock"},
		"dev:rust-lock-apt":   {"svid", "tool:deblock"},
		"dev:python-lock-apt": {"svid", "tool:deblock"},
		"dev:ts-lock-apt":     {"svid", "tool:deblock"},
	}
	devEnv = map[string]map[string]string{
		"dev:go-vendor": {"GOFLAGS": "-mod=mod"},
		// The suite runs as go:test runs it (atoms.suiteScrub).
		"dev:go-test": {"ATOM_WORK": "", "ATOM_OUT": "", "ATOM_TREE": ""},
	}
	devLong = []string{"dev:go-test", "dev:python-build", "dev:ts-build", "dev:ts-test", "dev:ts-typecheck", "dev:ts-check"}
)

// TestTheDevAtomsTimeoutsEnvAndNeeds: the long verbs get 30m, go test mounts
// dies as Dagger's dev test does, go vendor ignores the vendor tree it replaces,
// and the read-only python verbs keep every file they would write off /src.
func TestTheDevAtomsTimeoutsEnvAndNeeds(t *testing.T) {
	for _, tc := range devAtoms {
		m := manifestOf(t, tc.id)
		want := "10m"
		if slices.Contains(devLong, tc.id) {
			want = "30m"
		}
		if m.Timeout != want {
			t.Errorf("%s: timeout %s, want %s", tc.id, m.Timeout, want)
		}
		if !slices.Equal(m.Needs, devNeeds[tc.id]) {
			t.Errorf("%s: needs %v, want %v", tc.id, m.Needs, devNeeds[tc.id])
		}
		if wantEnv := devEnv[tc.id]; !mapsEqual(m.Env, wantEnv) {
			t.Errorf("%s: env %v, want %v", tc.id, m.Env, wantEnv)
		}
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// manifestOf is the parsed manifest of a catalogue id.
func manifestOf(t *testing.T, id string) atomkit.Manifest {
	t.Helper()
	e, ok := Lookup(id)
	if !ok || e.Manifest == nil {
		t.Fatalf("%s: no manifest", id)
	}
	return *e.Manifest
}

// todaysArgv is each dev atom's argv with no caller arguments as the landed
// manifests carried it before the argument rules (foundry-tools#503): the
// proofs that ran on that shape depend on it staying byte-identical.
var todaysArgv = map[string][]string{
	"dev:go-build":     {"go", "build", "./..."},
	"dev:go-vet":       {"go", "vet", "./..."},
	"dev:go-check":     {"go", "vet", "./..."},
	"dev:go-lint":      {"staticcheck", "-checks", gateStaticcheckChecks, "./..."},
	"dev:go-update":    {"go", "get", "-u", "./..."},
	"dev:go-test":      {"go", "test", "-race", "./..."},
	"dev:go-fmt":       {"go", "fmt", "./..."},
	"dev:go-tidy":      {"go", "mod", "tidy"},
	"dev:go-vendor":    {"go", "mod", "vendor"},
	"dev:rust-fmt":     {"cargo", "fmt", "--all"},
	"dev:rust-lock":    {"cargo", "generate-lockfile"},
	"dev:rust-update":  {"cargo", "update"},
	"dev:go-typecheck": {"go", "vet", "./..."},
}

// TestTheDevAtomsKeepTodaysArgvWithNoArguments: the byte-identical guarantee.
func TestTheDevAtomsKeepTodaysArgvWithNoArguments(t *testing.T) {
	// 13: the six cargo verbs that scope to the change are logic atoms now
	// (internal/devrust); with no change to scope they keep this argv, which
	// their own tests hold.
	if len(todaysArgv) != 13 {
		t.Fatalf("%d rows, want 13", len(todaysArgv))
	}
	for id, want := range todaysArgv {
		m := manifestOf(t, id)
		for _, args := range [][]string{nil, {}} {
			if got := m.Compose(args); !slices.Equal(got, want) {
				t.Errorf("%s: argv %v, want %v", id, got, want)
			}
		}
	}
}

// TestTheDevAtomsComposeAsDaggerDoes: with caller arguments the manifests give
// devlane's own argv for every verb devlane spells (GoArgv, RustArgv).
func TestTheDevAtomsComposeAsDaggerDoes(t *testing.T) {
	goCalls := map[string][][]string{
		"build": {{"./pkg"}, {"-v"}, {"-v", "./a/..."}, {"./a", "./b"}, {"."}, {"example.com/x/y"}, {"-tags", "x"}, {"-tags", "x", "./p"}},
		"vet":   {{"./pkg"}, {"-v"}, {"-v", "./a/..."}, {"./a", "./b"}},
		"test": {{"./pkg"}, {"-run", "TestCanon"}, {"-run", "TestX", "-count=1"}, {"-race=false"}, {"-race=false", "./p"}, {"-race"},
			{"-args", "-v"}, {"./p", "-args", "./x"}, {"-run", "TestX", "./p"}, {"-v", "./..."}, {"-timeout", "5m", "./p"}},
	}
	for verb, calls := range goCalls {
		m := manifestOf(t, "dev:go-"+verb)
		for _, args := range calls {
			want, err := devlane.GoArgv(verb, args, true)
			if err != nil {
				t.Fatal(err)
			}
			if got := m.Compose(args); !slices.Equal(got, want) {
				t.Errorf("go %s %v: manifest %v, devlane %v", verb, args, got, want)
			}
		}
	}
	// cargo check, build, test and clippy are logic atoms (internal/devrust) that
	// call devlane.RustArgv themselves, so they cannot drift from it. The verbs
	// with no default to replace append, as devlane does.
	for _, tc := range []struct {
		verb string
		args []string
		want []string
	}{
		{"fmt", []string{"--check"}, []string{"cargo", "fmt", "--all", "--check"}},
		{"lock", []string{"--offline"}, []string{"cargo", "generate-lockfile", "--offline"}},
		{"update", []string{"-p", "foo"}, []string{"cargo", "update", "-p", "foo"}},
	} {
		want, err := devlane.RustArgv(tc.verb, tc.args)
		if err != nil || !slices.Equal(want, tc.want) {
			t.Fatalf("devlane %s %v: %v, %v", tc.verb, tc.args, want, err)
		}
		if got := manifestOf(t, "dev:rust-"+tc.verb).Compose(tc.args); !slices.Equal(got, tc.want) {
			t.Errorf("cargo %s %v: %v, want %v", tc.verb, tc.args, got, tc.want)
		}
	}
}

// TestTheDevAtomsDocumentedArguments: the worked examples of the change.
func TestTheDevAtomsDocumentedArguments(t *testing.T) {
	for _, tc := range []struct {
		id   string
		args []string
		want []string
	}{
		{"dev:go-test", []string{"./pkg"}, []string{"go", "test", "-race", "./pkg"}},
		{"dev:go-test", []string{"-run", "TestCanon"}, []string{"go", "test", "-race", "-run", "TestCanon", "./..."}},
		{"dev:go-check", []string{"./pkg"}, []string{"go", "vet", "./pkg"}},
		{"dev:go-lint", []string{"./pkg"}, []string{"staticcheck", "-checks", gateStaticcheckChecks, "./pkg"}},
		{"dev:go-update", []string{"golang.org/x/net@v0.40.0"}, []string{"go", "get", "golang.org/x/net@v0.40.0"}},
		{"dev:go-update", []string{"-t"}, []string{"go", "get", "-u", "-t", "./..."}},
		{"dev:go-update", []string{"-u=patch"}, []string{"go", "get", "-u=patch", "./..."}},
		{"dev:go-fmt", []string{"./pkg"}, []string{"go", "fmt", "./pkg"}},
		{"dev:go-tidy", []string{"-v"}, []string{"go", "mod", "tidy", "-v"}},
		{"dev:go-vendor", []string{"-v"}, []string{"go", "mod", "vendor", "-v"}},
	} {
		if got := manifestOf(t, tc.id).Compose(tc.args); !slices.Equal(got, tc.want) {
			t.Errorf("%s %v: %v, want %v", tc.id, tc.args, got, tc.want)
		}
	}
}

// TestOnlyTheLockfileVerbsSkipThePreflight: dev:rust-lock, dev:rust-update and
// dev:go-update exist for a stale or missing lockfile, which the toolchain's
// preflight (`cargo fetch --locked`; `go mod download` over a go.mod that go get
// is about to change) would refuse before the verb could mend it.
func TestOnlyTheLockfileVerbsSkipThePreflight(t *testing.T) {
	for _, tc := range devAtoms {
		want := !slices.Contains([]string{"dev:rust-lock", "dev:rust-update", "dev:go-update", "dev:ts-lock", "dev:ts-update", "dev:go-lock-apt", "dev:rust-lock-apt", "dev:python-lock-apt", "dev:ts-lock-apt"}, tc.id)
		if got := manifestOf(t, tc.id).RunsPreflight(); got != want {
			t.Errorf("%s: RunsPreflight %v, want %v", tc.id, got, want)
		}
	}
}

// pytsArgv is each python and ts dev atom's argv with no caller arguments.
var pytsArgv = map[string][]string{
	"dev:python-lock":   {"uv", "lock", "--python", "python3"},
	"dev:python-update": {"uv", "lock", "--python", "python3", "--upgrade"},
	"dev:python-build":  {"uv", "build", "--python", "python3"},
	"dev:ts-build":      {"bun", "run", "build"},
	"dev:ts-lock":       {"bun", "install", "--lockfile-only"},
	"dev:ts-update":     {"bun", "update"},
	"dev:ts-test":       {"bun", "run", "test"},
	"dev:ts-fmt":        {"bun", "run", "format"},
	"dev:ts-typecheck":  {"bun", "run", "typecheck"},
	"dev:ts-check":      {"bun", "run", "typecheck"},
}

// TestThePythonAndTSDevAtomsKeepTheirArgv: what the local loop runs with no
// arguments, and nothing else.
func TestThePythonAndTSDevAtomsKeepTheirArgv(t *testing.T) {
	if len(pytsArgv) != 10 {
		t.Fatalf("%d rows, want 10", len(pytsArgv))
	}
	for id, want := range pytsArgv {
		m := manifestOf(t, id)
		for _, args := range [][]string{nil, {}} {
			if got := m.Compose(args); !slices.Equal(got, want) {
				t.Errorf("%s: argv %v, want %v", id, got, want)
			}
		}
	}
}

// TestThePythonAndTSDevAtomsCoverTheLocalLoop: test, lint, fmt and typecheck,
// for both languages, are the minimum set.
func TestThePythonAndTSDevAtomsCoverTheLocalLoop(t *testing.T) {
	for _, lang := range []string{"python", "ts"} {
		for _, verb := range []string{"test", "lint", "fmt", "typecheck"} {
			if _, ok := Lookup("dev:" + lang + "-" + verb); !ok {
				t.Errorf("dev:%s-%s has no manifest", lang, verb)
			}
		}
	}
}

// TestThePythonAndTSDevAtomsComposeWithCallerArguments: a caller's test options
// replace -q, a caller's target replaces mypy's src and tests, and everything
// else is appended after the head.
func TestThePythonAndTSDevAtomsComposeWithCallerArguments(t *testing.T) {
	for _, tc := range []struct {
		id   string
		args []string
		want []string
	}{
		{"dev:python-lock", []string{"--check"}, []string{"uv", "lock", "--python", "python3", "--check"}},
		{"dev:python-update", []string{"-P", "requests"}, []string{"uv", "lock", "--python", "python3", "-P", "requests"}},
		{"dev:python-update", []string{"--upgrade-package", "requests"}, []string{"uv", "lock", "--python", "python3", "--upgrade-package", "requests"}},
		{"dev:python-update", []string{"-U"}, []string{"uv", "lock", "--python", "python3", "-U"}},
		{"dev:ts-test", []string{"--filter=web"}, []string{"bun", "run", "test", "--filter=web"}},
		{"dev:ts-fmt", []string{"--check"}, []string{"bun", "run", "format", "--check"}},
	} {
		if got := manifestOf(t, tc.id).Compose(tc.args); !slices.Equal(got, tc.want) {
			t.Errorf("%s %v: %v, want %v", tc.id, tc.args, got, tc.want)
		}
	}
}

// gateTwins pair each dev logic atom that lays the fleet's ruleset with the gate
// atom that runs the same tool.
var gateTwins = []struct{ dev, gate string }{
	{"dev:python-lint", "python:ruff-check"},
	{"dev:python-fmt", "python:ruff-format"},
	{"dev:ts-lint", "ts:bun-gate"},
}

// absorb-alien-island: the dev lint and fmt verbs lay the fleet's ruleset as
// their gate twins do, which a command manifest (one fixed argv, no step that
// lays a file) cannot. Each is a logic atom that keeps its twin's toolchain,
// lane and needs, so both resolve the tool from the same tools.toml entry.
func TestTheDevLintVerbsAreLogicAtomsOnTheirGateTwinsResolution(t *testing.T) {
	tools := landedToolsFile(t)
	for _, tc := range gateTwins {
		dev, ok := Lookup(tc.dev)
		if !ok || dev.Def == nil || dev.Manifest != nil {
			t.Fatalf("%s is not a logic atom: %+v", tc.dev, dev)
		}
		twin, ok := Lookup(tc.gate)
		if !ok || twin.Def == nil {
			t.Fatalf("%s: no logic atom in the catalogue", tc.gate)
		}
		d, g := dev.Def, twin.Def
		if d.Stage != "dev" || d.Toolchain != g.Toolchain || d.Lane != g.Lane || d.Surface != g.Surface && tc.gate != "python:ruff-check" && tc.gate != "python:ruff-format" {
			t.Errorf("%s: %s/%s/%s, gate twin %s has %s/%s/%s", tc.dev, d.Stage, d.Toolchain, d.Lane, tc.gate, g.Stage, g.Toolchain, g.Lane)
		}
		for _, need := range g.Needs {
			if !slices.Contains(d.Needs, need) {
				t.Errorf("%s: needs %v, but the gate twin %s needs %s", tc.dev, d.Needs, tc.gate, need)
			}
		}
		for _, need := range d.Needs {
			if name, isTool := strings.CutPrefix(need, "tool:"); isTool {
				if _, ok := tools.Tools[name]; !ok {
					t.Errorf("%s needs %s, which tools.toml does not pin", tc.dev, need)
				}
			}
		}
	}
}

// DEVSCOPE: dev:python-test is a logic atom on its gate twin's surface, so a
// tree with python and no pyproject.toml (paneless) runs it as its gate runs
// python:pytest; a manifest could only spell the project form.
func TestDevPythonTestRunsWhereItsGateTwinRuns(t *testing.T) {
	dev, ok := Lookup("dev:python-test")
	if !ok || dev.Def == nil || dev.Manifest != nil {
		t.Fatalf("dev:python-test is not a logic atom: %+v", dev)
	}
	twin, _ := Lookup("python:pytest")
	if dev.Def.Surface != twin.Def.Surface || dev.Def.Toolchain != twin.Def.Toolchain || !slices.Equal(dev.Def.Needs, twin.Def.Needs) {
		t.Errorf("dev %s/%s %v, twin %s/%s %v", dev.Def.Surface, dev.Def.Toolchain, dev.Def.Needs, twin.Def.Surface, twin.Def.Toolchain, twin.Def.Needs)
	}
}

// dev:python-typecheck and its twin dev:python-check are logic atoms: they run
// mypy, then pyright where the tree configures it, which one manifest command
// cannot. Both keep python:mypy's toolchain, lane, surface and needs, and are
// the same atom under two names.
func TestDevPythonTypecheckIsALogicAtom(t *testing.T) {
	twin, _ := Lookup("python:mypy")
	var defs []*atomkit.Def
	for _, id := range []string{"dev:python-typecheck", "dev:python-check"} {
		e, ok := Lookup(id)
		if !ok || e.Def == nil || e.Manifest != nil {
			t.Fatalf("%s is not a logic atom: %+v", id, e)
		}
		d := e.Def
		if d.Toolchain != twin.Def.Toolchain || d.Lane != twin.Def.Lane || d.Surface != twin.Def.Surface {
			t.Errorf("%s: %s/%s/%s, python:mypy has %s/%s/%s", id, d.Toolchain, d.Lane, d.Surface, twin.Def.Toolchain, twin.Def.Lane, twin.Def.Surface)
		}
		for _, need := range twin.Def.Needs {
			if !slices.Contains(d.Needs, need) {
				t.Errorf("%s: needs %v, but python:mypy needs %s", id, d.Needs, need)
			}
		}
		defs = append(defs, d)
	}
	tc, ck := defs[0], defs[1]
	if !slices.Equal(tc.Needs, ck.Needs) || tc.Timeout != ck.Timeout || tc.WritesTree != ck.WritesTree || !strings.Contains(ck.Desc, "typecheck") {
		t.Errorf("dev:python-check %+v is not dev:python-typecheck %+v", ck, tc)
	}
}

// landedToolsFile is the module's tools.toml, parsed.
func landedToolsFile(t *testing.T) atomkit.ToolsFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "tools.toml"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := atomkit.ParseTools(raw)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// TestThePythonDevAtomsShareTheirGateTwinsResolution (R36: no atom exists
// twice): a dev verb with a gate twin runs on the twin's toolchain, from the
// twin's tool pins (tools.toml, by the same `tool:` need), and spells the
// twin's command where the twin is a command the manifest can spell.
func TestThePythonDevAtomsShareTheirGateTwinsResolution(t *testing.T) {
	tools := landedToolsFile(t)
	// the pin itself: ruff is the version the gate atoms run.
	if pin := tools.Tools["ruff"]; pin.Kind != "uv" || pin.Version != checks.RuffVersion {
		t.Errorf("tools.toml pins ruff %+v, the gate atoms run %s", pin, checks.RuffVersion)
	}
}

// TestTheTSDevAtomsShareTheBunGatesResolution: ts:bun-gate runs the repo's own
// `gate` script on the bun toolchain with node first on PATH, after a frozen
// install. The dev verbs run the repo's other scripts the same way: same
// toolchain, same node, and the install is the toolchain's preflight, which is
// the gate's own `bun install --frozen-lockfile`.
func TestTheTSDevAtomsShareTheBunGatesResolution(t *testing.T) {
	e, ok := Lookup("ts:bun-gate")
	if !ok || e.Def == nil {
		t.Fatal("ts:bun-gate is not a logic atom in the catalogue")
	}
	tools := landedToolsFile(t)
	if got := tools.Preflights["bun"]; !slices.Equal(got, []string{"bun", "install", "--frozen-lockfile"}) {
		t.Errorf("toolchain bun preflight %v, want the gate's frozen install", got)
	}
	for _, verb := range []string{"test", "fmt", "typecheck", "check"} {
		m := manifestOf(t, "dev:ts-"+verb)
		if m.Toolchain != e.Def.Toolchain || string(e.Def.Lane) != m.Lane || !slices.Equal(m.Needs, e.Def.Needs) || m.Surface != e.Def.Surface {
			t.Errorf("dev:ts-%s: %s/%s %v %s, ts:bun-gate has %s/%s %v %s", verb, m.Toolchain, m.Lane, m.Needs, m.Surface,
				e.Def.Toolchain, e.Def.Lane, e.Def.Needs, e.Def.Surface)
		}
		if !m.RunsPreflight() || !m.WritesTree {
			t.Errorf("dev:ts-%s: the install needs the preflight and a tree it may write", verb)
		}
	}
}

// TestBunAuditKeepsItsOwnCopyWithoutTheInstall: the bun preflight is the dev
// verbs' install; ts:bun-audit reads the lockfile and installs nothing.
func TestBunAuditKeepsItsOwnCopyWithoutTheInstall(t *testing.T) {
	if manifestOf(t, "ts:bun-audit").RunsPreflight() {
		t.Error("ts:bun-audit runs the bun preflight; it audits the lockfile and must not install")
	}
}

// TestTheReadOnlyPythonDevAtomsWriteOffTheTree: /src is read-only to an atom
// that does not write the tree, so every file uv, pytest, mypy and ruff would
// put beside the sources is pointed at /tmp.
func TestTheReadOnlyPythonDevAtomsWriteOffTheTree(t *testing.T) {
	for _, tc := range devAtoms {
		if tc.lang != "python" || tc.writes {
			continue
		}
		m := manifestOf(t, tc.id)
		if len(m.Env) == 0 {
			t.Errorf("%s: no env", tc.id)
		}
		for k, v := range m.Env {
			if k != "PYTHONDONTWRITEBYTECODE" && !strings.Contains(v, "/tmp/") {
				t.Errorf("%s: %s=%s is not under /tmp", tc.id, k, v)
			}
		}
	}
}

// gateStaticcheckChecks is the -checks value go:staticcheck runs.
const gateStaticcheckChecks = "all,-ST1000,-ST1003,-ST1016,-ST1020,-ST1021,-ST1022,-ST1023"

// TestTheCheckLintAndUpdateVerbsAreTheirTwins: dev:go-check is go:vet's command
// (the commit stage's compile-bearing go atom) and dev:go-vet's twin; dev:go-lint
// is go:staticcheck's argv and need, flag for flag; dev:rust-lint is dev:rust-clippy
// under the other name the refusal shim routes; dev:go-test writes the tree
// (it rewrites goldens) and dev:go-update, which mends go.mod, skips the preflight.
func TestTheCheckLintAndUpdateVerbsAreTheirTwins(t *testing.T) {
	vet, check := manifestOf(t, "dev:go-vet"), manifestOf(t, "dev:go-check")
	if !slices.Equal(vet.Compose(nil), check.Compose(nil)) || vet.Toolchain != check.Toolchain || !strings.Contains(check.Desc, "go:vet") {
		t.Errorf("dev:go-check %v (%s) is not dev:go-vet %v", check.Compose(nil), check.Desc, vet.Compose(nil))
	}
	e, ok := Lookup("go:staticcheck")
	if !ok || e.Manifest == nil {
		t.Fatal("go:staticcheck has no manifest")
	}
	lint := manifestOf(t, "dev:go-lint")
	if !slices.Equal(lint.Compose(nil), e.Manifest.Argv) || !slices.Equal(lint.Needs, e.Manifest.Needs) || lint.Toolchain != e.Manifest.Toolchain {
		t.Errorf("dev:go-lint %v %v, go:staticcheck %v %v", lint.Compose(nil), lint.Needs, e.Manifest.Argv, e.Manifest.Needs)
	}
	clippy, _ := Lookup("dev:rust-clippy")
	rl, _ := Lookup("dev:rust-lint")
	if clippy.Def == nil || rl.Def == nil || !slices.Equal(clippy.Def.Needs, rl.Def.Needs) || clippy.Def.Timeout != rl.Def.Timeout {
		t.Errorf("dev:rust-lint %+v is not dev:rust-clippy %+v", rl.Def, clippy.Def)
	}
	for _, id := range []string{"dev:go-test", "dev:go-update"} {
		if !manifestOf(t, id).WritesTree {
			t.Errorf("%s does not write the tree", id)
		}
	}
}

// TestEveryShimRouteHasAnAtom: the rob02 refusal shims send go and cargo
// commands to ask dev verbs; each verb must exist for its language.
func TestEveryShimRouteHasAnAtom(t *testing.T) {
	for lang, verbs := range map[string][]string{
		"go":     {"build", "test", "vet", "update", "tidy", "check", "lint"},
		"rust":   {"build", "check", "test", "clippy", "lint"},
		"python": {"check", "lint", "test", "typecheck", "fmt"},
		"ts":     {"check", "lint", "test", "typecheck", "fmt"},
	} {
		for _, v := range verbs {
			if _, ok := Lookup("dev:" + lang + "-" + v); !ok {
				t.Errorf("dev:%s-%s has no manifest", lang, v)
			}
		}
	}
}

// TestTheTSCheckVerbIsItsTypecheckTwin: `check` is the compile-bearing verb of a
// language (dev:go-check is go vet, dev:rust-check is cargo check); for ts that
// is the repo's typecheck script, so it is its typecheck verb under the other
// name the refusal shim routes, down to the pod asks (needs, timeout, tree
// writes). Python's pair are logic atoms over devpython.Typecheck
// (TestDevPythonTypecheckIsALogicAtom).
func TestTheTSCheckVerbIsItsTypecheckTwin(t *testing.T) {
	for _, lang := range []string{"ts"} {
		tc, ck := manifestOf(t, "dev:"+lang+"-typecheck"), manifestOf(t, "dev:"+lang+"-check")
		if !slices.Equal(tc.Compose(nil), ck.Compose(nil)) || !slices.Equal(tc.Needs, ck.Needs) || tc.Toolchain != ck.Toolchain ||
			tc.Timeout != ck.Timeout || tc.WritesTree != ck.WritesTree || tc.Surface != ck.Surface || tc.Lane != ck.Lane {
			t.Errorf("dev:%s-check %+v is not dev:%s-typecheck %+v", lang, ck, lang, tc)
		}
		if !strings.Contains(ck.Desc, "typecheck") {
			t.Errorf("dev:%s-check says %q, which does not name its twin", lang, ck.Desc)
		}
	}
}

// TestTheRustVulnVerbIsTheGatesCargoAudit: dev:rust-vuln runs what rust:cargo-audit
// runs: the same argv, tool, toolchain, surface and judge, so `just vuln` and the
// push stage cannot disagree. It is a read-only verb that takes the caller's
// arguments (`--deny warnings`).
func TestTheRustVulnVerbIsTheGatesCargoAudit(t *testing.T) {
	e, ok := Lookup("rust:cargo-audit")
	if !ok || e.Manifest == nil {
		t.Fatal("rust:cargo-audit has no manifest")
	}
	dev := manifestOf(t, "dev:rust-vuln")
	if !slices.Equal(dev.Argv, e.Manifest.Argv) || !slices.Equal(dev.Needs, e.Manifest.Needs) || dev.Toolchain != e.Manifest.Toolchain ||
		dev.Surface != e.Manifest.Surface || dev.Judge != e.Manifest.Judge || dev.WritesTree || dev.Stage != "dev" || dev.Args != "append" {
		t.Errorf("dev:rust-vuln %+v is not rust:cargo-audit %+v", dev, *e.Manifest)
	}
	if got := dev.Compose([]string{"--deny", "warnings"}); !slices.Equal(got, []string{"cargo", "audit", "--deny", "warnings"}) {
		t.Errorf("composed %v", got)
	}
}

// A build's `-o <file>` is delivered only when the verb runs on its own copy of
// the tree: the dev lane hands back what a writing atom changed, and an atom
// that writes nothing answers "not delivered: no atom writes the tree" beside a
// green build (narcissus `build -o ./narc.bin ./cmd/narc`). So every verb that
// can leave a file the caller asked for writes the tree: go and python and ts
// builds, and `-o` composes through to go build untouched.
func TestTheBuildVerbsDeliverTheirOutput(t *testing.T) {
	for _, id := range []string{"dev:go-build", "dev:python-build", "dev:ts-build"} {
		if !manifestOf(t, id).WritesTree {
			t.Errorf("%s does not write the tree, so a file it builds is never delivered", id)
		}
	}
	got := manifestOf(t, "dev:go-build").Compose([]string{"-o", "./narc.bin", "./cmd/narc"})
	if !slices.Equal(got, []string{"go", "build", "-o", "./narc.bin", "./cmd/narc"}) {
		t.Errorf("composed %v", got)
	}
}
