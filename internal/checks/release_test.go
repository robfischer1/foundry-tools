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

// THE CONVENTION IS THE STAR'S OWN NAME, which is what 27 of the fleet's 29 Go
// repos build and all of them build it from ./cmd/<name>.
func TestGoReleasePlanDerivesTheStarsOwnBinary(t *testing.T) {
	p, err := GoReleasePlan("hephaestus", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if plannedNames(p) != "hephaestus=./cmd/hephaestus" || p.Declared || p.Vendored {
		t.Errorf("%+v", p)
	}
	if got := strings.Join(GoReleaseArgs(p.Binaries[0], p.Vendored), " "); got != "go build -trimpath -ldflags=-s -w -o /out/hephaestus ./cmd/hephaestus" {
		t.Errorf("argv %q", got)
	}
}

// A RECORD SPEAKS ONLY WHEN THE REPO SHIPS MORE THAN ITS OWN NAME — measured:
// blade-runner and clio, and nobody else.
func TestGoReleasePlanTakesTheRecordsBinariesWhenItNamesThem(t *testing.T) {
	p, err := GoReleasePlan("blade-runner", []string{"blade-runner", "blade-controller"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if plannedNames(p) != "blade-controller=./cmd/blade-controller,blade-runner=./cmd/blade-runner" {
		t.Errorf("sorted by name: %s", plannedNames(p))
	}
	if !p.Declared {
		t.Errorf("the plan says the record spoke: %+v", p)
	}
	if !strings.Contains(ReleaseScope(p), "tools.build.binaries in the record") {
		t.Errorf("scope %q", ReleaseScope(p))
	}
}

// VENDOR IS A DIRECTORY, NOT A DECLARATION: the flag was present in exactly the
// five repos carrying vendor/ and in no others.
func TestGoReleaseArgsVendorFollowsTheTree(t *testing.T) {
	p, err := GoReleasePlan("ourea", nil, true)
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
		{"", nil, "no star"},
		{"x", []string{"../escape"}, "a path is not a binary name"},
		{"x", []string{"-flag"}, "a flag is not a binary name"},
		{"x", []string{"a", "a"}, "the same name twice"},
	} {
		if _, err := GoReleasePlan(c.star, c.declared, false); err == nil {
			t.Errorf("%s: want a refusal", c.why)
		}
	}
}

func TestReleaseBinariesReadsOnlyWhatTheRecordDeclares(t *testing.T) {
	if got := ReleaseBinaries(`{"tools":{"build":{"binaries":["clio","clio-query"]}}}`); strings.Join(got, ",") != "clio,clio-query" {
		t.Errorf("declared %v", got)
	}
	for _, slag := range []string{`{}`, `{"tools":{}}`, `{"tools":{"cast":{"binaries":["x"]}}}`, `not json`} {
		if got := ReleaseBinaries(slag); got != nil {
			t.Errorf("%s: the convention answers, not %v", slag, got)
		}
	}
}

// THE RUST CONVENTION IS THE SAME NAME, AS A WORKSPACE PACKAGE: the fleet's
// Rust star Dockerfiles and the template that pours them run
// `cargo build --release -p <star>`, and the binary is target/release/<star>.
func TestRustReleasePlanDerivesTheStarsOwnCrate(t *testing.T) {
	p, err := RustReleasePlan("tron", nil)
	if err != nil {
		t.Fatal(err)
	}
	if plannedNames(p) != "tron=tron" || p.Declared || p.Vendored || p.Lane != LaneRust {
		t.Errorf("%+v", p)
	}
	if got := strings.Join(RustReleaseArgs(p.Binaries[0]), " "); got != "cargo build --release --locked -p tron" {
		t.Errorf("argv %q", got)
	}
	if got := RustReleaseBinary(p.Binaries[0]); got != "/work/target/release/tron" {
		t.Errorf("binary at %q", got)
	}
	scope := ReleaseScope(p)
	if !strings.Contains(scope, "release build: tron") || !strings.Contains(scope, "the star's own name") || !strings.Contains(scope, "--locked") {
		t.Errorf("scope %q", scope)
	}
	if strings.Contains(scope, "vendor") {
		t.Errorf("a Rust scope does not talk about Go's vendoring: %q", scope)
	}
	// The Go plan's lane is Go, and its scope stays Go's.
	g, err := GoReleasePlan("hades", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if g.Lane != LaneGo || !strings.Contains(ReleaseScope(g), "resolves its dependencies") {
		t.Errorf("%+v %q", g, ReleaseScope(g))
	}
}

// A RUST RECORD SPEAKS THE SAME WAY: each declared binary is the workspace
// package of that name, sorted, and the same names are refused.
func TestRustReleasePlanTakesTheRecordsBinariesAndRefusesTheSameNames(t *testing.T) {
	p, err := RustReleasePlan("cerberus", []string{"cerberus", "cerberus-admin"})
	if err != nil {
		t.Fatal(err)
	}
	if plannedNames(p) != "cerberus=cerberus,cerberus-admin=cerberus-admin" || !p.Declared {
		t.Errorf("%+v", p)
	}
	if !strings.Contains(ReleaseScope(p), "tools.build.binaries in the record") {
		t.Errorf("scope %q", ReleaseScope(p))
	}
	for _, c := range []struct {
		star     string
		declared []string
		why      string
	}{
		{"", nil, "no star"},
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
		"http://100.93.64.106:8215/tron.git":                         "rob/tron",
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
	if got := RecordRepo(`{"meta":{"name":"hephaestus","repo":"rob/hephaestus"}}`); got != "rob/hephaestus" {
		t.Errorf("meta.repo %q", got)
	}
	for _, slag := range []string{`{}`, `{"meta":{}}`, `not json`} {
		if got := RecordRepo(slag); got != "" {
			t.Errorf("%s: %q", slag, got)
		}
	}
	for p, want := range map[string]string{
		"fleet/stars/hephaestus/slag.json": "hephaestus",
		"fleet/stars/hephaestus/data.json": "",
		"fleet/stars/a/b/slag.json":        "",
		"fleet/data.json":                  "",
		"fleet/stars//slag.json":           "",
		"policy/authz_grants/data.json":    "",
	} {
		if got := StarOfRecordPath(p); got != want {
			t.Errorf("StarOfRecordPath(%q) = %q, want %q", p, got, want)
		}
	}
}
