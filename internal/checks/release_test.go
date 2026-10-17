package checks

import (
	"strings"
	"testing"
)

func plannedNames(p ReleasePlan) string {
	var out []string
	for _, b := range p.Binaries {
		out = append(out, b.Name+"="+b.Package)
	}
	return strings.Join(out, ",")
}

// THE BINARIES ARE THE DOCKERFILE'S COPY LINES: a star whose image carries
// only its own binary copies only that.
func TestGoReleasePlanTakesTheDockerfilesCopies(t *testing.T) {
	p, err := GoReleasePlan("hephaestus", []string{"hephaestus"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if plannedNames(p) != "hephaestus=./cmd/hephaestus" || p.Vendored {
		t.Errorf("%+v", p)
	}
	if got := strings.Join(GoReleaseArgs(p.Binaries[0], p.Vendored), " "); got != "go build -trimpath -ldflags=-s -w -o /out/hephaestus ./cmd/hephaestus" {
		t.Errorf("argv %q", got)
	}
}

// A Dockerfile that copies more than the star's name builds more: blade-runner
// and clio, and nobody else.
func TestGoReleasePlanBuildsEveryCopiedBinary(t *testing.T) {
	p, err := GoReleasePlan("blade-runner", []string{"blade-runner", "blade-controller"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if plannedNames(p) != "blade-controller=./cmd/blade-controller,blade-runner=./cmd/blade-runner" {
		t.Errorf("sorted by name: %s", plannedNames(p))
	}
	if !strings.Contains(ReleaseScope(p), "the Dockerfile's COPY lines") {
		t.Errorf("scope %q", ReleaseScope(p))
	}
	// The star's own name is no longer assumed: blade-runner's Dockerfile
	// copies blade-controller alone, and that is all it builds.
	q, err := GoReleasePlan("blade-runner", []string{"blade-controller"}, false)
	if err != nil || plannedNames(q) != "blade-controller=./cmd/blade-controller" {
		t.Errorf("%v %+v", err, q)
	}
}

// VENDOR IS A DIRECTORY, NOT A DECLARATION: the flag was present in exactly the
// five repos carrying vendor/ and in no others.
func TestGoReleaseArgsVendorFollowsTheTree(t *testing.T) {
	p, err := GoReleasePlan("ourea", []string{"ourea"}, true)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(GoReleaseArgs(p.Binaries[0], p.Vendored), " ")
	if got != "go build -mod=vendor -trimpath -ldflags=-s -w -o /out/ourea ./cmd/ourea" {
		t.Errorf("argv %q", got)
	}
	if !strings.Contains(ReleaseScope(p), "-mod=vendor") {
		t.Errorf("scope %q", ReleaseScope(p))
	}
}

// A binary the plan cannot name is an error, never an invented one.
func TestGoReleasePlanRefusesWhatItCannotName(t *testing.T) {
	for _, c := range []struct {
		star     string
		declared []string
		why      string
	}{
		{"", []string{"x"}, "no star"},
		{"x", nil, "a Dockerfile that copies nothing out of release/"},
		{"x", []string{"../escape"}, "a path is not a binary name"},
		{"x", []string{"-flag"}, "a flag is not a binary name"},
		{"x", []string{"a", "a"}, "the same name twice"},
	} {
		if _, err := GoReleasePlan(c.star, c.declared, false); err == nil {
			t.Errorf("%s: want a refusal", c.why)
		}
	}
}

// THE RUST CONVENTION IS THE SAME NAME, AS A WORKSPACE PACKAGE: the fleet's
// Rust star Dockerfiles and the template that pours them run
// `cargo build --release -p <star>`, and the binary is target/release/<star>.
func TestRustReleasePlanDerivesTheStarsOwnCrate(t *testing.T) {
	p, err := RustReleasePlan("tron", []string{"tron"})
	if err != nil {
		t.Fatal(err)
	}
	if plannedNames(p) != "tron=tron" || p.Vendored || p.Lane != LaneRust {
		t.Errorf("%+v", p)
	}
	if got := strings.Join(RustReleaseArgs(p.Binaries[0]), " "); got != "cargo build --release --locked -p tron" {
		t.Errorf("argv %q", got)
	}
	// Built in the release volume, read from the copy the build made of it.
	if got := RustReleaseBuilt(p.Binaries[0]); got != "/cache/cargo-release/release/tron" {
		t.Errorf("built at %q", got)
	}
	if got := RustReleaseBinary(p.Binaries[0]); got != "/out/tron" {
		t.Errorf("binary at %q", got)
	}
	scope := ReleaseScope(p)
	if !strings.Contains(scope, "release build: tron") || !strings.Contains(scope, "the Dockerfile's COPY lines") || !strings.Contains(scope, "--locked") {
		t.Errorf("scope %q", scope)
	}
	if strings.Contains(scope, "vendor") {
		t.Errorf("a Rust scope does not talk about Go's vendoring: %q", scope)
	}
	// The Go plan's lane is Go, and its scope stays Go's.
	g, err := GoReleasePlan("hades", []string{"hades"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if g.Lane != LaneGo || !strings.Contains(ReleaseScope(g), "resolves its dependencies") {
		t.Errorf("%+v %q", g, ReleaseScope(g))
	}
}

// A RUST DOCKERFILE SPEAKS THE SAME WAY: each copied binary is the workspace
// package of that name, sorted, and the same names are refused.
func TestRustReleasePlanTakesTheCopiedBinariesAndRefusesTheSameNames(t *testing.T) {
	p, err := RustReleasePlan("cerberus", []string{"cerberus", "cerberus-admin"})
	if err != nil {
		t.Fatal(err)
	}
	if plannedNames(p) != "cerberus=cerberus,cerberus-admin=cerberus-admin" {
		t.Errorf("%+v", p)
	}
	for _, c := range []struct {
		star     string
		declared []string
		why      string
	}{
		{"", []string{"x"}, "no star"},
		{"x", nil, "a Dockerfile that copies nothing out of release/"},
		{"x", []string{"../escape"}, "a path is not a binary name"},
		{"x", []string{"-flag"}, "a flag is not a binary name"},
		{"x", []string{".hidden"}, "a dotfile is not a binary name"},
		{"x", []string{""}, "an empty name is not a binary name"},
		{"x", []string{"a", "a"}, "the same name twice"},
	} {
		if _, err := RustReleasePlan(c.star, c.declared); err == nil {
			t.Errorf("%s: want a refusal", c.why)
		}
	}
}

// A CLONE URL NAMES A CUSTODY KEY the way a record's meta.repo spells it:
// the door's bare names are rob/'s, an owner path is itself, and every face
// of the door (the in-cluster service, the Tailscale IP, git.notusmi.com)
// reads the same. This is how a repository with no answers file — hephaestus,
// not copier-templated — is matched to its record.
func TestRepoKeyReadsTheCustodyKeyOffACloneURL(t *testing.T) {
	for url, want := range map[string]string{
		"http://ourea.default.svc.cluster.local:8215/hephaestus.git": "rob/hephaestus",
		"http://ourea.notusmi.com:8215/tron.git":                     "rob/tron",
		"https://git.notusmi.com/foundry/foundry-dies.git":           "foundry/foundry-dies",
		"https://git.notusmi.com/foundry-tools":                      "rob/foundry-tools",
		"git@git.notusmi.com:foundry/base-images.git":                "foundry/base-images",
		"https://git.notusmi.com/":                                   "",
		"http:///x.git":                                              "rob/x",
		"":                                                           "",
	} {
		if got := RepoKey(url); got != want {
			t.Errorf("RepoKey(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestRecordRepoAndStarOfRecordPath(t *testing.T) {
	if got := RecordRepo(`{"name":"hephaestus","repo":"rob/hephaestus"}`); got != "rob/hephaestus" {
		t.Errorf("meta.repo %q", got)
	}
	for _, slag := range []string{`{}`, `{"name":"x"}`, `not json`} {
		if got := RecordRepo(slag); got != "" {
			t.Errorf("%s: %q", slag, got)
		}
	}
	for p, want := range map[string]string{
		"fleet/repos/hephaestus.json":      "hephaestus",
		"fleet/repos/hephaestus.toml":      "",
		"fleet/repos/a/b.json":             "",
		"fleet/data.json":                  "",
		"fleet/repos/.json":                "",
		"fleet/stars/hephaestus/slag.json": "",
		"policy/grants/data.json":          "",
	} {
		if got := StarOfRecordPath(p); got != want {
			t.Errorf("StarOfRecordPath(%q) = %q, want %q", p, got, want)
		}
	}
}

// The bun release is the repo's own release script; the python release is
// the lock as a wheel, without dev and with no extras.
func TestTheReleaseArgvsAreTheScriptAndTheLockAsAWheel(t *testing.T) {
	if got := strings.Join(TSReleaseArgs, " "); got != "bun run release" {
		t.Errorf("bun: %q", got)
	}
	if got := strings.Join(PythonReleaseArgs(), " "); got != "uv sync --locked --no-dev --no-editable" {
		t.Errorf("python: %q", got)
	}
}

// A release step's exit is read for WHOSE fault it was: the tree's (any exit
// a command chose), the substrate's (a network fault in its words, whatever
// the code), or the run's (a command that could not run or be found, a
// signal).
func TestReleaseStepStateReadsWhoseFaultAFailureWas(t *testing.T) {
	for _, c := range []struct {
		code int
		out  string
		want int
	}{
		{0, "", 0},
		{0, "retrying after a connection reset", 0}, // a clean step that recovered is clean
		{1, "error TS2322", 1},
		{2, "error: The lockfile needs to be updated", 1}, // uv's every error is 2
		{125, "", 1},
		{126, "permission denied", 2},
		{127, "bunx: not found", 2},
		{137, "", 2},
		{1, "error: ECONNRESET", 2},
		{2, "dial tcp 10.43.42.181:443: i/o timeout", 2},
	} {
		if got := ReleaseStepState(c.code, c.out); got != c.want {
			t.Errorf("ReleaseStepState(%d, %q) = %d, want %d", c.code, c.out, got, c.want)
		}
	}
}

// THE STAMP: foundry-tools#18676/#18695 — every star the lane built reported
// version=dev because the release flags never named the version. The linker
// ignores a -X naming a symbol the binary lacks, so one flag set covers the
// fleet's conventions: main.Version / main.version, and the config and verbs
// packages the stars keep a var in.
func TestGoReleaseStampedArgsStampEveryConvention(t *testing.T) {
	p, err := GoReleasePlan("techne", []string{"techne"}, false)
	if err != nil {
		t.Fatal(err)
	}
	got := GoReleaseStampedArgs(p.Binaries[0], p.Vendored, "git.notusmi.com/rob/techne", "1792086104-b3db6f1")
	want := "go build -trimpath -ldflags=-s -w" +
		" -X main.Version=1792086104-b3db6f1 -X main.version=1792086104-b3db6f1" +
		" -X git.notusmi.com/rob/techne/internal/config.Version=1792086104-b3db6f1" +
		" -X git.notusmi.com/rob/techne/internal/verbs.Version=1792086104-b3db6f1" +
		" -X git.notusmi.com/rob/techne/verbs.Version=1792086104-b3db6f1" +
		" -o /out/techne ./cmd/techne"
	if s := strings.Join(got, " "); s != want {
		t.Errorf("argv\n got %q\nwant %q", s, want)
	}
	// The ldflags are ONE argv element: go splits it on spaces itself.
	n := 0
	for _, a := range got {
		if strings.HasPrefix(a, "-ldflags=") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d -ldflags elements in %q", n, got)
	}
}

// No version, no stamp: the gate's go:release stays byte-identical to what it
// was, which is what lets the build lane's later compile be the only new one.
func TestGoReleaseArgsUnstampedWhenNoVersion(t *testing.T) {
	p, _ := GoReleasePlan("ourea", []string{"ourea"}, true)
	a := strings.Join(GoReleaseArgs(p.Binaries[0], p.Vendored), " ")
	b := strings.Join(GoReleaseStampedArgs(p.Binaries[0], p.Vendored, "git.notusmi.com/rob/ourea", ""), " ")
	if a != b || strings.Contains(a, "-X") {
		t.Errorf("unstamped argv differs:\n%q\n%q", a, b)
	}
}

// A version or module that could break out of the ldflags string is refused.
func TestReleaseStampRefusesWhatSplitsLdflags(t *testing.T) {
	for _, c := range []struct{ module, version string }{
		{"m", "1 -X main.evil=x"}, {"m", `a"b`}, {"m", "a'b"}, {"m", ""},
		{"m x", "1-abc"}, {"", "1-abc"}, {"m\"", "1-abc"},
	} {
		if _, err := ReleaseStamp(c.module, c.version); err == nil {
			t.Errorf("ReleaseStamp(%q, %q) accepted", c.module, c.version)
		}
	}
	if _, err := ReleaseStamp("git.notusmi.com/rob/x", "1792086104-b3db6f1"); err != nil {
		t.Errorf("a stamp tag was refused: %v", err)
	}
}

func TestGoModulePathReadsTheModuleLine(t *testing.T) {
	if got := GoModulePath("// c\nmodule git.notusmi.com/rob/x\n\ngo 1.22\n"); got != "git.notusmi.com/rob/x" {
		t.Errorf("%q", got)
	}
}
