// Package visuallane holds the visual lane's judgements as pure functions over
// bytes: which screenshot suites a tree declares (visual.toml), which
// Playwright its lockfile pins, what a Playwright JSON report says failed, and
// where a run's artifact lives. The atom in package main (ts:visual) runs the
// containers and hands their output here; every decision is here, where a test
// reaches it without an engine.
//
// THE LANE IS visual, its own Job and its own row in the CI record (Rob,
// 2026-10-04: "Give 'em a cluster lane"). A screenshot baseline is a fact
// about ONE rasteriser, so the suite runs in one pinned image — the ts lane's
// bun, which is the image gijmo-ui's visual-in-ci-image.sh pinned — and never
// on a workstation, where the fonts differ enough to fail on nothing.
package visuallane

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"dagger/foundry-tools/internal/checks"
)

// ConfigFile is the tree's declaration, at its root: the screenshot suites
// the lane runs. A tree without one has no visual surface, and the door asks
// no visual lane of it.
const ConfigFile = "visual.toml"

// Suite is one screenshot suite: a workspace package directory holding a
// playwright.config.* whose suite compares against committed baselines.
type Suite struct {
	Dir string
}

// ParseConfig reads visual.toml. Every refusal names what is wrong, because a
// declaration the lane cannot read is the committer's to fix, and an unknown
// key is refused rather than ignored — a misspelt `dirs` must not read as a
// tree that declared nothing.
func ParseConfig(raw string) ([]Suite, error) {
	var doc struct {
		Suite []struct {
			Dir string `toml:"dir"`
		} `toml:"suite"`
	}
	meta, err := toml.Decode(raw, &doc)
	if err != nil {
		return nil, fmt.Errorf("%s does not parse: %v", ConfigFile, err)
	}
	if extra := meta.Undecoded(); len(extra) > 0 {
		return nil, fmt.Errorf("%s carries a key the lane does not read: %s", ConfigFile, extra[0])
	}
	if len(doc.Suite) == 0 {
		return nil, fmt.Errorf("%s names no [[suite]]", ConfigFile)
	}
	out := make([]Suite, 0, len(doc.Suite))
	for _, s := range doc.Suite {
		dir, err := suiteDir(s.Dir)
		if err != nil {
			return nil, err
		}
		if slices.Contains(out, Suite{Dir: dir}) {
			return nil, fmt.Errorf("%s names the suite %q twice", ConfigFile, dir)
		}
		out = append(out, Suite{Dir: dir})
	}
	return out, nil
}

// suiteDir is a declared directory, cleaned: relative, inside the tree, and
// spelled the way the tree spells it. "." is the root.
func suiteDir(dir string) (string, error) {
	clean := path.Clean(dir)
	switch {
	case strings.TrimSpace(dir) == "":
		return "", fmt.Errorf("%s: a [[suite]] has no dir", ConfigFile)
	case path.IsAbs(clean), clean == "..", strings.HasPrefix(clean, "../"):
		return "", fmt.Errorf("%s: the suite dir %q is not inside the tree", ConfigFile, dir)
	}
	return clean, nil
}

// lockPin is one Playwright the lockfile resolves, as bun.lock spells its
// package key ("@playwright/test@1.62.0"). The version is the whole argv
// element `playwright@<v>` later, so it is matched strictly here.
var lockPin = regexp.MustCompile(`"@playwright/test@([0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.]+)?)"`)

