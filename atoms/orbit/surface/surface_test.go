package orbitsurface

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomlib/atomtest"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// run is the atom's check over an input, as the runner calls it.
func run(in atoms.Input) checks.Verdict { return check(context.Background(), in) }

func TestTheDeclarationIsTheRowItReplaced(t *testing.T) {
	atomtest.DeclaresItsRow(t, Def, "orbit", "none")
	if Def.ID != id {
		t.Errorf("%+v", Def)
	}
}

// narcReport is a narcissus JSON report holding verdict|subject|cause|detail rows.
func narcReport(rows ...string) string {
	var parts []string
	for _, r := range rows {
		f := strings.SplitN(r, "|", 4)
		parts = append(parts, `{"verdict":"`+f[0]+`","subject":"`+f[1]+`","cause":"`+f[2]+`","detail":"`+f[3]+`"}`)
	}
	return `{"version":1,"findings":[` + strings.Join(parts, ",") + `]}`
}

// surfaceIn is star "a" (the producer of the a-b contract, whose verb is a_verb)
// with foundry-dies beside it, and narc answering per scan.
func surfaceIn(t *testing.T, scans map[string]func() (string, int)) (atoms.Input, *atomtest.ToolFake) {
	t.Helper()
	in, f := atomtest.ToolTree(t, map[string]string{"main.go": "package main\n"}, func(c atoms.Cmd) (string, int) {
		return scans[c.Args[1]]()
	})
	in.Dies = atomtest.DiesAt(t, nil)
	in.Origin = "http://door/rob/a.git"
	return in, f
}

func answering(surface, orbits string) map[string]func() (string, int) {
	return map[string]func() (string, int){
		"surface": func() (string, int) { return surface, 0 },
		"orbits":  func() (string, int) { return orbits, 0 },
	}
}

func TestOrbitSurface(t *testing.T) {
	holds := narcReport("inert|a_verb|one-exposer|registered via literal")
	none := narcReport()
	t.Run("a star whose code serves the contracted verb holds, and narc is run as the chain ran it", func(t *testing.T) {
		in, f := surfaceIn(t, answering(holds, none))
		v := run(in)
		atomtest.Expect(t, v, atomtest.StateOf(0), atomtest.Pass, "orbit:surface: ")
		if len(v.Findings) == 0 || v.Findings[0].Verdict != checks.VerdictHolds || v.Findings[0].Cause != "served" {
			t.Errorf("findings %+v: want the served contract held", v.Findings)
		}
		if len(f.Calls) != 2 {
			t.Fatalf("narc ran %d times: %v", len(f.Calls), f.Ran())
		}
		for i, scan := range []string{"surface", "orbits"} {
			c := f.Calls[i]
			if c.Name != "narc" || atomtest.Flagged(c) != "scan "+scan+" --json - "+in.Root || c.Dir != in.Root || !c.StdoutOnly {
				t.Errorf("call %d was %+v: want narc scan %s --json - <root> in the root, stdout alone", i, c, scan)
			}
		}
	})
	t.Run("a verb the contract names and the code no longer registers is recorded, report-only", func(t *testing.T) {
		in, _ := surfaceIn(t, answering(none, none))
		v := run(in)
		atomtest.Expect(t, v, atomtest.StateOf(0), atomtest.Pass, "orbit:surface: ")
		if len(v.Findings) == 0 || v.Findings[0].Verdict != checks.VerdictDrifted || v.Findings[0].Cause != "contracted-verb-unregistered" {
			t.Errorf("findings %+v: want a drifted unregistered verb (report-only until Enforce flips)", v.Findings)
		}
	})
	t.Run("narc exiting non-zero still hands its report over: the hole is part of the answer", func(t *testing.T) {
		in, _ := surfaceIn(t, map[string]func() (string, int){
			"surface": func() (string, int) { return holds, 2 },
			"orbits":  func() (string, int) { return none, 2 },
		})
		atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Pass, "orbit:surface: ")
	})
	for _, tc := range []struct {
		name    string
		mutate  func(in *atoms.Input)
		scans   map[string]func() (string, int)
		state   int
		result  string
		needles []string
	}{
		{"the contracts' own repository is absent", func(in *atoms.Input) {
			atomtest.Put(t, in.Root, "orbits/x-y.toml", atomtest.Contract)
		}, nil, 0, atomtest.Absent, []string{"the contracts' repository"}},
		{"a star on no contracted seam is absent", func(in *atoms.Input) { in.Origin = "http://door/rob/zzz.git" }, nil, 0, atomtest.Absent,
			[]string{"zzz takes part in no contracted seam in foundry-dies/orbits"}},
		{"foundry-dies not supplied is a 2", func(in *atoms.Input) { in.Dies = "" }, nil, 2, atomtest.Cannot, []string{"(-dies)"}},
		{"a pin that could not be read is a 2 in the chain's words", func(in *atoms.Input) {
			in.NarcErr = "foundry/flux prime/images/kustomization.yaml could not be read: gone"
		}, nil, 2, atomtest.Cannot,
			[]string{"orbit:surface: CANNOT RUN - foundry/flux prime/images/kustomization.yaml could not be read: gone"}},
		{"a narc that would not start never ran", nil, map[string]func() (string, int){
			"surface": func() (string, int) { return "narc: gone", -1 },
		}, 2, atomtest.Cannot, []string{"narc scan surface never ran: narc: gone"}},
		{"the second scan that would not start never ran", nil, map[string]func() (string, int){
			"surface": func() (string, int) { return holds, 0 },
			"orbits":  func() (string, int) { return "narc: gone", -1 },
		}, 2, atomtest.Cannot, []string{"narc scan orbits never ran"}},
		{"a report that is not narcissus's is a 2", nil, answering("not json", none), 2, atomtest.Cannot, []string{"narc scan surface answered something that is not its report"}},
		{"an orbits report that is not narcissus's is a 2", nil, answering(holds, "not json"), 2, atomtest.Cannot, []string{"narc scan orbits answered something that is not its report"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scans := tc.scans
			if scans == nil {
				scans = answering(holds, none)
			}
			in, f := surfaceIn(t, scans)
			if tc.mutate != nil {
				tc.mutate(&in)
			}
			atomtest.Expect(t, run(in), atomtest.StateOf(tc.state), tc.result, tc.needles...)
			if tc.state == 0 && len(f.Calls) != 0 {
				t.Errorf("an absent atom ran narc: %v", f.Ran())
			}
		})
	}
	t.Run("a contract that does not parse rides along as a finding", func(t *testing.T) {
		in, _ := surfaceIn(t, answering(holds, none))
		atomtest.Put(t, in.Dies, "orbits/bad-x.toml", "= not toml")
		v := run(in)
		var drifted int
		for _, f := range v.Findings {
			if f.Verdict == checks.VerdictDrifted {
				drifted++
			}
		}
		if drifted == 0 {
			t.Errorf("findings %+v: the bad contract is not named", v.Findings)
		}
	})
	t.Run("a star that only consumes is graded too, on what it dials", func(t *testing.T) {
		in, f := surfaceIn(t, answering(none, narcReport("unanalyzable|/src/x.go:1|loose-verb|a_verb — dial")))
		in.Origin = "http://door/rob/b.git"
		v := run(in)
		atomtest.Expect(t, v, atomtest.StateOf(0), atomtest.Pass, "orbit:surface: ")
		if len(f.Calls) != 2 || len(v.Findings) == 0 || v.Findings[0].Cause != "dials-contracted" {
			t.Errorf("ran %v, findings %+v: want the consumer's dial held", f.Ran(), v.Findings)
		}
	})
	t.Run("a verb dialled through the gateway is attributed by the roster's prefix", func(t *testing.T) {
		in, _ := surfaceIn(t, answering(none, narcReport("unanalyzable|/src/x.go:1|loose-verb|a_a_verb — via the gateway")))
		in.Origin = "http://door/rob/b.git"
		v := run(in)
		if len(v.Findings) == 0 || v.Findings[0].Cause != "dials-contracted" {
			t.Errorf("findings %+v: want the prefixed dial held", v.Findings)
		}
	})
	t.Run("a contract of this tree that will not read is a 2", func(t *testing.T) {
		in, _ := surfaceIn(t, answering(holds, none))
		atomtest.BrokenLink(t, in.Root, "orbits/z.toml")
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "orbits/* could not be read")
	})
	t.Run("a roster shard of foundry-dies that will not read is a 2", func(t *testing.T) {
		in, _ := surfaceIn(t, answering(holds, none))
		atomtest.BrokenLink(t, in.Dies, "fleet/repos/z.json")
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "foundry-dies: fleet/repos/z.json could not be read")
	})
}

