package fleetl7render

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/atomtest"
	"dagger/foundry-tools/internal/checks"
)

func TestTheDeclarationValidates(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrepush || Def.Lane != checks.LaneAny || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "none" || Def.Surface != "any:**" || len(Def.Needs) != 0 {
		t.Errorf("%+v", Def)
	}
}

// doc is a grants-daedalus document as nyx's renderer emits it: a hades-sa item
// with the given paths, and an item for another caller that must be ignored.
func doc(paths ...string) string {
	var b strings.Builder
	b.WriteString(`# grants-daedalus: hand-written header.
apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata:
  name: "grants-daedalus"
  annotations:
    nyx.notusmi.com/render-mode: "rest"
spec:
  ingress:
    - fromEndpoints:
        - matchLabels:
            "io.kubernetes.pod.namespace": "prime"
            "io.cilium.k8s.policy.serviceaccount": "hades-sa"
      toPorts:
        - ports:
            - port: "8226"
              protocol: TCP
          rules:
            http:
`)
	for _, p := range paths {
		b.WriteString(`              - path: "^/v1/` + p + `(?:/[^/?.%][^/?%]*)*(?:\\?.*)?$"` + "\n")
	}
	b.WriteString(`              - method: "GET"
                path: "^/openapi\\.json(?:\\?.*)?$"
    - fromEndpoints:
        - matchLabels:
            "io.cilium.k8s.policy.serviceaccount": "nyx-sa"
      toPorts:
        - ports:
            - port: "8226"
          rules:
            http:
              - path: "^/v1/only_for_nyx(?:\\?.*)?$"
`)
	return b.String()
}

const grants = `{"tx": "1", "stars": {"daedalus": ["ci_await", "ci_canary"]}}`

func published(routes string) string {
	return `{"star": "daedalus", "routes": {` + routes + `}}`
}

const bothRoutes = `"ci_await": [{"method": "POST", "path": "/v1/ci_await"}], "ci_canary": [{"method": "POST", "path": "/v1/ci_canary"}]`

type commit struct {
	files  map[string]string
	author string
}

const human = "rob@example.invalid"

