package atoms

import (
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

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
func surfaceIn(t *testing.T, scans map[string]func() (string, int)) (Input, *toolFake) {
	t.Helper()
	in, f := toolTree(t, map[string]string{"main.go": "package main\n"}, func(c Cmd) (string, int) {
		return scans[c.Args[1]]()
	})
	in.Dies = diesAt(t, nil)
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
	const id = "orbit:surface"
	holds := narcReport("inert|a_verb|one-exposer|registered via literal")
	none := narcReport()
	t.Run("a star whose code serves the contracted verb holds, and narc is run as the chain ran it", func(t *testing.T) {
		in, f := surfaceIn(t, answering(holds, none))
		v := runAtom(t, id, in)
		expect(t, v, stateOf(0), pass, "orbit:surface: ")
		if len(v.Findings) == 0 || v.Findings[0].Verdict != checks.VerdictHolds || v.Findings[0].Cause != "served" {
			t.Errorf("findings %+v: want the served contract held", v.Findings)
		}
		if len(f.calls) != 2 {
			t.Fatalf("narc ran %d times: %v", len(f.calls), f.ran())
		}
		for i, scan := range []string{"surface", "orbits"} {
			c := f.calls[i]
			if c.Name != "narc" || flagged(c) != "scan "+scan+" --json - "+in.Root || c.Dir != in.Root || !c.StdoutOnly {
				t.Errorf("call %d was %+v: want narc scan %s --json - <root> in the root, stdout alone", i, c, scan)
			}
		}
	})
	t.Run("a verb the contract names and the code no longer registers is recorded, report-only", func(t *testing.T) {
		in, _ := surfaceIn(t, answering(none, none))
		v := runAtom(t, id, in)
		expect(t, v, stateOf(0), pass, "orbit:surface: ")
		if len(v.Findings) == 0 || v.Findings[0].Verdict != checks.VerdictDrifted || v.Findings[0].Cause != "contracted-verb-unregistered" {
			t.Errorf("findings %+v: want a drifted unregistered verb (report-only until Enforce flips)", v.Findings)
		}
	})
	t.Run("narc exiting non-zero still hands its report over: the hole is part of the answer", func(t *testing.T) {
		in, _ := surfaceIn(t, map[string]func() (string, int){
			"surface": func() (string, int) { return holds, 2 },
			"orbits":  func() (string, int) { return none, 2 },
		})
		expect(t, runAtom(t, id, in), stateOf(0), pass, "orbit:surface: ")
	})
	for _, tc := range []struct {
		name    string
		mutate  func(in *Input)
		scans   map[string]func() (string, int)
		state   int
		result  string
		needles []string
	}{
		{"the contracts' own repository is absent", func(in *Input) {
			put(t, in.Root, "orbits/x-y.toml", contract)
		}, nil, 0, absent, []string{"the contracts' repository"}},
		{"a star on no contracted seam is absent", func(in *Input) { in.Origin = "http://door/rob/zzz.git" }, nil, 0, absent,
			[]string{"zzz takes part in no contracted seam in foundry-dies/orbits"}},
		{"foundry-dies not supplied is a 2", func(in *Input) { in.Dies = "" }, nil, 2, cannot, []string{"(-dies)"}},
		{"a pin that could not be read is a 2 in the chain's words", func(in *Input) { in.NarcErr = "foundry/flux prime/images/kustomization.yaml could not be read: gone" }, nil, 2, cannot,
			[]string{"orbit:surface: CANNOT RUN - foundry/flux prime/images/kustomization.yaml could not be read: gone"}},
		{"a narc that would not start never ran", nil, map[string]func() (string, int){
			"surface": func() (string, int) { return "narc: gone", -1 },
		}, 2, cannot, []string{"narc scan surface never ran: narc: gone"}},
		{"the second scan that would not start never ran", nil, map[string]func() (string, int){
			"surface": func() (string, int) { return holds, 0 },
			"orbits":  func() (string, int) { return "narc: gone", -1 },
		}, 2, cannot, []string{"narc scan orbits never ran"}},
		{"a report that is not narcissus's is a 2", nil, answering("not json", none), 2, cannot, []string{"narc scan surface answered something that is not its report"}},
		{"an orbits report that is not narcissus's is a 2", nil, answering(holds, "not json"), 2, cannot, []string{"narc scan orbits answered something that is not its report"}},
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
			expect(t, runAtom(t, id, in), stateOf(tc.state), tc.result, tc.needles...)
			if tc.state == 0 && len(f.calls) != 0 {
				t.Errorf("an absent atom ran narc: %v", f.ran())
			}
		})
	}
	t.Run("a contract that does not parse rides along as a finding", func(t *testing.T) {
		in, _ := surfaceIn(t, answering(holds, none))
		put(t, in.Dies, "orbits/bad-x.toml", "= not toml")
		v := runAtom(t, id, in)
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
		v := runAtom(t, id, in)
		expect(t, v, stateOf(0), pass, "orbit:surface: ")
		if len(f.calls) != 2 || len(v.Findings) == 0 || v.Findings[0].Cause != "dials-contracted" {
			t.Errorf("ran %v, findings %+v: want the consumer's dial held", f.ran(), v.Findings)
		}
	})
	t.Run("a verb dialled through the gateway is attributed by the roster's prefix", func(t *testing.T) {
		in, _ := surfaceIn(t, answering(none, narcReport("unanalyzable|/src/x.go:1|loose-verb|a_a_verb — via the gateway")))
		in.Origin = "http://door/rob/b.git"
		v := runAtom(t, id, in)
		if len(v.Findings) == 0 || v.Findings[0].Cause != "dials-contracted" {
			t.Errorf("findings %+v: want the prefixed dial held", v.Findings)
		}
	})
	t.Run("a contract of this tree that will not read is a 2", func(t *testing.T) {
		in, _ := surfaceIn(t, answering(holds, none))
		brokenLink(t, in.Root, "orbits/z.toml")
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "orbits/* could not be read")
	})
	t.Run("a roster shard of foundry-dies that will not read is a 2", func(t *testing.T) {
		in, _ := surfaceIn(t, answering(holds, none))
		brokenLink(t, in.Dies, "fleet/stars/z/data.json")
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "foundry-dies: fleet/stars/z/data.json could not be read")
	})
}