// PlaywrightVersion answers the one @playwright/test version bun.lock
// resolves. The browsers are installed for exactly that version, BEFORE the
// tree is mounted, so their layer is keyed on the version alone and a commit
// does not download Chromium again. None, or two different ones, is refused:
// a browser built for the wrong revision does not launch.
func PlaywrightVersion(lock string) (string, error) {
	var found []string
	for _, m := range lockPin.FindAllStringSubmatch(lock, -1) {
		if !slices.Contains(found, m[1]) {
			found = append(found, m[1])
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("bun.lock resolves no @playwright/test — a screenshot suite needs it")
	case 1:
		return found[0], nil
	}
	return "", fmt.Errorf("bun.lock resolves @playwright/test at %s — one revision, or the browsers fit only one suite",
		strings.Join(found, " and "))
}

// PackageName is a package.json's name, "" when it has none or does not read
// — a suite with no name is run without building its dependencies first.
func PackageName(packageJSON string) string {
	var p struct {
		Name string `json:"name"`
	}
	// A manifest that does not parse leaves p zero — the "" the doc promises —
	// so the error says nothing the answer does not already say.
	_ = json.Unmarshal([]byte(packageJSON), &p)
	return p.Name
}

// The causes a finding carries, so forty failing shots group as one.
const (
	CauseMismatch = "screenshot-mismatch"
	CauseMissing  = "snapshot-missing"
	CauseFailed   = "test-failed"
	CauseFlaky    = "flaky"
	CauseSuite    = "suite-error"
	CauseNoTests  = "no-tests"
)

// Stats is what the report counted.
type Stats struct {
	Expected   int `json:"expected"`
	Unexpected int `json:"unexpected"`
	Flaky      int `json:"flaky"`
	Skipped    int `json:"skipped"`
}

// Total is every test the suite ran or skipped.
func (s Stats) Total() int { return s.Expected + s.Unexpected + s.Flaky + s.Skipped }

type report struct {
	Suites []suite     `json:"suites"`
	Errors []errorBody `json:"errors"`
	Stats  Stats       `json:"stats"`
}

type suite struct {
	Title  string  `json:"title"`
	File   string  `json:"file"`
	Specs  []spec  `json:"specs"`
	Suites []suite `json:"suites"`
}

type spec struct {
	Title string `json:"title"`
	File  string `json:"file"`
	Tests []struct {
		Status  string   `json:"status"`
		Results []result `json:"results"`
	} `json:"tests"`
}

// result is one attempt at a test.
type result struct {
	Errors      []errorBody  `json:"errors"`
	Attachments []attachment `json:"attachments"`
}

type attachment struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type errorBody struct {
	Message string `json:"message"`
}

// Failures reads one suite's Playwright JSON report into findings: a test
// that failed every attempt is violated, one that passed on its retry is
// drifted (the config's one retry absorbs rasterisation jitter, and a reader
// should still see it), an error outside any test — the web server that did
// not build, a config that did not load — is unanalyzable. A report that
// counted no tests at all is unanalyzable too: a screenshot suite over nothing
// passes forever.
//
// results is the directory Playwright was told to write into (--output); a
// diff image is named by its path under it, which is where the artifact keeps
// it (ArtifactPath).
func Failures(dir, results string, raw []byte) ([]checks.Finding, Stats, error) {
	var r report
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, Stats{}, fmt.Errorf("the Playwright JSON report for %s does not parse: %v", dir, err)
	}
	var out []checks.Finding
	for _, e := range r.Errors {
		out = append(out, finding(checks.VerdictUnanalyzable, dir, CauseSuite, firstLine(e.Message)))
	}
	walk(r.Suites, nil, func(titles []string, s spec) {
		for _, t := range s.Tests {
			subject := dir + ": " + strings.Join(append(titles, s.Title), " › ")
			switch t.Status {
			case "unexpected":
				cause, detail := classify(t.Results)
				out = append(out, finding(checks.VerdictViolated, subject, cause, detail+diffs(results, dir, t.Results)))
			case "flaky":
				out = append(out, finding(checks.VerdictDrifted, subject, CauseFlaky, "failed, then passed on its retry"))
			}
		}
	})
	if r.Stats.Total() == 0 && len(r.Errors) == 0 {
		out = append(out, finding(checks.VerdictUnanalyzable, dir, CauseNoTests, "the suite ran no test — a screenshot suite over nothing passes forever"))
	}
	return out, r.Stats, nil
}

// walk visits every spec under the suites with the titles above it. The file
// suite's title is its file name, so the subject reads file › describe › test.
func walk(suites []suite, titles []string, visit func([]string, spec)) {
	for _, s := range suites {
		here := append(slices.Clone(titles), s.Title)
		for _, sp := range s.Specs {
			visit(here, sp)
		}
		walk(s.Suites, here, visit)
	}
}

// classify reads the LAST attempt's first error: what the test finally died of.
func classify(results []result) (string, string) {
	if len(results) == 0 || len(results[len(results)-1].Errors) == 0 {
		return CauseFailed, "failed with no error recorded"
	}
	msg := stripANSI(results[len(results)-1].Errors[0].Message)
	switch {
	case strings.Contains(msg, "snapshot doesn't exist"):
		return CauseMissing, firstLineWith(msg, "snapshot doesn't exist")
	case strings.Contains(msg, "toHaveScreenshot"), strings.Contains(msg, "Screenshot comparison failed"):
		return CauseMismatch, firstLineWith(msg, "pixels")
	}
	return CauseFailed, firstLine(msg)
}