func gitIn(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func as(who string) []string {
	return []string{"GIT_AUTHOR_NAME=a", "GIT_AUTHOR_EMAIL=" + who, "GIT_COMMITTER_NAME=c", "GIT_COMMITTER_EMAIL=" + who}
}

// tree is a flux tree on main with the given files, and the pull's commits on top.
func tree(t *testing.T, files map[string]string, pull ...commit) (atomkit.In, string) {
	t.Helper()
	in := atomtest.TreeIn(t, files)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	gitIn(t, in.Root, nil, "init", "-q", "-b", "main")
	gitIn(t, in.Root, nil, "add", "-A")
	gitIn(t, in.Root, as(human), "commit", "-q", "-m", "main")
	base := gitIn(t, in.Root, nil, "rev-parse", "HEAD")
	for i, c := range pull {
		for p, body := range c.files {
			atomtest.Put(t, in.Root, p, body)
		}
		gitIn(t, in.Root, nil, "add", "-A")
		gitIn(t, in.Root, as(c.author), "commit", "-q", "--allow-empty", "-m", "pull "+string(rune('a'+i)))
	}
	return in, base
}

func flux(paths []string, grantsJSON, daedalusJSON string) map[string]string {
	f := map[string]string{File: doc(paths...)}
	if grantsJSON != "" {
		f[Published+"grants.json"] = grantsJSON
		f[Published+"daedalus.json"] = daedalusJSON
	}
	return f
}

func run(t *testing.T, files map[string]string, pull ...commit) checks.Verdict {
	t.Helper()
	in, base := tree(t, files, pull...)
	in.Base = base
	return check(context.Background(), in)
}

// UT-l7-1 (red fixture): daedalus serves ci_canary, the grants allow it, and
// the committed file has no path for it. The gate says so by name.
func TestAServedAndAllowedVerbWithNoPathIsRefused(t *testing.T) {
	v := run(t, flux([]string{"ci_await"}, grants, published(bothRoutes)))
	atomtest.Expect(t, v, checks.StateFindings, atomtest.Findings, "daedalus", "ci_canary", "POST /v1/ci_canary", "no hades-sa path")
	if strings.Contains(v.Reason, "ci_await is") || strings.Contains(v.Reason, "verb ci_await") {
		t.Errorf("a verb that has its path was named:\n%s", v.Reason)
	}
}

// UT-l7-2 (green): every published verb has a path, and the other caller's
// item is not mistaken for hades-sa's.
func TestEveryPublishedVerbWithItsPathPasses(t *testing.T) {
	atomtest.Expect(t, run(t, flux([]string{"ci_await", "ci_canary"}, grants, published(bothRoutes))), checks.StatePass, atomtest.Pass, "every published verb")
}

func TestAPathOnlyTheOtherCallerHasDoesNotCount(t *testing.T) {
	only := `{"tx": "1", "stars": {"daedalus": ["only_for_nyx"]}}`
	pub := published(`"only_for_nyx": [{"method": "POST", "path": "/v1/only_for_nyx"}]`)
	atomtest.Expect(t, run(t, flux([]string{"ci_await"}, only, pub)), checks.StateFindings, atomtest.Findings, "only_for_nyx")
}

// A rule's method must be the route's: the openapi door's GET does not admit a POST.
func TestTheMethodMustMatchTheRoute(t *testing.T) {
	pub := published(`"ci_await": [{"method": "POST", "path": "/v1/ci_await"}], "ci_canary": [{"method": "POST", "path": "/openapi.json"}]`)
	atomtest.Expect(t, run(t, flux([]string{"ci_await", "ci_canary"}, grants, pub)), checks.StateFindings, atomtest.Findings, "POST /openapi.json")
}

// A route template's variables are matched as a path segment.
func TestATemplatedRouteIsMatched(t *testing.T) {
	g := `{"tx": "1", "stars": {"daedalus": ["ci_run"]}}`
	pub := published(`"ci_run": [{"method": "POST", "path": "/v1/ci_run/repo/{repo}"}]`)
	atomtest.Expect(t, run(t, flux([]string{"ci_run"}, g, pub)), checks.StatePass, atomtest.Pass)
}

// UT-l7-3: today's tree has nothing published yet: the worker's first render is
// the baseline, so the gate holds, and a tree without the policy is absent.
func TestNoBaselineYetPassesAndNoPolicyIsAbsent(t *testing.T) {
	atomtest.Expect(t, run(t, flux([]string{"ci_await"}, "", "")), checks.StatePass, atomtest.Pass)
	in := atomtest.TreeIn(t, map[string]string{"README.md": "x"})
	atomtest.Expect(t, check(context.Background(), in), checks.StatePass, "absent", File)
}

// A verb with no published route, and a star with no document, are findings.
func TestAVerbWithNoRouteAndAStarWithNoDocumentAreRefused(t *testing.T) {
	atomtest.Expect(t, run(t, flux([]string{"ci_await", "ci_canary"}, grants, published(`"ci_await": [{"method": "POST", "path": "/v1/ci_await"}]`))),
		checks.StateFindings, atomtest.Findings, "ci_canary", "publishes no route")
	g := `{"tx": "1", "stars": {"ghost": ["x"]}}`
	atomtest.Expect(t, run(t, flux([]string{"ci_await"}, g, published(bothRoutes))),
		checks.StateFindings, atomtest.Findings, "ghost", "no rest-mode grants-ghost")
}

// A published copy that cannot be read is a could-not-run, never a pass.
func TestUnreadablePublishedCopiesCannotRun(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"bad grants":   flux([]string{"ci_await"}, "{", published(bothRoutes)),
		"bad star":     flux([]string{"ci_await"}, grants, "{"),
		"missing star": {File: doc("ci_await"), Published + "grants.json": grants},
	} {
		t.Run(name, func(t *testing.T) {
			atomtest.Expect(t, run(t, files), checks.StateCannotRun, atomtest.Cannot)
		})
	}
}

