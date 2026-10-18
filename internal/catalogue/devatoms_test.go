package catalogue

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
	{"dev:go-build", "go", "root:go.mod", "go", false},
	{"dev:go-vet", "go", "root:go.mod", "go", false},
	{"dev:go-test", "go", "root:go.mod", "go", false},
	{"dev:go-fmt", "go", "root:go.mod", "go", true},
	{"dev:go-tidy", "go", "root:go.mod", "go", true},
	{"dev:go-vendor", "go", "root:go.mod", "go", true},
	{"dev:rust-check", "rust", "root:Cargo.toml", "rust", false},
	{"dev:rust-build", "rust", "root:Cargo.toml", "rust", false},
	{"dev:rust-test", "rust", "root:Cargo.toml", "rust", false},
	{"dev:rust-clippy", "rust", "root:Cargo.toml", "rust", false},
	{"dev:rust-fmt", "rust", "root:Cargo.toml", "rust", true},
	{"dev:rust-lock", "rust", "root:Cargo.toml", "rust", true},
	{"dev:rust-update", "rust", "root:Cargo.toml", "rust", true},
	{"dev:python-test", "python", "root:pyproject.toml", "python", false},
	{"dev:python-lint", "python", "any:**/*.py", "python", false},
	{"dev:python-fmt", "python", "any:**/*.py", "python", true},
	{"dev:python-typecheck", "python", "root:pyproject.toml", "python", false},
	{"dev:ts-test", "ts", "root:package.json", "bun", true},
	{"dev:ts-lint", "ts", "root:package.json", "bun", true},
	{"dev:ts-fmt", "ts", "root:package.json", "bun", true},
	{"dev:ts-typecheck", "ts", "root:package.json", "bun", true},
}

// TestUT_514_TheDevAtomsKeepTheirShape: all 21 parse, sit in the dev stage with
// the Dev surface's fields, and write the tree for the six rewriting go/rust
// verbs, the two python and the four ts ones (a ts install lands in node_modules,
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
	if got := len(devAtoms); got != 21 {
		t.Errorf("%d dev atoms in the table, want 21", got)
	}
}

// TestTheDevAtomsDoNotDriftFromDagger: for the text verbs the manifest's argv is
// devlane's own answer for no arguments, so the two surfaces cannot disagree.
func TestTheDevAtomsDoNotDriftFromDagger(t *testing.T) {
	for _, tc := range []struct {
		id, lang, verb string
	}{
		{"dev:go-build", "go", "build"}, {"dev:go-vet", "go", "vet"}, {"dev:go-test", "go", "test"},
		{"dev:rust-check", "rust", "check"}, {"dev:rust-build", "rust", "build"}, {"dev:rust-test", "rust", "test"},
		{"dev:rust-clippy", "rust", "clippy"}, {"dev:rust-fmt", "rust", "fmt"},
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
		"dev:go-test":          {"dies"},
		"dev:python-test":      {"tool:git", "tool:uv", "tool:jq", "tool:curl"},
		"dev:python-lint":      {"tool:ruff"},
		"dev:python-fmt":       {"tool:ruff"},
		"dev:python-typecheck": {"tool:uv"},
		"dev:ts-test":          {"tool:node", "tool:curl"},
		"dev:ts-lint":          {"tool:node", "tool:curl"},
		"dev:ts-fmt":           {"tool:node", "tool:curl"},
		"dev:ts-typecheck":     {"tool:node", "tool:curl"},
	}
	devEnv = map[string]map[string]string{
		"dev:go-vendor": {"GOFLAGS": "-mod=mod"},
		"dev:python-test": {
			"PYTHONDONTWRITEBYTECODE": "1",
			"PYTEST_ADDOPTS":          "-o cache_dir=/tmp/dev-python-test/pytest-cache",
			"UV_PROJECT_ENVIRONMENT":  "/tmp/dev-python-test/venv",
		},
		"dev:python-lint": {"RUFF_CACHE_DIR": "/tmp/dev-python-lint/ruff-cache"},
		"dev:python-typecheck": {
			"MYPY_CACHE_DIR":          "/tmp/dev-python-typecheck/mypy-cache",
			"PYTHONDONTWRITEBYTECODE": "1",
			"UV_PROJECT_ENVIRONMENT":  "/tmp/dev-python-typecheck/venv",
		},
	}
	devLong = []string{"dev:go-test", "dev:rust-build", "dev:rust-test", "dev:python-test", "dev:python-typecheck", "dev:ts-test", "dev:ts-lint", "dev:ts-typecheck"}
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
	"dev:go-build":    {"go", "build", "./..."},
	"dev:go-vet":      {"go", "vet", "./..."},
	"dev:go-test":     {"go", "test", "-race", "./..."},
	"dev:go-fmt":      {"go", "fmt", "./..."},
	"dev:go-tidy":     {"go", "mod", "tidy"},
	"dev:go-vendor":   {"go", "mod", "vendor"},
	"dev:rust-check":  {"cargo", "check", "--workspace"},
	"dev:rust-build":  {"cargo", "build", "--workspace"},
	"dev:rust-test":   {"cargo", "test", "--workspace"},
	"dev:rust-clippy": {"cargo", "clippy", "--workspace", "--all-targets", "--", "-W", "clippy::all", "-D", "warnings"},
	"dev:rust-fmt":    {"cargo", "fmt", "--all"},
	"dev:rust-lock":   {"cargo", "generate-lockfile"},
	"dev:rust-update": {"cargo", "update"},
}

