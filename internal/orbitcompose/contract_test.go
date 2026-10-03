package orbitcompose

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

const contractBody = `# a comment the composer drops
version = "1"
status = "generated"
wire_form = "native"
verbs = ["shape_for", "neighbors", "shape_for"]

[witness]
calls = 3
verbs = ["shape_for"]
`

func TestSplitNameIsProducerThenConsumer(t *testing.T) {
	p, c, err := SplitName("/x/orbits/urania-themis.toml", nil)
	if err != nil || p != "urania" || c != "themis" {
		t.Fatalf("got %q %q %v", p, c, err)
	}
}

func TestSplitNameRefusesAnythingButTwoStars(t *testing.T) {
	for _, f := range []string{"chaos.toml", "a-b-c.toml", "Chaos-themis.toml", "chaos-.toml", "-themis.toml", "chaos-1x.toml"} {
		if _, _, err := SplitName(f, nil); err == nil || !strings.Contains(err.Error(), "<producer>-<consumer>") {
			t.Errorf("%s: err %v, want the naming refusal", f, err)
		}
	}
}

var rosterStars = map[string]bool{
	"blade-runner": true, "ergo-recorder": true, "stellar-core-go": true, "stellar-core": true,
	"poseidon": true, "athena": true, "urania": true, "themis": true, "go": true,
}

// A NAME SPLITS AGAINST THE ROSTER: the one reading where both halves are
// stars, hyphens and all.
func TestSplitNameSplitsAgainstTheRoster(t *testing.T) {
	for file, want := range map[string]string{
		"/x/orbits/blade-runner-poseidon.toml": "blade-runner poseidon",
		"poseidon-blade-runner.toml":           "poseidon blade-runner",
		"ergo-recorder-athena.toml":            "ergo-recorder athena",
		"stellar-core-go-athena.toml":          "stellar-core-go athena",
		"athena-stellar-core-go.toml":          "athena stellar-core-go",
		"blade-runner-ergo-recorder.toml":      "blade-runner ergo-recorder",
		"urania-themis.toml":                   "urania themis",
		"stellar-core-go.toml":                 "stellar-core go",
		"athena-ghost.toml":                    "athena ghost",
		"ghost-athena.toml":                    "ghost athena",
	} {
		p, c, err := SplitName(file, rosterStars)
		if err != nil || p+" "+c != want {
			t.Errorf("%s: got %q %q %v, want %s", file, p, c, err, want)
		}
	}
}

// ZERO READINGS IS THE EXISTING REFUSAL, with or without a roster.
func TestSplitNameWithNoReadingIsTheExistingRefusal(t *testing.T) {
	for _, f := range []string{"blade-runner-ghost.toml", "ghost-blade-runner.toml", "Blade-runner-poseidon.toml", "a-b-c.toml"} {
		for _, stars := range []map[string]bool{rosterStars, nil} {
			if _, _, err := SplitName(f, stars); err == nil || !strings.Contains(err.Error(), "<producer>-<consumer>") {
				t.Errorf("%s (roster %v): err %v, want the naming refusal", f, stars != nil, err)
			}
		}
	}
	// a split must be AT a hyphen: two roster stars that merely abut are no pair
	if _, _, err := SplitName("abcd.toml", map[string]bool{"ab": true, "d": true}); err == nil {
		t.Error("a name with no hyphen split at a letter")
	}
	// without the roster a hyphenated star has no reading at all
	if _, _, err := SplitName("blade-runner-poseidon.toml", nil); err == nil {
		t.Error("a hyphenated star split with no roster")
	}
	// a roster entry that is not a star name is never a party
	if _, _, err := SplitName("Bad-x.toml", map[string]bool{"Bad": true, "x": true}); err == nil {
		t.Error("an uppercase roster entry became a party")
	}
	if _, _, err := SplitName("a-1x-b.toml", map[string]bool{"a": true, "1x-b": true}); err == nil {
		t.Error("a roster entry that starts with a digit became a party")
	}
}

