package catalogue

import (
	"slices"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
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
}

// TestUT_514_TheDevAtomsKeepTheirShape: all 13 parse, sit in the dev stage with
// the Dev surface's fields, and write the tree for exactly the six rewriting verbs.
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
	if got := len(devAtoms); got != 13 {
		t.Errorf("%d dev atoms in the table, want 13", got)
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

// TestTheDevAtomsTimeoutsEnvAndNeeds: the long verbs get 30m, go test mounts
// dies as Dagger's dev test does, and go vendor ignores the vendor tree it
// replaces.
func TestTheDevAtomsTimeoutsEnvAndNeeds(t *testing.T) {
	long := []string{"dev:go-test", "dev:rust-build", "dev:rust-test"}
	for _, tc := range devAtoms {
		m := manifestOf(t, tc.id)
		want := "10m"
		if slices.Contains(long, tc.id) {
			want = "30m"
		}
		if m.Timeout != want {
			t.Errorf("%s: timeout %s, want %s", tc.id, m.Timeout, want)
		}
		wantNeeds := []string(nil)
		if tc.id == "dev:go-test" {
			wantNeeds = []string{"dies"}
		}
		if !slices.Equal(m.Needs, wantNeeds) {
			t.Errorf("%s: needs %v, want %v", tc.id, m.Needs, wantNeeds)
		}
		wantEnv := map[string]string(nil)
		if tc.id == "dev:go-vendor" {
			wantEnv = map[string]string{"GOFLAGS": "-mod=mod"}
		}
		if len(m.Env) != len(wantEnv) || m.Env["GOFLAGS"] != wantEnv["GOFLAGS"] {
			t.Errorf("%s: env %v, want %v", tc.id, m.Env, wantEnv)
		}
	}
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
