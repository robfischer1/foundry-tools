package checks

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
)

// CapabilitiesServedID is the gate's atom id.
const CapabilitiesServedID = "dies:capabilities-served"

// TreeReader is the two reads the gate needs, so the in-process runner and the
// Dagger chain grade through the SAME function and cannot disagree.
type TreeReader struct {
	// Read answers a file's contents.
	Read func(rel string) (string, error)
	// Glob answers the tracked paths matching a pattern.
	Glob func(pattern string) []string
}

// GradeCapabilitiesServed is dies:capabilities-served over a policy-die tree
// (the caller has already settled that it IS one).
//
// STATES. ABSENT (0) when neither the declared document nor a single export is
// there: the capabilities root has not landed, and there is nothing to
// disagree. FINDINGS (1) when declared and served differ in any way
// CapabilitiesDrift names, or a file cannot be believed (malformed, wrong
// schema, wrong star). CANNOT RUN (2) when a file that exists cannot be read:
// a comparison nobody made is not one that passed.
func GradeCapabilitiesServed(a AtomDef, tr TreeReader) Verdict {
	declaredPaths := tr.Glob(CapabilitiesData)
	servedPaths := tr.Glob(CapabilitiesServedDir + "/*.json")
	sort.Strings(servedPaths)
	// ABSENT MUST NOT OUTLIVE THE DATA. If the capabilities root lands anywhere
	// under policy/capabilities/ other than CapabilitiesData (a sibling file, a
	// nested data.json), an ABSENT keyed on the one path would pass forever while
	// the real declaration drifts. So any file there without CapabilitiesData is a
	// finding naming the path this gate reads, never an absence.
	stray := append(tr.Glob(CapabilitiesDir+"/*"), tr.Glob(CapabilitiesDir+"/*/*")...)
	if len(declaredPaths) == 0 && len(servedPaths) == 0 && len(stray) == 0 {
		return VerdictOf(a, int(StatePass), a.ID+": ABSENT - this tree carries neither "+CapabilitiesData+" nor a served-verbs export under "+CapabilitiesServedDir+"/, so no capability is declared or served to compare.")
	}
	cannot := func(format string, args ...any) Verdict {
		return VerdictOf(a, int(StateCannotRun), a.ID+": CANNOT RUN - "+fmt.Sprintf(format, args...))
	}

	var findings []string
	if len(declaredPaths) == 0 && len(stray) > 0 {
		sort.Strings(stray)
		findings = append(findings, fmt.Sprintf("%s/ carries %s but not %s, the document this gate reads - the declaration is somewhere it does not look", CapabilitiesDir, strings.Join(stray, ", "), CapabilitiesData))
	}
	var declared Capabilities
	if len(declaredPaths) > 0 {
		raw, err := tr.Read(CapabilitiesData)
		if err != nil {
			return cannot("%s could not be read (%v).", CapabilitiesData, err)
		}
		// Lenient on extra keys: the document will grow (tx, per-verb data) and a
		// gate about verb names must not turn red on a field it does not read.
		var top map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &declared); err != nil {
			findings = append(findings, fmt.Sprintf("%s: not a capabilities document (%v)", CapabilitiesData, err))
		} else if _ = json.Unmarshal([]byte(raw), &top); top["stars"] == nil || string(top["stars"]) == "null" {
			// A document without its one key declares nothing, and with no exports
			// committed it would pass as "0 verbs across 0 stars".
			findings = append(findings, fmt.Sprintf("%s: no \"stars\" object - a capabilities document that declares nothing is not one", CapabilitiesData))
		}
	}

	var served []Served
	for _, p := range servedPaths {
		raw, err := tr.Read(p)
		if err != nil {
			return cannot("%s could not be read (%v).", p, err)
		}
		stem := strings.TrimSuffix(path.Base(p), ".json")
		s, err := ParseServed(stem, []byte(raw))
		if err != nil {
			findings = append(findings, fmt.Sprintf("%s: %v", p, err))
			continue
		}
		served = append(served, s)
	}

	prefixes := map[string]string{}
	for _, p := range tr.Glob("fleet/repos/*.json") {
		raw, err := tr.Read(p)
		if err != nil {
			return cannot("%s could not be read (%v), so the star's route prefix is unknown.", p, err)
		}
		var rec struct {
			VerbPrefix string `json:"verb_prefix"`
		}
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			return cannot("%s is not JSON (%v), so the star's route prefix is unknown.", p, err)
		}
		prefixes[strings.TrimSuffix(path.Base(p), ".json")] = rec.VerbPrefix
	}

	findings = append(findings, CapabilitiesDrift(declared, served, prefixes)...)
	if len(findings) > 0 {
		sort.Strings(findings)
		return VerdictOf(a, int(StateFindings), fmt.Sprintf("%s: FINDINGS - declared capabilities differ from what the stars serve (%d):\n%s\nRe-render %s from the stars' exports; a served verb the document omits is denied to every caller.",
			a.ID, len(findings), strings.Join(findings, "\n"), CapabilitiesData))
	}
	verbs := 0
	for _, s := range served {
		verbs += len(s.Verbs)
	}
	return VerdictOf(a, int(StatePass), fmt.Sprintf("%s: %d verb(s) across %d star(s) are declared exactly as served.", a.ID, verbs, len(served)))
}
