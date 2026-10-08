package atoms

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

const contractBody = "verbs = [\"a\"]\n"

func digestOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func orbitToml(contract, digest string) string {
	return "[[consumes]]\nfrom = \"chaos\"\ncontract = \"" + contract + "\"\ndigest = \"" + digest + "\"\n"
}

func TestOrbitDrift(t *testing.T) {
	const id = "fleet:orbit-drift"
	door := map[string]string{"foundry/foundry-dies orbits/chaos-x.toml": contractBody}
	for _, tc := range []struct {
		name    string
		files   map[string]string
		dead    bool
		state   int
		result  string
		needles []string
	}{
		{"no orbit.toml declares no seams", map[string]string{"a": "x"}, false, 0, absent, []string{"no orbit.toml in this tree"}},
		{"a digest that matches agrees", map[string]string{"orbit.toml": orbitToml("chaos-x", digestOf(contractBody))}, false, 0, pass,
			[]string{"1 seam(s) agree"}},
		{"a digest that moved is drift", map[string]string{"orbit.toml": orbitToml("chaos-x", "sha256:00")}, false, 1, findings,
			[]string{"hashes to " + digestOf(contractBody)}},
		{"an edge that pins nothing is a finding, not a pass", map[string]string{"orbit.toml": orbitToml("chaos-x", "")}, false, 1, findings,
			[]string{"carries no digest"}},
		{"a door nothing answers at is a 2", map[string]string{"orbit.toml": orbitToml("chaos-x", "sha256:00")}, true, 2, cannot,
			[]string{"the door is unreachable"}},
		{"an orbit.toml that will not parse is a 2", map[string]string{"orbit.toml": "= not toml"}, false, 2, cannot, []string{"did not parse"}},
		{"an orbit.toml that will not read is a 2", map[string]string{"orbit.toml/x": "x"}, false, 2, cannot, []string{"orbit.toml did not parse"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := treeIn(t, tc.files)
			in.Door = doorOf(t, door)
			if tc.dead {
				in.Door = deadDoor(t)
			}
			expect(t, runAtom(t, id, in), stateOf(tc.state), tc.result, tc.needles...)
		})
	}
	t.Run("a tree that would not enumerate", func(t *testing.T) {
		expect(t, runAtom(t, id, missingRoot(t)), stateOf(2), cannot, "the tree would not enumerate")
	})
}

const engineRef = "registry.dagger.io/engine:v0.20.0@sha256:0000000000000000000000000000000000000000000000000000000000000000"

func TestDaggerLockstep(t *testing.T) {
	const id = "fleet:dagger-lockstep"
	flux := map[string]string{"foundry/flux forge/dagger-engine-helm.yaml": "image: " + engineRef + "\n"}
	module := func(v string) map[string]string {
		return map[string]string{"dagger.json": `{"name":"x","sdk":{"source":"go"},"engineVersion":"` + v + `"}`}
	}
	for _, tc := range []struct {
		name    string
		files   map[string]string
		dead    bool
		state   int
		result  string
		needles []string
	}{
		{"a tree that pins no dagger is absent", map[string]string{"a": "x"}, false, 0, absent, []string{"nothing to hold in lockstep"}},
		{"a module at the engine's version agrees", module("v0.20.0"), false, 0, pass, []string{"1 pin(s) agree with the engine v0.20.0"}},
		{"a module newer than the engine is out of lockstep", module("v0.21.0"), false, 1, findings, []string{"is newer than the engine v0.20.0"}},
		{"an engine that could not be read is a 2", module("v0.20.0"), true, 2, cannot, []string{"the door is unreachable"}},
		{"a manifest that is not JSON is a 2", map[string]string{"dagger.json": "{"}, false, 2, cannot, []string{"dagger.json did not parse"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := treeIn(t, tc.files)
			in.Door = doorOf(t, flux)
			if tc.dead {
				in.Door = deadDoor(t)
			}
			expect(t, runAtom(t, id, in), stateOf(tc.state), tc.result, tc.needles...)
		})
	}
	t.Run("the pin files outside dagger.json are read, and the CLI follows the engine", func(t *testing.T) {
		engine := map[string]string{"forge/dagger-engine-helm.yaml": "image: " + engineRef + "\n"}
		for name, tc := range map[string]struct {
			cli    string
			state  int
			result string
		}{
			"a CLI at the engine's version agrees": {"v0.20.0", 0, pass},
			"a CLI behind the engine is a finding": {"v0.19.0", 1, findings},
		} {
			in := treeIn(t, map[string]string{
				"forge/dagger-engine-helm.yaml":     engine["forge/dagger-engine-helm.yaml"],
				"bases/layer-dagger-cli/Dockerfile": "FROM x\nARG DAGGER_VERSION=" + tc.cli + "\n",
			})
			in.Door = deadDoor(t) // the tree holds the engine itself; the door is not asked
			expect(t, runAtom(t, id, in), stateOf(tc.state), tc.result)
			_ = name
		}
	})
	t.Run("a pin file that will not read is a 2", func(t *testing.T) {
		in := treeIn(t, map[string]string{"a": "x"})
		in.Files = append(in.Files, "dagger.json") // listed, and not on disk
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "dagger.json would not read")
	})
	t.Run("a tree that would not enumerate", func(t *testing.T) {
		in := missingRoot(t)
		in.FilesErr = errBoom
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the tree would not enumerate")
	})
}

