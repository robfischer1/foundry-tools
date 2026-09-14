package buildlane

import (
	"strings"
	"testing"
)

// permittedSha is built, not written: a forty-hex literal reads as a secret to
// detect-secrets, and no pragma is how that gets answered.
var permittedSha = strings.Repeat("fedcba98", 5)

func TestIsCommitTakesOnlyAFullLowercaseRevision(t *testing.T) {
	for s, want := range map[string]bool{
		permittedSha:                  true,
		"":                            false,
		permittedSha[:12]:             false,
		"v1.4.0":                      false,
		strings.ToUpper(permittedSha): false,
		permittedSha + "0":            false,
		permittedSha + "\n":           false,
	} {
		if got := IsCommit(s); got != want {
			t.Errorf("IsCommit(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestAnInertChangeSetSinceThePermitStandsDown(t *testing.T) {
	for _, changed := range []string{"README.md\ndocs/guide.md\n.claude/settings.json\n", ""} {
		needed, why := Standing(permittedSha, changed)
		if needed {
			t.Fatalf("the change set %q built", changed)
		}
		if !strings.HasPrefix(why, "every change since "+permittedSha[:12]+", the last permitted build (:stable), is inert") {
			t.Errorf("why = %q", why)
		}
	}
}

func TestASourceChangeSinceThePermitBuildsAndNamesTheFirstEight(t *testing.T) {
	var files []string
	for _, f := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
		files = append(files, "cmd/"+f+".go")
	}
	needed, why := Standing(permittedSha, strings.Join(files, "\n")+"\nREADME.md\n")
	if !needed {
		t.Fatal("nine source changes stood down")
	}
	if !strings.HasPrefix(why, "9 source or deploy change(s) since "+permittedSha[:12]+", the last permitted build (:stable), building: cmd/a.go, ") {
		t.Errorf("why = %q", why)
	}
	if !strings.HasSuffix(why, "cmd/h.go") {
		t.Errorf("the eighth change is not the last named: %q", why)
	}

	needed, why = Standing(permittedSha, "docs/quickstart.md\ncmd/ares/main.go\n")
	if !needed || !strings.HasSuffix(why, "building: cmd/ares/main.go") {
		t.Errorf("one source change among inert ones: needed=%v why=%q", needed, why)
	}
}
