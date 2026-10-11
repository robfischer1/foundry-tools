package atoms

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// starData is a roster shard: a star and its verb prefix.
func starData(prefix string) string { return `{"verb_prefix":"` + prefix + `"}` }

// contract is the smallest contract orbitcompose parses: it names its producer
// and consumer by its file name, and a draft one is not yet approved.
const contract = "version = \"v1\"\nstatus = \"draft\"\nverbs = [\"a_verb\"]\n"

// diesAt is a foundry-dies checkout holding a roster, one contract between its
// two stars, and the named files. (A directory of orbits that holds no contract
// does not compose: that is a 2, and the atoms say so.)
func diesAt(t *testing.T, extra map[string]string) string {
	t.Helper()
	files := map[string]string{
		"fleet/stars/a/data.json": starData("a"), "fleet/stars/b/data.json": starData("b"), "orbits/a-b.toml": contract,
	}
	for k, v := range extra {
		files[k] = v
	}
	return treeIn(t, files).Root
}

func TestOrbitContracts(t *testing.T) {
	const id = "orbit:contracts"
	own := map[string]string{"orbits/bad.toml": "= not toml", "fleet/stars/a/data.json": starData("a")}
	t.Run("a tree that is not the contracts' repository is absent", func(t *testing.T) {
		expect(t, runAtom(t, id, treeIn(t, map[string]string{"a": "x"})), stateOf(0), absent, "not the contracts' repository")
	})
	t.Run("contracts without a roster beside them are absent too", func(t *testing.T) {
		expect(t, runAtom(t, id, treeIn(t, map[string]string{"orbits/a-b.toml": "x"})), stateOf(0), absent, "not the contracts' repository")
	})
	t.Run("a directory of notes is not a contract", func(t *testing.T) {
		notes := map[string]string{"orbits/README.md": "x", "fleet/stars/a/data.json": starData("a")}
		expect(t, runAtom(t, id, treeIn(t, notes)), stateOf(0), absent, "not the contracts' repository")
	})
	t.Run("foundry-dies main not supplied is a 2", func(t *testing.T) {
		expect(t, runAtom(t, id, treeIn(t, own)), stateOf(2), cannot, "foundry-dies main: ", "(-dies)")
	})
	t.Run("a contract of this tree that will not read is a 2", func(t *testing.T) {
		in := treeIn(t, own)
		brokenLink(t, in.Root, "orbits/z.toml")
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "orbits/* could not be read")
	})
	t.Run("a roster shard that will not read is a 2", func(t *testing.T) {
		in := treeIn(t, own)
		brokenLink(t, in.Root, "fleet/stars/z/data.json")
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "fleet/stars/z/data.json could not be read")
	})
	t.Run("foundry-dies' contracts that will not read are a 2", func(t *testing.T) {
		in := treeIn(t, own)
		in.Dies = diesAt(t, nil)
		brokenLink(t, in.Dies, "orbits/z.toml")
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "foundry-dies main: orbits/*.toml could not be read")
	})
	t.Run("a contract that does not parse is recorded, report-only, and the atom stays 0", func(t *testing.T) {
		in := treeIn(t, own)
		in.Dies = diesAt(t, nil)
		v := runAtom(t, id, in)
		expect(t, v, stateOf(0), pass, "orbit:contracts: ")
		if len(v.Findings) != 1 || v.Findings[0].Verdict != checks.VerdictDrifted || v.Findings[0].Probe != id {
			t.Errorf("findings %+v: want one drifted finding for the bad contract (report-only until Enforce flips)", v.Findings)
		}
	})
	t.Run("a directory named like a contract in the base is named, not read", func(t *testing.T) {
		in := treeIn(t, map[string]string{"orbits/a.toml": "x", "fleet/stars/a/data.json": starData("a")})
		in.Dies = diesAt(t, map[string]string{"orbits/x.toml/inner": "x"}) // a directory named like a contract
		v := runAtom(t, id, in)
		if v.State != 0 {
			t.Errorf("a directory in the base is named, not read: state %d\n%s", v.State, v.Reason)
		}
	})
}

