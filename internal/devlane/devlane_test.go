package devlane

import (
	"reflect"
	"strings"
	"testing"
)

func TestLangReadsTheManifestsAndRefusesToGuess(t *testing.T) {
	for _, c := range []struct {
		entries []string
		want    string
		lang    string
		err     string
	}{
		{[]string{"go.mod", "main.go"}, "", Go, ""},
		{[]string{"Cargo.toml", "src/"}, "", Rust, ""},
		{[]string{"go.mod", "Cargo.toml"}, "", "", "both go.mod and Cargo.toml"},
		{[]string{"go.mod", "Cargo.toml"}, "rust", Rust, ""},
		{[]string{"README.md"}, "", "", "no go.mod or Cargo.toml"},
		{[]string{"README.md"}, "go", Go, ""},
		{[]string{"go.mod"}, "python", "", `"python" is not go or rust`},
	} {
		got, err := Lang(c.entries, c.want)
		if got != c.lang || (c.err == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.err)) {
			t.Errorf("Lang(%v, %q) = %q, %v; want %q, %q", c.entries, c.want, got, err, c.lang, c.err)
		}
	}
}

func TestIdentityIsTheRepositoryNotTheWorktree(t *testing.T) {
	lock := "version = 4\n[[package]]\nname = \"zeta\"\nversion = \"1\"\n[[package]]\nname = \"serde\"\nversion = \"1\"\nsource = \"registry+https://x\"\n[[package]]\nname = \"alpha\"\nversion = \"1\"\n"
	for _, c := range []struct {
		name, lang, manifest, lock, want string
	}{
		{"go module", Go, "module example.com/x/y\n\ngo 1.26\n", "", "example.com/x/y"},
		{"go quoted and commented", Go, "module \"example.com/q\" // old\n", "", "example.com/q"},
		{"go tab", Go, "module\texample.com/t\n", "", "example.com/t"},
		{"go modulefoo is not a module line", Go, "modulefoo bar\n", "", ""},
		{"go bare module", Go, "module\n", "", ""},
		{"go none", Go, "go 1.26\n", "", ""},
		{"rust package", Rust, "[package]\nname = \"cerb\"\n", lock, "cerb"},
		{"rust workspace from lock", Rust, "[workspace]\nmembers = [\"crates/*\"]\n", lock, "workspace:alpha,zeta"},
		{"rust workspace members without a lock", Rust, "[workspace]\nmembers = [\"b\", \"a\"]\n", "", "workspace:a,b"},
		{"rust nothing", Rust, "[workspace]\n", "", ""},
		{"rust broken manifest", Rust, "[[[", lock, ""},
		{"rust broken lock falls to members", Rust, "[workspace]\nmembers = [\"a\"]\n", "[[[", "workspace:a"},
		{"nameless lock entry is skipped", Rust, "[workspace]\nmembers = [\"a\"]\n", "[[package]]\nsource = \"\"\n", "workspace:a"},
	} {
		if got := Identity(c.lang, c.manifest, c.lock); got != c.want {
			t.Errorf("%s: Identity = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRepoKeyPrefersTheCallersOwn(t *testing.T) {
	if got := RepoKey("mine", Go, "module x\n", ""); got != "mine" {
		t.Errorf("--repo must win, got %q", got)
	}
	if got := RepoKey("", Go, "module x\n", ""); got != "x" {
		t.Errorf("the manifest names it otherwise, got %q", got)
	}
}

func TestHasVendor(t *testing.T) {
	if !HasVendor([]string{"go.mod", "vendor/"}) || !HasVendor([]string{"vendor"}) {
		t.Error("a vendor entry is a vendored tree")
	}
	if HasVendor([]string{"go.mod", "vendored.txt", "internal/vendor/"}) {
		t.Error("only the root vendor/ counts")
	}
}

func TestGoArgv(t *testing.T) {
	for _, c := range []struct {
		name  string
		verb  string
		args  []string
		race  bool
		want  string
		fails bool
	}{
		{"build defaults to every package", "build", nil, false, "go build ./...", false},
		{"build keeps a named package", "build", []string{"./cmd/x"}, false, "go build ./cmd/x", false},
		{"vet defaults", "vet", nil, false, "go vet ./...", false},
		{"test is raced by default", "test", nil, true, "go test -race ./...", false},
		{"test with the detector off", "test", nil, false, "go test ./...", false},
		{"filter and package", "test", []string{"-run", "TestX", "./internal/foo/..."}, true, "go test -race -run TestX ./internal/foo/...", false},
		{"a -run value is not a package", "test", []string{"-run", "Test/sub.case"}, true, "go test -race -run Test/sub.case ./...", false},
		{"a -run=value is not a package", "test", []string{"-run=a/b.c"}, true, "go test -race -run=a/b.c ./...", false},
		{"the caller's -race wins", "test", []string{"-race", "./x"}, true, "go test -race ./x", false},
		{"the caller's -race=false wins", "test", []string{"-race=false"}, true, "go test -race=false ./...", false},
		{"a double-dash race flag is theirs too", "test", []string{"--race=false"}, true, "go test --race=false ./...", false},
		{"module path is a package", "test", []string{"example.com/x/y"}, false, "go test example.com/x/y", false},
		{"dot is a package", "test", []string{"."}, false, "go test .", false},
		{"dotdot is a package", "test", []string{".."}, false, "go test ..", false},
		{"parent dir is a package", "test", []string{"../x"}, false, "go test ../x", false},
		{"default lands before -args", "test", []string{"-v", "-args", "-flag", "./not-a-package"}, false, "go test -v ./... -args -flag ./not-a-package", false},
		{"-race after -args is the binary's", "test", []string{"-args", "-race"}, true, "go test -race ./... -args -race", false},
		{"a bare word race is not the flag", "test", []string{"race"}, true, "go test -race race ./...", false},
		{"--args also ends the go command's own", "test", []string{"--args", "x"}, false, "go test ./... --args x", false},
		{"an = flag does not eat the next argument", "test", []string{"-count=1", "./x"}, false, "go test -count=1 ./x", false},
		{"a trailing-dots pattern is a package", "test", []string{"cmd/..."}, false, "go test cmd/...", false},
		{"a value flag consumes its value", "test", []string{"-timeout", "30s", "-count=1"}, false, "go test -timeout 30s -count=1 ./...", false},
		{"fmt is not an argv verb", "fmt", nil, false, "", true},
		{"cargo verbs are not go", "clippy", nil, false, "", true},
	} {
		got, err := GoArgv(c.verb, c.args, c.race)
		if (err != nil) != c.fails {
			t.Errorf("%s: err = %v", c.name, err)
			continue
		}
		if !c.fails && strings.Join(got, " ") != c.want {
			t.Errorf("%s: %q, want %q", c.name, strings.Join(got, " "), c.want)
		}
	}
}

func TestRustArgv(t *testing.T) {
	lints := "-- -W clippy::all -D warnings"
	for _, c := range []struct {
		name  string
		verb  string
		args  []string
		want  string
		fails bool
	}{
		{"check is the workspace", "check", nil, "cargo check --workspace", false},
		{"build is the workspace", "build", nil, "cargo build --workspace", false},
		{"test is the workspace", "test", nil, "cargo test --workspace", false},
		{"a filter alone keeps the workspace", "test", []string{"uuid_shape"}, "cargo test --workspace uuid_shape", false},
		{"-p narrows", "test", []string{"-p", "cerberus", "uuid_shape"}, "cargo test -p cerberus uuid_shape", false},
		{"-pcrate narrows", "test", []string{"-pcerberus"}, "cargo test -pcerberus", false},
		{"--profile is not -p", "build", []string{"--profile", "release"}, "cargo build --workspace --profile release", false},
		{"--package= narrows", "build", []string{"--package=x"}, "cargo build --package=x", false},
		{"--manifest-path narrows", "check", []string{"--manifest-path=a/Cargo.toml"}, "cargo check --manifest-path=a/Cargo.toml", false},
		{"--workspace is not doubled", "test", []string{"--workspace"}, "cargo test --workspace", false},
		{"--all is the workspace", "build", []string{"--all"}, "cargo build --all", false},
		{"-p after the separator is the binary's", "test", []string{"--", "-p"}, "cargo test --workspace -- -p", false},
		{"clippy defaults", "clippy", nil, "cargo clippy --workspace --all-targets " + lints, false},
		{"clippy -p", "clippy", []string{"-p", "x"}, "cargo clippy --all-targets -p x " + lints, false},
		{"clippy with a target selector", "clippy", []string{"--lib"}, "cargo clippy --workspace --lib " + lints, false},
		{"clippy with its own lints", "clippy", []string{"--", "-W", "clippy::pedantic"}, "cargo clippy --workspace --all-targets -- -W clippy::pedantic", false},
		{"a selector after the separator does not count", "clippy", []string{"--", "--lib"}, "cargo clippy --workspace --all-targets -- --lib", false},
		{"fmt is the workspace", "fmt", nil, "cargo fmt --all", false},
		{"lock", "lock", nil, "cargo generate-lockfile", false},
		{"update narrows by -p", "update", []string{"-p", "serde"}, "cargo update -p serde", false},
		{"vet is not cargo", "vet", nil, "", true},
	} {
		got, err := RustArgv(c.verb, c.args)
		if (err != nil) != c.fails {
			t.Errorf("%s: err = %v", c.name, err)
			continue
		}
		if !c.fails && strings.Join(got, " ") != c.want {
			t.Errorf("%s: %q, want %q", c.name, strings.Join(got, " "), c.want)
		}
	}
}

func TestAppliesNamesWhatTheLanguageHas(t *testing.T) {
	for _, c := range []struct {
		lang, verb string
		ok         bool
	}{
		{Go, "vet", true}, {Go, "tidy", true}, {Go, "fmt", true}, {Go, "clippy", false}, {Go, "lock", false},
		{Rust, "clippy", true}, {Rust, "update", true}, {Rust, "check", true}, {Rust, "vet", false}, {Rust, "tidy", false},
		{Rust, "test", true}, {Go, "build", true},
	} {
		err := Applies(c.lang, c.verb)
		if (err == nil) != c.ok {
			t.Errorf("Applies(%s, %s) = %v", c.lang, c.verb, err)
		}
		if err != nil && !strings.Contains(err.Error(), c.verb+" does not exist for a "+c.lang+" source") {
			t.Errorf("Applies(%s, %s) says %q", c.lang, c.verb, err)
		}
	}
}

// A failed tool never settles on 0; a missing binary or a kill is the lane's
// could-not-run.
func TestSettleCode(t *testing.T) {
	for code, want := range map[int]int{0: 0, 1: 1, 2: 1, 101: 1, 125: 1, 126: 2, 127: 2, 137: 2, 255: 2} {
		if got := SettleCode(code); got != want {
			t.Errorf("SettleCode(%d) = %d, want %d", code, got, want)
		}
	}
}

func TestGoFilesKeepsOnlyGo(t *testing.T) {
	got := GoFiles([]string{"a.go", "b.txt", "x/y_test.go", "go.mod", "c.gox"})
	if !reflect.DeepEqual(got, []string{"a.go", "x/y_test.go"}) {
		t.Errorf("GoFiles = %v", got)
	}
	if GoFiles(nil) != nil {
		t.Error("no files, no list")
	}
}

func TestNonce(t *testing.T) {
	if Nonce(false, 12345) != "" {
		t.Error("an unforced run carries no nonce")
	}
	if Nonce(true, 12345) != "12345" {
		t.Error("a forced run is keyed on the clock it was given")
	}
}

// The upload ignore keeps build output and agent checkouts home, and .git too.
func TestIgnoreKeepsTheBigThingsHome(t *testing.T) {
	for _, want := range []string{"**/target", "**/node_modules", ".claude", ".git"} {
		found := false
		for _, g := range Ignore {
			found = found || g == want
		}
		if !found {
			t.Errorf("Ignore lacks %s", want)
		}
	}
}
