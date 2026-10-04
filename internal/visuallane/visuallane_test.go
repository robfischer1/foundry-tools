package visuallane

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

func TestParseConfigReadsEverySuiteCleaned(t *testing.T) {
	got, err := ParseConfig("[[suite]]\ndir = \"apps/gallery/\"\n\n[[suite]]\ndir = \"./apps/docs\"\n\n[[suite]]\ndir = \".\"\n")
	if err != nil {
		t.Fatal(err)
	}
	want := []Suite{{Dir: "apps/gallery"}, {Dir: "apps/docs"}, {Dir: "."}}
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("suite %d: %+v, want %+v", i, got[i], want[i])
		}
	}
}

// Every refusal names what is wrong; none reads as a tree that declared nothing.
func TestParseConfigRefusesByName(t *testing.T) {
	for raw, want := range map[string]string{
		"[[suite]\n":                       "does not parse",
		"[[suite]]\ndirs = \"apps/x\"\n":   "a key the lane does not read: suite.dirs",
		"title = \"x\"\n":                  "a key the lane does not read: title",
		"":                                 "names no [[suite]]",
		"[[suite]]\n":                      "a [[suite]] has no dir",
		"[[suite]]\ndir = \"  \"\n":        "a [[suite]] has no dir",
		"[[suite]]\ndir = \"/abs\"\n":      `the suite dir "/abs" is not inside the tree`,
		"[[suite]]\ndir = \"..\"\n":        `the suite dir ".." is not inside the tree`,
		"[[suite]]\ndir = \"../x\"\n":      `the suite dir "../x" is not inside the tree`,
		"[[suite]]\ndir = \"a/../../x\"\n": `the suite dir "a/../../x" is not inside the tree`,
		"[[suite]]\ndir = \"a\"\n[[suite]]\ndir = \"a/\"\n": `names the suite "a" twice`,
	} {
		_, err := ParseConfig(raw)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", raw, err, want)
		}
		if err != nil && !strings.Contains(err.Error(), ConfigFile) {
			t.Errorf("%q: the refusal does not name %s: %v", raw, ConfigFile, err)
		}
	}
	// A directory whose name only starts with dots is inside the tree.
	if got, err := ParseConfig("[[suite]]\ndir = \"..hidden\"\n"); err != nil || got[0].Dir != "..hidden" {
		t.Errorf("..hidden: %+v %v", got, err)
	}
}

const lock = `{
  "packages": {
    "@playwright/test": ["@playwright/test@1.62.0", "", { "dependencies": { "playwright": "1.62.0" } }, "sha512-x"],
    "playwright": ["playwright@1.62.0", "", {}, "sha512-y"],
  }
}`

func TestPlaywrightVersionIsTheOneTheLockResolves(t *testing.T) {
	if v, err := PlaywrightVersion(lock); err != nil || v != "1.62.0" {
		t.Errorf("%q %v", v, err)
	}
	// The same version named twice is still one.
	if v, err := PlaywrightVersion(lock + `"@playwright/test@1.62.0"`); err != nil || v != "1.62.0" {
		t.Errorf("repeat: %q %v", v, err)
	}
	if v, err := PlaywrightVersion(`"@playwright/test@1.63.0-beta.2"`); err != nil || v != "1.63.0-beta.2" {
		t.Errorf("prerelease: %q %v", v, err)
	}
	if _, err := PlaywrightVersion(`"playwright@1.62.0"`); err == nil || !strings.Contains(err.Error(), "resolves no @playwright/test") {
		t.Errorf("none: %v", err)
	}
	// Not a version an argv may carry: refused, not passed through.
	if _, err := PlaywrightVersion(`"@playwright/test@1.62.0; rm -rf /"`); err == nil {
		t.Error("a version that is not one was accepted")
	}
	_, err := PlaywrightVersion(`"@playwright/test@1.62.0" "@playwright/test@1.61.1"`)
	if err == nil || !strings.Contains(err.Error(), "1.62.0 and 1.61.1") {
		t.Errorf("two: %v", err)
	}
}

