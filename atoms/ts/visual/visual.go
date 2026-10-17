// Package tsvisual is the atom ts:visual: every Playwright screenshot suite
// visual.toml declares matches the baselines the tree commits.
package tsvisual

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/buildlane"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/svidcred"
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

const (
	// browsers is where the Playwright image keeps its browsers, and where the
	// Dagger runner's environment installs them.
	browsers = "/ms-playwright"

	envAuth   = "VISUAL_ARTIFACT_AUTH"
	envSha    = "ATOM_SHA"
	envSocket = "SPIFFE_ENDPOINT_SOCKET"
	envDocker = "DOCKER_CONFIG"
)

// env is where the suites run: the tree, and the scratch directory Playwright
// writes outside it, so a check run never writes into the tree it judges.
type env struct {
	in   atomkit.In
	work string
}

// workDir is the atom's scratch directory: the pod gives each atom one
// (ATOM_WORK); the Dagger runner does not, and a temporary one stands in.
func workDir() string {
	if w := os.Getenv("ATOM_WORK"); w != "" {
		return w
	}
	d, err := os.MkdirTemp("", "ts-visual-")
	if err != nil {
		return filepath.Join(os.TempDir(), "ts-visual")
	}
	return d
}

// `bun x` is bunx: the image tool's bunx is a link to a path outside the mount.

// programEnv is what the tree's own scripts run with besides the atom's: the
// browsers, no telemetry, and NOT the registry credential. The suite is the
// tree's code, and the artifact is pushed by the atom, after it: the identity
// and the static login are blanked for everything the tree runs.
func programEnv(extra ...string) []string {
	return append([]string{
		"PLAYWRIGHT_BROWSERS_PATH=" + browsers, "TURBO_TELEMETRY_DISABLED=1",
		envSocket + "=", envAuth + "=", envDocker + "=",
	}, extra...)
}

// run is a program whose output is the module's output(): stdout, and stderr
// too when the program failed.
func (e env) run(ctx context.Context, dir, name string, args ...string) (string, int) {
	return e.in.Run(ctx, atoms.Cmd{Dir: dir, Name: name, Args: args, Env: programEnv()})
}

// check builds what the suite at dir imports and runs it, outside the tree.
func (e env) check(ctx context.Context, dir, out string, turbo bool) *suiteRun {
	r := &suiteRun{dir: dir, out: out}
	cannot := func(why string) *suiteRun {
		r.state, r.reason = 2, dir+": CANNOT RUN - "+why
		return r
	}
	pkg, present := atoms.ReadIfPresent(e.in.Root, path.Join(dir, "package.json"))
	if !present {
		r.state, r.reason = 1, dir+": FINDINGS - the suite has no package.json: no such file"
		return r
	}
	// A workspace's own packages are built before the suite imports them —
	// turbo's `<pkg>^...` is the suite's dependencies, not the suite: its own
	// build is its web server's to run.
	if name := visuallane.PackageName(pkg); turbo && name != "" {
		built, code := e.run(ctx, e.in.Root, "bun", "x", "turbo", "run", "build", "--filter="+name+"^...")
		if !atoms.ToolRan(code) {
			return cannot("the build never ran: " + built)
		}
		if code != 0 {
			r.state, r.reason = 1, dir+": FINDINGS - what the suite imports did not build\n"+built
			return r
		}
	}
	report, results := filepath.Join(out, "report.json"), filepath.Join(out, "results")
	said, code := e.in.Run(ctx, atoms.Cmd{
		Dir: e.suite(dir), Name: "bun", Both: true,
		Args: []string{"x", "playwright", "test", "--reporter=list,json", "--update-snapshots=none", "--output=" + results},
		Env:  programEnv("PLAYWRIGHT_JSON_OUTPUT_NAME=" + report),
	})
	if !atoms.ToolRan(code) {
		return cannot("the suite never ran: " + said)
	}
	raw, wrote := atoms.ReadIfPresent(out, "report.json")
	if !wrote {
		return cannot("Playwright wrote no JSON report (exit " + strconv.Itoa(code) + ")\n" + said)
	}
	found, stats, err := visuallane.Failures(dir, results, []byte(raw))
	if err != nil {
		return cannot(err.Error())
	}
	r.found, r.ran = found, true
	r.state = visuallane.State(code, found)
	r.reason = visuallane.Summary(dir, stats) + visuallane.Lines(found)
	if r.state == 2 {
		r.reason += "\n" + said
	}
	return r
}

// update runs a suite that found something again, to regenerate the baselines
// it would accept. The exit is not read: a test that fails for a reason no
// snapshot answers still fails here, and what the run DID write is the point.
func (e env) update(ctx context.Context, r *suiteRun) {
	out, code := e.in.Run(ctx, atoms.Cmd{
		Dir: e.suite(r.dir), Name: "bun", Env: programEnv(),
		Args: []string{"x", "playwright", "test", "--reporter=list", "--update-snapshots=changed", "--output=" + filepath.Join(r.out, "update")},
	})
	if !atoms.ToolRan(code) {
		r.updateErr = "the update run never ran: " + out
		return
	}
	r.updated = true
}