func TestOrbitRepo(t *testing.T) {
	const id = "orbit:repo"
	laid := "[[consumes]]\nfrom = \"b\"\ncontract = \"a-b\"\n"
	t.Run("no orbit.toml is absent", func(t *testing.T) {
		expect(t, runAtom(t, id, treeIn(t, map[string]string{"a": "x"})), stateOf(0), absent, "carries no root orbit.toml")
	})
	t.Run("foundry-dies main not supplied is a 2", func(t *testing.T) {
		in := treeIn(t, map[string]string{"orbit.toml": laid})
		in.Origin = "http://door/rob/a.git"
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "(-dies)")
	})
	t.Run("an orbit.toml that will not read is a 2", func(t *testing.T) {
		in := treeIn(t, map[string]string{"orbit.toml/x": "x"})
		in.Dies = diesAt(t, nil)
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "orbit:repo: CANNOT RUN")
	})
	t.Run("a tree that would not enumerate", func(t *testing.T) {
		expect(t, runAtom(t, id, missingRoot(t)), stateOf(2), cannot, "orbit:repo: CANNOT RUN")
	})
	for name, tc := range map[string]struct {
		prepare func(t *testing.T, dies string)
		needle  string
	}{
		"contracts that will not read":      {func(t *testing.T, d string) { brokenLink(t, d, "orbits/z.toml") }, "foundry-dies: orbits/*.toml could not be read"},
		"a roster shard that will not read": {func(t *testing.T, d string) { brokenLink(t, d, "fleet/stars/z/data.json") }, "foundry-dies: fleet/stars/z/data.json could not be read"},
		"a directory of orbits with no contract": {func(t *testing.T, d string) {
			if err := os.Remove(filepath.Join(d, "orbits", "a-b.toml")); err != nil {
				t.Fatal(err)
			}
		}, "foundry-dies/orbits does not compose"},
	} {
		t.Run("foundry-dies with "+name+" is a 2", func(t *testing.T) {
			in := treeIn(t, map[string]string{"orbit.toml": laid})
			in.Dies = diesAt(t, nil)
			tc.prepare(t, in.Dies)
			expect(t, runAtom(t, id, in), stateOf(2), cannot, tc.needle)
		})
	}
	t.Run("a laid edge no contract composes to is recorded against this star", func(t *testing.T) {
		in := treeIn(t, map[string]string{"orbit.toml": laid})
		in.Origin = "http://door/rob/a.git"
		in.Dies = diesAt(t, nil)
		v := runAtom(t, id, in)
		expect(t, v, stateOf(0), pass, "orbit:repo: ")
		if len(v.Findings) == 0 {
			t.Errorf("the laid file declares an edge the contracts do not compose to, and the lane recorded nothing:\n%s", v.Reason)
		}
	})
	t.Run("a contract that does not parse is a finding, not the directory's 2", func(t *testing.T) {
		in := treeIn(t, map[string]string{"orbit.toml": laid})
		in.Dies = diesAt(t, map[string]string{"orbits/bad.toml": "= not toml"})
		v := runAtom(t, id, in)
		expect(t, v, stateOf(0), pass)
		if len(v.Findings) == 0 {
			t.Errorf("no finding for the unparseable contract:\n%s", v.Reason)
		}
	})
}