// UT-l7-4: a pull with a commit by nyx's worker changes only the file and the
// published copies; a human's pull is not so bound.
func TestANyxPullIsScopedToTheRender(t *testing.T) {
	ok := flux([]string{"ci_await", "ci_canary"}, grants, published(bothRoutes))
	render := map[string]string{File: doc("ci_await", "ci_canary", "extra"), Published + "daedalus.json": published(bothRoutes) + "\n"}
	for _, c := range []struct {
		name  string
		pull  []commit
		state checks.State
		res   string
		words []string
	}{
		{"nyx render of the file and a published copy", []commit{{render, Nyx}}, checks.StatePass, atomtest.Pass, nil},
		{"nyx touching another path", []commit{{map[string]string{File: doc("ci_await", "ci_canary"), "prime/ingress-fence.yaml": "x"}, Nyx}}, checks.StateFindings, atomtest.Findings, []string{"prime/ingress-fence.yaml"}},
		{"nyx writing under a published subdirectory", []commit{{map[string]string{Published + "sub/x.json": "{}"}, Nyx}}, checks.StateFindings, atomtest.Findings, []string{Published + "sub/x.json"}},
		{"nyx writing a non-json published file", []commit{{map[string]string{Published + "x.yaml": "x"}, Nyx}}, checks.StateFindings, atomtest.Findings, []string{Published + "x.yaml"}},
		{"another commit riding a nyx pull", []commit{{render, Nyx}, {map[string]string{"README.md": "x"}, human}}, checks.StateFindings, atomtest.Findings, []string{"README.md"}},
		{"a human may change anything", []commit{{map[string]string{"prime/ingress-fence.yaml": "x"}, human}}, checks.StatePass, atomtest.Pass, nil},
		{"nyx as committer alone", []commit{{map[string]string{"README.md": "x"}, "NYX@pantheon.foundry"}}, checks.StateFindings, atomtest.Findings, []string{"README.md"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			atomtest.Expect(t, run(t, ok, c.pull...), c.state, c.res, c.words...)
		})
	}
}

// With no base the tip is read against its parent.
func TestTheTipWithNoBaseIsReadAgainstItsParent(t *testing.T) {
	in, _ := tree(t, flux([]string{"ci_await", "ci_canary"}, grants, published(bothRoutes)), commit{map[string]string{"README.md": "x"}, Nyx})
	atomtest.Expect(t, check(context.Background(), in), checks.StateFindings, atomtest.Findings, "README.md")
}

// A base the history does not reach, and a git that will not list, cannot run.
func TestAGitThatWillNotAnswerCannotRun(t *testing.T) {
	in, _ := tree(t, flux([]string{"ci_await", "ci_canary"}, grants, published(bothRoutes)))
	in.Base = "0000000000000000000000000000000000000000"
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "not in this history")
	for _, c := range []struct{ fails, words string }{{"log", "commits since"}, {"diff", "paths changed since"}} {
		t.Run(c.fails, func(t *testing.T) {
			in, _ := atomtest.ToolTree(t, map[string]string{File: doc("ci_await")}, func(cmd atoms.Cmd) (string, int) {
				switch cmd.Args[2] {
				case c.fails:
					return "fatal: broken", 128
				case "log":
					return Nyx + "\x00" + Nyx + "\n", 0
				}
				return "", 0
			})
			atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, c.words, "broken")
		})
	}
}

// A file the atom cannot read is a could-not-run (a directory where the policy should be).
func TestAnUnreadablePolicyCannotRun(t *testing.T) {
	in := atomtest.TreeIn(t, map[string]string{File + "/x": "x"})
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, File)
}

// A rule Cilium could not compile admits nothing, and a bare (unquoted) scalar is read.
func TestUncompilableAndBareRulesAreHandled(t *testing.T) {
	f := strings.Replace(doc("ci_await", "ci_canary"), `"^/v1/ci_canary(?:/[^/?.%][^/?%]*)*(?:\\?.*)?$"`, `"^/v1/ci_canary(["`, 1)
	files := flux(nil, grants, published(bothRoutes))
	files[File] = f
	atomtest.Expect(t, run(t, files), checks.StateFindings, atomtest.Findings, "ci_canary")
	bare := strings.Replace(doc("ci_await", "ci_canary"), `"^/v1/ci_canary(?:/[^/?.%][^/?%]*)*(?:\\?.*)?$"`, `^/v1/ci_canary$`, 1)
	files[File] = bare
	atomtest.Expect(t, run(t, files), checks.StatePass, atomtest.Pass)
}