// TWO READINGS IS AN ERROR NAMING BOTH.
func TestSplitNameAmbiguousNamesBothReadings(t *testing.T) {
	stars := map[string]bool{"a": true, "a-b": true, "b-c": true, "c": true}
	_, _, err := SplitName("a-b-c.toml", stars)
	if err == nil || !strings.Contains(err.Error(), "a->b-c") || !strings.Contains(err.Error(), "a-b->c") || !strings.Contains(err.Error(), "a-b-c.toml") {
		t.Fatalf("err %v", err)
	}
	// one of the two halves off the roster leaves one reading
	delete(stars, "c")
	if p, c, err := SplitName("a-b-c.toml", stars); err != nil || p != "a" || c != "b-c" {
		t.Errorf("got %q %q %v", p, c, err)
	}
}

// A HYPHENATED CONTRACT PARSES, and is named for the pair.
func TestParseContractReadsAHyphenatedStar(t *testing.T) {
	c, err := ParseContract("blade-runner-poseidon.toml", []byte(contractBody), rosterStars)
	if err != nil || c.Producer != "blade-runner" || c.Consumer != "poseidon" || c.Name != "blade-runner-poseidon" {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := ParseContract("blade-runner-poseidon.toml", []byte(contractBody), nil); err == nil {
		t.Error("no roster, no hyphenated star")
	}
}

func TestParseContractReadsTheTopLevelVerbsSortedOnce(t *testing.T) {
	c, err := ParseContract("urania-themis.toml", []byte(contractBody), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := Contract{
		Name: "urania-themis", Producer: "urania", Consumer: "themis",
		Version: "1", Status: "generated", WireForm: "native",
		Verbs: []string{"neighbors", "shape_for"},
	}
	if c.Name != want.Name || c.Producer != want.Producer || c.Consumer != want.Consumer ||
		c.Version != want.Version || c.Status != want.Status || c.WireForm != want.WireForm ||
		!slices.Equal(c.Verbs, want.Verbs) {
		t.Errorf("got %+v\nwant %+v", c, want)
	}
}

func TestParseContractRefusesWhatTheReaderWouldTripOn(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"integer version": {strings.Replace(contractBody, `version = "1"`, "version = 1", 1), "does not parse"},
		"no version":      {strings.Replace(contractBody, `version = "1"`, "", 1), "version"},
		"blank status":    {strings.Replace(contractBody, `status = "generated"`, `status = ""`, 1), "status"},
		"no wire form":    {strings.Replace(contractBody, `wire_form = "native"`, "", 1), "wire_form"},
		"quoted status":   {strings.Replace(contractBody, `status = "generated"`, `status = "gen\"x"`, 1), "status"},
		"no verbs":        {strings.Replace(contractBody, `verbs = ["shape_for", "neighbors", "shape_for"]`, "verbs = []", 1), "no verbs"},
		"odd verb":        {strings.Replace(contractBody, `"neighbors"`, `"two words"`, 1), `verb "two words"`},
		"not toml":        {"verbs = [", "does not parse"},
	} {
		if _, err := ParseContract("urania-themis.toml", []byte(tc.body), nil); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err %v, want it to name %q", name, err, tc.want)
		}
	}
	var perr toml.ParseError
	if _, err := ParseContract("urania-themis.toml", []byte("verbs = ["), nil); !errors.As(err, &perr) {
		t.Errorf("the decoder's error is not carried: %v", err)
	}
	if _, err := ParseContract("urania.toml", []byte(contractBody), nil); err == nil {
		t.Error("a badly named file parsed")
	}
}

func TestSortedSetLeavesItsInputAlone(t *testing.T) {
	in := []string{"b", "a", "b"}
	if got := sortedSet(in); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("got %v", got)
	}
	if !slices.Equal(in, []string{"b", "a", "b"}) {
		t.Errorf("input changed to %v", in)
	}
}