func TestOrbitSidecarsAndOrbitComposed(t *testing.T) {
	sidecar := map[string]string{"prime/orbits/a.orbit.toml": "x"}
	t.Run("orbit:sidecars is absent where nothing renders sidecars", func(t *testing.T) {
		expect(t, runAtom(t, "orbit:sidecars", treeIn(t, map[string]string{"a": "x"})), stateOf(0), absent, "renders no orbit sidecars")
	})
	t.Run("ops:orbit-composed is absent where nothing renders sidecars", func(t *testing.T) {
		expect(t, runAtom(t, "ops:orbit-composed", treeIn(t, map[string]string{"a": "x"})), stateOf(0), absent, "renders no orbit sidecars (no prime/orbits/)")
	})
	t.Run("without foundry-dies main both are a 2", func(t *testing.T) {
		in := treeIn(t, sidecar)
		expect(t, runAtom(t, "ops:orbit-composed", in), stateOf(2), cannot, "foundry-dies' contracts could not be read")
		expect(t, runAtom(t, "orbit:sidecars", in), stateOf(2), cannot, "ops:orbit-composed could not run")
	})
	t.Run("a rendered sidecar the contracts do not compose is drift", func(t *testing.T) {
		in := treeIn(t, sidecar)
		in.Dies = diesAt(t, nil)
		v := runAtom(t, "ops:orbit-composed", in)
		expect(t, v, stateOf(1), findings, "prime/orbits against foundry-dies main: ")
	})
	for name, tc := range map[string]struct {
		in     func(t *testing.T) Input
		needle string
	}{
		"a rendered sidecar that will not read": {func(t *testing.T) Input {
			in := treeIn(t, sidecar)
			brokenLink(t, in.Root, "prime/orbits/z.orbit.toml")
			in.Dies = diesAt(t, nil)
			return in
		}, "the rendered sidecars could not be read"},
		"foundry-dies' contracts that will not read": {func(t *testing.T) Input {
			in := treeIn(t, sidecar)
			in.Dies = diesAt(t, nil)
			brokenLink(t, in.Dies, "orbits/z.toml")
			return in
		}, "foundry-dies' contracts could not be read"},
		"foundry-dies' roster that will not read": {func(t *testing.T) Input {
			in := treeIn(t, sidecar)
			in.Dies = diesAt(t, nil)
			brokenLink(t, in.Dies, "fleet/stars/z/data.json")
			return in
		}, "foundry-dies' roster could not be read"},
	} {
		t.Run("ops:orbit-composed: "+name+" is a 2", func(t *testing.T) {
			expect(t, runAtom(t, "ops:orbit-composed", tc.in(t)), stateOf(2), cannot, tc.needle)
		})
	}
	t.Run("a sidecar that cannot be read is named, not read", func(t *testing.T) {
		in := treeIn(t, map[string]string{"prime/orbits/sub/x": "x"})
		in.Dies = diesAt(t, nil)
		v := runAtom(t, "ops:orbit-composed", in)
		if v.State != 1 || !strings.Contains(v.Reason, "sub") {
			t.Errorf("a directory among the rendered files is a file the composer does not own: state %d\n%s", v.State, v.Reason)
		}
	})
	t.Run("orbit:sidecars records both gate atoms as findings", func(t *testing.T) {
		in := treeIn(t, sidecar)
		in.Dies = diesAt(t, nil)
		v := runAtom(t, "orbit:sidecars", in)
		expect(t, v, stateOf(0), pass, "orbit:sidecars: ")
		if len(v.Findings) != 2 || v.Findings[0].Cause != "ops:orbit-composed" || v.Findings[1].Cause != "ops:orbit-sidecars" {
			t.Fatalf("findings %+v: want one per gate atom, composed first", v.Findings)
		}
		if v.Findings[0].Verdict != checks.VerdictDrifted {
			t.Errorf("composed drift, report-only: %+v", v.Findings[0])
		}
		if v.Findings[1].Verdict != checks.VerdictHolds || !strings.Contains(v.Findings[1].Detail, "no ops shape") {
			t.Errorf("a tree that is not an ops tree holds ops:orbit-sidecars by absence: %+v", v.Findings[1])
		}
	})
	opsTree := map[string]string{"prime/orbits/a.orbit.toml": "x", "flux/k.yaml": "a: 1\n"}
	for _, tc := range []struct {
		name    string
		code    int
		out     string
		verdict string
		cannot  bool
	}{
		{"a sidecar the reader accepts holds", 0, "parsed 1", checks.VerdictHolds, false},
		{"a sidecar the reader refuses is recorded", 1, "a.orbit.toml: bad acl", checks.VerdictDrifted, false},
		{"a reader that would not start is a 2", -1, "orbitparse: not found", "", true},
	} {
		t.Run("ops:orbit-sidecars: "+tc.name, func(t *testing.T) {
			in := treeIn(t, opsTree)
			in.Dies = diesAt(t, nil)
			var called Cmd
			in.Exec = func(_ context.Context, c Cmd) (string, int) { called = c; return tc.out, tc.code }
			v := runAtom(t, "orbit:sidecars", in)
			if tc.cannot {
				expect(t, v, stateOf(2), cannot, "ops:orbit-sidecars could not run", "the atom never ran")
				return
			}
			// A pass's reason is "<id>: PASS"; only a finding carries the reader's words.
			got := v.Findings[1]
			if got.Verdict != tc.verdict || (tc.verdict == checks.VerdictDrifted && !strings.Contains(got.Detail, tc.out)) {
				t.Errorf("finding %+v, want %s carrying %q", got, tc.verdict, tc.out)
			}
			want := []string{in.Root + "/prime/orbits/a.orbit.toml"}
			if called.Name != "orbitparse" || !reflect.DeepEqual(called.Args, want) || !called.Both {
				t.Errorf("the reader was run as %+v, want orbitparse %v with both streams", called, want)
			}
		})
	}
	t.Run("ops:orbit-sidecars: an ops tree that tracks none says so", func(t *testing.T) {
		in := treeIn(t, map[string]string{"prime/orbits/a.orbit.toml": "x", "flux/k.yaml": "a: 1\n"})
		in.Committable = []string{"flux/k.yaml"}
		v := opsOrbitSidecars(context.Background(), checks.AtomByID("ops:orbit-sidecars"), in)
		expect(t, v, stateOf(0), absent, "tracks no composed orbit sidecar")
	})
	t.Run("ops:orbit-sidecars: a reader that exits above 2 is still a 2", func(t *testing.T) {
		in := treeIn(t, opsTree)
		in.Exec = func(context.Context, Cmd) (string, int) { return "boom", 7 }
		v := opsOrbitSidecars(context.Background(), checks.AtomByID("ops:orbit-sidecars"), in)
		expect(t, v, stateOf(2), cannot, "boom")
	})
	t.Run("ops:orbit-sidecars: a scan that failed", func(t *testing.T) {
		in := missingRoot(t)
		in.FilesErr = errBoom
		expect(t, opsOrbitSidecars(context.Background(), checks.AtomByID("ops:orbit-sidecars"), in), stateOf(2), cannot, "the tree would not enumerate")
	})
}