// A report shaped as Playwright 1.62's JSON reporter writes it.
const fixtureReport = `{
  "suites": [{
    "title": "gallery.spec.ts", "file": "gallery.spec.ts",
    "specs": [{"title": "nothing is fetched over the network", "tests": [{"status": "expected", "results": [{}]}]}],
    "suites": [
      {"title": "scheme: dusk", "specs": [
        {"title": "primitives render in dusk", "tests": [{"status": "unexpected", "results": [
          {"errors": [{"message": "an earlier attempt"}]},
          {"errors": [{"message": "\u001b[31mError: expect(locator).toHaveScreenshot(expected) failed\u001b[39m\n\n  1234 pixels (ratio 0.01 of all image pixels) are different.\n"}],
           "attachments": [
             {"name": "dusk-primitives-forms-expected.png", "path": "/tmp/visual/results/gallery-dusk/dusk-primitives-forms-expected.png"},
             {"name": "dusk-primitives-forms-diff.png", "path": "/tmp/visual/results/gallery-dusk/dusk-primitives-forms-diff.png"},
             {"name": "elsewhere-diff.png", "path": "/elsewhere/elsewhere-diff.png"}
           ]}
        ]}]},
        {"title": "specimen cards all render in dusk", "tests": [{"status": "flaky", "results": [{}, {}]}]}
      ]},
      {"title": "scheme: forest", "specs": [
        {"title": "primitives render in forest", "tests": [{"status": "unexpected", "results": [
          {"errors": [{"message": "Error: A snapshot doesn't exist at /src/apps/gallery/visual/x-snapshots/forest-linux.png, writing actual."}]}
        ]}]},
        {"title": "the shot list is complete", "tests": [{"status": "unexpected", "results": [
          {"errors": [{"message": "\n\nError: expect(locator).toHaveCount(expected) failed\nExpected: 44"}]}
        ]}]},
        {"title": "a test that died silently", "tests": [{"status": "unexpected", "results": [{}]}]},
        {"title": "a test with no attempt", "tests": [{"status": "unexpected", "results": []}]},
        {"title": "skipped", "tests": [{"status": "skipped", "results": []}]}
      ]}
    ]
  }],
  "errors": [],
  "stats": {"expected": 1, "unexpected": 5, "flaky": 1, "skipped": 1}
}`

func TestFailuresNamesEveryTestThatMovedAndWhy(t *testing.T) {
	found, stats, err := Failures("apps/gallery", "/tmp/visual/results/", []byte(fixtureReport))
	if err != nil {
		t.Fatal(err)
	}
	if stats != (Stats{Expected: 1, Unexpected: 5, Flaky: 1, Skipped: 1}) || stats.Total() != 8 {
		t.Errorf("stats %+v", stats)
	}
	want := []checks.Finding{
		{Verdict: checks.VerdictViolated, Subject: "apps/gallery: gallery.spec.ts › scheme: dusk › primitives render in dusk",
			Cause: CauseMismatch, Detail: "1234 pixels (ratio 0.01 of all image pixels) are different. (diff: results/apps/gallery/gallery-dusk/dusk-primitives-forms-diff.png)"},
		{Verdict: checks.VerdictDrifted, Subject: "apps/gallery: gallery.spec.ts › scheme: dusk › specimen cards all render in dusk",
			Cause: CauseFlaky, Detail: "failed, then passed on its retry"},
		{Verdict: checks.VerdictViolated, Subject: "apps/gallery: gallery.spec.ts › scheme: forest › primitives render in forest",
			Cause: CauseMissing, Detail: "Error: A snapshot doesn't exist at /src/apps/gallery/visual/x-snapshots/forest-linux.png, writing actual."},
		{Verdict: checks.VerdictViolated, Subject: "apps/gallery: gallery.spec.ts › scheme: forest › the shot list is complete",
			Cause: CauseFailed, Detail: "Error: expect(locator).toHaveCount(expected) failed"},
		{Verdict: checks.VerdictViolated, Subject: "apps/gallery: gallery.spec.ts › scheme: forest › a test that died silently",
			Cause: CauseFailed, Detail: "failed with no error recorded"},
		{Verdict: checks.VerdictViolated, Subject: "apps/gallery: gallery.spec.ts › scheme: forest › a test with no attempt",
			Cause: CauseFailed, Detail: "failed with no error recorded"},
	}
	if len(found) != len(want) {
		t.Fatalf("%d findings, want %d:%s", len(found), len(want), Lines(found))
	}
	for i := range want {
		want[i].Probe = Atom
		if found[i] != want[i] {
			t.Errorf("finding %d:\n got %+v\nwant %+v", i, found[i], want[i])
		}
	}
	if State(1, found) != 1 {
		t.Error("a failed suite is not a finding")
	}
}

// The other mismatch spelling, and a mismatch whose message has no pixel line.
func TestFailuresReadsEveryMismatchSpelling(t *testing.T) {
	for msg, detail := range map[string]string{
		"Screenshot comparison failed:\n\n  12 pixels (ratio 0.02) are different.": "12 pixels (ratio 0.02) are different.",
		"Error: expect(page).toHaveScreenshot(expected) failed\n\nTimeout 5000ms":  "Error: expect(page).toHaveScreenshot(expected) failed",
	} {
		raw := `{"suites":[{"title":"f","specs":[{"title":"t","tests":[{"status":"unexpected","results":[{"errors":[{"message":` +
			quote(msg) + `}]}]}]}]}],"stats":{"unexpected":1}}`
		found, _, err := Failures("d", "/out", []byte(raw))
		if err != nil || len(found) != 1 || found[0].Cause != CauseMismatch || found[0].Detail != detail {
			t.Errorf("%q: %+v %v", msg, found, err)
		}
	}
}

func quote(s string) string {
	return `"` + strings.NewReplacer("\n", `\n`, `"`, `\"`).Replace(s) + `"`
}

