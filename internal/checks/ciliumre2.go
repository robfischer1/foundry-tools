package checks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// THE CILIUM L7 REGEX PASS. flux 84f6efa5 grew charon's traefik-floor path
// alternation to RE2 program size 102. Envoy refuses a safe_regex whose program
// is over 100, so cilium-envoy on llm01 NACKed every NetworkPolicy update for
// six minutes and the fleet went peer_unattributed (foundry-tools#25995). The
// gate compiled nothing.
//
// THE SIZE IS RE2'S, NOT GO'S. Go's regexp/syntax counts instructions of its own
// program; RE2's ProgramSize counts the C++ program, and the two differ. The
// measure is a real RE2 (the google-re2 wheel in pytools, CiliumRE2Script), run
// with the Latin-1 encoding. That is a calibration, not a derivation, and it is
// the one the evidence fixes:
//
//	incident POST path (84f6efa5)        Latin-1 102   UTF-8 108   Envoy said 102
//	flux main's largest, ourea /v1/retire Latin-1  99   UTF-8 195   Envoy accepts it
//
// UTF-8, RE2's default, would fail a policy that has been live for weeks, so it
// is not what Envoy measures; Latin-1 reproduces the incident's 102 exactly and
// keeps every live policy under the cap. The headroom on flux main is ONE: 99.

// CiliumRE2MaxProgramSize is Envoy's cap on a safe_regex program.
const CiliumRE2MaxProgramSize = 100

// CiliumRegex is one regex a CiliumNetworkPolicy or CiliumClusterwideNetworkPolicy
// hands Envoy: a field of a rules.http[] entry. path, method and host are all
// regexes in Cilium; headerMatches are not and are not read.
type CiliumRegex struct{ Tree, Policy, Field, Pattern string }

// CiliumRegexes answers every rules.http[] path, method and host of the
// Cilium policies in the built streams (keyed by the tree built), in tree then
// document order. A stream that does not parse is an error.
func CiliumRegexes(streams map[string]string) ([]CiliumRegex, error) {
	trees := make([]string, 0, len(streams))
	for t := range streams {
		trees = append(trees, t)
	}
	sort.Strings(trees)
	var out []CiliumRegex
	for _, tree := range trees {
		dec := yaml.NewDecoder(strings.NewReader(streams[tree]))
		for {
			var doc map[string]any
			err := dec.Decode(&doc)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("a built tree did not parse: %w", err)
			}
			if k, _ := doc["kind"].(string); k != "CiliumNetworkPolicy" && k != "CiliumClusterwideNetworkPolicy" {
				continue
			}
			meta, _ := doc["metadata"].(map[string]any)
			name, _ := meta["name"].(string)
			ciliumHTTP(doc, func(field, pattern string) {
				out = append(out, CiliumRegex{Tree: tree, Policy: name, Field: field, Pattern: pattern})
			})
		}
	}
	return out, nil
}

// ciliumHTTP calls fn for each path, method and host string of every "http"
// list under n, whatever its depth (toPorts[].rules.http, in spec or specs[]).
func ciliumHTTP(n any, fn func(field, pattern string)) {
	switch v := n.(type) {
	case []any:
		for _, e := range v {
			ciliumHTTP(e, fn)
		}
	case map[string]any:
		for k, e := range v {
			list, isList := e.([]any)
			if k != "http" || !isList {
				ciliumHTTP(e, fn)
				continue
			}
			for _, rule := range list {
				m, _ := rule.(map[string]any)
				for _, field := range []string{"path", "method", "host"} {
					if p, ok := m[field].(string); ok && p != "" {
						fn(field, p)
					}
				}
			}
		}
	}
}

// CiliumRE2Script reads a JSON array of patterns on stdin and answers a JSON
// array of {"size", "error"}, one per pattern in order. RE2 is compiled as Envoy
// does (quiet, no match semantics changed) with the Latin-1 encoding the
// calibration above fixes. _re2 is google-re2's pybind module, the one place
// ProgramSize is exposed; the version is hash-locked in pytools/requirements.txt.
const CiliumRE2Script = `import json, sys
from re2 import _re2
o = _re2.RE2.Options()
o.encoding = _re2.RE2.Options.Encoding.LATIN1
o.log_errors = False
out = []
for p in json.load(sys.stdin):
    r = _re2.RE2(p.encode("utf-8"), o)
    out.append({"size": r.ProgramSize() if r.ok() else 0, "error": "" if r.ok() else r.error()})
json.dump(out, sys.stdout)
`

// CiliumRE2Patterns answers the distinct patterns, sorted, and the JSON request
// for them; many policies repeat one regex.
func CiliumRE2Patterns(rs []CiliumRegex) ([]string, string) {
	seen := map[string]bool{}
	var ps []string
	for _, r := range rs {
		if !seen[r.Pattern] {
			seen[r.Pattern] = true
			ps = append(ps, r.Pattern)
		}
	}
	sort.Strings(ps)
	body, _ := json.Marshal(ps)
	return ps, string(body)
}

// CiliumRE2Report settles CiliumRE2Script's answer for the patterns: 1 when a
// regex does not compile or has a program over the cap, 2 when the answer is not
// the script's, 0 otherwise. Every offender is named with its policy, tree and
// field; the head line names the largest, which is the headroom.
func CiliumRE2Report(rs []CiliumRegex, patterns []string, answer string) (int, string) {
	var res []struct {
		Size  int    `json:"size"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(bytes.NewReader([]byte(answer))).Decode(&res); err != nil || len(res) != len(patterns) {
		return 2, "Cilium regex pass UNANALYZABLE: RE2's answer was not one result per pattern: " + strings.TrimSpace(answer) + "\n"
	}
	byPattern := map[string]int{}
	for i, p := range patterns {
		byPattern[p] = i
	}
	var b strings.Builder
	bad, largest := 0, -1
	var at CiliumRegex
	for _, r := range rs {
		got := res[byPattern[r.Pattern]]
		if got.Error != "" {
			bad++
			fmt.Fprintf(&b, "  REGEX %s %s %s: does not compile in RE2 (%s): %s\n", r.Tree, r.Policy, r.Field, got.Error, ciliumShort(r.Pattern))
			continue
		}
		if got.Size > largest {
			largest, at = got.Size, r
		}
		if got.Size > CiliumRE2MaxProgramSize {
			bad++
			fmt.Fprintf(&b, "  REGEX %s %s %s: RE2 program size %d > %d, so cilium-envoy NACKs every policy update (flux 84f6efa5); split the alternation across rules: %s\n", r.Tree, r.Policy, r.Field, got.Size, CiliumRE2MaxProgramSize, ciliumShort(r.Pattern))
		}
	}
	head := fmt.Sprintf("Cilium regex pass: %d regex(es), %d distinct, %d over the cap of %d", len(rs), len(patterns), bad, CiliumRE2MaxProgramSize)
	if largest >= 0 {
		head += fmt.Sprintf("; largest %d (%s %s %s)", largest, at.Tree, at.Policy, at.Field)
	}
	if bad > 0 {
		return 1, head + "\n" + b.String()
	}
	return 0, head + "\n"
}

func ciliumShort(p string) string {
	if len(p) > 90 {
		return p[:90] + "..."
	}
	return p
}
