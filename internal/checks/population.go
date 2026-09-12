package checks

import (
	"regexp"
	"strings"
)

// GateExclude is THE FLEET'S EXCLUDE, NOT THE REPOSITORY'S. Until 2026-09-11
// the gate read each repo's own .pre-commit-config.yaml `exclude:` for its
// population, so a repo decided what the gate looked at. Rob, 2026-09-11: a
// repo has no say in anything that runs. The four repo templates pour one
// identical exclude line (measured: byte for byte across go/python/rust/
// frontend), and that line is this constant; a repo's config is not read.
//
// The regex is pre-commit's (Python re) as Go RE2 reads it; the shapes here —
// anchors, alternation, character classes, escaped dots — mean the same in
// both.
const GateExclude = `^\.(claude|specify|furnace)/|(^|/)(vendor|node_modules)/|\.melt$`

var gateExcludeRE = regexp.MustCompile(GateExclude)

// GatePopulation applies the fleet exclude to a file list and drops the
// directory entries a glob may have matched (Dagger names those with a
// trailing slash). The list in is the tree's committable files — the
// engine's own gitignore filter over the mounted tree — and the list out is
// what a fleet atom is allowed to grade.
func GatePopulation(files []string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		f = strings.TrimPrefix(f, "./")
		if f == "" || strings.HasSuffix(f, "/") || gateExcludeRE.MatchString(f) {
			continue
		}
		out = append(out, f)
	}
	return out
}
