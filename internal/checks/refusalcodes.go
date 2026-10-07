package checks

import (
	"slices"

	"github.com/BurntSushi/toml"
)

// RefusalRegistry is foundry-dies' fleet registry of refusal codes, and
// RefusalCheckerFiles are the four files dies:refusal-codes runs it with, in
// foundry-dies. A tree that is not foundry-dies has none of them, so the atom
// fetches them from the door rather than carrying a copy: the repo that owns the
// registry owns the rule about it (dies:contracts' argument).
const (
	RefusalRegistry = "contracts/refusal-codes.toml"
	RefusalChecker  = "tools/check_refusal_codes.py"
	// RefusalRepo is the door's name for the repo that owns all of them. The
	// owner is foundry, not rob: the door answers 404 for rob/foundry-dies, and that
	// 404 made every stellar-core pull could-not-run (opus-gate faraday37-037).
	RefusalRepo = "foundry/foundry-dies"
)

// RefusalCheckerFiles is the checker, the two modules it imports and the
// registry. check_refusal_codes imports check_contracts, which imports
// schema_stamp: a fetch that omitted either would fail on import, not on a code.
func RefusalCheckerFiles() []string {
	return []string{RefusalChecker, "tools/check_contracts.py", "tools/schema_stamp.py", RefusalRegistry}
}

// FirstRegistryUse answers the first declared use that reads from another repo,
// the file the door probe asks for: a checker that cannot reach the door has
// found nothing, and that is a could-not-run rather than a finding.
func FirstRegistryUse(registry string) (repo, path string, found bool) {
	var doc struct {
		Uses []struct {
			Source map[string]any `toml:"source"`
		} `toml:"uses"`
	}
	// A parse error leaves doc empty (the decoder fills nothing before it has
	// read the whole document), so an unparseable registry has no first use and
	// the checker is its judge: no branch to take.
	_, _ = toml.Decode(registry, &doc)
	for _, u := range doc.Uses {
		r, rok := u.Source["repo"].(string)
		p, pok := u.Source["path"].(string)
		if rok && pok {
			return r, p, true
		}
	}
	return "", "", false
}

// RefusalTreeFlag is the --tree argument that says THIS checkout is repo name:
// the checker reads that repo's uses from here and grades only them.
func RefusalTreeFlag(name string) string { return name + "=." }

// refusalCoreMarkers are the two paths that together say "stellar-core": the
// vendored result WIT every world uses, and the tapes the worlds' codes live on.
var refusalCoreMarkers = []string{"wit/aiws-result.wit", "conformance/tapes/*.json"}

// RefusalCoreMarkers returns a copy, so a caller cannot edit the pair.
func RefusalCoreMarkers() []string { return slices.Clone(refusalCoreMarkers) }
