package orbitlane

import (
	"slices"
	"strings"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/orbitcompose"
)

// orbit:contracts — the contract directory is well formed. Runs on the tree
// that holds the contracts (foundry-dies), on every pull to it.

// The statuses a contract may carry. Moving one to approved is Rob's edit.
var statuses = []string{"generated", "approved"}

// Teach is the authoring path every orbit finding ends with: what a session
// does about it.
const Teach = "the contract is foundry-dies/orbits/<producer>-<consumer>.toml; edit it in a foundry-dies pull (orbits/README.md, \"Who writes these\")"

// Contracts judges every contract file in files (name -> bytes, the
// directory's whole content) against the roster of star names, and each
// approved contract against base, the same directory on the branch the pull
// merges into: an approved contract whose verbs moved must move its version
// with them, so an approved surface cannot change silently.
//
// One file per ordered pair is the directory's own guarantee (a file name
// names the pair); what this adds is that the name parses as two stars on the
// roster, that the body parses as the composer reads it (version, status,
// wire_form and non-empty, well-formed verbs — orbitcompose.ParseContract),
// and that the status is one the ladder knows.
func Contracts(files, base map[string][]byte, roster map[string]bool) []checks.Finding {
	var names []string
	for n := range files {
		if strings.HasSuffix(n, ".toml") {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	if len(names) == 0 {
		return []checks.Finding{finding(checks.VerdictViolated, "orbits/", "no-contracts",
			"the contracts directory holds no <producer>-<consumer>.toml — every die built from it would be empty")}
	}
	var out []checks.Finding
	for _, n := range names {
		out = append(out, contract(n, files[n], base[n], roster))
	}
	return out
}

// contract judges one file.
func contract(name string, raw, baseRaw []byte, roster map[string]bool) checks.Finding {
	subject := "orbits/" + name
	c, err := orbitcompose.ParseContract(name, raw)
	if err != nil {
		return finding(checks.VerdictViolated, subject, "contract-unparseable", err.Error()+"; "+Teach)
	}
	if !slices.Contains(statuses, c.Status) {
		return finding(checks.VerdictViolated, subject, "status-unknown",
			"status "+c.Status+" is not one of generated, approved")
	}
	for _, star := range []string{c.Producer, c.Consumer} {
		if !roster[star] {
			return finding(checks.VerdictViolated, subject, "party-not-on-roster",
				star+" is not on the fleet roster (fleet/stars/"+star+"/data.json)")
		}
	}
	if c.Status == "approved" && baseRaw != nil {
		if was, err := orbitcompose.ParseContract(name, baseRaw); err == nil &&
			!slices.Equal(was.Verbs, c.Verbs) && was.Version == c.Version {
			return finding(checks.VerdictViolated, subject, "approved-verbs-moved",
				"an approved contract's verbs changed ("+strings.Join(was.Verbs, ",")+" -> "+strings.Join(c.Verbs, ",")+
					") and its version stayed "+c.Version+" — raise version in the same pull")
		}
	}
	return finding(checks.VerdictHolds, subject, "contract", c.Status+" v"+c.Version+", "+
		strings.Join(c.Verbs, ","))
}