func TestNodeKindsDeclared(t *testing.T) {
	const id = "fleet:node-kinds-declared"
	const schema = "INSERT INTO {schema}.node_kinds (kind, canonical_form, note)\nSELECT k, k, 'bootstrap' FROM unnest(ARRAY[\n    'Memory'\n]) k\n"
	capture := func(kind string) map[string]string {
		return map[string]string{"internal/x/x.go": "package x\nfunc f() { _ = CreateNode(\"" + kind + "\", \"l\") }\n"}
	}
	door := map[string]string{"rob/chaos bigintschema/schema.go": schema}
	for _, tc := range []struct {
		name    string
		files   map[string]string
		dead    bool
		state   int
		result  string
		needles []string
	}{
		{"a tree that captures nothing never asks the door", map[string]string{"internal/x/x.go": "package x\n"}, true, 0, pass, []string{"no node kind is captured"}},
		{"a declared kind passes", capture("Memory"), false, 0, pass, []string{"1 captured kind(s) are all declared"}},
		{"an undeclared kind is a finding", capture("ChronicleChapterX"), false, 1, findings, []string{"'ChronicleChapterX' is captured"}},
		{"a vocabulary that was not read is a 2, never a finding", capture("ChronicleChapterX"), true, 2, cannot,
			[]string{"A vocabulary that was not read declares nothing"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := treeIn(t, tc.files)
			in.Door = doorOf(t, door)
			if tc.dead {
				in.Door = deadDoor(t)
			}
			expect(t, runAtom(t, id, in), stateOf(tc.state), tc.result, tc.needles...)
		})
	}
	t.Run("a pass names the non-Go files it did not read, a finding does not", func(t *testing.T) {
		py := map[string]string{"client/chaos.py": "graph_capture(kind)\n"}
		in := treeIn(t, py)
		in.Door = deadDoor(t)
		expect(t, runAtom(t, id, in), stateOf(0), pass, "NOT JUDGED: this check reads Go only, and 1 non-Go file(s) look like captures (client/chaos.py)")
		files := capture("ChronicleChapterX")
		files["client/chaos.py"] = py["client/chaos.py"]
		in = treeIn(t, files)
		in.Door = doorOf(t, door)
		if v := runAtom(t, id, in); v.State != 1 || strings.Contains(v.Reason+strings.Join(v.Logs, "\n"), "NOT JUDGED") {
			t.Errorf("a finding carries no note: state %d\n%s", v.State, v.Reason)
		}
	})
	t.Run("a source file that will not read is a 2", func(t *testing.T) {
		in := treeIn(t, map[string]string{"a": "x"})
		in.Files = append(in.Files, "gone.go")
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "gone.go (", "would not read")
	})
	t.Run("a tree that would not enumerate", func(t *testing.T) {
		in := missingRoot(t)
		in.FilesErr = errBoom
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the tree would not enumerate")
	})
}

func TestConsumedEventsEmitted(t *testing.T) {
	const id = "fleet:consumed-events-emitted"
	consumer := map[string]string{"c.py": "if ev[\"event_type\"] == \"gone_type\":\n    pass\n"}
	for _, tc := range []struct {
		name    string
		files   map[string]string
		state   int
		result  string
		needles []string
	}{
		{"a tree that consumes no literal event_type has no question", map[string]string{"c.py": "x = 1"}, 0, pass, []string{"consumes no literal event_type"}},
		{"a type this tree emits itself needs no door",
			map[string]string{"c.py": "if ev[\"event_type\"] == \"t\":\n    pass\nemit({\"event_type\": \"t\"})\n"}, 0, pass, []string{"all emitted by this tree"}},
		{"a fleet no door answers for is a 2, not a guess", consumer, 2, cannot, []string{"CANNOT RUN"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := treeIn(t, tc.files)
			in.Door = deadDoor(t)
			expect(t, runAtom(t, id, in), stateOf(tc.state), tc.result, tc.needles...)
		})
	}
	t.Run("a source file that will not read is a 2", func(t *testing.T) {
		in := treeIn(t, map[string]string{"a": "x"})
		in.Files = append(in.Files, "gone.py")
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "gone.py (", "would not read")
	})
	t.Run("a tree that would not enumerate", func(t *testing.T) {
		in := missingRoot(t)
		in.FilesErr = errBoom
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the tree would not enumerate")
	})
}
