package checks

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// The dies lane's JUDGEMENTS, as pure functions over strings and exit codes.
//
// What used to be a `case` on opa's exit codes, a `sed -n` over its summary
// line and three `python3 -c` one-liners reading JSON is here instead, where a
// table test can hold each one. Nothing in package main is testable — dag
// panics without an engine — so every decision an atom makes about what a tool
// SAID lives on this side of the boundary.

// OpaVersionOK reads `opa version` and answers whether the binary IS the pin.
//
// THE PIN IS PART OF THE QUESTION. Rego's semantics are a property of the
// binary: a suite written for v1 and graded by another major answers a
// different question, and "the policy suite passed" would then be a true
// statement about the wrong language. foundry-dies pinned 1.18.0 by hand in
// its own workflow (since retired); the pin moved into
// OpaVersion and this is what proves the binary on PATH honours it.
//
// The match is a WHOLE LINE, which is what the shell's `grep -qx` meant: opa
// prints several `Key: value` lines and "Version: 1.18.0" must be one of them
// exactly, so 1.18.0-rc1 is not 1.18.0.
func OpaVersionOK(versionOutput, want string) bool {
	for _, line := range strings.Split(versionOutput, "\n") {
		if strings.TrimSpace(line) == "Version: "+want {
			return true
		}
	}
	return false
}

var opaPassRE = regexp.MustCompile(`^PASS: ([0-9]+)/[0-9]+$`)

// OpaTestState maps an `opa test policy/ -v` run to this module's three states.
//
// A ZERO-TEST RUN IS REFUSED, which the workflow did not do and this module
// cannot skip: `opa test` over a policy tree containing no test at all exits 0
// (measured against an empty directory, 2026-09-10). That renders as a clean
// suite and is not one — it is opengrep matching zero files wearing different
// clothes, and it gets the same answer. The count comes off opa's own last
// `PASS: n/m` summary line; no line, or n == 0, is CANNOT RUN.
//
// OPA'S OWN CODES ARE THREE-VALUED TOO, AND THEY DO NOT LINE UP WITH THIS
// MODULE'S: a failing assertion is exit 2 and a rego parse error is exit 1
// (both measured). Passing either through StateFor would file a real finding as
// CANNOT RUN, so both fold to 1 here and only a code opa does not use becomes a
// 2.
func OpaTestState(code int, out string) (state int, reason string) {
	switch code {
	case 0:
		n, ok := opaPassCount(out)
		if !ok || n == 0 {
			return 2, "REFUSING a zero-test run. opa test exits 0 over a policy tree with no assertion in it, which renders as a clean suite and is not one - nothing was examined."
		}
		return 0, fmt.Sprintf("%d assertion(s) pass", n)
	case 1, 2:
		return 1, "the rego suite did not come back clean - opa exits 1 for a load error and 2 for a failing assertion, and both are findings about the policy rather than about the run."
	default:
		return 2, fmt.Sprintf("opa exited %d, which is neither a clean suite (0), a load error (1) nor a failing assertion (2).", code)
	}
}

// opaPassCount reads the LAST `PASS: n/m` line, which is what `sed -n … | tail
// -1` meant: `-v` prints a line per file and the summary is the final one.
func opaPassCount(out string) (int, bool) {
	n, ok := 0, false
	for _, line := range strings.Split(out, "\n") {
		m := opaPassRE.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		v := 0
		if _, err := fmt.Sscanf(m[1], "%d", &v); err != nil {
			continue
		}
		n, ok = v, true
	}
	return n, ok
}

// opaEval is the shape `opa eval --format json` answers in: one result, one
// expression, one value. Anything else is a shape this lane cannot read.
type opaEval struct {
	Result []struct {
		Expressions []struct {
			Value json.RawMessage `json:"value"`
		} `json:"expressions"`
	} `json:"result"`
}

func opaEvalValue(doc string) (json.RawMessage, error) {
	var e opaEval
	if err := json.Unmarshal([]byte(doc), &e); err != nil {
		return nil, err
	}
	if len(e.Result) == 0 || len(e.Result[0].Expressions) == 0 || len(e.Result[0].Expressions[0].Value) == 0 {
		// An undefined rule answers `{}` — no result key at all — and that is
		// not a deny set of zero. It is an expression that did not evaluate.
		return nil, fmt.Errorf("no result[0].expressions[0].value in the eval output")
	}
	return e.Result[0].Expressions[0].Value, nil
}

// OpaDenySet reads the deny set `opa eval data.admission.deny --format json`
// answered, as text a human can read.
//
// A SHAPE THIS ATOM CANNOT READ IS AN ERROR, NOT AN EMPTY SET. The value is the
// atom's whole content — "the domain admitted THIS input" — and an unreadable
// answer means no claim can be made; reporting zero denies from one would make
// the claim anyway. The old body said the same thing about jq: "a shape this
// atom cannot read is a 2 rather than a deny count nobody computed."
//
// A deny is usually a string but the domain is free to yield an object; a
// non-string element is re-rendered as compact JSON rather than dropped.
func OpaDenySet(doc string) ([]string, error) {
	raw, err := opaEvalValue(doc)
	if err != nil {
		return nil, err
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("the deny value is not a set: %w", err)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		var s string
		if err := json.Unmarshal(item, &s); err == nil {
			out = append(out, s)
			continue
		}
		out = append(out, strings.TrimSpace(string(item)))
	}
	return out, nil
}

