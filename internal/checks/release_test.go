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