// TestTheDevAtomsKeepTodaysArgvWithNoArguments: the byte-identical guarantee.
func TestTheDevAtomsKeepTodaysArgvWithNoArguments(t *testing.T) {
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
	rustCalls := map[string][][]string{
		"check": {{"-p", "foo"}, {"--lib"}, {"--package=foo"}, {"-pfoo"}, {"--", "-A", "x"}, {"--workspace", "--exclude", "x"}, {"--manifest-path", "a/Cargo.toml"}, {"--profile", "release"}, {"foo_test"}},
		"clippy": {{"-p", "foo"}, {"--lib"}, {"--", "-A", "dead_code"}, {"--no-deps"}, {"--tests", "-p", "foo"}, {"--all-targets"},
			{"--manifest-path", "a/Cargo.toml"}, {"-p", "foo", "--", "-A", "x"}, {"--bin", "x"}, {"--examples"}, {"--workspace", "--"}},
	}
	rustCalls["build"], rustCalls["test"] = rustCalls["check"], rustCalls["check"]
	for verb, calls := range rustCalls {
		m := manifestOf(t, "dev:rust-"+verb)
		for _, args := range calls {
			want, err := devlane.RustArgv(verb, args)
			if err != nil {
				t.Fatal(err)
			}
			if got := m.Compose(args); !slices.Equal(got, want) {
				t.Errorf("cargo %s %v: manifest %v, devlane %v", verb, args, got, want)
			}
		}
	}
	// the verbs with no default to replace append, as devlane does.
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
		{"dev:rust-test", []string{"-p", "foo"}, []string{"cargo", "test", "-p", "foo"}},
		{"dev:rust-clippy", []string{"-p", "foo"}, []string{"cargo", "clippy", "--all-targets", "-p", "foo", "--", "-W", "clippy::all", "-D", "warnings"}},
		{"dev:rust-clippy", []string{"--lib"}, []string{"cargo", "clippy", "--workspace", "--lib", "--", "-W", "clippy::all", "-D", "warnings"}},
		{"dev:rust-clippy", []string{"--", "-A", "dead_code"}, []string{"cargo", "clippy", "--workspace", "--all-targets", "--", "-A", "dead_code"}},
		{"dev:go-fmt", []string{"./pkg"}, []string{"go", "fmt", "./pkg"}},
		{"dev:go-tidy", []string{"-v"}, []string{"go", "mod", "tidy", "-v"}},
		{"dev:go-vendor", []string{"-v"}, []string{"go", "mod", "vendor", "-v"}},
	} {
		if got := manifestOf(t, tc.id).Compose(tc.args); !slices.Equal(got, tc.want) {
			t.Errorf("%s %v: %v, want %v", tc.id, tc.args, got, tc.want)
		}
	}
}

// TestOnlyTheLockfileVerbsSkipThePreflight: dev:rust-lock and dev:rust-update
// exist for a stale or missing Cargo.lock, which `cargo fetch --locked` refuses.
func TestOnlyTheLockfileVerbsSkipThePreflight(t *testing.T) {
	for _, tc := range devAtoms {
		want := tc.id != "dev:rust-lock" && tc.id != "dev:rust-update"
		if got := manifestOf(t, tc.id).RunsPreflight(); got != want {
			t.Errorf("%s: RunsPreflight %v, want %v", tc.id, got, want)
		}
	}
}

// pytsArgv is each python and ts dev atom's argv with no caller arguments.
var pytsArgv = map[string][]string{
	"dev:python-test":      {"uv", "run", "--python", "python3", "--all-extras", "pytest", "-q"},
	"dev:python-lint":      {"ruff", "check"},
	"dev:python-fmt":       {"ruff", "format"},
	"dev:python-typecheck": {"uv", "run", "--python", "python3", "--all-extras", "mypy", "src", "tests"},
	"dev:ts-test":          {"bun", "run", "test"},
	"dev:ts-lint":          {"bun", "run", "lint"},
	"dev:ts-fmt":           {"bun", "run", "format"},
	"dev:ts-typecheck":     {"bun", "run", "typecheck"},
}