// OpaAllowed reads the verb list `data.authz.visible.allowed` answered for one
// probe principal. Every element must be a verb; a shape that is not a list of
// strings is an error, for OpaDenySet's reason.
func OpaAllowed(doc string) ([]string, error) {
	raw, err := opaEvalValue(doc)
	if err != nil {
		return nil, err
	}
	var verbs []string
	if err := json.Unmarshal(raw, &verbs); err != nil {
		return nil, fmt.Errorf("the allowed value is not a list of verbs: %w", err)
	}
	return verbs, nil
}

// DiesRequiredDataRoots are the documents the policy actually reads. They are
// asserted PRESENT and NON-EMPTY, by name, because an empty or partial data
// document is the silent fail-open the bundle gate exists to catch.
var DiesRequiredDataRoots = []string{"authz_audience", "authz_grants", "authz_meta", "path_grants", "subject_aliases"}

// DiesDataKeys grades the BUILT bundle's data.json: which required roots are
// missing or empty, and how many stars the star_only roster carried through the
// build.
//
// THE SOURCE TREE IS NOT A PROXY FOR THE ARTIFACT, which is the whole reason
// this gate exists. `opa test policy/` passes on a tree whose BUILT BUNDLE is
// empty: directory (--data) mode loads every *.json and merges by top-level
// key, while bundle mode reads ONLY files literally named data.json. A tree
// using arbitrary JSON names tests green and builds an artifact with
// data.json == {}, which makes star_only undefined, which makes the visibility
// comprehension collect nothing, which makes EVERY verb visible to EVERY
// principal. Fail-open, silent, and green the whole way down.
//
// EMPTY COUNTS AS MISSING, which is python's `not d.get(k)`: a root present as
// {} or [] is a root the policy will read nothing out of, and the defect above
// arrives exactly that way.
func DiesDataKeys(dataJSON string) (missing []string, stars int, err error) {
	var d map[string]json.RawMessage
	if err := json.Unmarshal([]byte(dataJSON), &d); err != nil {
		return nil, 0, err
	}
	for _, k := range DiesRequiredDataRoots {
		if !nonEmptyJSON(d[k]) {
			missing = append(missing, k)
		}
	}
	var audience struct {
		StarOnly map[string]json.RawMessage `json:"star_only"`
	}
	if raw, ok := d["authz_audience"]; ok {
		_ = json.Unmarshal(raw, &audience)
	}
	return missing, len(audience.StarOnly), nil
}

// nonEmptyJSON is python's truthiness over a JSON value: absent, null, {}, [],
// "", 0 and false are all "not there".
func nonEmptyJSON(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return false
	}
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case float64:
		return t != 0
	case string:
		return t != ""
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	}
	return true
}

// DiesCanary reads the canary verb OFF THE BUILT BUNDLE'S OWN ROSTER, and
// answers "" when chaos curates nothing.
//
// THE CANARY IS READ, NOT NAMED, and that is the post-mortem's own
// recommendation (2026-08-22). The first canary named graph_subscribe, a verb
// chaos retired in F2; the second named graph_nodes. A named canary goes stale
// the day its verb leaves the roster and the gate then goes red on a roster
// that is MORE correct — twice now. So the bundle's own data.json is asked for
// chaos's first star_only verb and THAT one is proved: whatever chaos curates
// first is, by construction, curated. chaos because it is the star with the
// largest curated surface — and an empty chaos row is itself the failure,
// because nothing curated means the roster did not survive the build.
func DiesCanary(dataJSON string) string {
	var d struct {
		AuthzAudience struct {
			StarOnly map[string][]string `json:"star_only"`
		} `json:"authz_audience"`
	}
	if err := json.Unmarshal([]byte(dataJSON), &d); err != nil {
		return ""
	}
	verbs := d.AuthzAudience.StarOnly["chaos"]
	if len(verbs) == 0 {
		return ""
	}
	return verbs[0]
}

// ContractFixtureVerdict grades ONE contracts fixture run: the line it prints
// and whether it is a failure of the gate.
//
// THE FIXTURES RUN FIRST AND MUST FAIL. The live check cannot prove the checker
// DETECTS anything while it is green, so nine fixtures must be caught and four
// controls must pass (the retiring pair and its control joined 2026-09-11 with
// check_contracts' `retiring`, pending's mirror for a member the authority
// dropped while a consumer still carries it). Without the controls the failure
// loop could be satisfied by a checker that simply fails everything — including
// a pending entry whose grounds genuinely still hold, which is a legitimate
// deferral. A gate that cannot fail is a gate that is not there.
func ContractFixtureVerdict(name string, expectFail bool, code int) (line string, bad bool) {
	passed := code == 0
	if expectFail {
		if passed {
			return "::error::fixture '" + name + "' PASSED - the checker no longer detects it", true
		}
		return "  fixture " + name + ": correctly detected", false
	}
	if passed {
		return "  control " + name + ": correctly passed", false
	}
	return "::error::control '" + name + "' FAILED - the checker invents divergence", true
}
