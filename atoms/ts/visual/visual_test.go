package tsvisual

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/svidcred"
	"dagger/foundry-tools/internal/visuallane"
)

const (
	sha        = "0123456789abcdef0123456789abcdef01234567"
	origin     = "http://ourea.prime.svc.cluster.local:8215/rob/gijmo-ui.git"
	authJSON   = `{"auths":{"registry.notusmi.com":{"username":"publisher","password":"pw"}}}`
	passReport = `{"suites":[],"errors":[],"stats":{"expected":3}}`
	failReport = `{"suites":[{"title":"gallery.spec.ts","specs":[{"title":"primitives render in dusk","tests":[{"status":"unexpected","results":[` +
		`{"errors":[{"message":"Error: expect(locator).toHaveScreenshot(expected) failed\n\n  9 pixels (ratio 0.01 of all image pixels) are different."}],` +
		`"attachments":[{"name":"dusk-diff.png","path":"RESULTS/g/dusk-diff.png"}]}]}]}]}],"errors":[],"stats":{"expected":2,"unexpected":1}}`

	ref = "registry.notusmi.com/foundry/visual/gijmo-ui:" + sha
)

// world is one run's doubles: the program runner that stands in for bun, and
// the registry push that stands in for the network.
type world struct {
	t     *testing.T
	root  string
	calls []atoms.Cmd
	// answers are keyed by a needle in the joined argv: the first that matches.
	exits map[string]int
	outs  map[string]string
	// report is what the check run writes; "" writes none. results are the files
	// it writes under --output, snapshots what the update run writes in its dir.
	report    string
	results   map[string]string
	snapshots map[string]string
	pushed    []pushCall
	pushErr   error
	env       map[string]string
	svidErr   error
}

type pushCall struct {
	cred   visuallane.Credential
	ref    string
	layers map[string]map[string]string // layer index -> tar path -> body
}

func argv(c atoms.Cmd) string { return strings.Join(append([]string{c.Name}, c.Args...), " ") }

func envOf(c atoms.Cmd, key string) string {
	for i := len(c.Env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(c.Env[i], key+"="); ok {
			return v
		}
	}
	return ""
}

func argOf(c atoms.Cmd, prefix string) string {
	for _, a := range c.Args {
		if v, ok := strings.CutPrefix(a, prefix); ok {
			return v
		}
	}
	return ""
}

func (w *world) exec(_ context.Context, c atoms.Cmd) (string, int) {
	w.calls = append(w.calls, c)
	line := argv(c)
	switch {
	case strings.Contains(line, "--update-snapshots=none"):
		w.checkRun(c)
	case strings.Contains(line, "--update-snapshots=changed"):
		for rel, body := range w.snapshots {
			put(w.t, c.Dir, rel, body)
		}
	}
	for needle, code := range w.exits {
		if strings.Contains(line, needle) {
			return w.outs[needle], code
		}
	}
	return "", 0
}

// checkRun does what the real suite does: the report where it was told, and
// the files under --output.
func (w *world) checkRun(c atoms.Cmd) {
	out := argOf(c, "--output=")
	if w.report != "" {
		report := strings.ReplaceAll(w.report, "RESULTS", out)
		put(w.t, filepath.Dir(envOf(c, "PLAYWRIGHT_JSON_OUTPUT_NAME")), filepath.Base(envOf(c, "PLAYWRIGHT_JSON_OUTPUT_NAME")), report)
	}
	for rel, body := range w.results {
		put(w.t, out, rel, body)
	}
}

