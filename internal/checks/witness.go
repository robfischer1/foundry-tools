package checks

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// THE CODE WITNESS'S DECISIONS, in Go. They were foundry-stocks'
// ci/lib/gate/witness.py; the atom (atoms_fleet.go fleetWitness) reads the
// change set with git, asks narcissus over MCP, and settles here.
//
// WHAT IT FEEDS THE WITNESS: each changed .py / .go file's post-image, whole,
// at granularity=code. A language the witness has no analyzer for is SKIPPED
// and named; so is a VENDORED file (vendor/, node_modules/, third_party/) — a
// dependency's authorship, not the star's (chaos #23 and ourea #104 went red
// on exactly that, 2026-09-11). A file the witness reads and finds NO UNIT in
// has nothing to witness, clean. Could-not-run is reserved for a file the
// witness could not READ or was never asked about.
//
// WHAT COUNTS AS A FINDING is narcissus's own vocabulary (standard
// "block-capable", convention "advise", novel "free"). A canonical-class match
// or a duplicated Standard is a finding; a Convention is advisory and clean,
// because a trivial two-line function comes back "convention" against a corpus
// this size; novel is clean; refused (could not read) and unknown (an empty
// corpus: "not looked at") are could-not-consult for that file.

// WitnessURL is narcissus's plaintext MCP port. It answers any in-cluster
// caller (stellar_core DualListener), measured 2026-09-10 from a container on
// the fleet's dagger engine.
const WitnessURL = "http://narcissus.default.svc.cluster.local:8200/mcp"

// WitnessWorkers is how many files are asked about at once. One answer takes
// 30-40 s (narcissus's latency log, 2026-09-14); serial, athena's 44-file
// landing ran 20 minutes.
const WitnessWorkers = 4

// WitnessLanguage is the witness's language for a path, or "" when it has no
// analyzer for it.
func WitnessLanguage(p string) string {
	switch path.Ext(p) {
	case ".py":
		return "python"
	case ".go":
		return "go"
	}
	return ""
}

// WitnessVendored reports whether a path sits under a dependency's directory.
func WitnessVendored(p string) bool {
	parts := strings.Split(p, "/")
	for _, part := range parts[:len(parts)-1] {
		if part == "vendor" || part == "node_modules" || part == "third_party" {
			return true
		}
	}
	return false
}

// WitnessChangeSet splits a change set's paths into what is witnessed, what
// is skipped for want of an analyzer (.ts, .tsx, .rs, .js — named, not
// consulted) and what is vendored.
func WitnessChangeSet(paths []string) (sources, skipped, vendored []string) {
	for _, p := range paths {
		switch path.Ext(p) {
		case ".py", ".go":
			if WitnessVendored(p) {
				vendored = append(vendored, p)
			} else {
				sources = append(sources, p)
			}
		case ".ts", ".tsx", ".rs", ".js":
			skipped = append(skipped, p)
		}
	}
	return sources, skipped, vendored
}

// testPath is the fleet's one definition of a test path: stop_justifications'
// _TEST_GLOB. `tests/`, `test_x.py`, `conftest.py` are test code; `testing.py`
// and `latest.py` are not.
var testPath = regexp.MustCompile(`(^|/)(tests?|conftest)([./_*]|$)`)

// DefinesCanonical reports whether p is the module a canonical class's import
// path names: `stellar_core.classes.adapter.Adapter` names
// src/stellar_core/classes/adapter.py or stellar_core/classes/adapter.py. The
// path is read from the import's top-level package down, a qualname must
// remain after the module, and a package __init__ never qualifies.
func DefinesCanonical(p, importPath string) bool {
	if importPath == "" || !strings.HasSuffix(p, ".py") || path.Base(p) == "__init__.py" {
		return false
	}
	parts := strings.Split(strings.TrimSuffix(p, ".py"), "/")
	dotted := strings.Split(importPath, ".")
	for start, part := range parts {
		module := parts[start:]
		if part == dotted[0] && len(dotted) > len(module) && sameParts(dotted[:len(module)], module) {
			return true
		}
	}
	return false
}

