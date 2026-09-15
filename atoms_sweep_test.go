package main

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// THE SWEEP LANE, AGAINST THE PAPER ENGINE (engine_fake_test.go). These four
// atoms run UNATTENDED — ca-sweep's CronJob is the only caller — which is
// where a silent pass does the most damage: nobody is watching, so a body that
// exits 0 because its tool never arrived reads as a clean fleet until somebody
// happens to look. So every branch below is exercised by its own case, and
// each is asserted on the WORDING it files, not merely on the state.

// sweepTree copies everyLaneTree, deletes every path in drop, and overlays
// add — the tree under check, minus or plus whatever the branch needs.
func sweepTree(drop []string, add map[string]string) map[string]string {
	tree := make(map[string]string, len(everyLaneTree))
	for k, v := range everyLaneTree {
		tree[k] = v
	}
	for _, d := range drop {
		delete(tree, d)
	}
	for k, v := range add {
		tree[k] = v
	}
	return tree
}

// rootEntries is the query text of a read of the repository root, and nothing
// else: every deeper read carries a directory(path:…) or a glob with it. A
// test failing THIS alone exercises the "the repository root could not be
// read" branch without also breaking the reads that follow it.
const rootEntries = "{directory{entries}}"

// wantNoContainer fails unless the atom asked the engine for nothing that
// runs.
func wantNoContainer(t *testing.T, why string) {
	t.Helper()
	for _, q := range engine.chains() {
		if strings.Contains(q, "container{") {
			t.Errorf("%s: the engine was asked to start a container:\n%s", why, q)
		}
	}
}

// ---- sweep:portfolio-sbom ----

// EVERY TERM OF THIS QUESTION IS A FACT ABOUT THE TREE, so no path through the
// atom may start anything. A container here would be a cost paid, on every
// repo in custody, for an answer already in hand.
func TestSweepPortfolioSbomIsAllGoAndNeverStartsAContainer(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tree   map[string]string
		fail   [2]string
		state  int
		result string
		says   string
	}{
		{
			name: "no Dockerfile is an absence",
			tree: sweepTree([]string{"Dockerfile"}, nil), state: 0, result: "absent",
			says: "no Dockerfile at the repository root",
		},
		{
			// A Dockerfile and no workflow tree: nothing here says whether the
			// image is ever built, so nothing here can say whether it is attested.
			name: "a Dockerfile and no workflow tree cannot be answered",
			tree: sweepTree([]string{".forgejo/workflows/ci.yml"}, nil), state: 2, result: "cannot-run",
			says: "a Dockerfile and no workflow tree",
		},
		{
			// The absence is two entries deep: .forgejo with no workflows under
			// it is the same nothing as no .forgejo at all.
			name:  "a .forgejo with no workflows under it is no workflow tree",
			tree:  sweepTree([]string{".forgejo/workflows/ci.yml"}, map[string]string{".forgejo/README.md": "no workflows here\n"}),
			state: 2, result: "cannot-run", says: "a Dockerfile and no workflow tree",
		},
		{
			name: "built through the attesting workflow",
			tree: sweepTree(nil, map[string]string{
				".forgejo/workflows/ci.yml": "jobs:\n  image:\n    uses: foundry/foundry-stocks/.forgejo/workflows/build.yml@main\n",
			}), state: 0, result: "pass",
			says: "",
		},
		{
			// The hole the fleet-wide re-score structurally cannot see: an
			// image nobody attests contributes no attestation to read, so it
			// is not scored badly — it is not scored at all.
			name: "a local build is not the attesting one",
			tree: sweepTree(nil, map[string]string{
				".forgejo/workflows/ci.yml": "jobs:\n  image:\n    uses: ./.forgejo/workflows/build.yml@main\n",
			}), state: 1, result: "findings",
			says: "no workflow calling foundry-stocks build.yml",
		},
		{
			name: "the root could not be read",
			tree: everyLaneTree, fail: [2]string{rootEntries, "mount evaporated"},
			state: 2, result: "cannot-run", says: "the repository root could not be read",
		},
		{
			name: "the workflow tree could not be read",
			tree: everyLaneTree, fail: [2]string{`glob(pattern:".forgejo/workflows/**")`, "index is gone"},
			state: 2, result: "cannot-run", says: "unknown rather than answered",
		},
		{
			name: "the .forgejo listing could not be read",
			tree: everyLaneTree, fail: [2]string{`directory(path:".forgejo")`, "listing failed"},
			state: 2, result: "cannot-run", says: "unknown rather than answered",
		},
		{
			name: "a workflow file could not be read",
			tree: everyLaneTree, fail: [2]string{`file(path:".forgejo/workflows/ci.yml")`, "blob missing"},
			state: 2, result: "cannot-run", says: "unknown rather than answered",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine.reset()
			engine.withTree(tc.tree)
			if tc.fail[0] != "" {
				engine.fail(tc.fail[0], tc.fail[1])
			}
			v := runAtom(t, "sweep:portfolio-sbom", "")
			wantState(t, v, tc.state, tc.says)
			if v.Result != tc.result {
				t.Errorf("result %q, want %q:\n%s", v.Result, tc.result, v.Reason)
			}
			wantNoContainer(t, tc.name)
		})
	}
}

