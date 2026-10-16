// Package diescanonical is the atom dies:canonical: every committed catalog entry is its own
// canonical form. It grades the FORM of fleet/repos/<name>.json in Go, with no
// program: each record re-emitted through checks.CanonicalJSON must come back
// byte for byte.
//
// WHY THE RECORD'S OWN REPO GRADES IT. The form was only ever enforced one repo
// away: hephaestus' internal/slag golden re-reads every committed record off
// /dies and reds when one diverges. Measured 2026-09-18, foundry-dies #270
// landed two records with a hand-placed `tools.build` block AFTER `tools.tofu`
// ("build" sorts before "calypso"); dies:schema passed, the pull landed, and
// hephaestus #120's mutation lane settled could-not-run on the golden two hours
// later: a red on the wrong repo for a defect this gate can see at the push. A
// record that does not parse is findings too: the schema atom names the shape,
// this one names the bytes.
package diescanonical

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/dieskit"
)

// Def is the atom.
var Def = atomkit.Def{
	ID: "dies:canonical", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupLanguage,
	Desc:      "Every committed fleet/repos/<name>.json catalog entry is byte-for-byte its own canonical form (sorted keys, two-space indent, ensure_ascii, trailing LF).",
	Surface:   "any:**",
	Toolchain: "none",
	Check:     check,
}

const id = "dies:canonical"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// check re-emits every record through the canonical form and compares bytes. A
// record that will not read is a 2: a shape the atom cannot read is a
// could-not-run, not a count nobody computed.
func check(_ context.Context, in atomkit.In) checks.Verdict {
	t := in.Tree()
	if stop := dieskit.Shape(t, row); stop != nil {
		return *stop
	}
	records := t.Glob("fleet/repos/*.json")
	if len(records) == 0 {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - fleet/repos/ carries no catalog entry, so there is no document whose form could be graded.")
	}
	sort.Strings(records)
	var findings []string
	for _, p := range records {
		committed, err := t.Read(p)
		if err != nil {
			return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - %s could not be read (%v).", id, p, err))
		}
		canon, err := checks.CanonicalJSON([]byte(committed))
		if err != nil {
			findings = append(findings, fmt.Sprintf("%s: not one JSON document (%v)", p, err))
			continue
		}
		if string(canon) != committed {
			findings = append(findings, p+": diverges from its canonical form — re-emit it with json.dumps(doc, sort_keys=True, indent=2) + \"\\n\" (keys sorted at every depth, two-space indent, non-ASCII escaped, one trailing LF)")
		}
	}
	if len(findings) > 0 {
		return checks.VerdictOf(row, int(checks.StateFindings), fmt.Sprintf("%s: FINDINGS - %d of %d catalog document(s) are not their own canonical form:\n%s", id, len(findings), len(records), strings.Join(findings, "\n")))
	}
	return checks.VerdictOf(row, int(checks.StatePass), fmt.Sprintf("%s: %d catalog document(s) are each their own canonical form.", id, len(records)))
}