func TestOpsYAML(t *testing.T) {
	const id = "ops:yaml"
	for _, tc := range []struct {
		name    string
		files   map[string]string
		state   int
		result  string
		needles []string
	}{
		{"a star is not an ops tree", map[string]string{"a.yaml": "a: 1\na: 2\n"}, 0, absent, []string{"has no ops shape"}},
		{"an ops tree with no yaml", map[string]string{"policy/a.rego": "x"}, 0, absent, []string{"ABSENT - no yaml in this tree"}},
		{"yaml that loads strictly passes", map[string]string{"flux/a.yaml": "a: 1\n---\nb: 2\n", "policy/a.rego": "x"}, 0, pass,
			[]string{"1 yaml file(s)", "0 with a duplicate key or a parse error"}},
		{"a duplicate key is the defect", map[string]string{"flux/a.yaml": "a: 1\na: 2\n", "flux/ok.yml": "b: 1\n"}, 1, findings,
			[]string{"flux/a.yaml: ", "already defined", "2 yaml file(s), 1 with a duplicate key", "yaml failed (rc=1) — findings"}},
		{"a parse error is a finding too", map[string]string{"flux/a.yaml": "a: [1\n"}, 1, findings, []string{"flux/a.yaml: "}},
		// The chain's other branch runs a repo's tools/yaml-strict. No repository
		// has one, the binary does not port the branch, and a tree that did carry
		// it is graded by the parse alone.
		{"a tools/yaml-strict is not run", map[string]string{"flux/a.yaml": "a: 1\n", "tools/yaml-strict": "#!/bin/sh\nexit 1\n"}, 0, pass, nil},
		// The raw tree, not the gate population: the chains' index was built over
		// everything the repository would commit.
		{"yaml under an excluded directory is still read", map[string]string{"flux/ok.yaml": "a: 1\n", "vendor/bad.yaml": "a: 1\na: 2\n"}, 1, findings,
			[]string{"vendor/bad.yaml: "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expect(t, runAtom(t, id, treeIn(t, tc.files)), stateOf(tc.state), tc.result, tc.needles...)
		})
	}
	t.Run("a file that will not read never ran the atom", func(t *testing.T) {
		in := treeIn(t, map[string]string{"flux/a.yaml": "a: 1\n"})
		in.Committable = append(in.Committable, "flux/gone.yaml")
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the atom never ran: ")
	})
	t.Run("a scan that failed", func(t *testing.T) {
		in := missingRoot(t)
		in.FilesErr = errBoom
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the tree would not enumerate")
	})
}

// ops:orbit-sidecars is an atom of its own as well as a finding of
// orbit:sidecars: pass, findings, absent and could-not-run, through the exec seam.
func TestOpsOrbitSidecarsIsRegistered(t *testing.T) {
	const id = "ops:orbit-sidecars"
	opsTree := map[string]string{"prime/orbits/a.orbit.toml": "x", "flux/k.yaml": "a: 1\n"}
	for _, tc := range []struct {
		name    string
		tree    map[string]string
		code    int
		out     string
		state   int
		result  string
		needles []string
	}{
		{"every sidecar parses", opsTree, 0, "parsed 1", 0, pass, []string{"parsed 1"}},
		{"a sidecar its star would refuse to boot on", opsTree, 1, "a.orbit.toml: bad acl", 1, findings, []string{"a.orbit.toml: bad acl"}},
		{"a reader that would not start never ran", opsTree, -1, "orbitparse: not found", 2, cannot, []string{"the atom never ran: orbitparse: not found"}},
		{"a reader that fails above 2 is still a 2", opsTree, 7, "boom", 2, cannot, []string{"boom"}},
		{"a tree that renders none is absent", map[string]string{"flux/k.yaml": "a: 1\n"}, 0, "", 0, absent, []string{"tracks no composed orbit sidecar"}},
		{"a tree that is not an ops tree is absent", map[string]string{"main.go": "x"}, 0, "", 0, absent, []string{"has no ops shape"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, f := toolTree(t, tc.tree, func(Cmd) (string, int) { return tc.out, tc.code })
			expect(t, runAtom(t, id, in), stateOf(tc.state), tc.result, tc.needles...)
			if tc.result == absent && len(f.calls) != 0 {
				t.Errorf("an absent atom ran %v", f.ran())
			}
		})
	}
}
