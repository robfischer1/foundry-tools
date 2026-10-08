package retiredverbs

import (
	"strings"
	"testing"
)

func TestReport(t *testing.T) {
	l := Ledger{"old_verb": {Successor: "new_verb"}}
	got := Report("fleet:retired-verbs", []Hit{{Path: "skills/a.md", Line: 3, Verb: "old_verb"}}, l)
	for _, want := range []string{
		"fleet:retired-verbs: FINDINGS - 1 line(s) still name a verb the gateway no longer serves.",
		"  skills/a.md:3  old_verb\n    call instead: new_verb\n",
		"FIX: write the successor in each line above.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks %q:\n%s", want, got)
		}
	}
}