// TestThePythonAndTSDevAtomsKeepTheirArgv: what the local loop runs with no
// arguments, and nothing else.
func TestThePythonAndTSDevAtomsKeepTheirArgv(t *testing.T) {
	if len(pytsArgv) != 8 {
		t.Fatalf("%d rows, want 8", len(pytsArgv))
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
		{"dev:python-test", []string{"tests/test_a.py"}, []string{"uv", "run", "--python", "python3", "--all-extras", "pytest", "-q", "tests/test_a.py"}},
		{"dev:python-test", []string{"-k", "canon", "-x"}, []string{"uv", "run", "--python", "python3", "--all-extras", "pytest", "-q", "-k", "canon", "-x"}},
		{"dev:python-test", []string{"-v"}, []string{"uv", "run", "--python", "python3", "--all-extras", "pytest", "-v"}},
		{"dev:python-test", []string{"-vv", "tests/"}, []string{"uv", "run", "--python", "python3", "--all-extras", "pytest", "-vv", "tests/"}},
		{"dev:python-test", []string{"--quiet"}, []string{"uv", "run", "--python", "python3", "--all-extras", "pytest", "--quiet"}},
		{"dev:python-lint", []string{"src/x.py"}, []string{"ruff", "check", "src/x.py"}},
		{"dev:python-lint", []string{"--select", "E"}, []string{"ruff", "check", "--select", "E"}},
		{"dev:python-fmt", []string{"--check"}, []string{"ruff", "format", "--check"}},
		{"dev:python-typecheck", []string{"./src/pkg"}, []string{"uv", "run", "--python", "python3", "--all-extras", "mypy", "./src/pkg"}},
		{"dev:python-typecheck", []string{"--strict"}, []string{"uv", "run", "--python", "python3", "--all-extras", "mypy", "--strict", "src", "tests"}},
		{"dev:python-typecheck", []string{"-m", "pkg.mod"}, []string{"uv", "run", "--python", "python3", "--all-extras", "mypy", "-m", "pkg.mod"}},
		{"dev:python-typecheck", []string{"--package=pkg"}, []string{"uv", "run", "--python", "python3", "--all-extras", "mypy", "--package=pkg"}},
		{"dev:ts-test", []string{"--filter=web"}, []string{"bun", "run", "test", "--filter=web"}},
		{"dev:ts-fmt", []string{"--check"}, []string{"bun", "run", "format", "--check"}},
	} {
		if got := manifestOf(t, tc.id).Compose(tc.args); !slices.Equal(got, tc.want) {
			t.Errorf("%s %v: %v, want %v", tc.id, tc.args, got, tc.want)
		}
	}
}

// gateTwins pair each dev atom with the discrete gate atom that runs the same tool.
var gateTwins = []struct{ dev, gate string }{
	{"dev:python-test", "python:pytest"},
	{"dev:python-lint", "python:ruff-check"},
	{"dev:python-fmt", "python:ruff-format"},
	{"dev:python-typecheck", "python:mypy"},
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
	for _, tc := range gateTwins {
		dev := manifestOf(t, tc.dev)
		e, ok := Lookup(tc.gate)
		if !ok || e.Def == nil {
			t.Fatalf("%s: no logic atom in the catalogue", tc.gate)
		}
		gate := e.Def
		if dev.Toolchain != gate.Toolchain || dev.Lane != string(gate.Lane) {
			t.Errorf("%s: toolchain/lane %s/%s, gate twin %s has %s/%s", tc.dev, dev.Toolchain, dev.Lane, tc.gate, gate.Toolchain, gate.Lane)
		}
		// every need of the twin is a need of the dev atom, so both resolve the
		// tool from the same tools.toml entry.
		for _, need := range gate.Needs {
			if !slices.Contains(dev.Needs, need) {
				t.Errorf("%s: needs %v, but the gate twin %s needs %s", tc.dev, dev.Needs, tc.gate, need)
			}
		}
		for _, need := range dev.Needs {
			name, isTool := strings.CutPrefix(need, "tool:")
			if !isTool {
				continue
			}
			if _, ok := tools.Tools[name]; !ok {
				t.Errorf("%s needs %s, which tools.toml does not pin", tc.dev, need)
			}
		}
	}
	// the pins themselves: ruff is the version the gate atoms run, and uv's
	// project form is the gate's own spelling (checks.UvRun).
	if pin := tools.Tools["ruff"]; pin.Kind != "uv" || pin.Version != checks.RuffVersion {
		t.Errorf("tools.toml pins ruff %+v, the gate atoms run %s", pin, checks.RuffVersion)
	}
	project := []string{"pyproject.toml"}
	for id, tool := range map[string][]string{
		"dev:python-test": {"pytest", "-q"}, "dev:python-typecheck": {"mypy"},
	} {
		// the gate's spelling plus the one addition, the image's own interpreter.
		want := slices.Insert(checks.UvRun(project, tool[0], tool[1:]...), 2, "--python", "python3")
		got := manifestOf(t, id).Compose(nil)
		if !slices.Equal(got[:len(want)], want) {
			t.Errorf("%s: %v does not start with the gate's %v plus --python python3", id, got, want)
		}
	}
	for id, sub := range map[string]string{"dev:python-lint": "check", "dev:python-fmt": "format"} {
		if got := manifestOf(t, id).Argv; !slices.Equal(got, []string{"ruff", sub}) {
			t.Errorf("%s: argv %v, the gate twin execs ruff %s by name", id, got, sub)
		}
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
	for _, verb := range []string{"test", "lint", "fmt", "typecheck"} {
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
