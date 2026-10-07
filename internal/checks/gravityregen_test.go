package checks

import (
	"strings"
	"testing"
)

const rev1 = "f450dd4c17fabbc2820904727f2d07f3f4916eb7"
const rev2 = "601aa0d9e1a5ad71e3987c9d52a1b0111c9b43e0"

func prov(cmd, commit string) string {
	return `{"generator":{"command":"` + cmd + `","commit":"` + commit + `"}}`
}

func TestRegenEnvIsTheDirectoryUpperCased(t *testing.T) {
	cases := map[string]string{
		"classescore":   "CLASSESCORE_REGEN_REQUIRED",
		"stamp":         "STAMP_REGEN_REQUIRED",
		"internal/fade": "FADE_REGEN_REQUIRED",
		"a-b.c9":        "A_B_C9_REGEN_REQUIRED",
		"Zeta":          "ZETA_REGEN_REQUIRED",
	}
	for dir, want := range cases {
		if got := RegenEnv(dir); got != want {
			t.Errorf("RegenEnv(%q) = %q, want %q", dir, got, want)
		}
	}
}

func TestGravityRegensListsOnlyGravityGeneratedPackagesInPathOrder(t *testing.T) {
	got, err := GravityRegens(map[string]string{
		"stamp/provenance.json":       prov("gravity --name stamp-core ./guest.wasm", rev1),
		"classescore/provenance.json": prov("gravity --name classes-core ./guest.wasm", rev1),
		"ts/provenance.json":          `{"tools":{"jco":"1.35.0"}}`,
		"other/provenance.json":       prov("jco transpile x", ""),
		"empty/provenance.json":       prov("", ""),
		"gravityish/provenance.json":  prov("gravity-ng --x", rev1),
		"untested/provenance.json":    prov("gravity --x", rev2),
	}, []string{"stamp/regen_test.go", "classescore/regen_test.go", "ts/regen_test.go", "other/regen_test.go",
		"empty/regen_test.go", "gravityish/regen_test.go", "stamp/other_test.go", "x/stamp/regen_test.go"})
	if err != nil {
		t.Fatal(err)
	}
	want := []GravityRegen{
		{Dir: "classescore", Env: "CLASSESCORE_REGEN_REQUIRED", Rev: rev1},
		{Dir: "stamp", Env: "STAMP_REGEN_REQUIRED", Rev: rev1},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %v, want %v", i, got[i], want[i])
		}
	}
}

func TestGravityRegensRefusesWhatItCannotRegenerate(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"no commit":    {"a/provenance.json": prov("gravity --x", "")},
		"short commit": {"a/provenance.json": prov("gravity --x", "f450dd4")},
		"upper commit": {"a/provenance.json": prov("gravity --x", strings.ToUpper(rev1))},
		"not json":     {"a/provenance.json": "{"},
	} {
		if _, err := GravityRegens(files, []string{"a/regen_test.go"}); err == nil {
			t.Errorf("%s: want an error, got none", name)
		}
	}
}

func TestGravityRegensAtTheRootNamesDot(t *testing.T) {
	got, err := GravityRegens(map[string]string{"provenance.json": prov("gravity --x", rev1)}, []string{"regen_test.go"})
	if err != nil || len(got) != 1 || got[0].Dir != "." || got[0].Env != "REGEN_REQUIRED" {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestGravityRevAgreesOrNames(t *testing.T) {
	if rev, err := GravityRev(nil); rev != "" || err != nil {
		t.Fatalf("empty: %q, %v", rev, err)
	}
	a := GravityRegen{Dir: "a", Rev: rev1}
	b := GravityRegen{Dir: "b", Rev: rev1}
	if rev, err := GravityRev([]GravityRegen{a, b}); rev != rev1 || err != nil {
		t.Fatalf("agree: %q, %v", rev, err)
	}
	_, err := GravityRev([]GravityRegen{a, {Dir: "c", Rev: rev2}})
	if err == nil || !strings.Contains(err.Error(), "a pins gravity "+rev1) || !strings.Contains(err.Error(), "c pins "+rev2) {
		t.Fatalf("disagree: %v", err)
	}
}

func TestGravityScopeSaysWhetherRegenIsRequired(t *testing.T) {
	if s := GravityScope(nil, ""); !strings.Contains(s, "no regenerate-and-diff test is required") {
		t.Errorf("none: %q", s)
	}
	rs := []GravityRegen{{Dir: "a", Env: "A_REGEN_REQUIRED", Rev: rev1}, {Dir: "b", Env: "B_REGEN_REQUIRED", Rev: rev1}}
	s := GravityScope(rs, rev1)
	if !strings.Contains(s, "gravity "+rev1+" staged") || !strings.Contains(s, "A_REGEN_REQUIRED=1, B_REGEN_REQUIRED=1 makes") {
		t.Errorf("some: %q", s)
	}
}