// A published grants copy that exists but cannot be read (a directory where the
// file should be) is a could-not-run, not the first-run baseline.
func TestAnUnreadableGrantsCopyIsNotTheBaseline(t *testing.T) {
	files := map[string]string{File: doc("ci_await"), Published + "grants.json/x": "x"}
	atomtest.Expect(t, run(t, files), checks.StateCannotRun, atomtest.Cannot, "grants.json would not read")
}

// A document of the file that is not in rest render-mode holds no rules; only
// the rest-mode document is graded.
func TestAnotherRenderModeDocumentIsNotGraded(t *testing.T) {
	other := strings.Replace(doc("only_here"), `render-mode: "rest"`, `render-mode: "mesh"`, 1)
	files := flux([]string{"ci_await", "ci_canary"}, grants, published(bothRoutes))
	files[File] = doc("ci_await", "ci_canary") + "---\n" + other
	atomtest.Expect(t, run(t, files), checks.StatePass, atomtest.Pass)
}

// The rules nyx's renderer emits for a verb with a rob_tier matcher in the
// compiled grants (verbPath with segmentNotIn): every sub-path of the verb
// except a segment that is the Rob-tier word. Copied from flux#5952's file.
const (
	pauseCarved   = `"^/v1/ci_run(?:/(?:[^/?%.p][^/?%]*|p(?:[^/?%a][^/?%]*|a(?:[^/?%u][^/?%]*|u(?:[^/?%s][^/?%]*|s(?:[^/?%e][^/?%]*|e(?:[^/?%]+)|)|)|)|)))*(?:\\?.*)?$"`
	forceCarved   = `"^/v1/merge(?:/(?:[^/?%.f][^/?%]*|f(?:[^/?%o][^/?%]*|o(?:[^/?%r][^/?%]*|r(?:[^/?%c][^/?%]*|c(?:[^/?%e][^/?%]*|e(?:[^/?%]+)|)|)|)|)))*(?:\\?.*)?$"`
	liveCarved    = `"^/v1/tripwire(?:/(?:[^/?%.l][^/?%]*|l(?:[^/?%i][^/?%]*|i(?:[^/?%v][^/?%]*|v(?:[^/?%e][^/?%]*|e(?:[^/?%]+)|)|)|)))*(?:\\?.*)?$"`
	confirmCarved = `"^/v1/retire(?:/(?:[^/?%.c][^/?%]*|c(?:[^/?%o][^/?%]*|o(?:[^/?%n][^/?%]*|n(?:[^/?%f][^/?%]*|f(?:[^/?%i][^/?%]*|i(?:[^/?%r][^/?%]*|r(?:[^/?%m][^/?%]*|m(?:[^/?%]+)|)|)|)|)|)|)))*(?:\\?.*)?$"`
)

// carvedDoc is grants-daedalus with ci_run and retire rendered as nyx renders
// their Rob-tier carve-outs (pause; confirm), and the other verbs plain.
func carvedDoc(plain ...string) string {
	f := doc(append([]string{"ci_run", "retire"}, plain...)...)
	f = strings.Replace(f, `"^/v1/ci_run(?:/[^/?.%][^/?%]*)*(?:\\?.*)?$"`, pauseCarved, 1)
	return strings.Replace(f, `"^/v1/retire(?:/[^/?.%][^/?%]*)*(?:\\?.*)?$"`, confirmCarved, 1)
}

const carvedRoutes = `"ci_run": [{"method": "POST", "path": "/v1/ci_run"}, {"method": "POST", "path": "/v1/ci_run/pause"},
	{"method": "POST", "path": "/v1/ci_run/pause/repo/{repo}"}, {"method": "POST", "path": "/v1/ci_run/run/repo/{repo}"}],
	"retire": [{"method": "DELETE", "path": "/v1/retire"}, {"method": "DELETE", "path": "/v1/retire/confirm"},
	{"method": "DELETE", "path": "/v1/retire/force"}, {"method": "DELETE", "path": "/v1/retire/force/confirm"}]`