// suite is the suite directory on disk.
func (e env) suite(dir string) string { return filepath.Join(e.in.Root, filepath.FromSlash(dir)) }

// registry is the artifact's registry, and the audience of the SVID it is
// pushed with (flux foundry/zot-config.json bearer.oidc.audiences).
const registry = "registry.notusmi.com"

// registryBase is where the registry is reached; a test points it at a double.
var registryBase = "https://" + registry

// The seams the push runs through, so a test pushes nowhere.
var (
	getenv = os.Getenv
	// fetchSVID asks the SPIRE agent at socket for a JWT-SVID for audience.
	fetchSVID = func(ctx context.Context, socket, audience string) (string, error) {
		_, tok, err := svidcred.Fetch(ctx, socket, audience)
		return tok, err
	}
	// push stores the layers at ref.
	push = func(ctx context.Context, cred visuallane.Credential, ref string, layers []visuallane.Layer) error {
		return visuallane.Registry{
			Base: registryBase, HTTP: &http.Client{Timeout: 5 * time.Minute}, Cred: cred,
		}.PushRef(ctx, ref, layers)
	}
)

// publish pushes the run's artifact and answers the line that says where it
// is, or why it is not. It never changes the verdict: the findings are the
// finding, and the artifact is how a person reads them and accepts a change.
func (e env) publish(ctx context.Context, runs []*suiteRun) string {
	ref, err := visuallane.ArtifactRef(registry, e.in.Origin, getenv(envSha))
	if err != nil {
		return "artifact: not pushed — " + err.Error()
	}
	if getenv(envAuth) == "" && getenv(envSocket) == "" {
		return "artifact: not pushed — the lane was given no --artifact-auth"
	}
	// NOTHING TO CARRY IS NOT A PUSH. A suite that never ran, because what it
	// imports did not build or it has no package.json, writes no results. An
	// image with no layers is a manifest the registry refuses: "PUT
	// …/manifests/<sha>: 400 Bad Request, unknown: manifest invalid" (gijmo-ui
	// 4ed2dae, 2026-10-05). That failed push put six ERROR spans into a
	// transcript whose real finding was the build. Baselines are only
	// regenerated for a suite that ran and wrote results, so results alone say
	// whether anything is carried.
	carried := false
	for _, r := range runs {
		carried = carried || r.ran
	}
	if !carried {
		return "artifact: not pushed — no suite ran far enough to write results or baselines"
	}
	cred, err := e.login(ctx)
	if err != nil {
		return "artifact: not pushed — " + err.Error()
	}
	layers, err := e.layers(runs)
	if err != nil {
		return "artifact: not pushed — " + err.Error()
	}
	if err := push(ctx, cred, ref, layers); err != nil {
		return "artifact: not pushed — " + err.Error()
	}
	return visuallane.FetchHint(ref)
}

// layers are the artifact's: per suite, the check run's results under
// results/<dir>, and every snapshot the update run left in the suite under
// baselines/<dir>, at its path in the tree.
func (e env) layers(runs []*suiteRun) ([]visuallane.Layer, error) {
	var out []visuallane.Layer
	for _, r := range runs {
		if r.updateErr != "" {
			return nil, errors.New(r.updateErr)
		}
		if r.ran {
			results := filepath.Join(r.out, "results")
			l, err := layerOf(path.Join("results", r.dir), results, nil)
			if err != nil {
				return nil, err
			}
			out = append(out, l)
		}
		if r.updated {
			l, err := layerOf(path.Join("baselines", r.dir), e.suite(r.dir), visuallane.Snapshots)
			if err != nil {
				return nil, err
			}
			out = append(out, l)
		}
	}
	return out, nil
}

// layerOf is the files under root (less what keep drops, when given) as a layer
// kept at prefix.
func layerOf(prefix, root string, keep func([]string) []string) (visuallane.Layer, error) {
	files, err := visuallane.Files(root)
	if err != nil {
		return visuallane.Layer{}, err
	}
	if keep != nil {
		files = keep(files)
	}
	return visuallane.TarLayer(prefix, root, files)
}

// login is the push's credential: the Dagger runner's static login when it
// handed one, else the pod's JWT-SVID, fetched now, right before the push (it
// lives 15 minutes).
func (e env) login(ctx context.Context) (visuallane.Credential, error) {
	if auth := getenv(envAuth); auth != "" {
		user, password, err := buildlane.RegistryLogin(auth, registry)
		return visuallane.Credential{User: user, Password: password}, err
	}
	tok, err := fetchSVID(ctx, getenv(envSocket), registry)
	if err != nil {
		return visuallane.Credential{}, fmt.Errorf("the registry JWT-SVID did not fetch: %w", err)
	}
	return visuallane.Credential{Bearer: tok}, nil
}