// The check Rob asked for: a star that dials a verb its contract does not name
// is told which file to edit and what to add.
func TestOrbitSurfaceTeachesTheContractToEdit(t *testing.T) {
	const contract = "version = \"1\"\nstatus = \"approved\"\nverbs = [\"neighbors\"]\n"
	in, _ := atomtest.ToolTree(t, map[string]string{"go.mod": "module themis"}, func(c atoms.Cmd) (string, int) {
		if c.Args[1] == "surface" {
			return narcReport(), 0
		}
		return narcReport("unanalyzable|/src/b.go:2|loose-verb|urania_shapes — new call"), 0
	})
	in.Origin = "http://door/rob/themis.git"
	in.Dies = atomtest.TreeIn(t, map[string]string{
		"orbits/urania-themis.toml": contract,
		"orbits/urania-athena.toml": strings.Replace(contract, `"neighbors"`, `"neighbors", "shapes"`, 1),
		"fleet/repos/urania.json":   `{"name":"urania","verb_prefix":"urania"}`,
		"fleet/repos/themis.json":   `{"name":"themis","verb_prefix":"themis"}`,
		"fleet/repos/athena.json":   `{"verb_prefix":"tasks"}`,
	}).Root
	v := run(in)
	atomtest.Expect(t, v, atomtest.StateOf(0), atomtest.Pass,
		`themis dials urania_shapes, which urania serves, and orbits/urania-themis.toml does not name it — add "shapes"`)
}

// A star that registers what its contract names holds, and the contracts' own
// repository is checked in each producer's and consumer's lane instead.
func TestOrbitSurfaceReadsTheStarFromItsOrigin(t *testing.T) {
	for _, tc := range []struct{ origin, star string }{
		{"http://door/rob/a.git", "a"},
		{"http://door/rob/a", "a"},
	} {
		in, _ := surfaceIn(t, answering(narcReport("inert|a_verb|one-exposer|registered via literal"), narcReport()))
		in.Origin = tc.origin
		atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Pass, "orbit:surface: ")
	}
}
