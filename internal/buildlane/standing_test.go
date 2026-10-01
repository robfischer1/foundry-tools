package buildlane

import (
	"strings"
	"testing"
)

// permittedSha is built, not written: a forty-hex literal read as a secret to
// detect-secrets, and no pragma was how that got answered. The atom went on
// 2026-09-25 and the pragmas with it; the construction is kept on its own
// merit.
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

func TestAnInertChangeSetSinceTheLastPublishedBuildStandsDown(t *testing.T) {
	for _, changed := range []string{"README.md\ndocs/guide.md\n.claude/settings.json\n", ""} {
		needed, why := Standing(permittedSha, "1790812800-fedcba9", changed)
		if needed {
			t.Fatalf("the change set %q built", changed)
		}
		want := "every change since " + permittedSha[:12] + ", the last published build (:1790812800-fedcba9), is inert — nothing built or published; :1790812800-fedcba9 already carries this commit's source"
		if why != want {
			t.Errorf("why = %q", why)
		}
	}
}

func TestASourceChangeSinceTheLastPublishedBuildBuildsAndNamesTheFirstEight(t *testing.T) {
	var files []string
	for _, f := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
		files = append(files, "cmd/"+f+".go")
	}
	needed, why := Standing(permittedSha, "stable", strings.Join(files, "\n")+"\nREADME.md\n")
	if !needed {
		t.Fatal("nine source changes stood down")
	}
	if !strings.HasPrefix(why, "9 source or deploy change(s) since "+permittedSha[:12]+", the last published build (:stable), building: cmd/a.go, ") {
		t.Errorf("why = %q", why)
	}
	if !strings.HasSuffix(why, "cmd/h.go") {
		t.Errorf("the eighth change is not the last named: %q", why)
	}

	needed, why = Standing(permittedSha, "stable", "docs/quickstart.md\ncmd/ares/main.go\n")
	if !needed || !strings.HasSuffix(why, "building: cmd/ares/main.go") {
		t.Errorf("one source change among inert ones: needed=%v why=%q", needed, why)
	}
}

// The newest stamp is the highest timestamp, whatever order the registry
// lists tags in and whatever else the repository carries.
func TestTheNewestStampIsTheHighestTimestamp(t *testing.T) {
	listing := strings.Join([]string{
		"stable",
		"g0123456789ab",
		"1790812800-0123456",
		"inuse-0123456789ab",
		"1790899200-89abcde",
		"999999999-fffffff",
		"1790812900-abcdef0",
		"sha256-" + strings.Repeat("a", 64) + ".sig",
	}, "\n") + "\n"
	tag, ok := NewestStamp(listing)
	if !ok || tag != "1790899200-89abcde" {
		t.Errorf("NewestStamp = %q, %v", tag, ok)
	}
}

// What is not exactly a stamp is not one: a g-pin, a stamp with a longer or
// shorter sha, an uppercase sha, a signed suffix, a stamp inside a line.
func TestOnlyAWholeStampTagIsAStamp(t *testing.T) {
	for _, listing := range []string{
		"",
		"stable\ng0123456789ab\n",
		"1790812800-0123456789ab\n",
		"1790812800-012345\n",
		"1790812800-ABCDEF0\n",
		"1790812800-0123456-signed\n",
		"x1790812800-0123456\n",
		"-0123456\n",
		"1234567890123456789-0123456\n",
	} {
		if tag, ok := NewestStamp(listing); ok {
			t.Errorf("NewestStamp(%q) = %q", listing, tag)
		}
	}
	// Surrounding whitespace is the listing's, not the tag's.
	if tag, ok := NewestStamp("  1790812800-0123456\r\n"); !ok || tag != "1790812800-0123456" {
		t.Errorf("a padded stamp = %q, %v", tag, ok)
	}
}

// Two stamps at one second order by name, so the answer does not turn on the
// order the registry listed them in.
func TestTwoStampsAtOneSecondOrderByName(t *testing.T) {
	for _, listing := range []string{
		"1790812800-0123456\n1790812800-89abcde\n",
		"1790812800-89abcde\n1790812800-0123456\n",
	} {
		if tag, _ := NewestStamp(listing); tag != "1790812800-89abcde" {
			t.Errorf("NewestStamp(%q) = %q", listing, tag)
		}
	}
	// A lone stamp at timestamp zero is still the newest there is.
	if tag, ok := NewestStamp("0-0123456\n"); !ok || tag != "0-0123456" {
		t.Errorf("a stamp at zero = %q, %v", tag, ok)
	}
}
