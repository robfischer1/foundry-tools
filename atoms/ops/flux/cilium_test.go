package opsflux

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomlib/atomtest"
	"dagger/foundry-tools/internal/atoms"
)

// The regexes of flux 84f6efa5 and the split that replaced it, with the RE2
// program sizes google-re2 1.1.20251105 (Latin-1) measured: 102, 80 and 33. The
// atom's python is faked to answer them; the numbers are recorded, not computed.
const (
	incidentPath = `^/(?:athena|clotho|body|telemetry|grants|logout|broadcast|tasks/answer|choices/answer|choices/withdraw|grade/label)/?(?:\\?.*)?$`
	splitPathA   = `^/(?:athena|clotho|body|telemetry|grants|logout|broadcast|tasks/answer|grade/label)/?(?:\\?.*)?$`
	splitPathB   = `^/(?:choices/answer|choices/withdraw)/?(?:\\?.*)?$`
)

// recordedSizes is keyed by the pattern as RE2 sees it (the YAML double-quoted
// `\\?` above is `\?` once parsed).
var recordedSizes = map[string]int{
	strings.ReplaceAll(incidentPath, `\\`, `\`): 102,
	strings.ReplaceAll(splitPathA, `\\`, `\`):   80,
	strings.ReplaceAll(splitPathB, `\\`, `\`):   33,
}

func policy(paths ...string) string {
	var b strings.Builder
	b.WriteString("apiVersion: cilium.io/v2\nkind: CiliumNetworkPolicy\nmetadata:\n  name: charon-ingress\n  namespace: apps\nspec:\n  ingress:\n    - toPorts:\n        - rules:\n            http:\n")
	for _, p := range paths {
		b.WriteString("              - method: \"POST\"\n                path: \"" + p + "\"\n")
	}
	return b.String()
}

// pythonFake answers the atom's python3 calls: the import probe with probeRc and
// the script with a JSON answer built from recordedSizes (or runRc/runOut).
func pythonFake(t *testing.T, tools *fluxTools, probeRc, runRc int, runOut string, ran *int) func(atoms.Cmd) (string, int) {
	base := tools.answer(t)
	return func(c atoms.Cmd) (string, int) {
		if c.Name != "python3" {
			return base(c)
		}
		if strings.HasPrefix(c.Args[1], "from re2") {
			return "no module named re2", probeRc
		}
		*ran++
		if runOut != "" || runRc != 0 {
			return runOut, runRc
		}
		var patterns []string
		if err := json.Unmarshal([]byte(c.Stdin), &patterns); err != nil {
			t.Errorf("the request was not a JSON array: %v", err)
		}
		var out []string
		for _, pat := range patterns {
			out = append(out, fmt.Sprintf(`{"size":%d,"error":""}`, recordedSizes[pat]))
		}
		return "[" + strings.Join(out, ",") + "]", 0
	}
}

func TestCiliumRegexPass(t *testing.T) {
	for _, tc := range []struct {
		name    string
		built   string
		probe   int
		runRc   int
		runOut  string
		state   int
		result  string
		needles []string
		python  bool
	}{
		{"the 84f6efa5 alternation (102) fails the atom", policy(incidentPath), 0, 0, "", 1, atomtest.Findings,
			[]string{"RE2 program size 102 > 100", "charon-ingress", "1 over the cap of 100; largest 102"}, true},
		{"the split form (80 and 33) passes", policy(splitPathA, splitPathB), 0, 0, "", 0, atomtest.Pass,
			[]string{"Cilium regex pass: 4 regex(es)", "largest 80"}, true},
		{"a tree with no rules.http regex never asks python", builtStream, 0, 0, "", 0, atomtest.Pass, []string{"no rules.http regex in the built trees"}, false},
		{"a venv without google-re2 is a 2, not a finding", policy(splitPathA), 1, 0, "", 2, atomtest.Cannot, []string{"cannot import google-re2"}, false},
		{"a script that dies is a 2", policy(splitPathA), 0, 1, "Traceback", 2, atomtest.Cannot, []string{"RE2 did not run (rc=1): Traceback"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, f := atomtest.ToolTree(t, fluxTree(), nil)
			tools := &fluxTools{build: func(string) (string, int) { return tc.built, 0 }}
			ran := 0
			f.Answer = pythonFake(t, tools, tc.probe, tc.runRc, tc.runOut, &ran)
			atomtest.Expect(t, run(in), atomtest.StateOf(tc.state), tc.result, tc.needles...)
			if (ran > 0) != tc.python {
				t.Errorf("python ran %d time(s), want ran=%v", ran, tc.python)
			}
		})
	}
}
