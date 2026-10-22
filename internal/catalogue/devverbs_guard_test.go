package catalogue

import (
	"slices"
	"strings"
	"testing"
)

// devVerbs are the verbs a star's `_dev <verb>` can carry: the list in
// foundry-stocks speckit/dev.just and `foundry-toolbox ask --help`, plus
// test-live. foundry-stocks pins the same list (tests/test_dev_just.py), so a
// verb added on either side fails on the other.
var devVerbs = []string{
	"build", "test", "vet", "check", "clippy", "fmt", "tidy", "vendor", "lock", "update",
	"regen", "replay", "lint", "typecheck", "test-live",
}

// checkVerbs are the read-only check verbs a star's recipes ask for beyond the
// list foundry-stocks pins: fmt-check (the gofmt listing), vuln (the advisory
// scan) and sast (the opengrep scan). They route like any verb, to
// dev:<lang>-<verb>.
var checkVerbs = []string{"fmt-check", "vuln", "sast"}

// devNotApplicable is every language x verb that has no dev atom ON PURPOSE,
// with the reason. Anything else a star can ask must resolve to dev:<lang>-<verb>.
var devNotApplicable = map[string]string{
	"go/lock": "go has no lock step: go.sum is written by tidy", "go/clippy": "rust only", "go/replay": "rust wit replay only",
	"rust/vet": "go only", "rust/tidy": "go only", "rust/vendor": "go only (cargo vendor is not a fleet verb)",
	"rust/regen": "go and ts worlds only", "rust/test-live": "go only",
	"python/vet": "go only", "python/clippy": "rust only", "python/tidy": "go only", "python/vendor": "go only",
	"python/regen": "go and ts worlds only", "python/replay": "rust wit replay only", "python/test-live": "go only",
	"ts/vet": "go only", "ts/clippy": "rust only", "ts/tidy": "go only", "ts/vendor": "go only",
	"ts/replay": "rust wit replay only", "ts/test-live": "go only",
	// the check verbs: go has all three; rust has vuln (its fmt check is `fmt --check`
	// and no rust recipe asks for sast); python and ts have none
	"rust/fmt-check": "rust asks `fmt --check`, the fmt verb with its flag", "rust/sast": "no rust star recipe asks for it",
	"python/fmt-check": "no python star recipe asks for it", "python/vuln": "no python star recipe asks for it", "python/sast": "no python star recipe asks for it",
	"ts/fmt-check": "no ts star recipe asks for it", "ts/vuln": "no ts star recipe asks for it", "ts/sast": "no ts star recipe asks for it",
}

// TestEveryDevVerbResolvesOrIsDocumentedNotApplicable is the guard foundry-stocks
// #430 lacked: `ask dev <verb>` for a supported language either has an atom or an
// explicit reason it should not.
func TestEveryDevVerbResolvesOrIsDocumentedNotApplicable(t *testing.T) {
	for _, lang := range []string{"go", "rust", "python", "ts"} {
		for _, verb := range append(slices.Clone(devVerbs), checkVerbs...) {
			key := lang + "/" + verb
			_, has := Lookup("dev:" + lang + "-" + verb)
			reason, na := devNotApplicable[key]
			switch {
			case has && na:
				t.Errorf("%s has an atom and a not-applicable entry (%q): drop one", key, reason)
			case !has && !na:
				t.Errorf("%s: no dev:%s-%s atom and no documented not-applicable entry", key, lang, verb)
			case na && reason == "":
				t.Errorf("%s: not-applicable entry has no reason", key)
			}
		}
	}
	for key := range devNotApplicable {
		lang, verb, _ := strings.Cut(key, "/")
		if !(slices.Contains(devVerbs, verb) || slices.Contains(checkVerbs, verb)) || lang == "" {
			t.Errorf("not-applicable entry %q names no known verb", key)
		}
	}
}
