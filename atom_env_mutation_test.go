package main

import (
	"context"
	"strings"
	"testing"
)

// THE ENVIRONMENT OF THE MUTATION ATOMS, against the paper engine: which
// container each runs in, what the run hands it (the base, the commit, the
// lookup credential, a nonce) and what it does not. What each atom asks of the
// tree is tested beside the atom (atoms/<lang>/mutation).

const (
	pyMutationNeedle = "python__mutation.json"
	tsMutationNeedle = "ts__mutation.json"
)

func mutationVerdict(id, lane string) string {
	return `{"atom":"` + id + `","stage":"mutation","lane":"` + lane + `","state":0,"result":"pass","logs":[]}`
}

// Every mutation atom judges the change: it is handed the base and the origin, and
// the commit, which the audit sample and the over-budget key are read from.
func TestTheMutationAtomsAreHandedTheBaseAndTheCommit(t *testing.T) {
	for _, tc := range []struct{ id, lane, needle string }{
		{"go:mutation", "go", goMutationNeedle}, {"rust:mutation", "rust", rustMutationNeedle},
		{"python:mutation", "python", pyMutationNeedle}, {"ts:mutation", "ts", tsMutationNeedle},
	} {
		engine.reset()
		engine.withTree(everyLaneTree)
		engine.contents(tc.needle, mutationVerdict(tc.id, tc.lane))
		r := newRun(dag.Directory(), "", "abc123").withArtifacts(nil, "cafe")
		if v, err := verdictFor(context.Background(), r, tc.id); err != nil || v.Atom != tc.id {
			t.Fatalf("%s: %+v %v", tc.id, v, err)
		}
		c := engine.chain(tc.needle, "contents")
		wantCalls(t, c,
			[]string{"withEnvVariable", `name:"ATOM_BASE"`, `value:"abc123"`},
			[]string{"withEnvVariable", `name:"ATOM_SHA"`, `value:"cafe"`},
			[]string{"withExec", "expect:ANY", `args:["/atom","/src"]`},
		)
		if hasCall(c, "withSecretVariable", `name:"ATOM_MUTANTS_TOKEN"`) {
			t.Errorf("%s: a run with no --reuse carries the credential", tc.id)
		}
	}
}

// The record token is the credential for the lookup, handed as a secret and only
// to the mutation atoms. It is never in the cache key.
func TestTheLookupCredentialReachesOnlyTheMutationAtoms(t *testing.T) {
	for _, tc := range []struct {
		id, lane, needle string
		mutation         bool
	}{
		{"go:mutation", "go", goMutationNeedle, true}, {"python:mutation", "python", pyMutationNeedle, true},
		{"go:test", "go", goTestNeedle, false},
	} {
		engine.reset()
		engine.withTree(everyLaneTree)
		engine.contents(tc.needle, mutationVerdict(tc.id, tc.lane))
		r := newRun(dag.Directory(), "", "abc123").withMutants(dag.SetSecret("record-token", "tok"))
		if _, err := verdictFor(context.Background(), r, tc.id); err != nil {
			t.Fatal(err)
		}
		if got := hasCall(engine.chain(tc.needle, "contents"), "withSecretVariable", `name:"ATOM_MUTANTS_TOKEN"`); got != tc.mutation {
			t.Errorf("%s: credential handed = %v", tc.id, got)
		}
	}
}

// GateFile arms the credential only under --reuse with a token to ask with.
func TestGateFileArmsTheLookupUnderReuse(t *testing.T) {
	for _, c := range []struct{ reuse, token, armed bool }{{false, false, false}, {true, false, false}, {false, true, false}, {true, true, true}} {
		m := gateOn(t, cleanVector)
		tok := dag.SetSecret("record-token", "tok")
		if !c.token {
			tok = nil
		}
		if _, err := m.GateFile(context.Background(), fakeTree, gatePin, "base-sha", "mutation", tok, c.reuse, nil, nil, "", nil, ""); err != nil {
			t.Fatal(err)
		}
		if (m.mutantsToken != nil) != c.armed {
			t.Errorf("reuse=%v token=%v: armed %v", c.reuse, c.token, m.mutantsToken != nil)
		}
	}
}

// The go lane's mutation atom runs beside go:test-race and binds servers of its own, under the
// lane's alias, and is keyed afresh on every ask (it re-asks its own modules, so
// the whole-atom re-ask's nonce is its).
func TestGoMutationBindsItsOwnServersAndIsKeyedAfresh(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{".copier-answers.yml": "service_name: x\n"})
	engine.stdout(goTagsNeedle, "//go:build live_db\n")
	engine.contents(goMutationNeedle, mutationVerdict("go:mutation", "go"))
	runAtom(t, "go:mutation", "abc123")
	c := engine.chain(goMutationNeedle, "contents")
	wantCalls(t, c,
		[]string{"withServiceBinding", `alias:"db-mutation"`},
		[]string{"withEnvVariable", `name:"ATOM_ASK"`},
		[]string{"withMountedDirectory", `path:"/dies"`},
	)
	if hasCall(c, "withServiceBinding", `alias:"db"`) || strings.Contains(c, "gravity") {
		t.Errorf("the mutation lane binds the shared server or stages a generator:\n%s", c)
	}
	// the nonce differs ask to ask
	a := engine.chain(goMutationNeedle, "contents")
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.contents(goMutationNeedle, mutationVerdict("go:mutation", "go"))
	runAtom(t, "go:mutation", "abc123")
	if b := engine.chain(goMutationNeedle, "contents"); a == b {
		t.Error("two asks of go:mutation are one exec")
	}
}

// rust:mutation builds in target dirs of its own: the shared target's variable
// and mount are gone, the stale-lock sweep runs, and the atom is not keyed afresh.
func TestRustMutationRunsWithoutTheSharedTarget(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.contents(rustMutationNeedle, mutationVerdict("rust:mutation", "rust"))
	runAtom(t, "rust:mutation", "abc123")
	c := engine.chain(rustMutationNeedle, "contents")
	wantCalls(t, c,
		[]string{"withoutEnvVariable", `name:"CARGO_TARGET_DIR"`},
		[]string{"withoutMount", `path:"/cache/cargo-target"`},
		[]string{"withExec", `"flock"`},
	)
	if hasCall(c, "withEnvVariable", `name:"ATOM_ASK"`) {
		t.Error("only go:mutation is keyed afresh")
	}
}
