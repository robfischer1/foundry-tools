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

// The fleet atoms that exec a program or the tree's python run in the tools
// container the atoms binary votes in: it carries opengrep, hadolint, the
// witness helper and the python venv already, so nothing is fetched per run and
// the adapter is only their re-ask.
func TestTheFleetAtomsThatExecAProgramRunInTheToolsContainer(t *testing.T) {
	for _, id := range []string{"fleet:opengrep-sast", "fleet:hadolint", "fleet:wit-topics", "fleet:witness"} {
		if !atomEnvironments[id].tools {
			t.Errorf("%s does not run in the tools container", id)
		}
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