func put(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// layerFiles reads a pushed layer back as the fetch hint does.
func layerFiles(t *testing.T, l visuallane.Layer) map[string]string {
	t.Helper()
	zr, err := gzip.NewReader(strings.NewReader(string(l.Data)))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	out := map[string]string{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(tr)
		out[h.Name] = string(body)
	}
}

// gijmo is gijmo-ui's shape: a turbo workspace whose gallery declares the
// suite, and a lockfile pinning Playwright. extra overrides any file; an empty
// value deletes it.
func gijmo(t *testing.T, extra map[string]string) *world {
	t.Helper()
	w := &world{
		t: t, root: t.TempDir(), report: passReport, env: map[string]string{"ATOM_SHA": sha, "SPIFFE_ENDPOINT_SOCKET": "unix:///run/spire/agent/public/api.sock"},
	}
	t.Setenv("ATOM_WORK", t.TempDir())
	tree := map[string]string{
		"package.json":              `{"name":"gijmo-ui"}`,
		"turbo.json":                "{}",
		"visual.toml":               "[[suite]]\ndir = \"apps/gallery\"\n",
		"bun.lock":                  `{"packages":{"@playwright/test":["@playwright/test@1.62.0"]}}`,
		"apps/gallery/package.json": `{"name":"@gijmo/gallery"}`,
	}
	for k, v := range extra {
		tree[k] = v
	}
	for k, v := range tree {
		if v != "" {
			put(t, w.root, k, v)
		}
	}
	oldGetenv, oldPush, oldFetch := getenv, push, fetchSVID
	getenv = func(k string) string { return w.env[k] }
	push = func(_ context.Context, cred visuallane.Credential, r string, layers []visuallane.Layer) error {
		p := pushCall{cred: cred, ref: r, layers: map[string]map[string]string{}}
		for i, l := range layers {
			p.layers[string(rune('0'+i))] = layerFiles(t, l)
		}
		w.pushed = append(w.pushed, p)
		return w.pushErr
	}
	fetchSVID = func(context.Context, string, string) (string, error) { return "svid-jwt", w.svidErr }
	t.Cleanup(func() { getenv, push, fetchSVID = oldGetenv, oldPush, oldFetch })
	return w
}

func (w *world) run() checks.Verdict {
	in := atomkit.In{Root: w.root, Origin: origin, Exec: w.exec}
	return check(context.Background(), in)
}

func want(t *testing.T, v checks.Verdict, state int, in ...string) {
	t.Helper()
	if v.State != state {
		t.Errorf("state %d, want %d: %s", v.State, state, v.Reason)
	}
	for _, s := range in {
		if !strings.Contains(v.Reason, s) {
			t.Errorf("reason lacks %q:\n%s", s, v.Reason)
		}
	}
}

func (w *world) argvs() []string {
	var out []string
	for _, c := range w.calls {
		out = append(out, argv(c))
	}
	return out
}

// TestTheDeclarationValidatesAndKeepsTheRowItReplaces holds the row the chain
// was catalogued by: id, stage, lane, group, description and surface.
func TestTheDeclarationValidatesAndKeepsTheRowItReplaces(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != "ts:visual" || Def.Stage != checks.StageVisual || Def.Lane != checks.LaneTS || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "playwright" || !Def.NeedsManifest || Def.Surface != "root:package.json" || !Def.WritesTree ||
		Def.NotOnForks != "" || Def.SubsumedBy != "" || !slices.Equal(Def.Needs, []string{"svid", "tool:bun"}) ||
		Def.Desc != "Every Playwright screenshot suite visual.toml declares matches its committed baselines, rendered in the lane's pinned image." ||
		Def.Timeout != 30*time.Minute {
		t.Errorf("%+v", Def)
	}
}

func TestTSVisualIsAbsentWithoutADeclaration(t *testing.T) {
	w := gijmo(t, map[string]string{"visual.toml": ""})
	v := w.run()
	if v.State != 0 || v.Result != "absent" || !strings.Contains(v.Reason, "ts:visual: ABSENT - no visual.toml at the root") {
		t.Fatalf("%+v", v)
	}
	if len(w.calls) != 0 {
		t.Errorf("an absent tree ran %v", w.argvs())
	}
}

func TestTSVisualRefusesWhatItCannotReadOrRun(t *testing.T) {
	w := gijmo(t, nil)
	w.root = filepath.Join(w.root, "nowhere")
	want(t, w.run(), 2, "the tree's root could not be listed")

	// A dangling link is listed and will not read.
	w = gijmo(t, map[string]string{"visual.toml": ""})
	if err := os.Symlink("/nonexistent", filepath.Join(w.root, "visual.toml")); err != nil {
		t.Fatal(err)
	}
	want(t, w.run(), 2, "visual.toml could not be read")

	w = gijmo(t, map[string]string{"visual.toml": "[[suite]]\ndirs = \"x\"\n"})
	want(t, w.run(), 1, "FINDINGS - visual.toml carries a key the lane does not read")

	w = gijmo(t, map[string]string{"bun.lock": ""})
	want(t, w.run(), 2, "no readable bun.lock")

	w = gijmo(t, map[string]string{"bun.lock": "{}"})
	want(t, w.run(), 1, "FINDINGS - bun.lock resolves no @playwright/test")

	w = gijmo(t, nil)
	w.exits, w.outs = map[string]int{"bun install": 1}, map[string]string{"bun install": "lockfile had changes, but lockfile is frozen"}
	want(t, w.run(), 2, "frozen lockfile install failed", "lockfile is frozen")

	w = gijmo(t, nil)
	w.exits, w.outs = map[string]int{"bun install": -1}, map[string]string{"bun install": "exec: bun: not found"}
	want(t, w.run(), 2, "the atom never ran", "not found")
}

// The browsers are the image's, the suite's packages are installed frozen, the
// dependencies are built by turbo, and the suite runs from its own directory
// with Playwright writing outside the tree.
func TestTSVisualPassesAndBuildsTheChainInOrder(t *testing.T) {
	w := gijmo(t, nil)
	v := w.run()
	want(t, v, 0)
	if len(v.Findings) != 0 {
		t.Errorf("%+v", v.Findings)
	}
	if said := strings.Join(v.Logs, "\n"); !strings.Contains(said, "apps/gallery: 3 passed, 0 failed, 0 flaky, 0 skipped") {
		t.Errorf("logs:\n%s", said)
	}
	checkLine := "bunx playwright test --reporter=list,json --update-snapshots=none --output=" + filepath.Join(os.Getenv("ATOM_WORK"), "visual", "0", "results")
	wantOrder := []string{"bun install --frozen-lockfile", "bunx turbo run build --filter=@gijmo/gallery^...", checkLine}
	if !slices.Equal(w.argvs(), wantOrder) {
		t.Fatalf("%v\nwant %v", w.argvs(), wantOrder)
	}
	if w.calls[0].Dir != w.root || w.calls[1].Dir != w.root || w.calls[2].Dir != filepath.Join(w.root, "apps", "gallery") {
		t.Errorf("dirs %q %q %q", w.calls[0].Dir, w.calls[1].Dir, w.calls[2].Dir)
	}
	if !w.calls[2].Both {
		t.Error("the suite's reason carries stdout and stderr")
	}
	for _, c := range w.calls {
		if envOf(c, "PLAYWRIGHT_BROWSERS_PATH") != "/ms-playwright" || envOf(c, "TURBO_TELEMETRY_DISABLED") != "1" {
			t.Errorf("%s: browsers %q telemetry %q", argv(c), envOf(c, "PLAYWRIGHT_BROWSERS_PATH"), envOf(c, "TURBO_TELEMETRY_DISABLED"))
		}
		// THE TREE'S CODE NEVER SEES THE REGISTRY CREDENTIAL.
		for _, k := range []string{"SPIFFE_ENDPOINT_SOCKET", "VISUAL_ARTIFACT_AUTH", "DOCKER_CONFIG"} {
			if v := envOf(c, k); v != "" || !slices.Contains(c.Env, k+"=") {
				t.Errorf("%s: %s=%q is not blanked", argv(c), k, v)
			}
		}
	}
	if envOf(w.calls[2], "PLAYWRIGHT_JSON_OUTPUT_NAME") != filepath.Join(os.Getenv("ATOM_WORK"), "visual", "0", "report.json") {
		t.Errorf("%v", w.calls[2].Env)
	}
	if len(w.pushed) != 0 {
		t.Error("a passing run pushed something")
	}
}

// A tree with no turbo.json, or a suite with no package name, builds nothing
// first: its web server is the suite's own business.
func TestTSVisualBuildsNothingFirstWithoutTurboOrAName(t *testing.T) {
	for name, extra := range map[string]map[string]string{
		"no turbo.json": {"turbo.json": ""},
		"no name":       {"apps/gallery/package.json": `{"private":true}`},
	} {
		w := gijmo(t, extra)
		want(t, w.run(), 0)
		for _, a := range w.argvs() {
			if strings.Contains(a, "turbo") {
				t.Errorf("%s: turbo ran: %v", name, w.argvs())
			}
		}
	}
}

func TestTSVisualReadsTheSuitesOwnFailures(t *testing.T) {
	w := gijmo(t, map[string]string{"apps/gallery/package.json": ""})
	want(t, w.run(), 1, "apps/gallery: FINDINGS - the suite has no package.json: no such file")

	w = gijmo(t, nil)
	w.exits, w.outs = map[string]int{"turbo run build": 1}, map[string]string{"turbo run build": "@gijmo/ui#build: tsc error"}
	want(t, w.run(), 1, "what the suite imports did not build", "tsc error")

	w = gijmo(t, nil)
	w.exits, w.outs = map[string]int{"turbo run build": -1}, map[string]string{"turbo run build": "engine gone"}
	want(t, w.run(), 2, "apps/gallery: CANNOT RUN - the build never ran", "engine gone")

	w = gijmo(t, nil)
	w.exits, w.outs = map[string]int{"--update-snapshots=none": -1}, map[string]string{"--update-snapshots=none": "engine gone"}
	want(t, w.run(), 2, "the suite never ran", "engine gone")

	w = gijmo(t, nil)
	w.report = ""
	w.exits, w.outs = map[string]int{"--update-snapshots=none": 1}, map[string]string{"--update-snapshots=none": "Error: no tests found"}
	want(t, w.run(), 2, "Playwright wrote no JSON report (exit 1)", "no tests found")

	w = gijmo(t, nil)
	w.report = "not json"
	want(t, w.run(), 2, "the Playwright JSON report for apps/gallery does not parse")

	w = gijmo(t, nil)
	w.exits, w.outs = map[string]int{"--update-snapshots=none": 3}, map[string]string{"--update-snapshots=none": "worker crashed"}
	want(t, w.run(), 2, "apps/gallery: 3 passed", "worker crashed")
	for _, a := range w.argvs() {
		if strings.Contains(a, "--update-snapshots=changed") {
			t.Error("a run that could not run regenerated baselines")
		}
	}
}

// A run that finds something regenerates the baselines it would accept and
// pushes them with the diffs, tagged by the commit, and says where.
func TestTSVisualPushesTheDiffsAndTheBaselinesItWouldAccept(t *testing.T) {
	w := gijmo(t, map[string]string{"apps/gallery/gallery.spec.ts-snapshots/kept-linux.png": "committed"})
	w.report = failReport
	w.results = map[string]string{"g/dusk-diff.png": "diff-bytes"}
	w.snapshots = map[string]string{
		"gallery.spec.ts-snapshots/dusk-linux.png":    "regenerated",
		"node_modules/pkg/a.spec.ts-snapshots/no.png": "dependency",
		"src/gallery.ts": "not a snapshot",
	}
	w.exits = map[string]int{"--update-snapshots=none": 1}
	v := w.run()
	want(t, v, 1,
		"apps/gallery: 2 passed, 1 failed",
		"violated  apps/gallery: gallery.spec.ts › primitives render in dusk — 9 pixels",
		"(diff: results/apps/gallery/g/dusk-diff.png)",
		"artifact "+ref,
		"oras manifest fetch "+ref)
	if len(v.Findings) != 1 || v.Findings[0].Cause != visuallane.CauseMismatch {
		t.Errorf("%+v", v.Findings)
	}
	// the update run is its own run, after every check run
	last := w.argvs()[len(w.calls)-1]
	if !strings.HasPrefix(last, "bunx playwright test --reporter=list --update-snapshots=changed --output=") || w.calls[len(w.calls)-1].Dir != filepath.Join(w.root, "apps", "gallery") {
		t.Errorf("the update run: %s in %s", last, w.calls[len(w.calls)-1].Dir)
	}
	if len(w.pushed) != 1 || w.pushed[0].ref != ref {
		t.Fatalf("%+v", w.pushed)
	}
	p := w.pushed[0]
	if p.cred.Bearer != "svid-jwt" || p.cred.User != "" {
		t.Errorf("the pod's identity is the credential: %+v", p.cred)
	}
	if got := p.layers["0"]; len(got) != 1 || got["results/apps/gallery/g/dusk-diff.png"] != "diff-bytes" {
		t.Errorf("results layer %v", got)
	}
	base := p.layers["1"]
	if len(base) != 2 || base["baselines/apps/gallery/gallery.spec.ts-snapshots/dusk-linux.png"] != "regenerated" ||
		base["baselines/apps/gallery/gallery.spec.ts-snapshots/kept-linux.png"] != "committed" {
		t.Errorf("baselines layer %v", base)
	}
}

// The Dagger runner hands a static login in place of an identity.
func TestTSVisualPushesWithTheStaticLoginTheDaggerRunnerHands(t *testing.T) {
	w := gijmo(t, nil)
	w.env["VISUAL_ARTIFACT_AUTH"] = authJSON
	w.env["SPIFFE_ENDPOINT_SOCKET"] = ""
	w.report = failReport
	w.exits = map[string]int{"--update-snapshots=none": 1}
	want(t, w.run(), 1, "artifact "+ref)
	if len(w.pushed) != 1 || w.pushed[0].cred != (visuallane.Credential{User: "publisher", Password: "pw"}) {
		t.Errorf("%+v", w.pushed)
	}
}

// The artifact never changes the verdict; why it was not pushed is said.
func TestTSVisualSaysWhyItPushedNothing(t *testing.T) {
	for name, c := range map[string]struct {
		set  map[string]string
		fail func(*world)
		want string
	}{
		"no sha":        {map[string]string{"ATOM_SHA": ""}, nil, "artifact: not pushed — the run names no commit sha"},
		"no credential": {map[string]string{"SPIFFE_ENDPOINT_SOCKET": ""}, nil, "artifact: not pushed — the lane was given no --artifact-auth"},
		"bad json":      {map[string]string{"VISUAL_ARTIFACT_AUTH": "nope"}, nil, "artifact: not pushed — the registry credential is not a docker config JSON"},
		"push refused":  {nil, func(w *world) { w.pushErr = errors.New("denied") }, "artifact: not pushed — denied"},
		"no identity":   {nil, func(w *world) { w.svidErr = errors.New("no agent") }, "artifact: not pushed — the registry JWT-SVID did not fetch: no agent"},
	} {
		w := gijmo(t, nil)
		w.report = failReport
		w.exits = map[string]int{"--update-snapshots=none": 1}
		for k, v := range c.set {
			w.env[k] = v
		}
		if c.fail != nil {
			c.fail(w)
		}
		v := w.run()
		if v.State != 1 || !strings.Contains(v.Reason, c.want) {
			t.Errorf("%s: state %d, reason lacks %q:\n%s", name, v.State, c.want, v.Reason)
		}
	}
}

// An update run that never started leaves no baselines to push, and says so.
func TestTSVisualSaysWhenTheUpdateRunNeverRan(t *testing.T) {
	w := gijmo(t, nil)
	w.report = failReport
	w.exits = map[string]int{"--update-snapshots=none": 1, "--update-snapshots=changed": -1}
	w.outs = map[string]string{"--update-snapshots=changed": "killed"}
	want(t, w.run(), 1, "artifact: not pushed — the update run never ran: killed")
	if len(w.pushed) != 0 {
		t.Error("pushed without baselines")
	}
}

// Two suites fold to the worst, each reason kept, and each writes to a place of
// its own.
func TestTSVisualFoldsEverySuite(t *testing.T) {
	w := gijmo(t, map[string]string{
		"visual.toml": "[[suite]]\ndir = \"apps/gallery\"\n\n[[suite]]\ndir = \"apps/docs\"\n",
	})
	v := w.run()
	want(t, v, 1, "apps/gallery: 3 passed", "apps/docs: FINDINGS - the suite has no package.json")
	if len(w.pushed) != 1 {
		t.Errorf("the suite that ran carries results: %d pushes", len(w.pushed))
	}
	seen := map[string]bool{}
	for _, c := range w.calls {
		if o := argOf(c, "--output="); o != "" {
			seen[o] = true
		}
	}
	if len(seen) != 1 {
		t.Errorf("outputs %v", seen)
	}
}

// A suite whose imports did not build wrote nothing, so there is no artifact
// to push. An empty image would be a manifest the registry refuses.
func TestTSVisualPushesNothingWhenNoSuiteRan(t *testing.T) {
	w := gijmo(t, nil)
	w.exits = map[string]int{"turbo run build": 2}
	want(t, w.run(), 1, "what the suite imports did not build", "artifact: not pushed — no suite ran far enough to write results or baselines")
	if len(w.pushed) != 0 {
		t.Errorf("nothing was carried, yet a push was attempted: %+v", w.pushed)
	}
}

// One suite that passed and wrote results, beside one whose imports did not
// build: the results are carried, so the artifact is pushed.
func TestTSVisualPushesWhatAPassingSuiteWrote(t *testing.T) {
	w := gijmo(t, map[string]string{
		"visual.toml":               "[[suite]]\ndir = \"apps/gallery\"\n\n[[suite]]\ndir = \"apps/docs\"\n",
		"apps/docs/package.json":    `{"name":"@gijmo/docs"}`,
		"apps/gallery/package.json": `{"name":"@gijmo/gallery"}`,
	})
	w.exits = map[string]int{"--filter=@gijmo/docs^...": 2}
	w.results = map[string]string{"g/a.png": "kept"}
	want(t, w.run(), 1, "apps/gallery: 3 passed", "apps/docs: FINDINGS - what the suite imports did not build")
	if len(w.pushed) != 1 || w.pushed[0].layers["0"]["results/apps/gallery/g/a.png"] != "kept" {
		t.Errorf("%+v", w.pushed)
	}
}

// A layer that cannot be read is a push that did not happen: whether the
// results directory or the suite's snapshots cannot be listed, or a file the
// listing named is gone by the time it is read.
func TestTSVisualSaysWhenTheArtifactCannotBeBuilt(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "afile", "x")
	e := env{in: atomkit.In{Root: dir}, work: dir}
	if _, err := e.layers([]*suiteRun{{dir: "s", out: filepath.Join(dir, "afile"), ran: true}}); err == nil {
		t.Error("results under a file listed")
	}
	if _, err := e.layers([]*suiteRun{{dir: "afile/below", out: dir, updated: true}}); err == nil {
		t.Error("a suite under a file listed")
	}
	if _, err := layerOf("p", dir, func([]string) []string { return []string{"gone"} }); err == nil {
		t.Error("a file that is gone was read")
	}
	if _, err := e.layers([]*suiteRun{{dir: "s", out: dir, ran: true}}); err != nil {
		t.Errorf("an absent results directory carries nothing: %v", err)
	}
}