// UT-l7-5 (flux#5952): the grants list ci_run, retire, merge and tripwire at
// verb level, and hades serves their Rob-tier sub-routes (ci_run pause, retire
// confirm, merge force, tripwire trip/live) too. The committed rule carves
// exactly those words out (they stay on the mTLS door via Charon), so they are
// not a missing path; the gate holds and says how many it left to the carve-out.
func TestASubRouteTheVerbsRuleCarvesOutIsNotAMissingPath(t *testing.T) {
	g := `{"tx": "1", "stars": {"daedalus": ["ci_run", "retire", "merge", "tripwire"]}}`
	routes := carvedRoutes + `, "merge": [{"method": "POST", "path": "/v1/merge"}, {"method": "POST", "path": "/v1/merge/force"},
	{"method": "POST", "path": "/v1/merge/repo/{repo}/force"}], "tripwire": [{"method": "POST", "path": "/v1/tripwire/trip"},
	{"method": "POST", "path": "/v1/tripwire/trip/live"}]`
	files := flux(nil, g, published(routes))
	f := carvedDoc("merge", "tripwire")
	f = strings.Replace(f, `"^/v1/merge(?:/[^/?.%][^/?%]*)*(?:\\?.*)?$"`, forceCarved, 1)
	files[File] = strings.Replace(f, `"^/v1/tripwire(?:/[^/?.%][^/?%]*)*(?:\\?.*)?$"`, liveCarved, 1)
	atomtest.Expect(t, run(t, files), checks.StatePass, atomtest.Pass, "every published verb has its hades-sa path", "; 7 Rob-tier sub-routes")
}

// UT-l7-6 (CONSISTENCY kept): a carve-out excuses only the word its own verb's
// rule excludes. A granted route that no rule admits is still refused: a verb
// with no rule, a verb whose rule admits only its bare path, and a carved word
// under a verb whose rule does not carve it.
func TestACarveOutExcusesOnlyItsOwnVerbsWord(t *testing.T) {
	g := `{"tx": "1", "stars": {"daedalus": ["ci_run", "retire", "ci_canary", "ci_logs", "ci_await"]}}`
	routes := carvedRoutes + `, "ci_canary": [{"method": "POST", "path": "/v1/ci_canary/pause"}],
	"ci_logs": [{"method": "POST", "path": "/v1/ci_logs/confirm"}], "ci_await": [{"method": "POST", "path": "/v1/ci_await"}]`
	files := flux(nil, g, published(routes))
	files[File] = strings.Replace(carvedDoc("ci_await"), `              - method: "GET"`,
		`              - path: "^/v1/ci_logs(?:\\?.*)?$"`+"\n"+`              - method: "GET"`, 1)
	v := run(t, files)
	atomtest.Expect(t, v, checks.StateFindings, atomtest.Findings, "POST /v1/ci_canary/pause", "POST /v1/ci_logs/confirm")
	if strings.Contains(v.Reason, "/v1/ci_run/pause") || strings.Contains(v.Reason, "/v1/retire/") {
		t.Errorf("a carved-out Rob-tier sub-route was named:\n%s", v.Reason)
	}
}

// UT-l7-5 (wiring): an L4 floor on hades's face is a finding of this atom, a
// policy that does not parse is a could-not-run, and the pass says the face was
// looked at. The cases live in internal/l4erasure.
func TestTheAtomRefusesAnL4AdmissionToHadesFace(t *testing.T) {
	floor := `apiVersion: cilium.io/v2
kind: CiliumClusterwideNetworkPolicy
metadata: {name: forge-baseline-nyx-prober}
spec:
  endpointSelector: {matchLabels: {io.kubernetes.pod.namespace: prime}}
  ingress:
    - fromEndpoints: [{matchLabels: {io.cilium.k8s.policy.serviceaccount: nyx-sa}}]
      toPorts: [{ports: [{port: "0", protocol: TCP}]}]
`
	f := flux([]string{"ci_await"}, "", "")
	f["network/floor.yaml"] = floor
	atomtest.Expect(t, run(t, f), checks.StateFindings, atomtest.Findings, "forge-baseline-nyx-prober", "voids the L7 stamp")
	f["network/floor.yaml"] = "kind: CiliumNetworkPolicy\nspec: [\n"
	atomtest.Expect(t, run(t, f), checks.StateCannotRun, atomtest.Cannot, "does not parse")
	atomtest.Expect(t, run(t, flux([]string{"ci_await"}, "", "")), checks.StatePass, atomtest.Pass, "hades :8101")
}
