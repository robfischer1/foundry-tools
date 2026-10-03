package orbitcompose

import (
	"errors"
	"os"
	"path/filepath"
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
	cs, err := ContractsOf(contractsFixture(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return Files("prime", Compose(cs))
}

func TestContractsOfParsesTheTomlInNameOrderAndSkipsTheRest(t *testing.T) {
	cs, err := ContractsOf(contractsFixture(), nil)
	if err != nil || len(cs) != 2 || cs[0].Name != "chaos-themis" || cs[1].Name != "urania-themis" {
		t.Fatalf("%+v %v", cs, err)
	}
	if _, err := ContractsOf(map[string][]byte{"README.md": nil}, nil); !errors.Is(err, ErrNoContract) {
		t.Fatalf("no .toml must be an error: %v", err)
	}
	if _, err := ContractsOf(map[string][]byte{"urania-themis.toml": []byte("version = 1")}, nil); err == nil {
		t.Fatal("a contract that does not parse must be an error")
	}
}

// A RENDER THAT MATCHES IS NO DRIFT, and says how many contracts it holds.
func TestCheckDriftOfTheComposedSetIsNone(t *testing.T) {
	d := CheckDrift(contractsFixture(), composedFixture(t), "prime", nil)
	if d.State != DriftNone || d.Report != "the directory is the composed set of 2 contract(s)" {
		t.Fatalf("%+v", d)
	}
}

// A STALE RENDER IS A FINDING THAT NAMES EVERY FILE A RE-RENDER WOULD TOUCH,
// and says how to bring it back.
func TestCheckDriftNamesWhatAReRenderWouldChange(t *testing.T) {
	have := composedFixture(t)
	delete(have, "chaos.orbit.toml")
	have["nyx.orbit.toml"] = []byte("gone\n")
	have["themis.orbit.toml"] = []byte("edited by hand\n")
	d := CheckDrift(contractsFixture(), have, "prime", nil)
	state, report := d.State, d.Report
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
	d := CheckDrift(contractsFixture(), have, "prime", nil)
	if d.State != DriftFound || !strings.Contains(d.Report, "notes.md is not the composer's") {
		t.Fatalf("%+v", d)
	}
}

// A DIRECTORY WITH NO CONTRACT AT ALL HAS NOTHING TO JUDGE THE RENDER BY.
func TestCheckDriftCannotGoWithoutAContract(t *testing.T) {
	d := CheckDrift(map[string][]byte{"README.md": nil}, composedFixture(t), "prime", nil)
	if d.State != DriftCannotGo || !strings.HasPrefix(d.Report, "the contracts do not compose: ") {
		t.Fatalf("%+v", d)
	}
}

// ONE BAD FILE IS A FINDING ON THAT FILE: the readable contracts still
// compose and the render is still compared with them.
func TestCheckDriftReportsABadFileAndStillComparesTheRest(t *testing.T) {
	contracts := contractsFixture()
	contracts["broken-themis.toml"] = []byte("version = 1")
	d := CheckDrift(contracts, composedFixture(t), "prime", nil)
	want := "the directory is the composed set of the 2 readable contract(s)\ncontract-unparseable: broken-themis.toml: does not parse:"
	if d.State != DriftFound || !strings.HasPrefix(d.Report, want) {
		t.Fatalf("a bad file beside a matching render: %+v", d)
	}

	have := composedFixture(t)
	delete(have, "themis.orbit.toml")
	contracts["also-bad.toml"] = []byte("x")
	d = CheckDrift(contracts, have, "prime", nil)
	for _, want := range []string{"write  themis.orbit.toml", RenderHint, "\ncontract-unparseable: also-bad.toml: ", "\ncontract-unparseable: broken-themis.toml: "} {
		if d.State != DriftFound || !strings.Contains(d.Report, want) {
			t.Errorf("stale render and two bad files lack %q: %+v", want, d)
		}
	}

	have["foreign.txt"] = []byte("a person's file")
	d = CheckDrift(contracts, have, "prime", nil)
	if d.State != DriftFound || !strings.Contains(d.Report, "foreign.txt is not the composer's") || !strings.Contains(d.Report, "\ncontract-unparseable: also-bad.toml") {
		t.Errorf("a foreign file and a bad contract are both named: %+v", d)
	}
}

// A HYPHENATED STAR COMPOSES AND IS COMPARED WHEN THE ROSTER IS GIVEN.
func TestCheckDriftReadsHyphenatedStarsAgainstTheRoster(t *testing.T) {
	stars := map[string]bool{"blade-runner": true, "poseidon": true}
	contracts := map[string][]byte{"blade-runner-poseidon.toml": []byte(contractBody)}
	cs, err := ContractsOf(contracts, stars)
	if err != nil {
		t.Fatal(err)
	}
	have := Files("prime", Compose(cs))
	if d := CheckDrift(contracts, have, "prime", stars); d.State != DriftNone {
		t.Errorf("with the roster: %+v", d)
	}
	if d := CheckDrift(contracts, have, "prime", nil); d.State != DriftFound || !strings.Contains(d.Report, "contract-unparseable: blade-runner-poseidon.toml") {
		t.Errorf("without it the file is its own finding: %+v", d)
	}
}

// ParseAll answers the good and the bad apart, each bad file named once, in
// name order; JoinUnreadable is every reason, or nil.
func TestParseAllSeparatesTheUnreadable(t *testing.T) {
	good, bad, err := ParseAll(map[string][]byte{
		"urania-themis.toml": []byte(contractBody),
		"z-bad.toml":         []byte("version = 1"),
		"a-bad.toml":         []byte("x"),
		"README.md":          []byte("not a contract"),
	}, nil)
	if err != nil || len(good) != 1 || good[0].Name != "urania-themis" || len(bad) != 2 || bad[0].File != "a-bad.toml" || bad[1].File != "z-bad.toml" {
		t.Fatalf("good %+v bad %+v err %v", good, bad, err)
	}
	joined := JoinUnreadable(bad)
	if joined == nil || !strings.Contains(joined.Error(), "a-bad.toml") || !strings.Contains(joined.Error(), "; z-bad.toml") {
		t.Errorf("joined %v", joined)
	}
	if JoinUnreadable(nil) != nil {
		t.Error("nothing unreadable is no error")
	}
	if _, _, err := ParseAll(map[string][]byte{"README.md": nil}, nil); !errors.Is(err, ErrNoContract) {
		t.Errorf("no .toml: %v", err)
	}
	if UnreadableReport(nil) != "" {
		t.Error("nothing unreadable reports nothing")
	}
	if got := UnreadableReport(bad[:1]); !strings.HasPrefix(got, "\ncontract-unparseable: a-bad.toml: ") {
		t.Errorf("report %q", got)
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

// A DIRECTORY WITH NO CONTRACT SAYS SO, AND SAYS WHICH: the error names the
// directory and is still ErrNoContract underneath.
func TestReadContractsOfAnEmptyDirectoryIsErrNoContract(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadContracts(dir, nil)
	if !errors.Is(err, ErrNoContract) || !strings.HasPrefix(err.Error(), dir+": ") {
		t.Fatalf("err %v", err)
	}
}