func sameParts(a, b []string) bool {
	return strings.Join(a, "\x00") == strings.Join(b, "\x00")
}

// WitnessCanonical is the canonical class a finding matched, and the failure
// modes its checklist names.
type WitnessCanonical struct {
	Class      string   `json:"class"`
	Descriptor string   `json:"descriptor"`
	Footguns   []string `json:"footguns"`
}

// WitnessRow is the witness's answer about one file, folded.
type WitnessRow struct {
	Path      string
	Class     string // finding, advisory, clean, no-unit, canonical-source, canonical-reuse, canonical-in-test, could-not-consult
	Verdict   string // narcissus's verdict, "" when there was none
	Reason    string
	Canonical *WitnessCanonical
}

// WitnessEnvelope reads narcissus's HTTP answer to one tools/call into the
// call's RESULT, JSON-encoded. MCP over streamable HTTP answers a JSON body or
// an event stream whose last `data:` line carries the same envelope; a JSON-RPC
// error becomes an isError result. A non-200 status passes through.
func WitnessEnvelope(status int, contentType, body string) (int, string) {
	if status != 200 {
		return status, body
	}
	if strings.Contains(contentType, "text/event-stream") {
		last := ""
		for _, ln := range strings.Split(body, "\n") {
			if data, ok := strings.CutPrefix(strings.TrimRight(ln, "\r"), "data:"); ok {
				last = strings.TrimSpace(data)
			}
		}
		body = last
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal([]byte(body), &envelope) != nil {
		return 200, body
	}
	if e, ok := envelope["error"]; ok {
		text, _ := json.Marshal(string(e))
		return 200, `{"isError": true, "content": [{"type": "text", "text": ` + string(text) + `}]}`
	}
	if result, ok := envelope["result"]; ok {
		return 200, string(result)
	}
	return 200, "{}"
}

// ClassifyWitness folds the witness's answer about one file — an HTTP status
// and an MCP tools/call result, JSON-encoded — into its row.
func ClassifyWitness(p string, status int, raw string) WitnessRow {
	row := WitnessRow{Path: p, Class: "could-not-consult"}
	notAnAnswer := "narcissus returned a body that is not a witness answer — could not consult."
	if status != 200 {
		row.Reason = fmt.Sprintf("narcissus returned HTTP %d — could not consult.", status)
		return row
	}
	var answer struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text *string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal([]byte(raw), &answer) != nil || len(answer.Content) == 0 || answer.Content[0].Text == nil {
		row.Reason = notAnAnswer
		return row
	}
	text := *answer.Content[0].Text
	if answer.IsError {
		row.Reason = "the witness verb errored: " + runeCut(text, 200)
		return row
	}
	var body map[string]any
	if json.Unmarshal([]byte(text), &body) != nil {
		row.Reason = notAnAnswer
		return row
	}
	verdict, _ := body["verdict"].(string)
	row.Verdict = verdict
	recommendation := jsonText(body["recommendation"])

	if verdict == "refused" {
		failure, _ := body["failure"].(map[string]any)
		detail := jsonText(failure["detail"])
		if strings.Contains(detail, "no function/method/class unit") {
			row.Class = "no-unit"
			row.Reason = "nothing to witness — no function, method or class unit (a data or doc file)"
			return row
		}
		kind := "?"
		if k, ok := failure["kind"]; ok {
			kind = jsonText(k)
		}
		row.Reason = fmt.Sprintf("the witness could not read this file (%s): %s — expected %s", kind, detail, jsonText(failure["expected"]))
		return row
	}
	if verdict == "unknown" {
		row.Reason = "the witness has an empty corpus for this: 'not looked at', not 'no prior art'."
		return row
	}
	novelty, _ := body["novelty"].(map[string]any)
	canonical, _ := novelty["canonical"].(map[string]any)
	if class := jsonText(canonical["class"]); class != "" {
		importPath := jsonText(canonical["import"])
		switch {
		// A CANONICAL'S OWN SOURCE IS NOT A REPEAT OF IT. narcissus seeds its
		// canonicals from the modules that define them, so a change to one
		// matches its own fingerprint exactly — which narcissus also grades
		// `standard`, so this is decided first (stellar_core#18, Rob,
		// 2026-09-13; the import path is narcissus#39's).
		case DefinesCanonical(p, importPath):
			row.Class = "canonical-source"
			row.Reason = fmt.Sprintf("defines canonical class '%s' (%s): its own source, not a repeat", class, importPath)
		// REUSING THE CANONICAL IS THE POINT OF THE MATCH (narcissus#9078).
		case canonical["reused"] == true:
			row.Class = "canonical-reuse"
			row.Reason = fmt.Sprintf("reuses canonical class '%s' (%s): adopting it is the point of the match", class, importPath)
		// A TEST THAT RESEMBLES A CANONICAL IS EXERCISING A SHAPE (Rob,
		// 2026-09-13, foundry-stocks#164) — advisory when narcissus says the
		// test does not grade with the class's own suite. A test that
		// duplicates a standard is still a finding.
		case verdict != "standard" && testPath.MatchString(p):
			_, tested := canonical["test"]
			graded := canonical["graded"] == true
			if tested && !graded {
				row.Class = "advisory"
				row.Reason = runeCut(fmt.Sprintf("advisory — a test resembling canonical class '%s': %s", class, recommendation), 300)
				break
			}
			row.Class = "canonical-in-test"
			how := "it exercises the shape, it does not re-implement it"
			if graded {
				how = "it grades with the class's own suite"
			}
			row.Reason = fmt.Sprintf("a test resembling canonical class '%s': %s", class, how)
		default:
			row.Class = "finding"
			row.Canonical = &WitnessCanonical{Class: class, Descriptor: jsonText(canonical["descriptor"])}
			if footguns, ok := canonical["footguns"].([]any); ok {
				for _, f := range footguns {
					row.Canonical.Footguns = append(row.Canonical.Footguns, jsonText(f))
				}
			}
			row.Reason = fmt.Sprintf("matches canonical class '%s' — %d known failure mode(s) to check", class, len(row.Canonical.Footguns))
		}
		return row
	}
	switch verdict {
	case "standard":
		row.Class = "finding"
		row.Reason = runeCut("duplicates a standard: "+recommendation, 300)
	case "convention":
		row.Class = "advisory"
		row.Reason = runeCut("advisory — duplicates a convention: "+recommendation, 300)
	case "novel":
		row.Class = "clean"
		row.Reason = "novel on both axes"
	default:
		shown := "None"
		if v, ok := body["verdict"]; ok && v != nil {
			shown = "'" + jsonText(v) + "'"
		}
		row.Reason = "unrecognised witness verdict " + shown + " — could not consult."
	}
	return row
}

// jsonText is a JSON value as text: a string as itself, nothing as "".
func jsonText(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// runeCut is s cut to n characters.
func runeCut(s string, n int) string {
	r := []rune(s)
	return string(r[:min(len(r), n)])
}

// AggregateWitness folds the rows into the atom's state and reason. Findings
// win over could-not-consult, which is reported beside them.
func AggregateWitness(rows []WitnessRow, skipped, vendored []string) (int, string) {
	byClass := map[string][]WitnessRow{}
	for _, r := range rows {
		byClass[r.Class] = append(byClass[r.Class], r)
	}
	paths := func(rs []WitnessRow, sep string) string {
		out := make([]string, len(rs))
		for i, r := range rs {
			out[i] = r.Path
		}
		return strings.Join(out, sep)
	}
	reasons := func(rs []WitnessRow) string {
		out := make([]string, len(rs))
		for i, r := range rs {
			out[i] = r.Path + ": " + r.Reason
		}
		return strings.Join(out, "; ")
	}

	tail := ""
	if rs := byClass["canonical-source"]; len(rs) > 0 {
		tail += fmt.Sprintf("; %d file(s) are a canonical class's own source: %s", len(rs), paths(rs, ", "))
	}
	if rs := byClass["canonical-in-test"]; len(rs) > 0 {
		tail += fmt.Sprintf("; %d test file(s) resemble a canonical class: %s", len(rs), paths(rs, ", "))
	}
	if rs := byClass["canonical-reuse"]; len(rs) > 0 {
		tail += fmt.Sprintf("; %d file(s) reuse a canonical class: %s", len(rs), paths(rs, ", "))
	}
	if len(skipped) > 0 {
		tail += fmt.Sprintf("; skipped %d file(s) in languages the witness has no analyzer for", len(skipped))
	}
	if len(vendored) > 0 {
		tail += fmt.Sprintf("; skipped %d vendored file(s)", len(vendored))
	}
	nounit := byClass["no-unit"]
	if len(nounit) > 0 {
		tail += fmt.Sprintf("; %d file(s) had no unit to witness: %s", len(nounit), paths(nounit, ", "))
	}

	if len(rows) == 0 {
		return 0, "nothing to witness — no changed .py or .go file the star authored" + tail
	}
	cnr := byClass["could-not-consult"]
	if findings := byClass["finding"]; len(findings) > 0 {
		reason := fmt.Sprintf("findings in %d of %d file(s): %s", len(findings), len(rows), reasons(findings))
		if len(cnr) > 0 {
			reason += fmt.Sprintf(" — also could not consult %d: %s", len(cnr), paths(cnr, "; "))
		}
		return 1, reason
	}
	if len(cnr) > 0 {
		return 2, fmt.Sprintf("could not consult %d of %d file(s): %s", len(cnr), len(rows), reasons(cnr))
	}
	witnessed := len(rows) - len(nounit)
	if advisory := byClass["advisory"]; len(advisory) > 0 {
		return 0, fmt.Sprintf("clean — %d file(s) witnessed; %d advisory (reuse, not rewrite): %s%s", witnessed, len(advisory), reasons(advisory), tail)
	}
	if witnessed == 0 {
		return 0, "nothing to witness — every changed source file had no unit" + tail
	}
	if len(byClass["canonical-source"])+len(byClass["canonical-in-test"])+len(byClass["canonical-reuse"]) > 0 {
		return 0, fmt.Sprintf("clean — %d file(s) witnessed%s", witnessed, tail)
	}
	return 0, fmt.Sprintf("clean — %d file(s) witnessed, novel on both axes%s", witnessed, tail)
}

// WitnessSummary is the table beside the reason: each file, its verdict and
// class, and every failure mode a matched canonical class names.
func WitnessSummary(rows []WitnessRow) string {
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("| file | verdict | class | reason |\n|---|---|---|---|\n")
	for _, r := range rows {
		v := r.Verdict
		if v == "" {
			v = "-"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", r.Path, v, r.Class, runeCut(r.Reason, 160))
	}
	for _, r := range rows {
		if r.Canonical != nil {
			for _, f := range r.Canonical.Footguns {
				fmt.Fprintf(&b, "- %s · %s: %s\n", r.Path, r.Canonical.Class, f)
			}
		}
	}
	return b.String()
}

// StarName is the star a checkout's origin URL names: its last path segment,
// less .git; "unknown" when there is none.
func StarName(originURL string) string {
	name := strings.TrimSuffix(path.Base(strings.TrimRight(strings.TrimSpace(originURL), "/")), ".git")
	if name == "" || name == "." || name == "/" {
		return "unknown"
	}
	return name
}

// WitnessRequest is the JSON-RPC tools/call body that asks the witness about
// one file. path lets narcissus tell a test from source (narcissus#9078).
func WitnessRequest(id int, query, language, caller, p string) string {
	b, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{"name": "witness", "arguments": map[string]any{
			"query": query, "granularity": "code", "caller": caller, "language": language, "path": p,
		}},
	})
	return string(b)
}