// An error outside every test is the run's, and a run that counted nothing
// photographs nothing.
func TestFailuresNamesTheRunsOwnErrorsAndAnEmptyRun(t *testing.T) {
	found, _, err := Failures("apps/x", "/out", []byte(`{"suites":[],"errors":[{"message":"\n Error: Process from config.webServer was not able to start. Exit code: 1\nmore"}],"stats":{}}`))
	if err != nil || len(found) != 1 {
		t.Fatalf("%+v %v", found, err)
	}
	if f := found[0]; f.Verdict != checks.VerdictUnanalyzable || f.Cause != CauseSuite || f.Subject != "apps/x" ||
		f.Detail != "Error: Process from config.webServer was not able to start. Exit code: 1" || f.Probe != Atom {
		t.Errorf("%+v", f)
	}

	found, _, err = Failures("apps/x", "/out", []byte(`{"suites":[],"errors":[],"stats":{}}`))
	if err != nil || len(found) != 1 || found[0].Cause != CauseNoTests || found[0].Verdict != checks.VerdictUnanalyzable {
		t.Fatalf("%+v %v", found, err)
	}
	if State(0, found) != 1 {
		t.Error("an exit-0 run that photographed nothing passed")
	}
	// One skipped test is a test.
	found, _, _ = Failures("apps/x", "/out", []byte(`{"stats":{"skipped":1}}`))
	if len(found) != 0 {
		t.Errorf("%+v", found)
	}
	if _, _, err := Failures("apps/x", "/out", []byte("not json")); err == nil || !strings.Contains(err.Error(), "report for apps/x does not parse") {
		t.Errorf("%v", err)
	}
	if got := firstLine(" \n\t\n"); got != "no message" {
		t.Errorf("%q", got)
	}
}

func TestStateReadsPlaywrightsExit(t *testing.T) {
	clean := []checks.Finding{{Cause: CauseFlaky}}
	for exit, want := range map[int]int{0: 0, 1: 1, 2: 2, 130: 2, -1: 2} {
		if got := State(exit, clean); got != want {
			t.Errorf("exit %d: %d, want %d", exit, got, want)
		}
	}
}

func TestArtifactPathKeepsOnlyWhatIsUnderTheOutput(t *testing.T) {
	for _, c := range []struct {
		results, file, want string
		ok                  bool
	}{
		{"/out", "/out/a/b-diff.png", "results/apps/x/a/b-diff.png", true},
		{"/out/", "/out/b.png", "results/apps/x/b.png", true},
		{"/out", "/outside/b.png", "", false},
		{"/out", "/out/", "", false},
	} {
		got, ok := ArtifactPath(c.results, "apps/x", c.file)
		if got != c.want || ok != c.ok {
			t.Errorf("%+v: %q %v", c, got, ok)
		}
	}
}

func TestSummaryAndLines(t *testing.T) {
	if got := Summary("apps/x", Stats{Expected: 4, Unexpected: 3, Flaky: 2, Skipped: 1}); got != "apps/x: 4 passed, 3 failed, 2 flaky, 1 skipped" {
		t.Errorf("%q", got)
	}
	got := Lines([]checks.Finding{{Verdict: "violated", Subject: "s", Detail: "d"}, {Verdict: "drifted", Subject: "t", Detail: "e"}})
	if got != "\n  violated  s — d\n  drifted  t — e" {
		t.Errorf("%q", got)
	}
}

func TestArtifactRefIsTheRepoAtTheCommit(t *testing.T) {
	sha := strings.Repeat("a", 40)
	got, err := ArtifactRef("registry.notusmi.com", "http://ourea:8215/rob/gijmo-ui.git", sha)
	if err != nil || got != "registry.notusmi.com/foundry/visual/gijmo-ui:"+sha {
		t.Errorf("%q %v", got, err)
	}
	if _, err := ArtifactRef("r", "http://d/rob/x.git", "abc"); err == nil || !strings.Contains(err.Error(), "no commit sha") {
		t.Errorf("short sha: %v", err)
	}
	if _, err := ArtifactRef("r", "http://d/rob/x.git", strings.Repeat("A", 40)); err == nil {
		t.Error("an upper-case sha is not a tag the retention keeps")
	}
	for _, repo := range []string{"", "http://d/rob/Gijmo.git", "http://d/rob/-x"} {
		if _, err := ArtifactRef("r", repo, sha); err == nil || !strings.Contains(err.Error(), "names no registry path") {
			t.Errorf("%q: %v", repo, err)
		}
	}
}

func TestFetchHintNamesTheRepoAndTheLayer(t *testing.T) {
	ref := "registry.notusmi.com/foundry/visual/gijmo-ui:" + strings.Repeat("b", 40)
	got := FetchHint(ref)
	for _, want := range []string{
		"artifact " + ref,
		"oras manifest fetch " + ref,
		"jq -r '.layers[].digest'",
		"oras blob fetch --output - registry.notusmi.com/foundry/visual/gijmo-ui@{} | tar xz'",
		"baselines/",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
}

func TestPackageName(t *testing.T) {
	for raw, want := range map[string]string{
		`{"name":"@gijmo/gallery","version":"0.1.0"}`: "@gijmo/gallery",
		`{"version":"0.1.0"}`:                         "",
		`{"name":"x"`:                                 "",
	} {
		if got := PackageName(raw); got != want {
			t.Errorf("%s: %q, want %q", raw, got, want)
		}
	}
}
