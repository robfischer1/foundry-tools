package orbitcompose

import (
	"slices"
	"strings"
	"testing"
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
	p, c, err := SplitName("/x/orbits/urania-themis.toml")
	if err != nil || p != "urania" || c != "themis" {
		t.Fatalf("got %q %q %v", p, c, err)
	}
}

func TestSplitNameRefusesAnythingButTwoStars(t *testing.T) {
	for _, f := range []string{"chaos.toml", "a-b-c.toml", "Chaos-themis.toml", "chaos-.toml", "-themis.toml", "chaos-1x.toml"} {
		if _, _, err := SplitName(f); err == nil || !strings.Contains(err.Error(), "<producer>-<consumer>") {
			t.Errorf("%s: err %v, want the naming refusal", f, err)
		}
	}
}

func TestParseContractReadsTheTopLevelVerbsSortedOnce(t *testing.T) {
	c, err := ParseContract("urania-themis.toml", []byte(contractBody))
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
		if _, err := ParseContract("urania-themis.toml", []byte(tc.body)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err %v, want it to name %q", name, err, tc.want)
		}
	}
	if _, err := ParseContract("urania.toml", []byte(contractBody)); err == nil {
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
