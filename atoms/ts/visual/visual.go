// Package tsvisual is the atom ts:visual: every Playwright screenshot suite
// visual.toml declares matches the baselines the tree commits.
package tsvisual

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/visuallane"
)

// Def is the atom. It runs the suites in a Playwright image whose browsers are
// the lockfile's version, installs the tree's packages and writes snapshots
// into the suite, so it writes the tree. It needs the pod's identity for one
// thing, the artifact push, and bun to run the tree's own scripts.
var Def = atomkit.Def{
	ID: "ts:visual", Stage: checks.StageVisual, Lane: checks.LaneTS,
	Group:         checks.GroupLanguage,
	Desc:          "Every Playwright screenshot suite visual.toml declares matches its committed baselines, rendered in the lane's pinned image.",
	Surface:       "root:package.json",
	Toolchain:     "playwright",
	Needs:         []string{"svid", "tool:bun"},
	Timeout:       30 * time.Minute,
	WritesTree:    true,
	NeedsManifest: true,
	Check:         check,
}

const id = visuallane.Atom

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StageVisual, Lane: checks.LaneTS, NeedsManifest: true}

// suiteRun is one suite's run: its findings, its state and what to keep.
type suiteRun struct {
	dir     string
	out     string // where Playwright wrote for this suite, outside the tree
	state   int
	reason  string
	found   []checks.Finding
	ran     bool // the check run wrote a report the lane could read: results are carried
	updated bool // the update run ran, so baselines are carried
	// updateErr is why the update run could not run, "" when it did.
	updateErr string
}

// check runs every suite visual.toml declares, in the tree's own bun and the
// image's own browsers, and settles on what failed.
//
// A BASELINE IS A FACT ABOUT ONE RASTERISER, so every baseline is generated in
// one image and that image runs in the cluster (gijmo-ui
// visual-in-ci-image.sh: the same source on the workstation and in the image
// differed by ratio 0.03 on 9 of 24 shots, six times the suite's tolerance).
//
// THE ARTIFACT IS THE UPDATE PATH. A run that finds something runs the suite
// again with --update-snapshots=changed and pushes one image to the registry
// (visuallane.ArtifactRef): the first run's diff images under results/ and
// every snapshot the second run wrote under baselines/, at its path in the
// tree. Accepting an intended change is copying baselines/ over the tree and
// committing it. The push never changes the verdict; the reason says where the
// artifact is, or why it is not.
//
// EVERY CHECK RUN COMES BEFORE ANY UPDATE RUN. The suites share one checkout
// here, and an update run writes snapshots into it; the suites were each their
// own container, so one suite's regenerated baselines must not be what the next
// suite is graded against.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	settle := func(state int, reason string) checks.Verdict {
		return checks.VerdictOf(row, state, id+": "+reason)
	}
	entries, err := atoms.RootEntries(in.Root)
	if err != nil {
		return settle(2, "CANNOT RUN - the tree's root could not be listed: "+err.Error())
	}
	if !slices.Contains(entries, visuallane.ConfigFile) {
		return settle(0, "ABSENT - no "+visuallane.ConfigFile+" at the root: this tree declares no screenshot suite")
	}
	raw, err := os.ReadFile(filepath.Join(in.Root, visuallane.ConfigFile))
	if err != nil {
		return settle(2, "CANNOT RUN - "+visuallane.ConfigFile+" could not be read: "+err.Error())
	}
	suites, err := visuallane.ParseConfig(string(raw))
	if err != nil {
		return settle(1, "FINDINGS - "+err.Error())
	}
	lock, err := os.ReadFile(filepath.Join(in.Root, "bun.lock"))
	if err != nil {
		return settle(2, "CANNOT RUN - the tree carries no readable bun.lock, so the Playwright to install is unknown: "+err.Error())
	}
	if _, err := visuallane.PlaywrightVersion(string(lock)); err != nil {
		return settle(1, "FINDINGS - "+err.Error())
	}

	work := workDir()
	e := env{in: in, work: work}
	if out, code := e.run(ctx, in.Root, "bun", "install", "--frozen-lockfile"); !atoms.ToolRan(code) {
		return settle(2, "CANNOT RUN - the atom never ran: "+out)
	} else if code != 0 {
		return settle(2, "CANNOT RUN - frozen lockfile install failed\n"+out)
	}

	var runs []*suiteRun
	for i, s := range suites {
		runs = append(runs, e.check(ctx, s.Dir, filepath.Join(work, "visual", strconv.Itoa(i)), slices.Contains(entries, "turbo.json")))
	}
	for _, r := range runs {
		if r.state == 1 && r.ran {
			e.update(ctx, r)
		}
	}
	state, reason, found := fold(runs)
	if state == 1 {
		reason += "\n" + e.publish(ctx, runs)
	}
	v := settle(state, reason)
	v.Findings = found
	return v
}

// fold is every suite's answer as one: the worst state, every reason, every
// finding.
func fold(runs []*suiteRun) (int, string, []checks.Finding) {
	state := 0
	var reasons []string
	var found []checks.Finding
	for _, s := range runs {
		state = max(state, s.state)
		reasons = append(reasons, s.reason)
		found = append(found, s.found...)
	}
	return state, strings.Join(reasons, "\n"), found
}
