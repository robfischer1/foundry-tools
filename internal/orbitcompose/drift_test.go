package orbitcompose

import (
	"strings"
	"testing"
)

func contractsFixture() map[string][]byte {
	return map[string][]byte{
		"urania-themis.toml": []byte(contractBody),
		"chaos-themis.toml":  []byte(contractBody),
		"README.md":          []byte("not a contract"),
	}
}

// composedFixture is the directory a correct render of contractsFixture holds.
func composedFixture(t *testing.T) map[string][]byte {
	t.Helper()
	cs, err := ContractsOf(contractsFixture())
	if err != nil {
		t.Fatal(err)
	}
	return Files("prime", Compose(cs))
}

func TestContractsOfParsesTheTomlInNameOrderAndSkipsTheRest(t *testing.T) {
	cs, err := ContractsOf(contractsFixture())
	if err != nil || len(cs) != 2 || cs[0].Name != "chaos-themis" || cs[1].Name != "urania-themis" {
		t.Fatalf("%+v %v", cs, err)
	}
	if _, err := ContractsOf(map[string][]byte{"README.md": nil}); err == nil || !strings.Contains(err.Error(), "holds no contract") {
		t.Fatalf("no .toml must be an error: %v", err)
	}
	if _, err := ContractsOf(map[string][]byte{"urania-themis.toml": []byte("version = 1")}); err == nil {
		t.Fatal("a contract that does not parse must be an error")
	}
}

// A RENDER THAT MATCHES IS NO DRIFT, and says how many contracts it holds.
func TestCheckDriftOfTheComposedSetIsNone(t *testing.T) {
	state, report := CheckDrift(contractsFixture(), composedFixture(t), "prime")
	if state != DriftNone || report != "the directory is the composed set of 2 contract(s)" {
		t.Fatalf("state %d report %q", state, report)
	}
}

// A STALE RENDER IS A FINDING THAT NAMES EVERY FILE A RE-RENDER WOULD TOUCH,
// and says how to bring it back.
func TestCheckDriftNamesWhatAReRenderWouldChange(t *testing.T) {
	have := composedFixture(t)
	delete(have, "chaos.orbit.toml")
	have["nyx.orbit.toml"] = []byte("gone\n")
	have["themis.orbit.toml"] = []byte("edited by hand\n")
	state, report := CheckDrift(contractsFixture(), have, "prime")
	if state != DriftFound {
		t.Fatalf("state %d: %s", state, report)
	}
	for _, want := range []string{"stale against its contracts", "remove nyx.orbit.toml", "write  chaos.orbit.toml", "write  themis.orbit.toml", RenderHint} {
		if !strings.Contains(report, want) {
			t.Errorf("the report lacks %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "urania.orbit.toml") {
		t.Errorf("an untouched sidecar is not part of the change:\n%s", report)
	}
}

// A FILE THE COMPOSER DOES NOT OWN IS A FINDING, NOT A COULD-NOT-RUN: it is
// a fault in the rendered tree, its committer's to remove.
func TestCheckDriftFindsAForeignFile(t *testing.T) {
	have := composedFixture(t)
	have["notes.md"] = []byte("mine")
	state, report := CheckDrift(contractsFixture(), have, "prime")
	if state != DriftFound || !strings.Contains(report, "notes.md is not the composer's") {
		t.Fatalf("state %d report %q", state, report)
	}
}

// CONTRACTS THAT DO NOT COMPOSE CANNOT JUDGE THE RENDER.
func TestCheckDriftCannotGoOnBrokenContracts(t *testing.T) {
	state, report := CheckDrift(map[string][]byte{"urania-themis.toml": []byte("version = 1")}, composedFixture(t), "prime")
	if state != DriftCannotGo || !strings.HasPrefix(report, "the contracts do not compose: ") {
		t.Fatalf("state %d report %q", state, report)
	}
}

func TestReportSortsWritesAndRemoves(t *testing.T) {
	got := Report(Change{Write: map[string][]byte{"b.orbit.toml": nil, "a.orbit.toml": nil}, Remove: []string{"c.orbit.toml"}})
	if got != "remove c.orbit.toml\nwrite  a.orbit.toml\nwrite  b.orbit.toml" {
		t.Fatalf("%q", got)
	}
	if Report(Change{}) != "orbitcompose: up to date" {
		t.Fatal("an empty change is up to date")
	}
}
