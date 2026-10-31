package fleetwittopics

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// run runs the atom over an input.
func run(t *testing.T, in atomkit.In) checks.Verdict {
	t.Helper()
	return check(context.Background(), in)
}

// expect holds a verdict to its state, its result word (an ABSENT pass reads
// "absent") and the words it must carry. A pass's Reason is only "<id>: PASS" —
// what the atom said rides in its Logs — so the words are looked for in both.
func expect(t *testing.T, v checks.Verdict, state checks.State, result string, needles ...string) {
	t.Helper()
	if v.State != int(state) || v.Result != result {
		t.Errorf("%s: state %d (%s), want %d (%s)\n%s", v.Atom, v.State, v.Result, state, result, v.Reason)
	}
	said := v.Reason + "\n" + strings.Join(v.Logs, "\n")
	for _, n := range needles {
		if !strings.Contains(said, n) {
			t.Errorf("%s: reason and logs lack %q:\n%s", v.Atom, n, said)
		}
	}
}

var pass = checks.StatePass.String()

var findings = checks.StateFindings.String()

var cannot = checks.StateCannotRun.String()

const absent = "absent"

// stateOf is a table's plain int as the catalogue's State.
func stateOf(n int) checks.State { return checks.State(n) }

// missingRoot is an input over a directory that is not there: the atoms that
// open with the root's listing settle 2.
func missingRoot(t *testing.T) atomkit.In {
	t.Helper()
	return atomkit.In{Root: t.TempDir() + "/not-there", Now: time.Now()}
}

// put writes a file, making its directories.
func put(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// treeIn writes files to a directory and answers the In an atom is handed over
// it, WITHOUT git: the population is the file list, as Collect would build it
// for a tree with nothing ignored.
func treeIn(t *testing.T, files map[string]string) atomkit.In {
	t.Helper()
	dir := t.TempDir()
	var names []string
	for name, body := range files {
		put(t, dir, name, body)
		names = append(names, name)
	}
	sort.Strings(names)
	return atomkit.In{
		Root: dir, Files: checks.GatePopulation(names), Committable: names, Tracked: names,
		Now: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC),
	}
}

// toolFake stands in for the programs on PATH through In.Exec: it answers each
// call from a script, records it, and fails the test for a program the tools
// container or the atom's toolchain does not carry (atoms.Programs, and
// toolchainPrograms: what the toolchain image provides).
var toolchainPrograms []string

type toolFake struct {
	t      *testing.T
	calls  []atoms.Cmd
	answer func(c atoms.Cmd) (string, int)
}

func (f *toolFake) exec(_ context.Context, c atoms.Cmd) (string, int) {
	f.t.Helper()
	if !slices.Contains(atoms.Programs, c.Name) && !slices.Contains(toolchainPrograms, c.Name) {
		f.t.Errorf("exec of %q: no layer of the tools container carries it", c.Name)
	}
	f.calls = append(f.calls, c)
	if f.answer == nil {
		return "", 0
	}
	return f.answer(c)
}

// toolTree is treeIn with the exec seam set to the script.
func toolTree(t *testing.T, files map[string]string, answer func(atoms.Cmd) (string, int)) (atomkit.In, *toolFake) {
	t.Helper()
	in := treeIn(t, files)
	f := &toolFake{t: t, answer: answer}
	in.Exec = f.exec
	return in, f
}

// ran is the program names the atom exec'd, in order.
func (f *toolFake) ran() []string {
	var names []string
	for _, c := range f.calls {
		names = append(names, c.Name)
	}
	return names
}

// flagged is an argv as one string, for a needle.
func flagged(c atoms.Cmd) string { return strings.Join(c.Args, " ") }

// flux is a tree that looks like the fleet's flux checkout to this atom, with
// the checker the tree itself holds.
func init() { toolchainPrograms = []string{"uv"} }

var flux = map[string]string{"prime/a.yaml": "x", checker: "#!/usr/bin/env python3\n"}

