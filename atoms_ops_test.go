package main

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

var opsIDs = []string{"ops:shell", "ops:chezmoi", "ops:yaml", "ops:dup", "ops:declaration", "ops:specs", "ops:ansible", "ops:flux"}

// A star is not an ops tree: every ops atom stands down ABSENT without a
// container, whatever scripts and YAML the star happens to carry. The lane
// never gated stars, and turning it on across the fleet at once is not what
// porting it means.
func TestOpsAtomsStandDownOffTheOpsShape(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{
		"go.mod": "module x\n", "main.go": "package main\n",
		"ci/run.sh": "#!/bin/sh\necho hi\n", "deploy/x.yaml": "a: 1\n",
	})
	for _, id := range opsIDs {
		wantState(t, runAtom(t, id, ""), 0, "ABSENT", "no ops shape")
	}
	if engine.chain("ops.sh") != "" {
		t.Errorf("no ops shape means no container:\n%s", engine.chain("ops.sh"))
	}
}

// opsWrote scripts the files the body leaves in OPS_DIR for a phase.
func opsWrote(phase, rc, absent string) {
	if rc != "" {
		engine.script(script{match: `file(path:"` + opsDir + `/` + phase + `.rc")`, leaf: "contents", value: rc})
	}
	if absent != "" {
		engine.script(script{match: `file(path:"` + opsDir + `/` + phase + `.absent")`, leaf: "contents", value: absent})
	}
}

// ops:shell in an ops tree: the fleet image with the stocks mounted, the body
// on OPS_LIB, shellcheck on PATH through the shellcheck-py wheel and probed
// under the default Expect, then detect and the phase as two plain execs, and
// the verdict read from the rc file the body wrote.
func TestOpsShellRunsTheBodyFromTheStocks(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	opsWrote("shell", "0", "")

	wantState(t, runAtom(t, "ops:shell", ""), 0)

	c := engine.chain(`ops.sh","shell"`, "exitCode")
	if c == "" {
		t.Fatal("ops:shell ran no phase")
	}
	if !strings.Contains(c, checks.ImageFleet) {
		t.Errorf("ops:shell must run in the fleet lane image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withMountedDirectory", `path:"/src"`},
		[]string{"withMountedDirectory", `path:"/stocks"`},
		[]string{"withEnvVariable", `name:"OPS_DIR"`, `value:"/tmp/ops"`},
		[]string{"withEnvVariable", `name:"OPS_LIB"`, `value:"/stocks/ci/lib/ops"`},
		[]string{"withEnvVariable", `name:"OPS_UV_INDEX"`},
		[]string{"withNewFile", `path:"/usr/local/bin/shellcheck"`, `shellcheck-py`},
		[]string{"withExec", `args:["shellcheck","--version"]`},
		[]string{"withExec", `expect:ANY`, `args:["bash","/stocks/ci/lib/ops/ops.sh","detect"]`},
		[]string{"withExec", `expect:ANY`, `args:["bash","/stocks/ci/lib/ops/ops.sh","shell"]`},
	)
	if hasCall(c, "withExec", `"shellcheck","--version"`, `expect:ANY`) {
		t.Errorf("the --version probe is provisioning and must run under the default Expect:\n%s", c)
	}
	if strings.Contains(c, "OPS_BASE") || strings.Contains(c, "GATE_BASE") {
		t.Errorf("rule 8: the ops atoms must not key on the pull's base:\n%s", c)
	}
}

