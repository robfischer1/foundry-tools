package main

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/catalogue"
	"dagger/foundry-tools/internal/checks"
)

// THE ENVIRONMENTS OF THE FLEET ATOMS, against the paper engine: which container
// each runs in, what it is handed and what its tool is. What an atom decides is
// tested beside its file.

// runFleetAtom runs a ported atom on the adapter over a tree, with the verdict
// file the atom would write scripted as a pass, and answers the chain of the exec
// that ran the atom.
func runFleetAtom(t *testing.T, r *run, id string) (checks.Verdict, string) {
	t.Helper()
	e, ok := catalogue.Lookup(id)
	if !ok {
		t.Fatalf("%s is not in the catalogue", id)
	}
	engine.contents(atomkit.EscapeID(id)+".json", `{"atom":"`+id+`","state":0,"result":"pass","logs":[]}`)
	v := runAtomOnDagger(t.Context(), r, e)
	return v, engine.chain(atomkit.EscapeID(id)+".json", "contents")
}

// fleet:wit-topics' toolchain is python, and the python image is the fleet lane's
// (checks.ImageFleet): its checker runs under uv, as its chain ran, with no
// environment of its own.
func TestWitTopicsRunsInTheFleetLane(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	v, c := runFleetAtom(t, newRun(dag.Directory(), "", ""), "fleet:wit-topics")
	wantState(t, v, 0)
	if !strings.Contains(c, checks.ImageFleet) || checks.ImageFleet != checks.ImagePython {
		t.Errorf("fleet:wit-topics must run in the fleet lane image:\n%s", c)
	}
	if _, ok := atomEnvironments["fleet:wit-topics"]; ok {
		t.Error("the lane of its toolchain is the lane its chain ran in; it needs no environment of its own")
	}
}

// fleet:renovate-config runs in the Renovate image and leaves the repository
// alone: that image's user cannot make one in /src, and the atom asks git nothing.
func TestRenovateConfigRunsInTheRenovateImageWithoutARepository(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	v, c := runFleetAtom(t, newRun(dag.Directory(), "", ""), "fleet:renovate-config")
	wantState(t, v, 0)
	if !strings.Contains(c, checks.ImageRenovate) {
		t.Errorf("fleet:renovate-config must run in the Renovate image:\n%s", c)
	}
	wantCalls(t, c, []string{"withUser", `name:"root"`})
	if strings.Contains(c, `"git","init"`) || strings.Contains(c, `"git","config"`) {
		t.Errorf("the atom asks git nothing, yet the environment readied a repository:\n%s", c)
	}
}

// fleet:hadolint's tool is fetched at its pin, mirror first, and placed in the
// fleet lane: the lane images carry no hadolint.
func TestHadolintRunsWithThePinnedBinaryInTheFleetLane(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	v, c := runFleetAtom(t, newRun(dag.Directory(), "", ""), "fleet:hadolint")
	wantState(t, v, 0)
	if !strings.Contains(c, checks.ImageFleet) {
		t.Errorf("fleet:hadolint must run in the fleet lane image:\n%s", c)
	}
	wantCalls(t, c, []string{"withFile", `path:"/usr/local/bin/hadolint"`, `permissions:493`})
	if pinnedFetch(checks.HadolintURL, "id") == "" {
		t.Errorf("the release URL's file is placed from the mirror at its pin:\n%v", engine.chains())
	}
}

// A hadolint that could not be fetched is the atom's could-not-run, said in the
// atom's words, and no second address is tried.
func TestHadolintCouldNotBeFetchedIsACouldNotRun(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	failBoth(checks.HadolintURL, "404")
	e, _ := catalogue.Lookup("fleet:hadolint")
	wantState(t, runAtomOnDagger(t.Context(), newRun(dag.Directory(), "", ""), e), 2,
		"fleet:hadolint: CANNOT RUN", "could not be fetched", checks.HadolintVersion, "A Dockerfile that was never linted is not a Dockerfile that passed.")
	if strings.Contains(strings.Join(engine.chains(), "\n"), "nexus.notusmi.com") {
		t.Errorf("no fleet address is tried:\n%v", engine.chains())
	}
}

// fleet:witness asks narcissus as the run's SVID: the lane carries the helper,
// the base is fetched and handed over with the origin and the fork record, and
// the run is keyed so no earlier answer stands in for a later ask.
func TestWitnessRunsWithTheHelperTheSocketAndTheChangesFacts(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	r := newRun(dag.Directory(), "http://door:8215/infra.git", "abc123").withSpire(dag.LoadSocketFromID("spire-agent-socket"))
	v, c := runFleetAtom(t, r, "fleet:witness")
	wantState(t, v, 0)
	wantCalls(t, c,
		[]string{"withFile", `path:"/usr/local/bin/witnesscall"`, `permissions:493`},
		[]string{"withEnvVariable", `name:"WITNESS_RUN"`},
		[]string{"withUnixSocket", `path:"` + atomsSpirePath + `"`},
		[]string{"withEnvVariable", `name:"SPIFFE_ENDPOINT_SOCKET"`},
		[]string{"withEnvVariable", `name:"ATOM_BASE"`, `value:"abc123"`},
		[]string{"withEnvVariable", `name:"ATOM_ORIGIN"`, `value:"http://door:8215/infra.git"`},
		[]string{"withEnvVariable", `name:"ATOM_FORK"`},
		[]string{"withExec", `"git","-c","safe.directory=*","-C","/src","fetch"`},
	)
	// two runs are two keys
	engine.reset()
	engine.withTree(everyLaneTree)
	_, again := runFleetAtom(t, r, "fleet:witness")
	if key := func(c string) string {
		i := strings.Index(c, `name:"WITNESS_RUN"`)
		if i < 0 {
			return ""
		}
		return c[i : i+60]
	}; key(c) == "" || key(c) == key(again) {
		t.Errorf("the witness's run key did not change between runs: %q %q", key(c), key(again))
	}
}

// fleet:opengrep-sast is handed the fork record and nothing else of the change's
// facts: a fork with no ruleset of its own is scanned with the built-in ones.
func TestOpengrepIsHandedTheForkRecordAndNoBase(t *testing.T) {
	engine.reset()
	engine.withTree(forkTree)
	engine.withTree(map[string]string{"/dies/fleet/repos/gravity.json": `{"name":"gravity","kind":"vendored","repo":"forks/gravity"}`})
	v, c := runFleetAtom(t, newRun(dag.Directory(), forkDoor, ""), "fleet:opengrep-sast")
	wantState(t, v, 0)
	wantCalls(t, c, []string{"withEnvVariable", `name:"ATOM_FORK"`, `value:"gravity"`})
	for _, not := range []string{"ATOM_BASE", "ATOM_ORIGIN", "GATE_BASE"} {
		if strings.Contains(c, not) {
			t.Errorf("the fork record alone is handed over, yet the run carries %s:\n%s", not, c)
		}
	}
}
