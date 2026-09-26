package main

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

// THE GENERATED FILE IS THE WIRE, AND IT GOES STALE SILENTLY.
//
// dagger.gen.go is written by `dagger develop`. It holds two things nothing in the
// hand-written source can substitute for: a per-type MarshalJSON that names every
// field allowed to cross a hop, and an invoke switch that names every function the
// CLI can call. Both are keyed on names that were true when the file was generated.
//
// Add a field and forget to regenerate and the field compiles, is assigned, crosses
// every hop in the module and is DROPPED at the wire — which is how `Findings`
// reached the door empty while go build, go vet, staticcheck, the whole suite,
// `dagger call check` and a green gate all agreed with the bug. Add a method and
// forget, and `dagger call <stage> record-file` answers "unknown function
// RecordFile": the door mounts a volume, collects nothing, reaps it, and settles
// could-not-run on a run that graded everything.
//
// So the regeneration is what is asserted here, not the code that depends on it.
// A test written against the struct cannot see this class of bug, because the
// struct is the half that is always right.

// theTypesThatTravel are the module objects whose fields cross a hop. A field of
// one of these that the generated marshaller does not name is a field the door
// never receives.
func theTypesThatTravel() []any { return []any{StageResult{}, AtomResult{}} }

// theFunctionsTheDoorCalls are the invoke names ourea's gate job asks for by
// string. Each must be a case in the generated switch under its own type.
var theFunctionsTheDoorCalls = map[string][]string{
	"StageResult": {"Record", "RecordFile", "Exit"},
}

func TestTheGeneratedMarshallerNamesEveryFieldThatTravels(t *testing.T) {
	gen := readGen(t)
	for _, v := range theTypesThatTravel() {
		rt := reflect.TypeOf(v)
		body := genBlock(gen, fmt.Sprintf("func (r %s) MarshalJSON()", rt.Name()))
		if body == "" {
			t.Errorf("%s has no generated MarshalJSON — run `dagger develop`", rt.Name())
			continue
		}
		for i := range rt.NumField() {
			f := rt.Field(i)
			if !f.IsExported() {
				continue
			}
			// The assignment, not the bare name: `concrete.X = r.X` is the line
			// that puts the field on the wire, and a name appearing anywhere else
			// in the block would pass a substring check while dropping the value.
			want := fmt.Sprintf("concrete.%s = r.%s\n", f.Name, f.Name)
			if !strings.Contains(body, want) {
				t.Errorf("%s.%s is not marshalled — dagger.gen.go is stale, run `dagger develop`",
					rt.Name(), f.Name)
			}
		}
	}
}

func TestTheGeneratedSwitchKnowsEveryFunctionTheDoorCalls(t *testing.T) {
	gen := readGen(t)
	for typeName, fns := range theFunctionsTheDoorCalls {
		body := genBlock(gen, fmt.Sprintf("\tcase %q:\n", typeName))
		if body == "" {
			t.Errorf("the invoke switch has no case for %s — run `dagger develop`", typeName)
			continue
		}
		for _, fn := range fns {
			if !strings.Contains(body, fmt.Sprintf("case %q:\n", fn)) {
				t.Errorf("%s.%s is not invokable — `dagger call` answers "+
					"\"unknown function %s\"; run `dagger develop`", typeName, fn, fn)
			}
		}
	}
}

// AND THE OTHER DIRECTION: a method added to a travelling type that is not in the
// door's list either needs regenerating or needs adding here. Without this, a
// method could be written, generated and wired while nothing states who calls it.
func TestEveryInvokableStageFunctionIsOneTheDoorNames(t *testing.T) {
	rt := reflect.TypeOf(&StageResult{})
	named := map[string]bool{}
	for _, fn := range theFunctionsTheDoorCalls["StageResult"] {
		named[fn] = true
	}
	for i := range rt.NumMethod() {
		m := rt.Method(i)
		// The JSON pair is the marshaller itself, not a dagger function.
		if m.Name == "MarshalJSON" || m.Name == "UnmarshalJSON" {
			continue
		}
		if !named[m.Name] {
			t.Errorf("StageResult.%s is exported but no caller is named for it — "+
				"add it to theFunctionsTheDoorCalls or unexport it", m.Name)
		}
	}
}

func readGen(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("dagger.gen.go")
	if err != nil {
		t.Fatalf("the generated file could not be read: %v", err)
	}
	return string(b)
}

// genBlock returns the text from head to the first line that is flush with head's
// own indentation and closes it — enough to read one marshaller or one type's arm
// of the switch without pulling in the next type's.
func genBlock(gen, head string) string {
	i := strings.Index(gen, head)
	if i < 0 {
		return ""
	}
	rest := gen[i+len(head):]
	if end := strings.Index(rest, "\n\tcase \""); end >= 0 {
		rest = rest[:end]
	}
	if end := strings.Index(rest, "\n}\n"); end >= 0 {
		rest = rest[:end]
	}
	return rest
}
