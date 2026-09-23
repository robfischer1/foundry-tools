package main

import (
	"regexp"
	"sort"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// Every /rulesets path anywhere in a chain, whether it was written or read.
// The dot is only ever an extension separator here — a trailing one is the
// sentence the ruleset's own header comment ends, not part of a filename.
var rulesetPathRE = regexp.MustCompile(
	regexp.QuoteMeta(checks.RulesetsDir) + `/[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)*`)

// THE PATH IS WRITTEN TWICE IN EVERY RULESET ATOM — once in the WithNewFile
// that renders the embedded config into the container, once in the argv that
// tells the tool where to look — and nothing in the type system pairs them. A
// ruleset handed the wrong file compiles, runs, and lints the wrong language:
// python:mypy shipped pointed at ruff.toml and was caught by reading the two
// lines back, not by a test.
//
// One distinct path per chain IS the pairing. Two means the halves diverged.
func TestEachRulesetAtomReadsTheFileItWrote(t *testing.T) {
	for _, tc := range []struct{ id, run, want string }{
		{"python:ruff-check", `"ruff@0.16.3","check"`, checks.RulesetsDir + "/ruff.toml"},
		{"python:ruff-format", `"ruff@0.16.3","format"`, checks.RulesetsDir + "/ruff.toml"},
		{"python:mypy", `"mypy","--config-file"`, checks.RulesetsDir + "/mypy.ini"},
	} {
		engine.reset()
		engine.withTree(everyLaneTree)
		runAtom(t, tc.id, "")

		c := engine.chain(tc.run, "exitCode")
		if c == "" {
			t.Errorf("%s: no container ran", tc.id)
			continue
		}

		seen := map[string]bool{}
		for _, p := range rulesetPathRE.FindAllString(c, -1) {
			seen[p] = true
		}
		var paths []string
		for p := range seen {
			paths = append(paths, p)
		}
		sort.Strings(paths)

		switch {
		case len(paths) == 0:
			t.Errorf("%s: no ruleset path in the chain at all — the atom is ungoverned", tc.id)
		case len(paths) > 1:
			t.Errorf("%s: %d ruleset paths in one chain %v — the file written is not the file read",
				tc.id, len(paths), paths)
		case paths[0] != tc.want:
			t.Errorf("%s: ruleset is %q, want %q", tc.id, paths[0], tc.want)
		}

		if !hasCall(c, "withNewFile", `path:"`+tc.want+`"`) {
			t.Errorf("%s: %s is read but never written — an atom reading a path nothing rendered",
				tc.id, tc.want)
		}
	}
}
