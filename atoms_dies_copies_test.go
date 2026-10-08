package main

import (
	"io"
	"net/http"
	"reflect"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// copiesManifest declares one vendored copy in star-a, authority in dies.
const copiesManifest = `[contracts.a]
authority = "dies/a.json"

[[contracts.a.copies]]
name = "dies/a.json"
source = { local = "schema/a.json" }
extract = { kind = "json-enum", pointer = "e" }
digest = true

[[contracts.a.copies]]
name = "star-a/a.json"
source = { repo = "rob/star-a", path = "vendor/a.json" }
extract = { kind = "json-enum", pointer = "e" }
digest = true
`

// starTree is a star that vendors the copy: neither of dies' two markers.
func starTree(add map[string]string) map[string]string {
	tree := map[string]string{"vendor/a.json": "{}"}
	for k, v := range add {
		tree[k] = v
	}
	return diesTree(tree, "policy", "fleet")
}

func copiesDoor(t *testing.T) *[]doorAsk {
	return fakeDoor(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("path") == checks.DoorManifest {
			_, _ = io.WriteString(w, copiesManifest)
			return
		}
		_, _ = io.WriteString(w, "# "+r.URL.Query().Get("path")+"\n")
	})
}

// A STAR GRADES THE COPY IT HOLDS (foundry-tools#15237): dies' checker, its
// import and the manifest come off the door, and the run is scoped to this tree.
func TestDiesContractCopiesGradesTheCopiesATreeHoldsScopedToIt(t *testing.T) {
	engine.reset()
	engine.withTree(starTree(nil))
	asks := copiesDoor(t)
	wantState(t, runAtom(t, "dies:contract-copies", ""), 0)

	var got []string
	for _, a := range *asks {
		if a.Repo == "foundry/foundry-dies" {
			got = append(got, a.Path)
		}
	}
	want := []string{"tools/check_contracts.py", "tools/schema_stamp.py", "contracts/contracts.toml"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fetched from dies: got %v want %v", got, want)
	}
	c := engine.chain("dies-contracts/tools/check_contracts.py", "exitCode")
	for _, p := range want {
		if !hasCall(c, "withNewFile", "/tmp/dies-contracts/"+p) {
			t.Errorf("%s was not placed in the container:\n%s", p, c)
		}
	}
	if !hasCall(c, "withExec", "expect:ANY", `"--manifest","/tmp/dies-contracts/contracts/contracts.toml"`, `"--tree","star-a=."`) {
		t.Errorf("the checker did not run scoped to this tree:\n%s", c)
	}
}

// The checker's ladder is the verdict: a drifted copy (1) is red in the star.
func TestDiesContractCopiesPassesTheExitCodeStraightThrough(t *testing.T) {
	for code, want := range map[int]int{0: 0, 1: 1, 2: 2} {
		engine.reset()
		engine.withTree(starTree(nil))
		copiesDoor(t)
		engine.exitCode(`"python3"`, code)
		if got := runAtom(t, "dies:contract-copies", "").State; got != want {
			t.Errorf("exit %d answered state %d, want %d", code, got, want)
		}
	}
}

// A tree that holds no declared copy, and foundry-dies itself, are ABSENT and
// run no checker.
func TestDiesContractCopiesIsAbsentWhereThereIsNothingToGrade(t *testing.T) {
	engine.reset()
	engine.withTree(diesTree(nil, "policy", "fleet"))
	copiesDoor(t)
	wantState(t, runAtom(t, "dies:contract-copies", ""), 0, "ABSENT", "holds none of the copies")

	engine.reset()
	engine.withTree(diesTree(nil))
	copiesDoor(t)
	wantState(t, runAtom(t, "dies:contract-copies", ""), 0, "ABSENT", "dies:contracts")
}

// A door that does not answer for the manifest is a COULD-NOT-RUN: the tree's
// copies cannot be named, which is not the same as there being none.
func TestDiesContractCopiesCannotRunWithoutTheDoor(t *testing.T) {
	engine.reset()
	engine.withTree(starTree(nil))
	deadDoor(t)
	wantState(t, runAtom(t, "dies:contract-copies", ""), 2, "tools/check_contracts.py", "unreachable")

	engine.reset()
	engine.withTree(starTree(nil))
	fakeDoor(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("path") == checks.DoorManifest {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, "x")
	})
	wantState(t, runAtom(t, "dies:contract-copies", ""), 2, "HTTP 404", "contracts/contracts.toml")
}

// A tree whose shape could not be read is not an answer about its copies.
func TestDiesContractCopiesRefusesWhenTheTreeCannotBeRead(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail("{directory{entries}}", "the directory would not evaluate")
	copiesDoor(t)
	wantState(t, runAtom(t, "dies:contract-copies", ""), 2, "the repository root could not be read")
}

// A lookup that fails is not a tree with no copies.
func TestDiesContractCopiesCannotRunWhenTheTreeCannotBeScanned(t *testing.T) {
	engine.reset()
	engine.withTree(starTree(nil))
	copiesDoor(t)
	engine.failLeaf(`pattern:"vendor/a.json"`, "glob", "engine went away")
	wantState(t, runAtom(t, "dies:contract-copies", ""), 2, "CANNOT RUN", "engine went away")
}

// The manifest was served but the first remote copy it names does not answer:
// the authority cannot be read, so the copy was not compared.
func TestDiesContractCopiesProbesTheManifestsFirstRemoteCopy(t *testing.T) {
	engine.reset()
	engine.withTree(starTree(nil))
	asks := copiesDoor(t)
	wantState(t, runAtom(t, "dies:contract-copies", ""), 0)
	if want := (doorAsk{"rob/star-a", "vendor/a.json"}); (*asks)[len(*asks)-1] != want {
		t.Errorf("the manifest's first remote copy was not probed last: %v", *asks)
	}

	engine.reset()
	engine.withTree(starTree(nil))
	fakeDoor(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("path") {
		case "vendor/a.json":
			if c, _, err := w.(http.Hijacker).Hijack(); err == nil {
				_ = c.Close()
			}
		case checks.DoorManifest:
			_, _ = io.WriteString(w, copiesManifest)
		default:
			_, _ = io.WriteString(w, "x")
		}
	})
	wantState(t, runAtom(t, "dies:contract-copies", ""), 2, "vendor/a.json", "unreachable")
}
