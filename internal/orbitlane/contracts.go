package orbitlane

import (
	"slices"
	"strings"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/orbitcompose"
)

// orbit:contracts — the contract directory is well formed. Runs on the tree
// that holds the contracts (foundry-dies), on every pull to it.

// The statuses a contract may carry. The ladder is retired (F15 cutover,
// 2026-10-06): every Contract fact renders approved, so approved is the only
// status. A generated file is a hand edit or a stale generator, and is refused.
var statuses = []string{"approved"}

// Teach is the authoring path every orbit finding ends with: what a session
// does about it.
const Teach = "the contract is a fact on graph:nyx, rendered to foundry-dies/orbits/<producer>-<consumer>.toml; declare or retire it with nyx_declare_contract / nyx_retire_contract, then re-render with `nyx facts render --dies . --apply` (orbits/README.md, \"Who writes these\"); never hand-edit the file"

// Contracts judges every contract file in files (name -> bytes, the
// directory's whole content) against the roster of star names. A contract's
// version is the seam's last chaos tx, so it moves with the facts and there is
// no version rule to check here.
//
// One file per ordered pair is the directory's own guarantee (a file name
// names the pair); what this adds is that the name parses as two stars on the
// roster, that the body parses as the composer reads it (version, status,
// and non-empty, well-formed verbs — orbitcompose.ParseContract),
// and that the status is approved, the only one a rendered fact carries.
func Contracts(files map[string][]byte, roster map[string]bool) []checks.Finding {
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
		out = append(out, contract(n, files[n], roster))
	}
	return out
}

// Unreadable is one contract-unparseable finding per file the composer could
// not read, so a bad file is a finding about that file in every lane that
// composes the directory, never the directory's could-not-run.
func Unreadable(bad []orbitcompose.Unreadable) []checks.Finding {
	out := make([]checks.Finding, len(bad))
	for i, b := range bad {
		out[i] = unparseable(b.File, b.Err)
	}
	return out
}

// unparseable is the finding for the contract file name that did not parse.
func unparseable(name string, err error) checks.Finding {
	return finding(checks.VerdictViolated, "orbits/"+name, "contract-unparseable", err.Error()+"; "+Teach)
}

// contract judges one file.
func contract(name string, raw []byte, roster map[string]bool) checks.Finding {
	subject := "orbits/" + name
	c, err := orbitcompose.ParseContract(name, raw, roster)
	if err != nil {
		return unparseable(name, err)
	}
	if !slices.Contains(statuses, c.Status) {
		return finding(checks.VerdictViolated, subject, "status-unknown",
			"status "+c.Status+" is not approved (the only status; the ladder is retired); "+Teach)
	}
	for _, star := range []string{c.Producer, c.Consumer} {
		// A CI lane consumer (job.<lane> or ci.<lane>) is no star and is on no roster.
		if !roster[star] && !orbitcompose.IsLane(star) {
			return finding(checks.VerdictViolated, subject, "party-not-on-roster",
				star+" is not on the fleet roster (fleet/repos/"+star+".json)")
		}
	}
	return finding(checks.VerdictHolds, subject, "contract", c.Status+" v"+c.Version+", "+
		strings.Join(c.Verbs, ","))
}
