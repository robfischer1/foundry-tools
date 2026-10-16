package tsvisual

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strconv"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/visuallane"
)

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
		built, code := e.run(ctx, e.in.Root, "bunx", "turbo", "run", "build", "--filter="+name+"^...")
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
		Dir: e.suite(dir), Name: "bunx", Both: true,
		Args: []string{"playwright", "test", "--reporter=list,json", "--update-snapshots=none", "--output=" + results},
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
		Dir: e.suite(r.dir), Name: "bunx", Env: programEnv(),
		Args: []string{"playwright", "test", "--reporter=list", "--update-snapshots=changed", "--output=" + filepath.Join(r.out, "update")},
	})
	if !atoms.ToolRan(code) {
		r.updateErr = "the update run never ran: " + out
		return
	}
	r.updated = true
}

// suite is the suite directory on disk.
func (e env) suite(dir string) string { return filepath.Join(e.in.Root, filepath.FromSlash(dir)) }