// The tree is read whole: one workflow calling the attesting build is enough,
// whichever file does it.
func TestSweepPortfolioSbomReadsTheWholeWorkflowTree(t *testing.T) {
	engine.reset()
	engine.withTree(sweepTree(nil, map[string]string{
		".forgejo/workflows/ci.yml":    "jobs:\n  gate:\n    uses: foundry/foundry-stocks/.forgejo/workflows/gate.yml@main\n",
		".forgejo/workflows/image.yml": "jobs:\n  image:\n    uses: foundry/foundry-stocks/.forgejo/workflows/bake-blade.yml@main\n",
	}))
	// A pass's reason is the verdict's own PASS line — reasonFor discards the
	// atom's output at state 0 — so the assertion is the state, and the
	// contrast with the gate-only tree above is what proves the tree was read.
	wantState(t, runAtom(t, "sweep:portfolio-sbom", ""), 0)
}

// A directory under .forgejo/workflows has no contents to read, and the glob
// names it with a trailing separator. Reading it as a file would turn a
// perfectly ordinary layout into a CANNOT RUN.
func TestSweepPortfolioSbomSkipsDirectoriesUnderTheWorkflowTree(t *testing.T) {
	engine.reset()
	engine.withTree(sweepTree(nil, map[string]string{
		".forgejo/workflows/ci.yml":  "jobs:\n  image:\n    uses: foundry/foundry-stocks/.forgejo/workflows/build.yml@main\n",
		".forgejo/workflows/shared/": "",
	}))
	wantState(t, runAtom(t, "sweep:portfolio-sbom", ""), 0)
	if q := engine.chain(`file(path:".forgejo/workflows/shared/")`); q != "" {
		t.Errorf("a directory was read as a file:\n%s", q)
	}
}

// ---- sweep:template-render-matrix ----