// uvTree is toolTree for the atom: `uv --version` (the provisioning probe) is
// answered, and every other call is the script. A nil script answers 0 and nothing.
func uvTree(t *testing.T, files map[string]string, answer func(atoms.Cmd) (string, int)) (atomkit.In, *toolFake) {
	t.Helper()
	return toolTree(t, files, func(c atoms.Cmd) (string, int) {
		if c.Name == "python3" && len(c.Args) == 1 && c.Args[0] == "--version" {
			return "python3: not found", -1 // no venv: uv provides python
		}
		if c.Name == "uv" && len(c.Args) == 1 && c.Args[0] == "--version" {
			return "uv 0.12.13", 0
		}
		if answer == nil {
			return "", 0
		}
		return answer(c)
	})
}

func TestFleetWitTopics(t *testing.T) {
	for _, tc := range []struct {
		name    string
		code    int
		out     string
		state   int
		result  string
		needles []string
	}{
		{"a checker that holds is a pass", 0, "wit-topics: redpanda/wit matches redpanda/schemas", 0, pass, []string{"redpanda/wit matches"}},
		{"stale is the checker's exit 1, unmapped", 1, "wit-topics: STALE - aiws-topics.wit", 1, findings, []string{"STALE - aiws-topics.wit"}},
		{"could not run is the checker's exit 2, unmapped", 2, "wit-topics: COULD NOT RUN - no schemas", 2, cannot, []string{"COULD NOT RUN - no schemas"}},
		{"a uv that would not start never ran", -1, "uv: gone", 2, cannot, []string{"the atom never ran: uv: gone"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, f := uvTree(t, flux, func(atoms.Cmd) (string, int) { return tc.out, tc.code })
			expect(t, run(t, in), stateOf(tc.state), tc.result, tc.needles...)
			c := f.calls[len(f.calls)-1]
			// the checker runs under uv with no package resolved, from the root, both streams, bytecode off
			if c.Name != "uv" || c.Dir != in.Root || !c.Both || !slices.Contains(c.Env, "PYTHONDONTWRITEBYTECODE=1") {
				t.Errorf("call %+v: want uv in %s with both streams and no bytecode", c, in.Root)
			}
			if got := flagged(c); got != "run --no-project --quiet --python python3 python3 tools/wit-topics" {
				t.Errorf("uv ran as %q: nothing is resolved with --with", got)
			}
		})
	}
	t.Run("a container with the venv runs the checker with its python3 and never touches uv", func(t *testing.T) {
		in, f := toolTree(t, flux, func(c atoms.Cmd) (string, int) {
			if c.Name == "python3" && c.Args[0] == "--version" {
				return "Python 3.14.0", 0
			}
			return "wit-topics: redpanda/wit matches redpanda/schemas", 0
		})
		expect(t, run(t, in), stateOf(0), pass, "redpanda/wit matches")
		c := f.calls[len(f.calls)-1]
		if c.Name != "python3" || flagged(c) != "tools/wit-topics" || !c.Both || !slices.Contains(c.Env, "PYTHONDONTWRITEBYTECODE=1") {
			t.Errorf("the checker ran as %+v", c)
		}
		for _, p := range f.ran() {
			if p == "uv" {
				t.Errorf("uv was touched: %v", f.ran())
			}
		}
	})
	t.Run("a container without uv never ran the checker", func(t *testing.T) {
		in, f := toolTree(t, flux, func(c atoms.Cmd) (string, int) {
			if c.Name == "python3" {
				return "python3: not found", -1
			}
			return "uv: not found", 127
		})
		expect(t, run(t, in), stateOf(2), cannot, "the atom never ran: uv --version exited 127: uv: not found")
		if len(f.calls) != 2 {
			t.Errorf("the checker ran after a failed probe: %v", f.ran())
		}
	})
	t.Run("a tree with no prime/ is not flux's: absent, and uv is not touched", func(t *testing.T) {
		in, f := uvTree(t, map[string]string{"a.txt": "x", checker: "x"}, nil)
		expect(t, run(t, in), stateOf(0), absent, "carries no prime/", "not the fleet's flux tree")
		if len(f.calls) != 0 {
			t.Errorf("an absent atom touched %v", f.ran())
		}
	})
	t.Run("flux's tree without the checker is a could-not-run naming it, never an absence", func(t *testing.T) {
		in, f := uvTree(t, map[string]string{"prime/a.yaml": "x"}, nil)
		expect(t, run(t, in), stateOf(2), cannot, checker+" is absent, so there is no checker to run")
		if len(f.calls) != 0 {
			t.Errorf("python ran with no checker: %v", f.ran())
		}
	})
	t.Run("a root that cannot be read", func(t *testing.T) {
		expect(t, run(t, missingRoot(t)), stateOf(2), cannot, "the tree would not enumerate")
	})
}

