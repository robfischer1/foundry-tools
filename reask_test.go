package main

import (
	"strings"
	"testing"
)

// A COULD-NOT-RUN IS ASKED AGAIN PAST THE CACHE, and the second answer is the
// one reported. The re-ask carries CA_REASK on its lane, so every exec after
// it is keyed afresh; the first ask never does.
func TestACouldNotRunIsAskedAgainPastTheCache(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(`"go","vet"`, 2)
	engine.exitCode(`name:"CA_REASK"`, 0)
	v, err := verdictFor(t.Context(), newRun(dag.Directory(), "", ""), "go:vet")
	if err != nil {
		t.Fatal(err)
	}
	if v.State != 0 || strings.Contains(v.Reason, "asked twice") {
		t.Errorf("a could-not-run that looks clean the second time is clean: %+v", v)
	}
	fresh, reasked := 0, 0
	for _, c := range engine.chains() {
		if !strings.Contains(c, `"go","vet"`) {
			continue
		}
		if strings.Contains(c, `name:"CA_REASK"`) {
			reasked++
		} else {
			fresh++
		}
	}
	if fresh == 0 || reasked == 0 {
		t.Errorf("want the first ask without CA_REASK and the re-ask with it: %d fresh, %d reasked", fresh, reasked)
	}

	// Could not run both times: still a could-not-run, and it says it looked twice.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(`"go","vet"`, 2)
	v, err = verdictFor(t.Context(), newRun(dag.Directory(), "", ""), "go:vet")
	if err != nil {
		t.Fatal(err)
	}
	if v.State != 2 || !strings.Contains(v.Reason, "asked twice, the second time past the engine's cache") {
		t.Errorf("twice could-not-run is could-not-run, named as asked twice: %+v", v)
	}
}

// A pass or a finding is the tool's answer and is never asked again.
func TestOnlyACouldNotRunIsAskedAgain(t *testing.T) {
	for _, code := range []int{0, 1} {
		engine.reset()
		engine.withTree(everyLaneTree)
		engine.exitCode(`"go","vet"`, code)
		v, err := verdictFor(t.Context(), newRun(dag.Directory(), "", ""), "go:vet")
		if err != nil {
			t.Fatal(err)
		}
		if v.State != code {
			t.Errorf("exit %d: state %d", code, v.State)
		}
		if engine.chain(`name:"CA_REASK"`) != "" {
			t.Errorf("exit %d was asked again", code)
		}
	}
}