func TestSweepTemplateRenderMatrixRunsTheCanonicalGateOverARealRepository(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "sweep:template-render-matrix", ""), 0)

	c := engine.chain(`"python3","/stocks/ci/lib/template_render_matrix.py"`, "exitCode")
	if !strings.Contains(c, checks.ImageFleet) {
		t.Errorf("the render matrix runs in the fleet image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withMountedDirectory", `path:"/src"`},
		[]string{"withMountedDirectory", `path:"/stocks"`},
		// gitReady's other half: git refuses a repository it does not own, and
		// the process here is root over a mounted tree.
		[]string{"withExec", `args:["git","config","--global","--add","safe.directory","*"]`},
		// The provisioning probe: the canonical gate shells out to uvx, and a
		// missing one is state 2 with the engine's error, never a green.
		[]string{"withExec", `args:["uvx","--version"]`},
		[]string{"withExec", `expect:ANY`, `args:["python3","/stocks/ci/lib/template_render_matrix.py","--template","."]`},
	)
	if hasCall(c, "withExec", `args:["uvx","--version"]`, `expect:ANY`) {
		t.Errorf("the uvx probe is provisioning and must run under the default Expect:\n%s", c)
	}
	// A primary checkout's .git is a directory; gitReady has nothing to rebuild.
	if hasCall(c, "withExec", `args:["git","init","-q","."]`) {
		t.Errorf("a .git DIRECTORY needs no throwaway repository:\n%s", c)
	}
	if strings.Contains(c, "GATE_BASE") {
		t.Errorf("sweep:template-render-matrix must not read GATE_BASE:\n%s", c)
	}

	// Rule 2: the gate's own exit code reaches the verdict, and every code
	// that is not 0 or 1 is a could-not-run.
	for _, tc := range []struct{ exit, state int }{{0, 0}, {1, 1}, {2, 2}, {127, 2}} {
		engine.exitCode(`"python3","/stocks/ci/lib/template_render_matrix.py"`, tc.exit)
		engine.stdout(`"python3","/stocks/ci/lib/template_render_matrix.py"`, "case go-service: rendered")
		v := runAtom(t, "sweep:template-render-matrix", "")
		if v.State != tc.state {
			t.Errorf("exit %d answered state %d, want %d:\n%s", tc.exit, v.State, tc.state, v.Reason)
		}
	}

	// An engine error on the probe is the atom never running.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"uvx","--version"`, "executable file not found")
	wantState(t, runAtom(t, "sweep:template-render-matrix", ""), 2, "never ran", "executable file not found")
}

func TestSweepTemplateRenderMatrixRefusesATreeThatIsNotARepository(t *testing.T) {
	// No matrix declared: an absence, and nothing runs.
	engine.reset()
	engine.withTree(sweepTree([]string{"ci-matrix.toml"}, nil))
	v := runAtom(t, "sweep:template-render-matrix", "")
	wantState(t, v, 0, "no ci-matrix.toml at the repository root")
	if v.Result != "absent" {
		t.Errorf("result %q, want absent:\n%s", v.Result, v.Reason)
	}
	wantNoContainer(t, "no render matrix is decided in Go")

	// The root itself.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(rootEntries, "mount evaporated")
	wantState(t, runAtom(t, "sweep:template-render-matrix", ""), 2, "the repository root could not be read", "mount evaporated")

	// The canonical gate.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail("ci/lib/template_render_matrix.py", "no such file")
	wantState(t, runAtom(t, "sweep:template-render-matrix", ""), 2, "the canonical gate is not reachable through the door")

	// No .git at all: the matrix renders the template AT ITS GIT HEAD, and
	// without a repository copier resolves some other tree.
	engine.reset()
	engine.withTree(sweepTree([]string{".git/HEAD"}, nil))
	v = runAtom(t, "sweep:template-render-matrix", "")
	wantState(t, v, 2, "no .git in the tree under check", "--vcs-ref=HEAD is load-bearing")
	if strings.Contains(v.Reason, "is a FILE") {
		t.Errorf("there is no .git at all; calling it a file misreports the cause:\n%s", v.Reason)
	}
	wantNoContainer(t, "a tree with no repository never runs the gate")

	// A LINKED WORKTREE's .git is a FILE naming a host path that does not
	// exist inside the container. gitReady can rebuild an index; it cannot
	// rebuild the history copier resolves a ref against, so this is a refusal
	// on purpose.
	engine.reset()
	engine.withTree(sweepTree([]string{".git/HEAD"}, map[string]string{
		".git": "gitdir: /home/rob/Forge/Outputs/foundry-tools/.git/worktrees/Tesla19\n",
	}))
	v = runAtom(t, "sweep:template-render-matrix", "")
	wantState(t, v, 2, "no .git in the tree under check", "Here .git is a FILE, not a directory", "linked worktree")
	wantNoContainer(t, "a linked worktree never runs the gate")
}

// ---- sweep:kubeconform ----