// diffs names the diff images the last attempt attached, where the artifact
// holds them.
func diffs(results, dir string, rs []result) string {
	if len(rs) == 0 {
		return ""
	}
	var names []string
	for _, a := range rs[len(rs)-1].Attachments {
		if strings.HasSuffix(a.Name, "-diff.png") {
			if p, ok := ArtifactPath(results, dir, a.Path); ok {
				names = append(names, p)
			}
		}
	}
	if len(names) == 0 {
		return ""
	}
	return " (diff: " + strings.Join(names, ", ") + ")"
}

// ArtifactPath is where the artifact keeps a file Playwright wrote under its
// output directory: results/<suite dir>/<path under the output>. A path
// outside the output directory has no place in the artifact.
func ArtifactPath(results, dir, file string) (string, bool) {
	rel, ok := strings.CutPrefix(file, strings.TrimSuffix(results, "/")+"/")
	if !ok || rel == "" {
		return "", false
	}
	return path.Join("results", dir, rel), true
}

var ansi = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSI(s string) string { return ansi.ReplaceAllString(s, "") }

// firstLine is a message's first non-empty line, trimmed.
func firstLine(msg string) string {
	for _, l := range strings.Split(stripANSI(msg), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return "no message"
}

// firstLineWith is the first line carrying needle, or the first line.
func firstLineWith(msg, needle string) string {
	for _, l := range strings.Split(msg, "\n") {
		if strings.Contains(l, needle) {
			return strings.TrimSpace(l)
		}
	}
	return firstLine(msg)
}

func finding(verdict, subject, cause, detail string) checks.Finding {
	return checks.Finding{Verdict: verdict, Subject: subject, Cause: cause, Detail: detail, Probe: Atom}
}

// Atom is the lane's one atom.
const Atom = "ts:visual"

// State is a suite's state from Playwright's exit and what its report said.
// Playwright exits 0 when everything passed and 1 when a test failed or the
// run itself errored (a web server that did not build is the tree's, not the
// runner's); anything else did not run. An exit-0 run that counted no tests
// is still a finding: the suite exists and photographs nothing.
func State(exit int, found []checks.Finding) int {
	switch exit {
	case 0:
		for _, f := range found {
			if f.Cause == CauseNoTests {
				return 1
			}
		}
		return 0
	case 1:
		return 1
	}
	return 2
}

// Summary is the reason's head for one suite: its counts.
func Summary(dir string, s Stats) string {
	return fmt.Sprintf("%s: %d passed, %d failed, %d flaky, %d skipped", dir, s.Expected, s.Unexpected, s.Flaky, s.Skipped)
}

// Lines is every finding, one per line, so the reason reads as what moved.
func Lines(found []checks.Finding) string {
	var b strings.Builder
	for _, f := range found {
		fmt.Fprintf(&b, "\n  %s  %s — %s", f.Verdict, f.Subject, f.Detail)
	}
	return b.String()
}

// ArtifactRepo is the registry repository a repo's visual artifacts are
// pushed to. Its tags are commit shas, which the registry's retention keeps
// for a day and the two newest always (flux foundry/zot-config.json,
// ^[0-9a-f]{12,40}$) — an artifact is for the pull in front of you, not an
// archive.
const ArtifactRepo = "foundry/visual"

var shaRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

var starRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// ArtifactRef is where a run's artifact is pushed: <registry>/foundry/visual/
// <star>:<sha>. A run with no commit (a local `dagger check`) or a repo name a
// registry path cannot carry has nowhere to push, and says so.
func ArtifactRef(registry, repo, sha string) (string, error) {
	star := path.Base(strings.TrimSuffix(repo, ".git"))
	switch {
	case !shaRE.MatchString(sha):
		return "", fmt.Errorf("the run names no commit sha, so its artifact has no tag")
	case !starRE.MatchString(star): // "" is path.Base's ".", which no star is
		return "", fmt.Errorf("the repo %q names no registry path", repo)
	}
	return registry + "/" + ArtifactRepo + "/" + star + ":" + sha, nil
}

// FetchHint is the line that says how to read an artifact back: one layer,
// a tar of results/ (the diffs, per suite dir) and baselines/ (every snapshot
// the update run wrote, at its path in the tree).
func FetchHint(ref string) string {
	return "artifact " + ref + " — results/<suite>/ holds the diffs, baselines/ the regenerated snapshots at their tree paths; " +
		"fetch: oras manifest fetch " + ref + " | jq -r '.layers[0].digest' | xargs -I{} oras blob fetch --output - " +
		ref[:strings.LastIndex(ref, ":")] + "@{} | tar xz"
}