// The phase's answer is the rc file the body wrote, three states; a phase
// that wrote <phase>.absent is ABSENT with the body's own reason; a phase that
// wrote nothing is a stand-down when detect left a reason and CANNOT RUN
// when it did not.
func TestOpsPhaseReadsTheBodysVerdictFiles(t *testing.T) {
	const phase = `ops.sh","ansible"`

	engine.reset()
	engine.withTree(everyLaneTree)
	opsWrote("ansible", "1", "")
	engine.stdout(phase, "ansible/playbooks/site.yml:3: syntax error")
	wantState(t, runAtom(t, "ops:ansible", ""), 1, "syntax error")

	engine.reset()
	engine.withTree(everyLaneTree)
	opsWrote("ansible", "2", "")
	wantState(t, runAtom(t, "ops:ansible", ""), 2)

	engine.reset()
	engine.withTree(everyLaneTree)
	opsWrote("ansible", "0", "")
	wantState(t, runAtom(t, "ops:ansible", ""), 0)

	engine.reset()
	engine.withTree(everyLaneTree)
	opsWrote("ansible", "", "no ansible/playbooks in this tree")
	wantState(t, runAtom(t, "ops:ansible", ""), 0, "ABSENT", "no ansible/playbooks in this tree")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.script(script{match: `file(path:"` + opsDir + `/reason")`, leaf: "contents", value: "no ops facet in this tree"})
	wantState(t, runAtom(t, "ops:ansible", ""), 0, "ABSENT", "no ops facet")

	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "ops:ansible", ""), 2, "CANNOT RUN", "wrote no verdict")

	engine.reset()
	engine.withTree(everyLaneTree)
	opsWrote("ansible", "7", "")
	wantState(t, runAtom(t, "ops:ansible", ""), 2, "CANNOT RUN", `"7"`)
}

// ops:flux and ops:chezmoi bring their binary pinned: mirror first, upstream
// second, installed executable and probed before the phase runs. A fetch
// that fails both ways is 2 — provisioning, not a finding — and no phase
// runs.
func TestOpsFluxAndChezmoiFetchTheirToolPinned(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)

	opsWrote("flux", "0", "")
	opsWrote("chezmoi", "0", "")
	wantState(t, runAtom(t, "ops:flux", ""), 0)
	c := engine.chain(`ops.sh","flux"`, "exitCode")
	wantCalls(t, c,
		[]string{"withFile", `path:"/usr/local/bin/kubectl"`, `permissions:493`},
		[]string{"withExec", `args:["kubectl","version","--client=true"]`},
	)
	if engine.chain(`http(url:"`+checks.KubectlURL+`")`, "sync") == "" {
		t.Errorf("ops:flux must fetch kubectl from %s", checks.KubectlURL)
	}

	wantState(t, runAtom(t, "ops:chezmoi", ""), 0)
	c = engine.chain(`ops.sh","chezmoi"`, "exitCode")
	wantCalls(t, c,
		[]string{"withFile", `path:"/usr/local/bin/chezmoi"`, `permissions:493`},
		[]string{"withExec", `args:["chezmoi","--version"]`},
	)
	if engine.chain(`http(url:"`+checks.ChezmoiMirror+`")`, "sync") == "" {
		t.Errorf("ops:chezmoi must try the mirror first: %s", checks.ChezmoiMirror)
	}

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`http(url:"`+checks.ChezmoiMirror+`")`, "mirror down")
	engine.fail(`http(url:"`+checks.ChezmoiURL+`")`, "upstream down")
	wantState(t, runAtom(t, "ops:chezmoi", ""), 2, "CANNOT RUN", "could not be provisioned")
	if engine.chain(`ops.sh","chezmoi"`) != "" {
		t.Error("a tool that did not arrive must not run the phase")
	}
}

// The catalogue carries all eight on the fleet lane, prepush, needing the
// stocks, so the runner mounts /stocks and the seed arm can register them.
func TestOpsAtomsAreCatalogued(t *testing.T) {
	for _, id := range opsIDs {
		a := checks.AtomByID(id)
		if a.ID != id {
			t.Fatalf("%s is not in the catalogue", id)
		}
		if a.Stage != checks.StagePrepush || a.Lane != checks.LaneAny || a.Image != checks.ImageFleet || !a.NeedsStocks {
			t.Errorf("%s: stage=%s lane=%v image=%s stocks=%v", id, a.Stage, a.Lane, a.Image, a.NeedsStocks)
		}
		if checks.OpsPhase(id) != strings.TrimPrefix(id, "ops:") {
			t.Errorf("%s: phase %s", id, checks.OpsPhase(id))
		}
	}
}