func TestSweepKubeconformProbesTheCatalogueThenReadsTheSummary(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.stderr(`"/kubeconform"`, "Summary: 12 resources found in 4 files - Valid: 12, Invalid: 0, Errors: 0, Skipped: 0\n")
	// A count it could read, and a non-zero one: the only two states that let
	// this atom pass. reasonFor discards a pass's output, so the assertion is
	// the state — the zero-count and no-summary refusals below are what prove
	// the summary was read rather than assumed.
	v := runAtom(t, "sweep:kubeconform", "")
	wantState(t, v, 0)

	// The probe is a fetch from the engine, not a wget inside the image: the
	// answer is cached and the image is never pulled for a catalogue outage.
	if q := engine.chain("http(", "helmrelease_v2.json"); q == "" {
		t.Error("the CRD catalogue must be probed before the scan")
	}
	c := engine.chain(`"/kubeconform"`, "exitCode")
	if !strings.Contains(c, checks.ImageKubeconform) {
		t.Errorf("sweep:kubeconform must run in the kubeconform image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withMountedDirectory", `path:"/src"`},
		[]string{"withWorkdir", `path:"/src"`},
		// The catalogue is not an enhancement here: the default store alone
		// SKIPS most of a real flux/ tree.
		[]string{"withExec", `expect:ANY`,
			`"/kubeconform","-ignore-missing-schemas","-ignore-filename-pattern","\\.json$","-schema-location","default","-schema-location","` + checks.CRDSchemaLocation + `","-summary","-n","8","flux/"`},
	)
	if strings.Contains(c, "GATE_BASE") {
		t.Errorf("sweep:kubeconform must not read GATE_BASE:\n%s", c)
	}
	// BOTH STREAMS, ALWAYS — which one the summary lands on is not a promise
	// kubeconform makes, and a clean run is exactly where the count still has
	// to be read.
	if engine.chain(`"/kubeconform"`, "stdout") == "" || engine.chain(`"/kubeconform"`, "stderr") == "" {
		t.Error("the summary may land on either stream, so both are read on a clean exit")
	}

	// The summary on stdout instead: same answer.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.stdout(`"/kubeconform"`, "Summary: 9 resources found in 3 files - Valid: 9, Invalid: 0, Errors: 0, Skipped: 0\n")
	wantState(t, runAtom(t, "sweep:kubeconform", ""), 0)
}

func TestSweepKubeconformRefusesAScanThatMeasuredNothing(t *testing.T) {
	// A zero-resource validation: 0 invalid because NOTHING WAS EXAMINED, not
	// because the tree is correct.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.stderr(`"/kubeconform"`, "Summary: 395 resources found in 120 files - Valid: 0, Invalid: 0, Errors: 0, Skipped: 395\n")
	v := runAtom(t, "sweep:kubeconform", "")
	wantState(t, v, 2, "REFUSING a zero-resource validation", "NOTHING WAS EXAMINED", "Skipped: 395")

	// No summary at all: there is no count to read.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.stderr(`"/kubeconform"`, "panic: runtime error\n")
	wantState(t, runAtom(t, "sweep:kubeconform", ""), 2, "printed no summary", "panic: runtime error")

	// A summary with no Valid field is the same nothing measured.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.stdout(`"/kubeconform"`, "Summary: something else entirely\n")
	wantState(t, runAtom(t, "sweep:kubeconform", ""), 2, "printed no summary")
}

func TestSweepKubeconformReportsFindingsWithTheirLines(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(`"/kubeconform"`, 1)
	engine.stdout(`"/kubeconform"`, "flux/app.yaml - Deployment app is invalid: spec.replicas: not a number\nflux/svc.yaml - Service api is invalid: missing port\n")
	engine.stderr(`"/kubeconform"`, "Summary: 6 resources found in 2 files - Valid: 5, Invalid: 1, Errors: 0, Skipped: 0\n")
	v := runAtom(t, "sweep:kubeconform", "")
	wantState(t, v, 1,
		"Valid: 5",
		"flux/app.yaml - Deployment app is invalid: spec.replicas: not a number",
		"flux/svc.yaml - Service api is invalid: missing port",
	)
	// The summary is printed on its own line; repeating it in the findings is
	// noise.
	if n := strings.Count(v.Reason, "Summary:"); n != 1 {
		t.Errorf("the summary appears %d times in the reason:\n%s", n, v.Reason)
	}
}