// The declaration is the catalogue row it replaced: the same stage, group, text and
// fork lane, and the needs the split table gives it.
func TestTheDeclarationIsTheRowItReplaced(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrecommit || Def.Lane != checks.LaneAny || Def.Group != checks.GroupOf(Def.ID) ||
		Def.Toolchain != "python" || Def.NotOnForks != "" || Def.SubsumedBy != "" || Def.NeedsManifest {
		t.Errorf("%+v", Def)
	}
	if Def.Desc != "flux's committed redpanda/wit is current with redpanda/schemas - a topic schema changed, added or removed without regenerating, or a hand edit of aiws-topics.wit, fails (stellar-core F2)." {
		t.Errorf("the description moved: %q", Def.Desc)
	}
	if !slices.Equal(Def.Needs, []string{"tool:uv"}) {
		t.Errorf("needs %v", Def.Needs)
	}
}

// fluxChecker is flux's tools/wit-topics verbatim (flux 5c706b6456e73cf700e8643c01ef63d42f91e59b),
// so the tests below run the real checker through the atom's real exec path. The other
// tests fake the checker's exit code and so prove only that the atom relays one; these
// prove that a flux-shaped tree that has parted from its WIT is a finding, end to end.
// When flux changes the checker, its copy here is the one to refresh.
const fluxChecker = `#!/usr/bin/env python3
"""wit-topics — redpanda/wit is the WIT of the schemas beside it (stellar-core F2).

redpanda/schemas/*.yaml are the topic contracts. redpanda/wit/aiws-topics.wit (package
aiws:topics) is their rendering as WIT records, generated by foundry-dies
tools/wit-from-schema, and aiws-topics.report.json is the account of what the WIT
does not carry. THIS TREE HOLDS NEITHER THE GENERATOR NOR A WAY TO RUN IT, so the
gate compares digests the generator recorded:

  - every redpanda/schemas/*.yaml the report names still hashes to its recorded
    source_sha256 (a schema edited and not regenerated);
  - every schema yaml is in the report, or in its skipped_not_a_schema list, and
    every reported source still exists (a topic added or removed, unregenerated);
  - aiws-topics.wit still hashes to the report's wit_sha256 (a hand edit).

Regenerate, from a foundry-dies checkout:

  tools/wit-from-schema --in <flux>/redpanda/schemas --out <flux>/redpanda/wit

  wit-topics     exit 0 current, 1 stale, 2 could not run (a missing or unparseable
                 file: "found nothing" and "could not look" must not print the same)

Stdlib only.
"""
from __future__ import annotations

import hashlib
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SCHEMAS = ROOT / "redpanda" / "schemas"
WIT = ROOT / "redpanda" / "wit"
REGEN = "regenerate: foundry-dies tools/wit-from-schema --in redpanda/schemas --out redpanda/wit"


def _sha(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def check(schemas: Path, wit: Path) -> tuple[list[str], str | None]:
    """(stale lines, could-not-run reason). The reason wins: nothing was judged."""
    try:
        report = json.loads((wit / "aiws-topics.report.json").read_text(encoding="utf-8"))
        entries = report["schemas"]
        skipped = set(report["skipped_not_a_schema"])
        want_wit = report["wit_sha256"]
        recorded = {e["source"]: e["source_sha256"] for e in entries.values()}
        have_wit = _sha(wit / "aiws-topics.wit")
        on_disk = {p.name for p in schemas.glob("*.yaml")}
    except (OSError, ValueError, KeyError, TypeError) as e:
        return [], f"{type(e).__name__}: {e}"
    if not on_disk:
        return [], f"no *.yaml in {schemas}"
    stale: list[str] = []
    for name in sorted(on_disk - skipped - set(recorded)):
        stale.append(f"{name}: a topic schema the report does not name")
    for name in sorted(set(recorded) - on_disk):
        stale.append(f"{name}: in the report but no longer in {schemas.name}/")
    for name in sorted(set(recorded) & on_disk):
        if _sha(schemas / name) != recorded[name]:
            stale.append(f"{name}: changed since the WIT was generated")
    if have_wit != want_wit:
        stale.append("aiws-topics.wit: differs from the report's wit_sha256 (hand edit?)")
    return stale, None


def main() -> int:
    stale, why = check(SCHEMAS, WIT)
    if why:
        sys.stderr.write(f"wit-topics: COULD NOT RUN - {why}\n")
        return 2
    for line in stale:
        sys.stderr.write(f"wit-topics: STALE - {line}\n")
    if stale:
        sys.stderr.write(REGEN + "\n")
        return 1
    sys.stdout.write("wit-topics: redpanda/wit matches redpanda/schemas\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
`

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// fluxTree is a flux-shaped tree whose redpanda/wit is current with redpanda/schemas:
// two schemas, a WIT, and the report that records their digests, as the generator wrote it.
func fluxTree(t *testing.T, edit func(files map[string]string)) atomkit.In {
	t.Helper()
	const wit = "package aiws:topics@0.1.0;\n"
	schemas := map[string]string{"redpanda/schemas/a.yaml": "title: a\n", "redpanda/schemas/b.yaml": "title: b\n"}
	entry := func(src string) map[string]string {
		return map[string]string{"source": src, "source_sha256": sha(schemas["redpanda/schemas/"+src])}
	}
	report, err := json.Marshal(map[string]any{
		"schemas":              map[string]any{"a": entry("a.yaml"), "b": entry("b.yaml")},
		"skipped_not_a_schema": []string{"kustomization.yaml"},
		"wit_sha256":           sha(wit),
	})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"prime/a.yaml":                         "x",
		checker:                                fluxChecker,
		"redpanda/wit/aiws-topics.wit":         wit,
		"redpanda/wit/aiws-topics.report.json": string(report),
	}
	for k, v := range schemas {
		files[k] = v
	}
	if edit != nil {
		edit(files)
	}
	return treeIn(t, files)
}

