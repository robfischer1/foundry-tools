package checks

import (
	"slices"
	"strings"
	"testing"
)

func TestParseWitGuestBudgets(t *testing.T) {
	cases := []struct {
		name, src, err string
		want           map[string]int64
	}{
		{"ok", "# bytes\n[budget]\nwit-guest-tail = 140000\n", "", map[string]int64{"wit-guest-tail": 140000}},
		{"not toml", "[budget\n", "not TOML", nil},
		{"unknown key", "margin = 5\n[budget]\nwit-guest-tail = 1\n", "unknown key margin", nil},
		{"empty", "[budget]\n", "no [budget] table", nil},
		{"zero", "[budget]\nwit-guest-tail = 0\n", "budget.wit-guest-tail is 0", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseWitGuestBudgets([]byte(c.src))
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("err %v, want %q", err, c.err)
				}
				return
			}
			if err != nil || len(got) != len(c.want) || got["wit-guest-tail"] != c.want["wit-guest-tail"] {
				t.Fatalf("%v %v", got, err)
			}
		})
	}
}

func TestWitGuestBudgetFindingsListsEveryBreachInWorldOrder(t *testing.T) {
	sizes := map[string]int64{"wit-guest-b": 200, "wit-guest-a": 150, "wit-guest-c": 10}
	budgets := map[string]int64{"wit-guest-a": 100, "wit-guest-b": 200, "wit-guest-z": 5}
	got := WitGuestBudgetFindings(sizes, budgets)
	want := []string{
		"wit-guest-a: 150 bytes, budget 100, OVER BY 50 bytes (+50.0%)",
		"wit-guest-c: 10 bytes and no budget",
		"wit-guest-z: budgeted in wit-guest-budgets.toml, but Cargo.toml declares no such world",
	}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("line %d: %q, want prefix %q", i, got[i], want[i])
		}
	}
	if f := WitGuestBudgetFindings(map[string]int64{"wit-guest-a": 100}, map[string]int64{"wit-guest-a": 100}); !slices.Equal(f, nil) {
		t.Errorf("at budget is within: %v", f)
	}
}

func TestWitGuestSizeLine(t *testing.T) {
	if got := WitGuestSizeLine("wit-guest-a", 90, 100); got != "size wit-guest-a: 90 bytes of 100 budget (10 headroom)" {
		t.Error(got)
	}
	if got := WitGuestSizeLine("wit-guest-a", 90, 0); got != "size wit-guest-a: 90 bytes (no budget)" {
		t.Error(got)
	}
}
