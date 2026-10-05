package main

import (
	"context"
	"path"
	"slices"
	"strconv"
	"strings"

	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
	"dagger/foundry-tools/internal/visuallane"
)

// THE VISUAL LANE'S ATOM (stage `visual`, run by `gate-file --stage=visual` as
// a lane of its own). It runs every Playwright screenshot suite visual.toml
// declares, in the ts lane's pinned bun, against the baselines the tree
// commits, and settles on what failed. The judgements are internal/
// visuallane's; this file runs the containers.
//
// WHY IN THE CLUSTER. A baseline is a fact about one rasteriser: the same
// source on the workstation and in the image differed by ratio 0.03 on 9 of 24
// shots, six times the suite's tolerance (gijmo-ui visual-in-ci-image.sh). So
// every baseline is generated in ONE image, and that image runs on the engine.
//
// THE ARTIFACT IS THE UPDATE PATH. A run that finds something runs the suite
// again with --update-snapshots=changed and pushes one image to the registry
// (visuallane.ArtifactRef): the first run's diff images under results/ and
// every snapshot the second run wrote under baselines/, at its path in the
// tree. Accepting an intended change is copying baselines/ over the tree and
// committing it — no workstation run, and nothing rendered anywhere but here.
// The push needs --artifact-auth (the lane's registry credential); without it
// the verdict is unchanged and the reason says the artifact was not pushed.

func init() {
	register("ts:visual", tsVisual)
}

// visualOut is where Playwright writes, outside the mounted tree, so a check
// run never writes into the tree it judges.
const (
	visualOut     = "/tmp/visual"
	visualResults = visualOut + "/results"
	visualReport  = visualOut + "/report.json"
	visualBrowser = "/ms-playwright"
	visualReg     = "registry.notusmi.com"
)

// withArtifacts hands the run the registry credential the visual atom pushes
// its artifact with, and the commit it is tagged by. Nil pushes nothing.
func (r *run) withArtifacts(auth *dagger.Secret, sha string) *run {
	r.artifactAuth, r.sha = auth, sha
	return r
}

// visualSuite is one suite's run: its findings, its state and what to keep.
type visualSuite struct {
	dir     string
	state   int
	reason  string
	found   []checks.Finding
	results *dagger.Directory // the check run's output, nil when it never ran
	updated *dagger.Directory // the suite dir after the update run, nil when none
}

func tsVisual(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID(visuallane.Atom)
	settle := func(state int, reason string) checks.Verdict {
		return checks.VerdictOf(a, state, a.ID+": "+reason)
	}
	entries, err := r.src.Entries(ctx)
	if err != nil {
		return settle(2, "CANNOT RUN - the tree's root could not be listed: "+err.Error())
	}
	if !slices.Contains(entries, visuallane.ConfigFile) {
		return settle(0, "ABSENT - no "+visuallane.ConfigFile+" at the root: this tree declares no screenshot suite")
	}
	raw, err := r.src.File(visuallane.ConfigFile).Contents(ctx)
	if err != nil {
		return settle(2, "CANNOT RUN - "+visuallane.ConfigFile+" could not be read: "+err.Error())
	}
	suites, err := visuallane.ParseConfig(raw)
	if err != nil {
		return settle(1, "FINDINGS - "+err.Error())
	}
	lock, err := r.src.File("bun.lock").Contents(ctx)
	if err != nil {
		return settle(2, "CANNOT RUN - the tree carries no readable bun.lock, so the Playwright to install is unknown: "+err.Error())
	}
	version, err := visuallane.PlaywrightVersion(lock)
	if err != nil {
		return settle(1, "FINDINGS - "+err.Error())
	}

	// THE BROWSERS BEFORE THE TREE: this layer is keyed on the image and the
	// version alone, so a new commit downloads no Chromium. Provisioning, under
	// the default Expect: a failed install is the substrate's.
	ctr := r.laneBase(checks.ImageTS).
		WithEnvVariable("PLAYWRIGHT_BROWSERS_PATH", visualBrowser).
		WithEnvVariable("TURBO_TELEMETRY_DISABLED", "1").
		WithExec([]string{"bunx", "playwright@" + version, "install", "--with-deps", "chromium"}).
		WithMountedDirectory("/src", r.src).
		WithWorkdir("/src")
	installed := ctr.WithExec([]string{"bun", "install", "--frozen-lockfile"}, anyExit)
	out, code, err := output(ctx, installed)
	if err != nil {
		return settle(2, "CANNOT RUN - the atom never ran: "+err.Error())
	}
	if code != 0 {
		return settle(2, "CANNOT RUN - frozen lockfile install failed\n"+out)
	}

	var runs []visualSuite
	for _, s := range suites {
		runs = append(runs, runVisualSuite(ctx, installed, s.Dir, slices.Contains(entries, "turbo.json")))
	}
	state, reason, found := foldVisual(runs)
	if state == 1 {
		reason += "\n" + publishVisual(ctx, r, runs)
	}
	v := settle(state, reason)
	v.Findings = found
	return v
}