func TestSweepKubeconformIsAbsentOrRefusesBeforeItRuns(t *testing.T) {
	// No flux/ tree: an absence, and no container.
	engine.reset()
	engine.withTree(sweepTree([]string{"flux/x.yaml"}, nil))
	v := runAtom(t, "sweep:kubeconform", "")
	wantState(t, v, 0, "no flux/ tree at the repository root")
	if v.Result != "absent" {
		t.Errorf("result %q, want absent:\n%s", v.Result, v.Reason)
	}
	wantNoContainer(t, "no flux/ tree is decided in Go")
	if q := engine.chain("http("); q != "" {
		t.Errorf("an absent flux/ tree must not probe the catalogue:\n%s", q)
	}

	// The root.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(rootEntries, "mount evaporated")
	wantState(t, runAtom(t, "sweep:kubeconform", ""), 2, "the repository root could not be read", "mount evaporated")

	// An unreachable catalogue renders every custom resource as `skipped`,
	// which is indistinguishable from a clean validation and is not one.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail("helmrelease_v2.json", "unreachable")
	v = runAtom(t, "sweep:kubeconform", "")
	wantState(t, v, 2, "the CRD schema catalogue is unreachable", "indistinguishable from a clean validation", "unreachable")
	wantNoContainer(t, "a catalogue outage must not pull the image")

	// The engine itself.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"/kubeconform"`, "failed to resolve image")
	wantState(t, runAtom(t, "sweep:kubeconform", ""), 2, "never ran", "failed to resolve image")
}

// ---- sweep:kube-linter ----

func TestSweepKubeLinterBuildsItsChainAndMapsItsExitCodes(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "sweep:kube-linter", ""), 0)

	c := engine.chain(`"/kube-linter"`, "exitCode")
	if !strings.Contains(c, checks.ImageKubeLinter) {
		t.Errorf("sweep:kube-linter must run in the kube-linter image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withMountedDirectory", `path:"/src"`},
		[]string{"withWorkdir", `path:"/src"`},
		[]string{"withExec", `expect:ANY`, `args:["/kube-linter","lint","--fail-if-no-objects-found","flux/"]`},
	)
	if strings.Contains(c, "GATE_BASE") {
		t.Errorf("sweep:kube-linter must not read GATE_BASE:\n%s", c)
	}
	// The refusal arrives on stderr and the findings on stdout, and the code
	// is the same 1 for both — so both streams are read before anything is
	// decided.
	if engine.chain(`"/kube-linter"`, "stdout") == "" || engine.chain(`"/kube-linter"`, "stderr") == "" {
		t.Error("both streams must be read before the state is decided")
	}

	// Findings: the linter's own count leads (it is the line a human reads
	// first, and kube-linter prints it last), then the findings themselves.
	engine.exitCode(`"/kube-linter"`, 1)
	engine.stdout(`"/kube-linter"`, "flux/a.yaml: (object: app apps/v1, Deployment) container \"app\" does not have a read-only root file system\n")
	engine.stderr(`"/kube-linter"`, "Error: found 1 lint errors\n")
	v := runAtom(t, "sweep:kube-linter", "")
	wantState(t, v, 1, "Error: found 1 lint errors", "read-only root file system")
	if !strings.Contains(v.Reason, "Error: found 1 lint errors\nflux/a.yaml:") {
		t.Errorf("the tail must lead the head:\n%s", v.Reason)
	}

	// --fail-if-no-objects-found IS kube-linter's own zero-population refusal,
	// and it exits 1 for it — the same code it uses for a finding. Reading
	// that as findings would be wrong in the direction that still looks like
	// the check worked.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(`"/kube-linter"`, 1)
	engine.stderr(`"/kube-linter"`, "Error: no valid objects found\n")
	wantState(t, runAtom(t, "sweep:kube-linter", ""), 2, "CANNOT RUN", "parsed no object under flux/")
}

