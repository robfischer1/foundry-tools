package main

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/catalogue"
	"dagger/foundry-tools/internal/checks"
)

const regenNeedle = "dev__go-regen.json"

func regenVerdict(state int, result string) string {
	return `{"atom":"dev:go-regen","stage":"dev","lane":"go","state":` + string(rune('0'+state)) + `,"result":"` + result + `","reason":"dev:go-regen: ` + result + `","logs":["alarm: guest unchanged"]}`
}

// THE ATOM'S ENVIRONMENT: the rust lane with the pinned programs it execs, every
// gravity prebuilt under its tool name, and the dev request's arguments after
// the root on the atom's command line.
func TestDevGoRegenRunsInTheRustLaneWithItsToolsAndArguments(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.contents(regenNeedle, regenVerdict(0, "pass"))
	r := newRun(dag.Directory(), "", "")
	r.args = []string{"alarm", "--new", "mode"}
	fn := registry["dev:go-regen"]
	if v := fn(context.Background(), r); v.State != 0 {
		t.Fatalf("%+v", v)
	}
	c := engine.chain(regenNeedle, "contents")
	wantCalls(t, c,
		[]string{"withExec", `args:["tar","xzf","/tmp/wasm-tools.tar.gz"`},
		[]string{"withFile", `path:"/usr/local/bin/wac"`},
		[]string{"withFile", `path:"/usr/local/bin/gravity-68d8146"`},
		[]string{"withFile", `path:"/usr/local/bin/gravity-aeb03bf"`},
		[]string{"withExec", `args:["tar","xzf","/tmp/gofmt.tar.gz","-C","/usr/local/bin","--strip-components=2","go/bin/gofmt"]`},
		[]string{"withExec", "expect:ANY", `args:["/atom","/src","alarm","--new","mode"]`},
	)
	if pinnedFetch(checks.GofmtURL, "id") == "" {
		t.Errorf("the pinned Go release was not fetched:\n%v", engine.chains())
	}
	if hasCall(c, "withExec", `args:["cargo","fetch","--locked"]`) {
		t.Errorf("a Go tree has no crate to fetch:\n%s", c)
	}
}

func TestAGateRunOfAnAtomPassesNoArguments(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.contents(regenNeedle, regenVerdict(0, "pass"))
	registry["dev:go-regen"](context.Background(), newRun(dag.Directory(), "", ""))
	if c := engine.chain(regenNeedle, "contents"); !hasCall(c, "withExec", `args:["/atom","/src"]`) {
		t.Errorf("%s", c)
	}
}

func TestDevRegenAnswersTheFilesTheAtomWrote(t *testing.T) {
	d := devOver(t, devGoTree, "", "")
	engine.contents(regenNeedle, regenVerdict(0, "pass"))
	dir, err := d.Regen(t.Context(), []string{"alarm"}, false)
	if err != nil || dir == nil {
		t.Fatalf("%v %v", dir, err)
	}
	c := engine.chain(regenNeedle, "contents")
	wantCalls(t, c, []string{"withExec", "expect:ANY", `args:["/atom","/src","alarm"]`})
	if engine.chain(`"/usr/local/bin/verdict"`) != "" {
		t.Error("a regeneration that passed ran the verdict exec")
	}
}

func TestDevRegenEndsOnTheAtomsVerdict(t *testing.T) {
	for state, code := range map[int]string{1: "1", 2: "2"} {
		d := devOver(t, devGoTree, "", "")
		engine.contents(regenNeedle, regenVerdict(state, "fail"))
		engine.fail(`"/usr/local/bin/verdict"`, "exit code: "+code)
		if _, err := d.Regen(t.Context(), []string{"alarm"}, true); err == nil || !strings.Contains(err.Error(), "exit code: "+code) {
			t.Fatalf("state %d: %v", state, err)
		}
		if got := settledWith(t, code); !strings.Contains(got, "dev:go-regen: fail") || !strings.Contains(got, "alarm: guest unchanged") {
			t.Errorf("reason %q", got)
		}
	}
}

func TestDevRegenSettlesEvenWhenTheVerdictExecDoesNotFail(t *testing.T) {
	d := devOver(t, devGoTree, "", "")
	engine.contents(regenNeedle, regenVerdict(1, "fail"))
	if _, err := d.Regen(t.Context(), nil, false); err == nil || !strings.Contains(err.Error(), "dev:go-regen: fail") {
		t.Fatalf("%v", err)
	}
}

func TestDevRegenIsForAGoTree(t *testing.T) {
	d := devOver(t, devRustTree, "", "")
	if _, err := d.Regen(t.Context(), nil, false); err == nil || !strings.Contains(err.Error(), "Go tree") {
		t.Fatalf("%v", err)
	}
}

func TestDevRegenNeedsTheAtomInTheRegistry(t *testing.T) {
	old := regenEntry
	regenEntry = func(string) (catalogue.Entry, bool) { return catalogue.Entry{}, false }
	t.Cleanup(func() { regenEntry = old })
	d := devOver(t, devGoTree, "", "")
	if _, err := d.Regen(t.Context(), nil, false); err == nil || !strings.Contains(err.Error(), "not in the generated registry") {
		t.Fatalf("%v", err)
	}
}