// The scratch directory is the pod's, and a temporary one where the runner
// gives none.
func TestWorkDirIsTheAtomsOwnOrATemporaryOne(t *testing.T) {
	t.Setenv("ATOM_WORK", "/work/ts_visual")
	if got := workDir(); got != "/work/ts_visual" {
		t.Errorf("%q", got)
	}
	t.Setenv("ATOM_WORK", "")
	got := workDir()
	if fi, err := os.Stat(got); err != nil || !fi.IsDir() {
		t.Errorf("%q: %v", got, err)
	}
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "gone"))
	if got := workDir(); !strings.HasSuffix(got, "ts-visual") {
		t.Errorf("%q", got)
	}
}

// The seams the atom ships with: the identity comes from the agent, and the
// push reaches the registry's API.
func TestTheShippedSeamsFetchTheIdentityAndPushToTheRegistry(t *testing.T) {
	oldFetch := svidcred.Fetch
	defer func() { svidcred.Fetch = oldFetch }()
	svidcred.Fetch = func(_ context.Context, socket, audience string) (string, string, error) {
		if socket != "unix:///s" || audience != "registry.notusmi.com" {
			t.Errorf("asked %q for %q", socket, audience)
		}
		return "spiffe://notusmi.com/ci/x", "jwt", nil
	}
	tok, err := fetchSVID(context.Background(), "unix:///s", registry)
	if err != nil || tok != "jwt" {
		t.Fatalf("%q %v", tok, err)
	}

	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
		switch {
		case r.Method == http.MethodHead:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost:
			w.Header().Set("Location", r.URL.Path+"s")
			w.WriteHeader(http.StatusAccepted)
		default:
			w.WriteHeader(http.StatusCreated)
		}
	}))
	defer srv.Close()
	oldBase := registryBase
	registryBase = srv.URL
	defer func() { registryBase = oldBase }()
	root := t.TempDir()
	put(t, root, "a.png", "A")
	l, err := visuallane.TarLayer("results", root, []string{"a.png"})
	if err != nil {
		t.Fatal(err)
	}
	if err := push(context.Background(), visuallane.Credential{Bearer: "jwt"}, ref, []visuallane.Layer{l}); err != nil {
		t.Fatal(err)
	}
	last := seen[len(seen)-1]
	if last != "PUT /v2/foundry/visual/gijmo-ui/manifests/"+sha+" Bearer jwt" {
		t.Errorf("%v", seen)
	}
}
