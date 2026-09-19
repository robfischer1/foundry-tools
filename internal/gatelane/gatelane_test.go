package gatelane

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

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