func TestSweepKubeLinterIsAbsentWithoutFluxAndRefusesAnUnreadableRoot(t *testing.T) {
	engine.reset()
	engine.withTree(sweepTree([]string{"flux/x.yaml"}, nil))
	v := runAtom(t, "sweep:kube-linter", "")
	wantState(t, v, 0, "no flux/ tree at the repository root")
	if v.Result != "absent" {
		t.Errorf("result %q, want absent:\n%s", v.Result, v.Reason)
	}
	wantNoContainer(t, "no flux/ tree is decided in Go")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(rootEntries, "mount evaporated")
	wantState(t, runAtom(t, "sweep:kube-linter", ""), 2, "the repository root could not be read", "mount evaporated")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"/kube-linter"`, "failed to resolve image")
	wantState(t, runAtom(t, "sweep:kube-linter", ""), 2, "never ran", "failed to resolve image")
}

// THE ACCEPTANCE CRITERION OF CA F9, ON THE WIRE. A sweep atom describes a
// REPOSITORY rather than a change, so none of them may reach a pull's path —
// and none of them may key its cache on the pull either, which is the same
// property read off the chain instead of off the table.
func TestNoSweepAtomReadsTheChangeSet(t *testing.T) {
	for _, a := range checks.Atoms {
		if a.Stage != checks.StageSweep {
			continue
		}
		engine.reset()
		engine.withTree(everyLaneTree)
		runAtom(t, a.ID, "base-sha")
		for _, q := range engine.chains() {
			if strings.Contains(q, "GATE_BASE") || strings.Contains(q, "base-sha") {
				t.Errorf("%s keys its cache on the pull:\n%s", a.ID, q)
			}
		}
	}
}

// THE `+check` SURFACE IS WHAT ca-sweep ACTUALLY CALLS, and it is the one
// place a verdict becomes nil-or-error. A refusal that answered nil would be a
// green gate over a check that never ran — the exact conflation the three
// states exist to prevent, one layer further out than the atoms.
func TestSweepCheckEntrypointsAnswerNilOnlyForAPass(t *testing.T) {
	s := &Sweep{Source: dag.Directory()}
	for _, tc := range []struct {
		id string
		fn func(context.Context) (string, error)
	}{
		{"sweep:portfolio-sbom", s.PortfolioSbom},
		{"sweep:template-render-matrix", s.TemplateRenderMatrix},
		{"sweep:kubeconform", s.Kubeconform},
		{"sweep:kube-linter", s.KubeLinter},
	} {
		t.Run(tc.id, func(t *testing.T) {
			engine.reset()
			engine.withTree(everyLaneTree)
			// kubeconform needs a count to read before it will pass.
			engine.stderr(`"/kubeconform"`, "Summary: 12 resources found in 4 files - Valid: 12, Invalid: 0, Errors: 0, Skipped: 0\n")
			out, err := tc.fn(t.Context())
			if err != nil {
				t.Fatalf("%s answered an error on its happy path: %v", tc.id, err)
			}
			if !strings.Contains(out, tc.id) {
				t.Errorf("%s answered %q, which does not name the atom", tc.id, out)
			}

			engine.reset()
			engine.withTree(everyLaneTree)
			engine.fail(rootEntries, "mount evaporated")
			if _, err := tc.fn(t.Context()); err == nil {
				t.Fatalf("%s answered nil for a tree it could not read", tc.id)
			} else if !strings.Contains(err.Error(), "CANNOT RUN") {
				t.Errorf("%s: %v", tc.id, err)
			}
		})
	}
}

// EACH READ OFF THE CONTAINER IS ITS OWN CHANCE FOR THE ENGINE TO GO AWAY, and
// all three are the engine's failure rather than the tool's — state 2, never a
// verdict about the tree. outputBoth reads exitCode, stdout AND stderr,
// because kubeconform's summary and kube-linter's zero-population refusal each
// land on whichever stream they feel like.
func TestSweepReadsOfTheContainerThatFailPartWayAreNeverAVerdict(t *testing.T) {
	for _, tc := range []struct {
		name, atom, match, leaf string
	}{
		{"kubeconform loses stdout", "sweep:kubeconform", `"/kubeconform"`, "stdout"},
		{"kubeconform loses stderr", "sweep:kubeconform", `"/kubeconform"`, "stderr"},
		{"kube-linter loses stderr", "sweep:kube-linter", `"/kube-linter"`, "stderr"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine.reset()
			engine.withTree(everyLaneTree)
			engine.failLeaf(tc.match, tc.leaf, "the engine went away")
			wantState(t, runAtom(t, tc.atom, ""), 2, "never ran", "the engine went away")
		})
	}
}
