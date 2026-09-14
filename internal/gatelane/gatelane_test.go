package gatelane

import (
	"encoding/json"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// The stamp is "<attester>:<tree>" with no colon before the tree, which is
// the only shape the join's StampAgrees accepts.
func TestTheClientIDStampsTheTreeAfterTheFirstColon(t *testing.T) {
	tree := strings.Repeat("a", 40)
	for _, star := range []string{"ares", "odd:name"} {
		id := ClientID(star, tree)
		before, after, ok := strings.Cut(id, ":")
		if !ok || after != tree || !strings.HasPrefix(before, "ca-gate/") {
			t.Errorf("ClientID(%q) = %q", star, id)
		}
	}
}

// The receipt carries exactly the fields the join reads, and no svid of its
// own: hadescall sets that from the SVID it holds.
func TestTheReceiptIsTheJoinsShape(t *testing.T) {
	b, err := json.Marshal(Receipt{Tree: "t", ModulePin: "p", ClientID: "ca-gate/x:t", Verdict: []checks.Verdict{{Atom: "a", State: 0, Result: "pass"}}})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"tree", "module_pin", "client_id", "verdict"} {
		if _, ok := got[k]; !ok {
			t.Errorf("the receipt lacks %s: %s", k, b)
		}
	}
	if _, ok := got["svid"]; ok {
		t.Errorf("the receipt names its own svid: %s", b)
	}
}

func TestACannotRunVectorIsOneAtomAtStateTwo(t *testing.T) {
	v := CannotRunVector("gate", "", strings.Repeat("x", 2000))
	if len(v) != 1 || v[0].Atom != "gate" || v[0].Stage != "prepush" || v[0].State != CouldNotRun || v[0].Result != "cannot-run" || len(v[0].Reason) != 1200 {
		t.Fatalf("%+v", v)
	}
	if v := CannotRunVector("mutation", "mutation", "why"); v[0].Stage != "mutation" || v[0].Reason != "why" {
		t.Fatalf("%+v", v)
	}
	if Worst(v) != CouldNotRun {
		t.Fatal("a cannot-run vector is not its own worst state")
	}
}

func TestTheVectorParsesOrIsRefused(t *testing.T) {
	v, err := ParseVector(`[{"atom":"go:vet","stage":"precommit","lane":"go","state":1,"result":"findings","reason":"x"}]`)
	if err != nil || len(v) != 1 || v[0].State != 1 {
		t.Fatalf("%+v %v", v, err)
	}
	if _, err := ParseVector(`"not a vector"`); err == nil || !strings.Contains(err.Error(), `"not a vector"`) {
		t.Fatalf("a string parsed as a vector, or the refusal did not quote it: %v", err)
	}
}

// The worst state wins, and a state the door cannot read counts as 2.
func TestTheWorstStateIsTheExit(t *testing.T) {
	for _, c := range []struct {
		states []int
		want   int
	}{
		{[]int{0, 0}, Clean},
		{[]int{0, 1, 0}, Findings},
		{[]int{1, 0}, Findings},
		{[]int{1, 2}, CouldNotRun},
		{[]int{2, 1}, CouldNotRun},
		{[]int{0, 3}, CouldNotRun},
		{[]int{0, -1}, CouldNotRun},
		{nil, Clean},
	} {
		var v []checks.Verdict
		for _, s := range c.states {
			v = append(v, checks.Verdict{State: s})
		}
		if got := Worst(v); got != c.want {
			t.Errorf("Worst(%v) = %d, want %d", c.states, got, c.want)
		}
	}
}

func TestTheSummaryCountsAndNamesTheRedAtoms(t *testing.T) {
	s := Summary([]checks.Verdict{
		{Atom: "go:vet", State: 0},
		{Atom: "go:build", State: 0},
		{Atom: "go:test-race", State: 1, Reason: "FAIL TestX"},
		{Atom: "fleet:witness", State: 2, Reason: "no base"},
		{Atom: "fleet:odd", State: 3, Reason: strings.Repeat("r", 400)},
	})
	for _, want := range []string{"5 atom(s): 2 pass, 1 findings, 2 cannot-run", "go:test-race state=1 FAIL TestX", "fleet:witness state=2 no base", "fleet:odd state=3 " + strings.Repeat("r", 300)} {
		if !strings.Contains(s, want) {
			t.Errorf("summary lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "go:vet") || strings.Contains(s, strings.Repeat("r", 301)) {
		t.Errorf("a passing atom was named, or a reason was not cut at 300:\n%s", s)
	}
	if got := Summary(nil); got != "0 atom(s): 0 pass, 0 findings, 0 cannot-run" {
		t.Errorf("an empty summary: %q", got)
	}
}

func TestTheAttestationAnswerFolds(t *testing.T) {
	for _, c := range []struct {
		status int
		body   string
		want   Outcome
		why    string
	}{
		{200, `{"ok":true}`, Landed, "attested"},
		{403, "forbidden: unidentifiable caller", Refused, "DERIVE a principal"},
		{403, `"tartarus_attest_emit" is not granted`, Refused, "POLICY refused"},
		{503, "produce hold-down: retry", HeldDown, "held down"},
		{500, "produce hold-down: retry", HeldDown, "held down"},
		{499, "produce hold-down: retry", Refused, "HTTP 499"},
		{503, "service unavailable", Refused, "HTTP 503"},
		{502, "bad gateway", Refused, "HTTP 502"},
		{401, "", Refused, "HTTP 401"},
	} {
		got, why := Attested(c.status, c.body)
		if got != c.want || !strings.Contains(why, c.why) {
			t.Errorf("%d %q: %v %q, want %v containing %q", c.status, c.body, got, why, c.want, c.why)
		}
	}
}
