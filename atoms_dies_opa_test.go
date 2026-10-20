package main

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// scriptOpa scripts the pinned binary answering for itself. The version is CHECKED
// rather than assumed.
func scriptOpa() {
	engine.stdout(`"opa","version"`, "Version: "+checks.OpaVersion+"\nBuild Commit: c0ffee\nGo Version: go1.24\n")
}

// THE PIN IS PART OF THE QUESTION: rego's semantics are a property of the
// binary, so a suite graded by another major answers a different question. The
// bundle lane provisions it here; the dies atoms that run opa get theirs from
// their lane's environment (atom_env_dies.go).
func TestTheOpaClientIsPinnedFetchedAndProbed(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	scriptOpa()
	ctr, err := newRun(dag.Directory(), "", "").opaClient(t.Context())
	if err != nil || ctr == nil {
		t.Fatalf("%v", err)
	}
	c := engine.chain(`"opa","version"`, "exitCode")
	if !strings.Contains(c, checks.ImageFleet) {
		t.Errorf("the opa client runs in the fleet image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withFile", `path:"/usr/local/bin/opa"`, "permissions:493"},
		[]string{"withMountedDirectory", `path:"/src"`},
		[]string{"withWorkdir", `path:"/src"`},
		[]string{"withExec", "expect:ANY", `args:["opa","version"]`},
	)
	if pinnedFetch(checks.OpaURL, "id") == "" {
		t.Errorf("opa's release URL is fetched and its file is the one placed:\n%v", engine.chains())
	}
}

func TestTheOpaClientRefusesEveryProvisioningFailure(t *testing.T) {
	for _, tc := range []struct {
		name    string
		script  func()
		needles []string
	}{
		{"the release URL has no such asset", func() { failTool(checks.OpaURL, "404") },
			[]string{"could not be fetched", checks.OpaVersion}},
		{"the probe never ran", func() { engine.fail(`"opa","version"`, "engine went away") },
			[]string{"the opa version probe never ran", "engine went away"}},
		{"on disk but will not run", func() { engine.exitCode(`"opa","version"`, 126) },
			[]string{"opa is on disk but does not run (exit 126)"}},
		{"the wrong major answers", func() { engine.stdout(`"opa","version"`, "Version: 0.60.0\n") },
			[]string{"the rego semantics are not the pinned ones", "0.60.0"}},
		{"a prerelease is not the pin", func() { engine.stdout(`"opa","version"`, "Version: "+checks.OpaVersion+"-rc1\n") },
			[]string{"the rego semantics are not the pinned ones"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine.reset()
			engine.withTree(everyLaneTree)
			scriptOpa()
			tc.script()
			_, err := newRun(dag.Directory(), "", "").opaClient(t.Context())
			if err == nil {
				t.Fatal("a failed provisioning produced a client")
			}
			for _, n := range tc.needles {
				if !strings.Contains(err.Error(), n) {
					t.Errorf("the refusal lacks %q: %v", n, err)
				}
			}
		})
	}
}