// runVisualSuite builds what the suite imports and runs it, then — when it
// found something — runs it again to regenerate the baselines it would accept.
func runVisualSuite(ctx context.Context, installed *dagger.Container, dir string, turbo bool) visualSuite {
	vs := visualSuite{dir: dir}
	cannot := func(why string) visualSuite {
		vs.state, vs.reason = 2, dir+": CANNOT RUN - "+why
		return vs
	}
	pkg, present, err := ctrFileIfPresent(ctx, installed, path.Join("/src", dir, "package.json"))
	if !present {
		why := "no such file"
		if err != nil {
			why = err.Error()
		}
		vs.state, vs.reason = 1, dir+": FINDINGS - the suite has no package.json: "+why
		return vs
	}
	built := installed.WithWorkdir(path.Join("/src", dir))
	// A workspace's own packages are built before the suite imports them —
	// turbo's `<pkg>^...` is the suite's dependencies, not the suite: its own
	// build is its web server's to run.
	if name := visuallane.PackageName(pkg); turbo && name != "" {
		built = built.WithWorkdir("/src").
			WithExec([]string{"bunx", "turbo", "run", "build", "--filter=" + name + "^..."}, anyExit)
		out, code, err := output(ctx, built)
		if err != nil {
			return cannot("the build never ran: " + err.Error())
		}
		if code != 0 {
			vs.state, vs.reason = 1, dir+": FINDINGS - what the suite imports did not build\n"+out
			return vs
		}
		built = built.WithWorkdir(path.Join("/src", dir))
	}
	check := built.
		WithEnvVariable("PLAYWRIGHT_JSON_OUTPUT_NAME", visualReport).
		WithExec([]string{"bunx", "playwright", "test", "--reporter=list,json", "--update-snapshots=none", "--output=" + visualResults}, anyExit)
	out, code, err := outputBoth(ctx, check)
	if err != nil {
		return cannot("the suite never ran: " + err.Error())
	}
	raw, wrote, _ := ctrFileIfPresent(ctx, check, visualReport)
	if !wrote {
		return cannot("Playwright wrote no JSON report (exit " + strconv.Itoa(code) + ")\n" + out)
	}
	found, stats, err := visuallane.Failures(dir, visualResults, []byte(raw))
	if err != nil {
		return cannot(err.Error())
	}
	vs.found, vs.results = found, check.Directory(visualResults)
	vs.state = visuallane.State(code, found)
	vs.reason = visuallane.Summary(dir, stats) + visuallane.Lines(found)
	if vs.state == 2 {
		vs.reason += "\n" + out
		return vs
	}
	if vs.state == 1 {
		// The exit is not read: a test that fails for a reason no snapshot
		// answers still fails here, and what the run DID write is the point.
		vs.updated = built.
			WithExec([]string{"bunx", "playwright", "test", "--reporter=list", "--update-snapshots=changed", "--output=" + visualOut + "/update"}, anyExit).
			Directory(path.Join("/src", dir))
	}
	return vs
}

// foldVisual is every suite's answer as one: the worst state, every reason,
// every finding.
func foldVisual(runs []visualSuite) (int, string, []checks.Finding) {
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

// publishVisual pushes the run's artifact and answers the line that says
// where it is, or why it is not. It never changes the verdict: the findings
// are the finding, and the artifact is how a person reads them and accepts a
// change.
func publishVisual(ctx context.Context, r *run, runs []visualSuite) string {
	ref, err := visuallane.ArtifactRef(visualReg, r.repo, r.sha)
	if err != nil {
		return "artifact: not pushed — " + err.Error()
	}
	if r.artifactAuth == nil {
		return "artifact: not pushed — the lane was given no --artifact-auth"
	}
	art := dag.Directory()
	carried := false
	for _, s := range runs {
		if s.results != nil {
			art = art.WithDirectory(path.Join("results", s.dir), s.results)
			carried = true
		}
		if s.updated != nil {
			art = art.WithDirectory(path.Join("baselines", s.dir), s.updated.Filter(dagger.DirectoryFilterOpts{
				Include: []string{"**/*-snapshots/**"},
				Exclude: []string{"**/node_modules/**"},
			}))
			carried = true
		}
	}
	// NOTHING TO CARRY IS NOT A PUSH. A suite that never ran, because what it
	// imports did not build or it has no package.json, writes no results. An
	// image of an empty rootfs is a manifest with no layers, and the registry
	// refuses it: "PUT …/manifests/<sha>: 400 Bad Request, unknown: manifest
	// invalid" (gijmo-ui 4ed2dae, 2026-10-05). That failed push put six ERROR
	// spans into a transcript whose real finding was the build.
	if !carried {
		return "artifact: not pushed — no suite ran far enough to write results or baselines"
	}
	auth, err := r.artifactAuth.Plaintext(ctx)
	if err != nil {
		return "artifact: not pushed — the registry credential did not read: " + err.Error()
	}
	user, password, err := buildlane.RegistryLogin(auth, visualReg)
	if err != nil {
		return "artifact: not pushed — " + err.Error()
	}
	if _, err := publish(ctx, dag.Container().WithRootfs(art), ref, user, dag.SetSecret("visual-registry-password", password)); err != nil {
		return "artifact: not pushed — " + err.Error()
	}
	return visuallane.FetchHint(ref)
}