// The real checker, through the real exec: a matching tree passes, and each way the WIT
// can part from the schemas is exit 1. The checker needs python3 or uv; a lane that has
// neither cannot run these, and says so rather than skip (a skipped proof is a pass).
func TestARealFluxCheckerGoesRedWhenTheWitPartsFromTheSchemas(t *testing.T) {
	if _, ok := runnable(); !ok {
		t.Fatal("neither python3 nor uv on PATH: the end-to-end proof cannot run here")
	}
	for _, tc := range []struct {
		name    string
		edit    func(map[string]string)
		state   int
		result  string
		needles []string
	}{
		{"the matching tree passes", nil, 0, pass, []string{"redpanda/wit matches redpanda/schemas"}},
		{"a schema edited and not regenerated is a finding", func(f map[string]string) { f["redpanda/schemas/a.yaml"] = "title: a2\n" },
			1, findings, []string{"STALE - a.yaml: changed since the WIT was generated"}},
		{"a topic added and not regenerated is a finding", func(f map[string]string) { f["redpanda/schemas/c.yaml"] = "title: c\n" },
			1, findings, []string{"STALE - c.yaml: a topic schema the report does not name"}},
		{"a topic removed and not regenerated is a finding", func(f map[string]string) { delete(f, "redpanda/schemas/b.yaml") },
			1, findings, []string{"STALE - b.yaml: in the report but no longer in schemas/"}},
		{"a hand edit of aiws-topics.wit is a finding", func(f map[string]string) { f["redpanda/wit/aiws-topics.wit"] += "// edited\n" },
			1, findings, []string{"STALE - aiws-topics.wit: differs from the report's wit_sha256"}},
		{"a missing report is could-not-run, never a finding or a pass", func(f map[string]string) { delete(f, "redpanda/wit/aiws-topics.report.json") },
			2, cannot, []string{"COULD NOT RUN"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expect(t, run(t, fluxTree(t, tc.edit)), stateOf(tc.state), tc.result, tc.needles...)
		})
	}
}

// runnable reports whether the real python3 or uv is on PATH.
func runnable() (string, bool) {
	for _, n := range []string{"python3", "uv"} {
		if p, err := exec.LookPath(n); err == nil {
			return p, true
		}
	}
	return "", false
}
