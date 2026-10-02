package main

import (
	"context"
	"testing"
)

// ON THE DOOR'S TREE, ORIGIN IS THE REPO IT WAS FETCHED FROM, WITHOUT ASKING.
// sourceAt sets it; stop-justifications names the repository by it (the
// exemption keyed on cerberus applies) and no `git remote get-url` runs.
func TestStopJustificationsReadsTheDoorsOriginWithoutAnExec(t *testing.T) {
	drive := map[string]string{"probes/drive.py": "p = Popen([x])  # no" + "qa: S603\n"}
	sjRepo(drive)
	// What the exec would have said, were it asked: a different repository,
	// so a run that asked anyway would lose the exemption and fail below.
	engine.stdout(sjOriginNeedle, "http://ourea.default.svc.cluster.local:8215/x.git\n")
	v := registry["fleet:stop-justifications"](context.Background(), newRun(dag.Directory(), "http://door:8215/cerberus.git", ""))
	wantState(t, v, 0)
	if engine.chain(sjOriginNeedle) != "" {
		t.Errorf("the door's tree names its origin; nothing should have asked git: %v", engine.chains())
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
