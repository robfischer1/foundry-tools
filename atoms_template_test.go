package main

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// THE SWEEP LANE, AGAINST THE PAPER ENGINE (engine_fake_test.go). These four
// this atom finds a surface four repos in eighty have, which is
// where a silent pass does the most damage: nobody is watching, so a body that
// exits 0 because its tool never arrived reads as a clean fleet until somebody
// happens to look. So every branch below is exercised by its own case, and
// each is asserted on the WORDING it files, not merely on the state.

// sweepTree copies everyLaneTree, deletes every path in drop, and overlays
// add — the tree under check, minus or plus whatever the branch needs.
func templateTree(drop []string, add map[string]string) map[string]string {
	// The template lane's own surface: everyLaneTree carries no ci-matrix.toml
	// (see engine_fake_test.go), so every tree this builds starts with one and a
	// caller that wants it gone names it in drop.
	base := map[string]string{"ci-matrix.toml": "parse = [\"**/*.json\"]\n\n[[case]]\nname = \"only\"\nanswers = { variant = \"star\" }\npresent = [\"go.mod\"]\nabsent = [\".forgejo\"]\n"}
	tree := make(map[string]string, len(everyLaneTree)+len(base))
	for k, v := range everyLaneTree {
		tree[k] = v
	}
	for k, v := range base {
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

// ---- template:render-matrix ----

// renderNeedle is the copier render for the tree's one declared case.
const renderNeedle = `"copier","copy"`

// renderedTree seeds everyLaneTree plus a rendered output at /out/only.
func renderedTree(rendered map[string]string) map[string]string {
	add := map[string]string{}
	for p, body := range rendered {
		add["/out/only/"+p] = body
	}
	return templateTree(nil, add)
}

// THE RENDER IS THE GATE: copier writes the tree, and everything after is read
// off the rendered output rather than parsed out of a script's stdout.
func TestTemplateRenderMatrixRendersEachCaseAndGradesTheOutput(t *testing.T) {
	engine.reset()
	engine.withTree(renderedTree(map[string]string{"go.mod": "module x\n", "data.json": `{"a": 1}`}))
	v := runAtom(t, "template:render-matrix", "")
	wantState(t, v, 0)

	c := engine.chain(renderNeedle, "exitCode")
	if !strings.Contains(c, checks.ImageFleet) {
		t.Errorf("the render runs in the fleet image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withMountedDirectory", `path:"/src"`},
		// gitReady's other half: git refuses a repository it does not own, and
		// the process here is root over a mounted tree.
		[]string{"withNewFile", `path:"/etc/gitconfig"`, `directory = *`},
		// The provisioning probe: copier is fetched through uvx, and a missing
		// uvx is state 2 with the engine's error, never a green.
		[]string{"withExec", `args:["uvx","--version"]`},
		[]string{"withExec", `expect:ANY`, `"--trust"`},
		[]string{"withExec", `expect:ANY`, `"--skip-tasks"`},
		[]string{"withExec", `expect:ANY`, `"--vcs-ref=HEAD"`},
		[]string{"withExec", `expect:ANY`, `"--data","variant=star"`},
		[]string{"withExec", `expect:ANY`, `"/src","/out/only"`},
	)
	if hasCall(c, "withExec", `args:["uvx","--version"]`, `expect:ANY`) {
		t.Errorf("the uvx probe is provisioning and must run under the default Expect:\n%s", c)
	}
	// A primary checkout's .git is a directory; gitReady has nothing to rebuild.
	if hasCall(c, "withExec", `args:["git","init","-q","."]`) {
		t.Errorf("a .git DIRECTORY needs no throwaway repository:\n%s", c)
	}
	if strings.Contains(c, "GATE_BASE") {
		t.Errorf("template:render-matrix must not read GATE_BASE:\n%s", c)
	}
	// Nothing is read from foundry-stocks any more: the gate is this module.
	for _, q := range engine.chains() {
		if strings.Contains(q, `path:"/stocks"`) || strings.Contains(q, "python3") {
			t.Errorf("the render matrix runs no script and mounts no stocks:\n%s", q)
		}
	}
}

// Every way a case can be wrong, and the report that says which.
func TestTemplateRenderMatrixGradesTheRenderedTree(t *testing.T) {
	for _, tc := range []struct {
		label    string
		rendered map[string]string
		state    int
		says     string
	}{
		{"a clean render", map[string]string{"go.mod": "module x\n"}, 0, ""},
		{"an empty tree", map[string]string{}, 1, "rendered nothing — copier reported success but the tree is empty"},
		{"a conditional path that did not resolve", map[string]string{"go.mod": "", "{% if x %}only{% endif %}/a.txt": ""}, 1, "unresolved jinja in rendered PATH"},
		{"a suffix copier did not strip", map[string]string{"go.mod": "", "a.py.jinja": ""}, 1, "unstripped .jinja suffix: a.py.jinja"},
		{"a present expectation that is missing", map[string]string{"README.md": ""}, 1, "expected PRESENT but missing: go.mod"},
		{"an absent expectation that rendered", map[string]string{"go.mod": "", ".forgejo/": "", ".forgejo/workflows/ci.yml": ""}, 1, "expected ABSENT but rendered: .forgejo -> .forgejo"},
		{"a born-red stamp", map[string]string{"go.mod": "", "data.json": "{oops}"}, 1, "data.json does not parse as json"},
		{"a suppression in the pour surface", map[string]string{"go.mod": "", "src/a.py": "x = 1  # no" + "qa: E501\n"}, 1, "a suppression in the POUR SURFACE reaches every repo born from this template"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			engine.reset()
			engine.withTree(renderedTree(tc.rendered))
			v := runAtom(t, "template:render-matrix", "")
			wantState(t, v, tc.state)
			if tc.says != "" && !strings.Contains(v.Reason, tc.says) {
				t.Errorf("want %q in:\n%s", tc.says, v.Reason)
			}
			if tc.state == 1 && !strings.Contains(v.Reason, "::error::render matrix FAILED for: only") {
				t.Errorf("a failed case is named in the roll-up:\n%s", v.Reason)
			}
		})
	}
}

// A copier that refused is the case's failure, with copier's own last words —
// never a green, and never an engine error.
func TestTemplateRenderMatrixReportsARefusedRender(t *testing.T) {
	engine.reset()
	engine.withTree(renderedTree(map[string]string{"go.mod": ""}))
	engine.exitCode(renderNeedle, 1)
	engine.stderr(renderNeedle, "Error: conflict\nTemplate does not declare `variant`")
	v := runAtom(t, "template:render-matrix", "")
	wantState(t, v, 1, "copier render failed:", "Template does not declare `variant`")

	engine.reset()
	engine.withTree(renderedTree(map[string]string{"go.mod": ""}))
	engine.fail(renderNeedle, "the engine went away")
	wantState(t, runAtom(t, "template:render-matrix", ""), 2, "never ran", "the engine went away")
}

func TestTemplateRenderMatrixRefusesATreeThatIsNotARepository(t *testing.T) {
	// No matrix declared: an absence, and nothing runs.
	engine.reset()
	engine.withTree(templateTree([]string{"ci-matrix.toml"}, nil))
	v := runAtom(t, "template:render-matrix", "")
	wantState(t, v, 0, "no ci-matrix.toml at the repository root")
	if v.Result != "absent" {
		t.Errorf("result %q, want absent:\n%s", v.Result, v.Reason)
	}
	wantNoContainer(t, "no render matrix is decided in Go")

	// The root itself.
	engine.reset()
	engine.withTree(templateTree(nil, nil))
	engine.fail(rootEntries, "mount evaporated")
	wantState(t, runAtom(t, "template:render-matrix", ""), 2, "the repository root could not be read", "mount evaporated")

	// The matrix itself: unreadable, and unusable.
	engine.reset()
	engine.withTree(templateTree(nil, nil))
	engine.fail(`file(path:"ci-matrix.toml")`, "blob missing")
	wantState(t, runAtom(t, "template:render-matrix", ""), 2, "ci-matrix.toml could not be read", "blob missing")

	engine.reset()
	engine.withTree(templateTree(nil, map[string]string{"ci-matrix.toml": "parse = []\n"}))
	wantState(t, runAtom(t, "template:render-matrix", ""), 2, "::error::ci-matrix.toml declares no [[case]]")
	wantNoContainer(t, "a matrix with no case renders nothing")

	// No .git at all: the matrix renders the template AT ITS GIT HEAD, and
	// without a repository copier resolves some other tree.
	engine.reset()
	engine.withTree(templateTree([]string{".git/HEAD"}, nil))
	v = runAtom(t, "template:render-matrix", "")
	wantState(t, v, 2, "no .git in the tree under check", "--vcs-ref=HEAD is load-bearing")
	if strings.Contains(v.Reason, "is a FILE") {
		t.Errorf("there is no .git at all; calling it a file misreports the cause:\n%s", v.Reason)
	}
	wantNoContainer(t, "a tree with no repository never renders")

	// A LINKED WORKTREE's .git is a FILE naming a host path that does not
	// exist inside the container. gitReady can rebuild an index; it cannot
	// rebuild the history copier resolves a ref against, so this is a refusal
	// on purpose.
	engine.reset()
	engine.withTree(templateTree([]string{".git/HEAD"}, map[string]string{
		".git": "gitdir: /home/rob/Forge/Outputs/foundry-tools/.git/worktrees/Tesla19\n",
	}))
	v = runAtom(t, "template:render-matrix", "")
	wantState(t, v, 2, "no .git in the tree under check", "Here .git is a FILE, not a directory", "linked worktree")
	wantNoContainer(t, "a linked worktree never renders")
}

// A REPO-CADENCE ATOM MUST NOT KEY ITS CACHE ON THE PULL. This was CA F9's
// acceptance criterion read off the chain rather than off the table: a sweep
// atom described a REPOSITORY rather than a change, so it could not reach a
// pull's path and could not key on one either.
//
// THE SWEEP IS GONE (F18) AND THE SECOND HALF STILL BINDS. These two atoms now
// run IN the pull path — that is the whole point of re-homing them — but what
// they ask is still about the repository, so keying on GATE_BASE would re-run
// them for every pull against one unchanged tree for no new answer. The first
// draft of this landing widened the loop to every prepush atom and caught
// fleet:witness, which is diff-scoped BY DESIGN; the scope is the two, not the
// stage.
func TestNoRepoCadenceAtomReadsTheChangeSet(t *testing.T) {
	for _, a := range []checks.AtomDef{
		checks.AtomByID("ops:kube-linter"),
		checks.AtomByID("template:render-matrix"),
	} {
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

// EACH READ OFF THE CONTAINER IS ITS OWN CHANCE FOR THE ENGINE TO GO AWAY, and
// all three are the engine's failure rather than the tool's — state 2, never a
// verdict about the tree. outputBoth reads exitCode, stdout AND stderr,
// because kubeconform's summary and kube-linter's zero-population refusal each
// land on whichever stream they feel like.
func TestReadsOfTheContainerThatFailPartWayAreNeverAVerdict(t *testing.T) {
	for _, tc := range []struct {
		name, atom, match, leaf string
	}{
		{"kube-linter loses stderr", "ops:kube-linter", `"/kube-linter"`, "stderr"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine.reset()
			engine.withTree(everyLaneTree)
			engine.failLeaf(tc.match, tc.leaf, "the engine went away")
			wantState(t, runAtom(t, tc.atom, ""), 2, "never ran", "the engine went away")
		})
	}
}

// tailLines had no direct test — the truncation arm read NOT COVERED on PR
// #77's mutation gate, which is the sharper verdict: no test executed it at
// all. Its callers only ever fed it short output.
func TestTailLinesCutsTheEndOfWhatCopierSaid(t *testing.T) {
	five := "a\nb\nc\nd\ne"
	for _, tc := range []struct {
		name string
		out  string
		n    int
		want []string
	}{
		{"n well under the length", five, 2, []string{"d", "e"}},
		{"n one under the length", five, 4, []string{"b", "c", "d", "e"}},
		{"n is the length", five, 5, []string{"a", "b", "c", "d", "e"}},
		{"n one past the length", five, 6, []string{"a", "b", "c", "d", "e"}},
		{"n well past the length", five, 120, []string{"a", "b", "c", "d", "e"}},
		{"n is zero", five, 0, nil},
		{"one line", "only", 12, []string{"only"}},
		{"empty output", "", 12, []string{""}},
		// TrimSpace cuts the WHOLE string's ends, not each line's — so the
		// spaces after the last "b" go with the trailing newlines, while a
		// space inside the block survives.
		{"surrounding whitespace is trimmed first", "\n\n  a\nb  \n\n", 12, []string{"a", "b"}},
		{"inner whitespace survives", "x  \ny", 12, []string{"x  ", "y"}},
		{"the cut is the TAIL, not the head", "first\nlast", 1, []string{"last"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tailLines(tc.out, tc.n)
			if len(got) != len(tc.want) {
				t.Fatalf("tailLines(%q, %d) = %q, want %q", tc.out, tc.n, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("tailLines(%q, %d) = %q, want %q", tc.out, tc.n, got, tc.want)
				}
			}
		})
	}
}
