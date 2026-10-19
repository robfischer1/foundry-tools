package main

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/catalogue"
	"dagger/foundry-tools/internal/checks"
)

// The four dies atoms that grade the policy run in the fleet lane with the
// pinned opa laid on PATH: the file placed is the pinned release asset.
func TestTheOpaAtomsRunInTheFleetLaneWithThePinnedOpa(t *testing.T) {
	for _, id := range []string{"dies:opa-test", "dies:admission-dogfood", "dies:data-keys", "dies:canary-visibility"} {
		t.Run(id, func(t *testing.T) {
			engine.reset()
			engine.withTree(everyLaneTree)
			name := strings.NewReplacer(":", "__").Replace(id)
			engine.contents(name+".json", `{"atom":"`+id+`","state":0,"result":"pass","logs":[]}`)
			wantState(t, runAtom(t, id, ""), 0)
			run := engine.chain(name+".json", "contents")
			if !strings.Contains(run, checks.ImageFleet) {
				t.Errorf("%s runs in the fleet lane image:\n%s", id, run)
			}
			wantCalls(t, run,
				[]string{"withFile", `path:"/usr/local/bin/opa"`, "permissions:493"},
				[]string{"withExec", `expect:ANY`, `args:["/atom","/src"]`})
			if pinnedFetch(checks.OpaURL, "id") == "" {
				t.Errorf("opa's release URL is fetched and its file is the one placed:\n%v", engine.chains())
			}
			if strings.Contains(run, "ATOM_BASE") {
				t.Errorf("%s does not judge the change and is not handed the pull's base:\n%s", id, run)
			}
		})
	}
}

// A pin with no sum is not fetched unverified: the lane gets an exec that
// cannot succeed, so every opa atom settles 2.
func TestOpaWithoutAPinnedSumFailsTheLaneClosed(t *testing.T) {
	engine.reset()
	engine.fail(`"/bin/false"`, "exit code: 1")
	c := withPinnedOpaSums(dag.Container().From("x"), map[string]string{})
	if _, err := c.Sync(t.Context()); err == nil {
		t.Error("a lane with no pinned opa sum provisioned anyway")
	}
	if engine.chain(`http(`, checks.OpaURL) != "" {
		t.Error("opa was fetched with nothing to verify it against")
	}
	if got := engine.chain(`"/bin/false"`); got == "" {
		t.Error("the closing exec never reached the engine")
	}
}

// dies:grants-stamp judges the change: it is handed the pull's base, the origin
// and the fork record, and the base is fetched.
func TestGrantsStampIsHandedThePullsBase(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.contents("dies__grants-stamp.json", `{"atom":"dies:grants-stamp","state":0,"result":"pass","logs":[]}`)
	r := newRun(dag.Directory(), "rob/nyx", "abc123")
	e, _ := catalogue.Lookup("dies:grants-stamp")
	wantState(t, runAtomOnDagger(t.Context(), r, e), 0)
	wantCalls(t, engine.chain("dies__grants-stamp.json", "contents"),
		[]string{"withEnvVariable", `name:"ATOM_BASE"`, `value:"abc123"`},
		[]string{"withEnvVariable", `name:"ATOM_ORIGIN"`, `value:"rob/nyx"`},
		[]string{"withEnvVariable", `name:"ATOM_FORK"`})
}

// dies:authz-grid asks the pull's Rego against the base's: it runs in the fleet lane
// with the pinned opa AND is handed the pull's base.
func TestAuthzGridIsHandedThePullsBaseAndRunsWithTheOpa(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.contents("dies__authz-grid.json", `{"atom":"dies:authz-grid","state":0,"result":"pass","logs":[]}`)
	r := newRun(dag.Directory(), "rob/nyx", "abc123")
	e, _ := catalogue.Lookup("dies:authz-grid")
	wantState(t, runAtomOnDagger(t.Context(), r, e), 0)
	run := engine.chain("dies__authz-grid.json", "contents")
	if !strings.Contains(run, checks.ImageFleet) {
		t.Errorf("it runs in the fleet lane image:\n%s", run)
	}
	wantCalls(t, run,
		[]string{"withFile", `path:"/usr/local/bin/opa"`, "permissions:493"},
		[]string{"withEnvVariable", `name:"ATOM_BASE"`, `value:"abc123"`},
		[]string{"withMountedCache", `path:"/decisions-cache"`},
		[]string{"withEnvVariable", `name:"AUTHZ_TABLE_CACHE"`, `value:"/decisions-cache"`})
}
