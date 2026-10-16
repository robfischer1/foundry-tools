package main

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/catalogue"
)

// ON THE DOOR'S TREE, ORIGIN IS THE REPO IT WAS FETCHED FROM, WITHOUT ASKING.
// sourceAt sets it; stop-justifications names the repository by it (the
// exemption keyed on cerberus applies), is handed it as ATOM_ORIGIN and nothing
// else of the change's facts (no base is fetched), and no `git remote get-url`
// runs.
func TestStopJustificationsIsHandedTheDoorsOriginWithoutAnExec(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.contents("fleet__stop-justifications.json", `{"atom":"fleet:stop-justifications","state":0,"result":"pass","logs":[]}`)
	e, ok := catalogue.Lookup("fleet:stop-justifications")
	if !ok {
		t.Fatal("fleet:stop-justifications is not in the catalogue")
	}
	r := newRun(dag.Directory(), "http://door:8215/cerberus.git", "")
	wantState(t, runAtomOnDagger(t.Context(), r, e), 0)
	c := engine.chain("fleet__stop-justifications.json", "contents")
	wantCalls(t, c, []string{"withEnvVariable", `name:"ATOM_ORIGIN"`, `value:"http://door:8215/cerberus.git"`})
	for _, not := range []string{"ATOM_BASE", "GATE_BASE", "ATOM_FORK", "get-url"} {
		if strings.Contains(c, not) {
			t.Errorf("the origin alone is handed over, yet the run carries %s:\n%s", not, c)
		}
	}
}

// originURL itself: the door's repo answered whole and clean; a caller's own
// tree asked of git, with git's code and the engine's error carried.
func TestOriginURLAsksGitOnlyForACallersTree(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	r := newRun(dag.Directory(), "http://door:8215/star.git", "")
	got, code, err := r.originURL(context.Background(), r.lane("img"))
	if got != "http://door:8215/star.git" || code != 0 || err != nil {
		t.Errorf("door's tree: %q %d %v", got, code, err)
	}
	if engine.chain(sjOriginNeedle) != "" {
		t.Errorf("the door's tree must not ask git: %v", engine.chains())
	}

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.stdout(sjOriginNeedle, "git@forgejo.notusmi.com:rob/cerberus.git\n")
	r = newRun(dag.Directory(), "", "")
	got, code, err = r.originURL(context.Background(), r.lane("img"))
	if got != "git@forgejo.notusmi.com:rob/cerberus.git" || code != 0 || err != nil {
		t.Errorf("caller's tree: %q %d %v", got, code, err)
	}
	if !hasCall(engine.chain(sjOriginNeedle), "withExec", sjOriginNeedle, "expect:ANY") {
		t.Errorf("a caller's tree is asked, under ANY: %v", engine.chains())
	}

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(sjOriginNeedle, 2)
	if _, code, err = r.originURL(context.Background(), r.lane("img")); code != 2 || err != nil {
		t.Errorf("git's own exit is carried: %d %v", code, err)
	}

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(sjOriginNeedle, "engine went away")
	if _, _, err = r.originURL(context.Background(), r.lane("img")); err == nil {
		t.Errorf("the engine's error is carried")
	}
}
