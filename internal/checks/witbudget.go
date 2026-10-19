package checks

import (
	"fmt"
	"maps"
	"slices"

	"github.com/BurntSushi/toml"
)

// THE GUEST SIZE BUDGET (Rob, 2026-10-18: retire F23 "rule 5", no new serde_json
// on a wasm guest, and measure the constraint it stood in for). The constraint
// is bytes: every guest ships inlined in the stellar-core-ts npm package, which
// the fleet registry takes only under its publish limit. So the tree commits a
// byte budget per world and rust:wit-guest holds each built core module to it. A
// deliberate increase raises the budget in the same pull, where review sees it.
//
// The file is optional: a tree without it is graded as before and the pass logs
// each world's size. A tree that carries it must budget every world it declares
// (a new guest states its size the day it lands) and nothing it does not.

// WitGuestBudgetsFile is the budget file at the tree's root.
const WitGuestBudgetsFile = "wit-guest-budgets.toml"

// witGuestBudgets is the file's shape: [budget] maps each wit-guest-<world>
// feature to the most bytes its wasm32 core module may weigh.
type witGuestBudgets struct {
	Budget map[string]int64 `toml:"budget"`
}

// ParseWitGuestBudgets reads the budget file. An unknown key, a missing
// [budget] table or a budget that is not a positive byte count is an error.
func ParseWitGuestBudgets(src []byte) (map[string]int64, error) {
	var b witGuestBudgets
	md, err := toml.Decode(string(src), &b)
	if err != nil {
		return nil, fmt.Errorf("not TOML: %w", err)
	}
	if un := md.Undecoded(); len(un) > 0 {
		return nil, fmt.Errorf("unknown key %s", un[0])
	}
	if len(b.Budget) == 0 {
		return nil, fmt.Errorf("no [budget] table, or an empty one")
	}
	for _, w := range slices.Sorted(maps.Keys(b.Budget)) {
		if b.Budget[w] <= 0 {
			return nil, fmt.Errorf("budget.%s is %d, want a positive byte count", w, b.Budget[w])
		}
	}
	return b.Budget, nil
}

// WitGuestSizeLine is one world's size as the atom logs it, with its budget
// and headroom when it has one (budget 0: none).
func WitGuestSizeLine(world string, size, budget int64) string {
	if budget <= 0 {
		return fmt.Sprintf("size %s: %d bytes (no budget)", world, size)
	}
	return fmt.Sprintf("size %s: %d bytes of %d budget (%d headroom)", world, size, budget, budget-size)
}

// WitGuestBudgetFindings answers one line per breach, in world order: a module
// over its budget (with the delta and the percentage over), a world the file
// does not budget, and a budget naming a world the manifest does not declare.
// Empty means every world is within budget.
func WitGuestBudgetFindings(sizes, budgets map[string]int64) []string {
	var out []string
	for _, w := range slices.Sorted(maps.Keys(sizes)) {
		size := sizes[w]
		budget, ok := budgets[w]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("%s: %d bytes and no budget - add budget.%s to %s", w, size, w, WitGuestBudgetsFile))
		case size > budget:
			over := size - budget
			out = append(out, fmt.Sprintf("%s: %d bytes, budget %d, OVER BY %d bytes (+%.1f%%) - shrink the guest, or raise budget.%s in %s in this pull",
				w, size, budget, over, 100*float64(over)/float64(budget), w, WitGuestBudgetsFile))
		}
	}
	for _, w := range slices.Sorted(maps.Keys(budgets)) {
		if _, ok := sizes[w]; !ok {
			out = append(out, fmt.Sprintf("%s: budgeted in %s, but Cargo.toml declares no such world - drop the entry", w, WitGuestBudgetsFile))
		}
	}
	return out
}
