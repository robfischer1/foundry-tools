package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// THE FLEET LANE'S TESTS, against the paper engine (engine_fake_test.go).
//
// These nine atoms run in EVERY repository in custody, so they are the ones a
// wrong branch costs the most — and eight of them were forty lines of shell
// prelude until the typed cut. Each atom below gets its happy path (the chain
// read back: the image, the mounts, which exec carries expect:ANY, rule 8),
// every decision it makes about the tool's answer (each exit code, each output
// parse, each absence, each CANNOT RUN), and the engine-error path.

// fleetTree copies everyLaneTree, deletes the paths named, and overlays extra
// — so a test declares only the difference its branch needs.
func fleetTree(extra map[string]string, drop ...string) map[string]string {
	out := map[string]string{}
	for k, v := range everyLaneTree {
		out[k] = v
	}
	for _, d := range drop {
		delete(out, d)
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// fleetNoContainer is rule 3's observable half: an absence decided from the
// Directory costs an entries read, never an image pull.
func fleetNoContainer(t *testing.T, why string) {
	t.Helper()
	for _, q := range engine.chains() {
		if strings.Contains(q, "container{") {
			t.Errorf("%s: a container was provisioned:\n%s", why, q)
		}
	}
}

// nulInQuery is how the querybuilder renders the NUL that joins the file list
// on the wire. Spelled by concatenation so this file carries no NUL of its own.
const nulInQuery = `\u` + `0000`

// fleetPopulationOf answers the paths inside the NUL-joined file list xargs
// reads. The query renders the separator as a \u escape, and the argument order is
// the querybuilder's, so this reads the call's arguments rather than grepping
// for one spelling of them.
func fleetPopulationOf(t *testing.T, chain string) []string {
	t.Helper()
	for i := 0; ; {
		k := strings.Index(chain[i:], "withNewFile(")
		if k < 0 {
			t.Fatalf("the chain carries no withNewFile:\n%s", chain)
		}
		start := i + k + len("withNewFile")
		end := start + matching(chain[start:], '(', ')')
		f := field{args: chain[start+1 : end]}
		if p, _ := f.arg("path"); p == fileList {
			c, ok := f.arg("contents")
			if !ok {
				t.Fatalf("the file list carries no contents:\n%s", chain)
			}
			return strings.Split(c, "\x00")
		}
		i = end + 1
	}
}

func fleetListHas(list []string, want string) bool {
	for _, f := range list {
		if f == want {
			return true
		}
	}
	return false
}

// ---- fleet:check-yaml ----

func TestFleetCheckYAMLRunsTheYAMLPopulationThroughXargs(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)

	wantState(t, runAtom(t, "fleet:check-yaml", ""), 0)

	c := engine.chain(`"check-yaml","--allow-multiple-documents"`, "exitCode")
	if !strings.Contains(c, checks.ImageFleet) {
		t.Errorf("fleet:check-yaml must run in the fleet lane image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withEnvVariable", `name:"CI"`, `value:"true"`},
		[]string{"withMountedCache", `path:"/opt/uv-cache"`},
		[]string{"withMountedDirectory", `path:"/src"`},
		[]string{"withWorkdir", `path:"/src"`},
		[]string{"withNewFile", `path:"/tmp/files0"`},
		// The provisioning probe: uvx resolves the hook before the hook judges
		// anything, under the DEFAULT Expect, so a failure is the engine's.
		[]string{"withExec", `args:["uvx","--from","pre-commit-hooks","check-yaml","--help"]`},
		// Both flags are the fleet's, unconditionally: --allow-multiple-documents
		// for a k3s manifest, --unsafe so a custom tag (!include) is parsed
		// rather than loaded.
		[]string{"withExec", `expect:ANY`, `args:["xargs","-0","-a","/tmp/files0","uvx","--from","pre-commit-hooks","check-yaml","--allow-multiple-documents","--unsafe"]`},
	)
	if hasCall(c, "withExec", `"check-yaml","--help"`, `expect:ANY`) {
		t.Errorf("the --help probe is provisioning and must run under the default Expect:\n%s", c)
	}
	if strings.Contains(c, "GATE_BASE") {
		t.Errorf("rule 8: fleet:check-yaml must not read GATE_BASE — it would key the cache on the pull:\n%s", c)
	}
	if !strings.Contains(c, nulInQuery) {
		t.Errorf("the population must reach xargs NUL-joined (a path may hold anything but a NUL):\n%s", c)
	}

	files := fleetPopulationOf(t, c)
	for _, want := range []string{".copier-answers.yml", ".forgejo/workflows/ci.yml", "compose.yaml", "flux/x.yaml", "rules/sast/go.yml"} {
		if !fleetListHas(files, want) {
			t.Errorf("the YAML population lacks %q: %q", want, files)
		}
	}
	for _, unwanted := range []string{"main.go", "go.mod", "package.json", ".git/HEAD"} {
		if fleetListHas(files, unwanted) {
			t.Errorf("%q is not YAML and must not be in the population: %q", unwanted, files)
		}
	}
}

// xargs answers 123 for a hook that exited 1, which is why this atom reads
// ToolThroughXargsState rather than StateFor: 123 straight into StateFor would
// file every finding as a could-not-run.
func TestFleetCheckYAMLMapsTheXargsExitCode(t *testing.T) {
	const tool = `"check-yaml","--allow-multiple-documents"`

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(tool, 123)
	engine.stdout(tool, "flux/x.yaml: could not determine a constructor for the tag '!include'")
	wantState(t, runAtom(t, "fleet:check-yaml", ""), 1, "could not determine a constructor")

	engine.exitCode(tool, 0)
	wantState(t, runAtom(t, "fleet:check-yaml", ""), 0)

	// xargs' own 1 is xargs failing, not the hook judging — a scan that did not
	// happen, never a finding and never a pass.
	engine.exitCode(tool, 1)
	wantState(t, runAtom(t, "fleet:check-yaml", ""), 2, "CANNOT RUN")

	for _, code := range []int{124, 125, 126, 127, 137} {
		engine.exitCode(tool, code)
		wantState(t, runAtom(t, "fleet:check-yaml", ""), 2)
	}
}

func TestFleetCheckYAMLWithoutYAMLPassesWithoutAContainer(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"go.mod": "module x\n", "main.go": "package main\n"})
	wantState(t, runAtom(t, "fleet:check-yaml", ""), 0)
	fleetNoContainer(t, "no YAML in the tree")
	if engine.chain("check-yaml") != "" {
		t.Error("no YAML means no hook run")
	}
}

func TestFleetCheckYAMLEngineFailures(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"check-yaml","--help"`, "exit code: 127: uvx: not found")
	wantState(t, runAtom(t, "fleet:check-yaml", ""), 2, "the atom never ran", "uvx: not found")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`glob(pattern:"**/*.yml")`, "the tree went away")
	wantState(t, runAtom(t, "fleet:check-yaml", ""), 2, "the tree would not enumerate", "the tree went away")

	// output() separates the ENGINE's failure from the tool's: an exec that
	// answered a code but whose output would not read never became a verdict
	// about the tree.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.script(script{match: `"check-yaml","--allow-multiple-documents"`, leaf: "stdout", fail: "the output would not read"})
	wantState(t, runAtom(t, "fleet:check-yaml", ""), 2, "the atom never ran", "the output would not read")
}

// ---- fleet:check-added-large-files ----

func TestFleetLargeFilesAsksStatForTheSizesOverTheGatePopulation(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)

	wantState(t, runAtom(t, "fleet:check-added-large-files", ""), 0)

	c := engine.chain(`"stat","-c"`, "exitCode")
	wantCalls(t, c,
		[]string{"withNewFile", `path:"/tmp/files0"`},
		[]string{"withExec", `expect:ANY`, `args:["xargs","-0","-a","/tmp/files0","stat","-c","%s %n"]`},
	)
	files := fleetPopulationOf(t, c)
	// The population is what the repository would COMMIT: a gitignored build
	// artifact cannot be a finding about the repository (tongs, 2026-09-09).
	if !fleetListHas(files, "main.go") || !fleetListHas(files, "compose.yaml") {
		t.Errorf("the population must be the whole committable tree: %q", files)
	}
	if fleetListHas(files, ".git/HEAD") {
		t.Errorf(".git is not part of the population: %q", files)
	}
}

func TestFleetLargeFilesReadsStatOutputThroughFilesOver(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)

	// 2048 KB exactly is not over; one byte more is.
	engine.stdout(`"stat","-c"`, strings.Join([]string{
		"12 README.md",
		"2097152 exactly/at/the/limit.bin",
		"3145728 assets/model.bin",
	}, "\n"))
	v := runAtom(t, "fleet:check-added-large-files", "")
	wantState(t, v, 1, "files over 2048 KB", "assets/model.bin")
	if strings.Contains(v.Reason, "exactly/at/the/limit.bin") || strings.Contains(v.Reason, "README.md") {
		t.Errorf("only the files over the ceiling are findings:\n%s", v.Reason)
	}

	engine.stdout(`"stat","-c"`, "12 README.md\n1034240 flux/infrastructure/cert-manager.yaml")
	wantState(t, runAtom(t, "fleet:check-added-large-files", ""), 0)
}

// A size that was never read is not a size under the limit: the shell swallowed
// stat's failures with 2>/dev/null and called the pass clean.
func TestFleetLargeFilesRefusesAPartialMeasurement(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(`"stat","-c"`, 1)
	engine.stderr(`"stat","-c"`, "stat: cannot statx 'gone.bin': No such file or directory")
	wantState(t, runAtom(t, "fleet:check-added-large-files", ""), 2,
		"stat did not measure every file", "cannot statx")

	// A FINDING OUTRANKS THE BAD EXIT: a file measured over the ceiling is over
	// it however the rest of the pass went.
	engine.stdout(`"stat","-c"`, "9999999 assets/model.bin")
	wantState(t, runAtom(t, "fleet:check-added-large-files", ""), 1, "assets/model.bin")
}

func TestFleetLargeFilesWithNoCommittableFileNeverBuildsAContainer(t *testing.T) {
	engine.reset()
	// Everything here is excluded by the fleet's own exclude, so the gate
	// population is empty and there is nothing to measure.
	engine.withTree(map[string]string{"vendor/a/big.bin": "", "node_modules/x/y.js": ""})
	wantState(t, runAtom(t, "fleet:check-added-large-files", ""), 0)
	fleetNoContainer(t, "an empty population")
}

func TestFleetLargeFilesEngineFailures(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"stat","-c"`, "exit code: 125: the mount would not evaluate")
	wantState(t, runAtom(t, "fleet:check-added-large-files", ""), 2, "the atom never ran", "would not evaluate")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`glob(pattern:"**")`, "the tree went away")
	wantState(t, runAtom(t, "fleet:check-added-large-files", ""), 2, "the tree would not enumerate")
}

// ---- fleet:check-merge-conflict ----

func TestFleetMergeConflictGrepsOnlyTheAnchoredMarkers(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)

	wantState(t, runAtom(t, "fleet:check-merge-conflict", ""), 0)

	c := engine.chain(`"grep","-In"`, "exitCode")
	wantCalls(t, c,
		[]string{"withNewFile", `path:"/tmp/files0"`},
		[]string{"withExec", `expect:ANY`, `args:["xargs","-0","-a","/tmp/files0","grep","-In","-E","^(<<<<<<< |>>>>>>> )"]`},
	)
	// The bare row of equals signs is a setext heading in Markdown and a table
	// rule in reStructuredText; matching it turns every docs repo red.
	if strings.Contains(c, "=======") {
		t.Errorf("the bare ======= row must not be matched:\n%s", c)
	}
}

// grep is three-valued and xargs collapses two of the three, so the OUTPUT
// decides and the code only confirms.
func TestFleetMergeConflictReadsTheOutputBeforeTheCode(t *testing.T) {
	const tool = `"grep","-In"`
	hit := "README.md:12:<<<<<<< HEAD"

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.stdout(tool, hit)
	engine.exitCode(tool, 0)
	wantState(t, runAtom(t, "fleet:check-merge-conflict", ""), 1, hit)

	// The ordinary large-repo run: one chunk matched, another did not.
	engine.exitCode(tool, 123)
	wantState(t, runAtom(t, "fleet:check-merge-conflict", ""), 1, hit)

	engine.reset()
	engine.withTree(everyLaneTree)
	for _, code := range []int{0, 1, 123} {
		engine.exitCode(tool, code)
		wantState(t, runAtom(t, "fleet:check-merge-conflict", ""), 0)
	}

	// A scan that was killed or whose binary was not there has not found
	// nothing — it has not looked.
	for _, code := range []int{2, 124, 125, 126, 127} {
		engine.exitCode(tool, code)
		wantState(t, runAtom(t, "fleet:check-merge-conflict", ""), 2,
			"the conflict-marker scan did not complete", "printed nothing")
	}
	engine.exitCode(tool, 126)
	wantState(t, runAtom(t, "fleet:check-merge-conflict", ""), 2, "xargs exit 126")

	// xargs itself killed (137) never reaches the mapping: no Expect covers the
	// signal range, so the engine errors and the atom never ran.
	engine.exitCode(tool, 137)
	wantState(t, runAtom(t, "fleet:check-merge-conflict", ""), 2, "exit code: 137")
}

func TestFleetMergeConflictWithNoCommittableFileNeverBuildsAContainer(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"vendor/a/b.go": ""})
	wantState(t, runAtom(t, "fleet:check-merge-conflict", ""), 0)
	fleetNoContainer(t, "an empty population")
}

func TestFleetMergeConflictEngineFailures(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"grep","-In"`, "exit code: 125: the image would not pull")
	wantState(t, runAtom(t, "fleet:check-merge-conflict", ""), 2, "the atom never ran", "would not pull")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`glob(pattern:"**")`, "the tree went away")
	wantState(t, runAtom(t, "fleet:check-merge-conflict", ""), 2, "the tree would not enumerate")
}

// ---- fleet:stop-justifications ----

// The needles for the two questions the atom asks git.
const (
	sjLsNeedle     = `"git","ls-files"`
	sjOriginNeedle = `"git","remote","get-url","origin"`
)

// sjRepo declares a repository to the paper engine: its files, and the
// tracked subset git lists (every file, unless tracked names them).
func sjRepo(files map[string]string, tracked ...string) {
	engine.reset()
	engine.withTree(files)
	if tracked == nil {
		for p := range files {
			tracked = append(tracked, p)
		}
		sort.Strings(tracked)
	}
	// `git ls-files` without -z: one path per line, as the atom now asks for it.
	engine.stdout(sjLsNeedle, strings.Join(tracked, "\n")+"\n")
	engine.stdout(sjOriginNeedle, "http://ourea.default.svc.cluster.local:8215/x.git\n")
}

// THE SCAN IS GO: git lists the tree and names the repository, the engine
// reads the files, and nothing runs in python or reads foundry-stocks.
func TestFleetStopJustificationsScansTheTrackedTreeInGo(t *testing.T) {
	sjRepo(map[string]string{"a.py": "def f():\n    return 1\n", "b.go": "package b\n"})
	wantState(t, runAtom(t, "fleet:stop-justifications", ""), 0)

	c := engine.chain(sjLsNeedle, "exitCode")
	if !strings.Contains(c, checks.ImageFleet) {
		t.Errorf("the tree is listed in the fleet lane:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withNewFile", `path:"/etc/gitconfig"`, `directory = *`},
		// NOT -z: one path per line, so a repository's file list never reaches
		// the log as a single entry Loki cuts in half (infra #10719).
		[]string{"withExec", `expect:ANY`, `args:["git","ls-files"]`},
	)
	for _, q := range engine.chains() {
		if strings.Contains(q, "python3") || strings.Contains(q, `path:"/stocks"`) {
			t.Errorf("the atom runs no script and mounts no stocks:\n%s", q)
		}
	}
	for _, p := range []string{"a.py", "b.go"} {
		if engine.chain(`file(path:"`+p+`")`, "contents") == "" {
			t.Errorf("%s was never read", p)
		}
	}
	if strings.Contains(c, "GATE_BASE") {
		t.Errorf("rule 8: fleet:stop-justifications must not read GATE_BASE:\n%s", c)
	}
}

func TestFleetStopJustificationsFilesTheScansFindings(t *testing.T) {
	sjRepo(map[string]string{"a.py": "x = 1  # no" + "qa: E501\n", "README.md": "x\n"})
	wantState(t, runAtom(t, "fleet:stop-justifications", ""), 1,
		"stop-justifications: 1 suppression(s) with no stated conflict.", "ruff · noqa", "a.py:1")

	// A file on disk that git does not track is out of scope, as it always was.
	sjRepo(map[string]string{"a.py": "x = 1\n", "scratch.py": "x = 1  # no" + "qa\n"}, "a.py")
	wantState(t, runAtom(t, "fleet:stop-justifications", ""), 0)
}

// A LISTING THE PARSER CANNOT READ IS A COULD-NOT-RUN, not a clean scan. A path
// that will not decode is a file the scan would then silently not look at, and
// "scanned nothing successfully" must never read as "found nothing".
func TestFleetStopJustificationsCannotRunOnAListingItCannotRead(t *testing.T) {
	sjRepo(map[string]string{"a.py": "x = 1\n"})
	engine.stdout(sjLsNeedle, "\"bad\\q.py\"\n")
	wantState(t, runAtom(t, "fleet:stop-justifications", ""), 2, "CANNOT RUN", "refusing to report success")
}

// The repository is what origin names, and DirectoryExempt is keyed on it: a
// cerberus probe driver is excused under cerberus's origin and nowhere else.
func TestFleetStopJustificationsNamesTheRepositoryByItsOrigin(t *testing.T) {
	drive := map[string]string{"probes/drive.py": "p = Popen([x])  # no" + "qa: S603\n"}
	sjRepo(drive)
	engine.stdout(sjOriginNeedle, "git@forgejo.notusmi.com:rob/cerberus.git\n")
	wantState(t, runAtom(t, "fleet:stop-justifications", ""), 0)

	sjRepo(drive)
	wantState(t, runAtom(t, "fleet:stop-justifications", ""), 1, "S603")

	// No origin names no repository, which excuses nothing and refuses nothing.
	sjRepo(drive)
	engine.exitCode(sjOriginNeedle, 2)
	engine.stdout(sjOriginNeedle, "git@forgejo.notusmi.com:rob/cerberus.git\n")
	wantState(t, runAtom(t, "fleet:stop-justifications", ""), 1, "S603")
}

// The fleet's exclude is honoured before a file is fetched: a vendored tree
// costs the engine nothing, and a repository's own config is never read.
func TestFleetStopJustificationsNeverReadsWhatTheFleetExcludes(t *testing.T) {
	sjRepo(map[string]string{
		"vendor/x.py": "x = 1  # no" + "qa\n",
		"src/a.py":    "x = 1\n",
	})
	wantState(t, runAtom(t, "fleet:stop-justifications", ""), 0)
	if q := engine.chain(`file(path:"vendor/x.py")`); q != "" {
		t.Errorf("an excluded file was read:\n%s", q)
	}

}

// Every way the repository will not answer is a CANNOT RUN, never a verdict.
func TestFleetStopJustificationsCannotRunWhenTheRepositoryWillNotAnswer(t *testing.T) {
	files := map[string]string{"a.py": "x = 1\n"}

	sjRepo(files)
	engine.exitCode(sjLsNeedle, 1)
	engine.stderr(sjLsNeedle, "error: index is corrupt")
	wantState(t, runAtom(t, "fleet:stop-justifications", ""), 2,
		"git ls-files failed", "index is corrupt", "refusing to report success without scanning")

	sjRepo(files)
	engine.fail(sjLsNeedle, "exit code: 128")
	wantState(t, runAtom(t, "fleet:stop-justifications", ""), 2, "the atom never ran", "exit code: 128")

	sjRepo(files)
	engine.fail(sjOriginNeedle, "the engine went away")
	wantState(t, runAtom(t, "fleet:stop-justifications", ""), 2, "the atom never ran", "the engine went away")

	sjRepo(files)
	engine.fail(`file(path:"a.py")`, "blob missing")
	wantState(t, runAtom(t, "fleet:stop-justifications", ""), 2, "CANNOT RUN — could not read a.py", "blob missing")
}

// ---- fleet:sast-ruleset-lanes ----

// NO CONTAINER RUNS: the whole question is answered from the Directory.
func TestFleetSastRulesetLanesIsAbsentWithoutRulesSast(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"go.mod": "module x\n"})
	v := runAtom(t, "fleet:sast-ruleset-lanes", "")
	wantState(t, v, 0, "ABSENT", "no rules/sast in this tree")
	if v.Result != "absent" {
		t.Errorf("want absent, got %q", v.Result)
	}
	fleetNoContainer(t, "no rules/ directory")

	engine.reset()
	engine.withTree(map[string]string{"go.mod": "module x\n", "rules/other/x.yml": ""})
	v = runAtom(t, "fleet:sast-ruleset-lanes", "")
	wantState(t, v, 0, "ABSENT")
	if v.Result != "absent" {
		t.Errorf("rules/ without sast/ is still absent, got %q", v.Result)
	}
	fleetNoContainer(t, "rules/ without rules/sast/")
}

func TestFleetSastRulesetLanesPassesWithoutAContainer(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "fleet:sast-ruleset-lanes", ""), 0)
	fleetNoContainer(t, "the ruleset declares every lane")
}

// A lane the ruleset never names is a lane nothing examined — exit 2 by design,
// and the message carries both the declaration and the argument.
func TestFleetSastRulesetLanesNamesTheMissingLanes(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{
		"go.mod":            "module x\n",
		"Cargo.toml":        "[package]\n",
		"rules/sast/py.yml": "rules:\n  - id: a\n    languages: [python]\n",
	})
	v := runAtom(t, "fleet:sast-ruleset-lanes", "")
	wantState(t, v, 2,
		"rules/sast declares [python] but this repo also builds: go(go.mod) rust(Cargo.toml)",
		"A ruleset that never names a lane never examines it",
		"foundry-stocks#4949")
	fleetNoContainer(t, "a missing lane")
}

func TestFleetSastRulesetLanesCannotReadARuleset(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`file(path:"rules/sast/go.yml")`, "the blob would not read")
	wantState(t, runAtom(t, "fleet:sast-ruleset-lanes", ""), 2,
		"rules/sast/go.yml would not read", "the blob would not read")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`glob(pattern:"rules/sast/*.yml")`, "the tree went away")
	wantState(t, runAtom(t, "fleet:sast-ruleset-lanes", ""), 2, "the tree would not enumerate")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`directory(path:"rules"){entries}`, "rules/ would not list")
	wantState(t, runAtom(t, "fleet:sast-ruleset-lanes", ""), 2, "the tree would not enumerate", "would not list")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`directory{entries}`, "the root would not list")
	wantState(t, runAtom(t, "fleet:sast-ruleset-lanes", ""), 2, "the tree would not enumerate")
}

// ---- fleet:orbit-drift ----

func TestFleetOrbitDriftIsAbsentWithoutAnOrbitToml(t *testing.T) {
	engine.reset()
	engine.withTree(fleetTree(nil, "orbit.toml"))
	v := runAtom(t, "fleet:orbit-drift", "")
	wantState(t, v, 0, "ABSENT", "this repo declares no seams")
	if v.Result != "absent" {
		t.Errorf("want absent, got %q", v.Result)
	}
	fleetNoContainer(t, "no orbit.toml")
}

// sha256Of is the digest an orbit.toml edge pins for a contract body.
func sha256Of(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "sha256:" + hex.EncodeToString(sum[:])
}

const seamContract = "verbs = [\"ping\"]\n"

// orbitToml declares one consumed edge per (peer, contract, digest) triple.
func orbitToml(edges ...[3]string) string {
	var b strings.Builder
	for _, e := range edges {
		b.WriteString("[[consumes]]\nfrom = \"" + e[0] + "\"\n")
		if e[1] != "" {
			b.WriteString("contract = \"" + e[1] + "\"\n")
		}
		if e[2] != "" {
			b.WriteString("digest = \"" + e[2] + "\"\n")
		}
	}
	return b.String()
}

// serveContracts answers the archive read from a name -> body map; anything
// else is the door's own 404.
func serveContracts(bodies map[string]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Query().Get("path"), "orbits/"), ".toml")
		body, ok := bodies[name]
		if !ok {
			http.Error(w, "archive: no file", http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, body)
	}
}

// THE DOOR IS OUREA, and it is asked for the orbits directory of
// foundry/foundry-dies, owner-qualified — no container runs for it.
func TestFleetOrbitDriftReadsTheContractsFromOurea(t *testing.T) {
	engine.reset()
	engine.withTree(fleetTree(map[string]string{
		"orbit.toml": orbitToml([3]string{"hades", "a-b", sha256Of(seamContract)}),
	}))
	asks := fakeDoor(t, serveContracts(map[string]string{"a-b": seamContract}))

	wantReport(t, runAtom(t, "fleet:orbit-drift", ""), 0, "1 seam(s) agree with foundry-dies/orbits")

	want := []doorAsk{{"foundry/foundry-dies", "orbits/a-b.toml"}}
	if !reflect.DeepEqual(*asks, want) {
		t.Errorf("the door was asked %v, want %v", *asks, want)
	}
	fleetNoContainer(t, "orbit-drift reads the door from the module")
	if strings.Contains(strings.Join(engine.chains(), "\n"), "GATE_BASE") {
		t.Errorf("rule 8: fleet:orbit-drift must not read GATE_BASE")
	}
	if checks.OureaDoor != "https://git.notusmi.com" {
		t.Errorf("the production door is %q, want git.notusmi.com", checks.OureaDoor)
	}
}

func TestFleetOrbitDriftLadderMatchesTheScriptItReplaced(t *testing.T) {
	good := sha256Of(seamContract)
	for _, tc := range []struct {
		name    string
		orbit   string
		door    http.HandlerFunc
		dead    bool
		state   int
		needles []string
	}{
		{"empty", "# no seams\n", nil, false, 0, []string{"declares no seams"}},
		{"unparseable", "this is = = not toml", nil, false, 2, []string{"orbit.toml did not parse"}},
		{"no contract named", orbitToml([3]string{"hades", "", ""}), nil, false, 1,
			[]string{"orbit-drift: consumes hades names no contract", "compared nothing"}},
		{"agrees", orbitToml([3]string{"hades", "c", good}), serveContracts(map[string]string{"c": seamContract}), false, 0,
			[]string{"1 seam(s) agree"}},
		{"digest moved", orbitToml([3]string{"hades", "c", "sha256:old"}), serveContracts(map[string]string{"c": seamContract}), false, 1,
			[]string{"hades: contract 'c' hashes to " + good + ", orbit.toml records sha256:old", "Re-lay orbit.toml from the die"}},
		{"no digest", orbitToml([3]string{"hades", "c", ""}), serveContracts(map[string]string{"c": seamContract}), false, 1,
			[]string{"contract 'c' carries no digest", "pins nothing"}},
		{"contract absent from the die", orbitToml([3]string{"hades", "gone", good}), serveContracts(nil), false, 1,
			[]string{"names contract 'gone', which is not in foundry-dies/orbits"}},
		{"door error", orbitToml([3]string{"hades", "c", good}),
			func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", http.StatusServiceUnavailable) }, false, 2,
			[]string{"CANNOT RUN", "answered HTTP 503", "git.notusmi.com", "archive?repo=foundry/foundry-dies&path=orbits/c.toml"}},
		{"door unreachable", orbitToml([3]string{"hades", "c", good}), nil, true, 2,
			[]string{"CANNOT RUN - the door is unreachable"}},
		{"html is not a contract", orbitToml([3]string{"hades", "c", good}), serveContracts(map[string]string{"c": "<html>login</html>"}), false, 2,
			[]string{"did not answer a TOML document", "not a contract that disagrees"}},
		{"not utf-8", orbitToml([3]string{"hades", "c", good}), serveContracts(map[string]string{"c": "verbs = \"\xff\"\n"}), false, 2,
			[]string{"did not answer a TOML document", "UTF-8"}},
		{"toml without verbs", orbitToml([3]string{"hades", "c", good}), serveContracts(map[string]string{"c": "x = 1\n"}), false, 2,
			[]string{"carries no verbs key"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine.reset()
			engine.withTree(fleetTree(map[string]string{"orbit.toml": tc.orbit}))
			switch {
			case tc.dead:
				deadDoor(t)
			case tc.door != nil:
				fakeDoor(t, tc.door)
			default:
				asks := fakeDoor(t, serveContracts(nil))
				defer func() {
					if len(*asks) != 0 {
						t.Errorf("settled before any fetch, yet the door was asked %v", *asks)
					}
				}()
			}
			wantReport(t, runAtom(t, "fleet:orbit-drift", ""), tc.state, tc.needles...)
		})
	}
}

// Edges are read from both directions, and from both TOML spellings.
func TestFleetOrbitDriftReadsEveryEdgeInBothDirectionsAndSpellings(t *testing.T) {
	engine.reset()
	engine.withTree(fleetTree(map[string]string{
		"orbit.toml": "consumes = [{ from = \"a\", contract = \"one\", digest = \"" + sha256Of(seamContract) + "\" }]\n" +
			"[[produces]]\nto = \"b\"\ncontract = \"two\"\ndigest = \"" + sha256Of(seamContract) + "\"\n" +
			"[[produces]]\ncontract = \"three\"\n",
	}))
	asks := fakeDoor(t, serveContracts(map[string]string{"one": seamContract, "two": seamContract, "three": seamContract}))
	wantReport(t, runAtom(t, "fleet:orbit-drift", ""), 1, "?: contract 'three' carries no digest")
	if len(*asks) != 3 || (*asks)[0].Path != "orbits/one.toml" || (*asks)[1].Path != "orbits/two.toml" {
		t.Errorf("want consumes then produces, three reads; got %v", *asks)
	}
}

func TestFleetOrbitDriftNeverRunsWhenTheTreeWillNotAnswer(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`directory{entries}`, "the tree went away")
	wantReport(t, runAtom(t, "fleet:orbit-drift", ""), 2, "the tree would not enumerate")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.failLeaf(`"orbit.toml"`, "contents", "orbit.toml would not read")
	wantReport(t, runAtom(t, "fleet:orbit-drift", ""), 2, "CANNOT RUN - orbit.toml did not parse", "would not read")
}

// ---- fleet:opengrep-sast ----

func TestFleetOpengrepIsAbsentWithoutRulesSast(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{"go.mod": "module x\n"})
	v := runAtom(t, "fleet:opengrep-sast", "")
	wantState(t, v, 0, "ABSENT", "no rules/sast in this tree")
	if v.Result != "absent" {
		t.Errorf("want absent, got %q", v.Result)
	}
	fleetNoContainer(t, "no rules/ directory")

	engine.reset()
	engine.withTree(map[string]string{"go.mod": "module x\n", "rules/other/x.yml": ""})
	wantState(t, runAtom(t, "fleet:opengrep-sast", ""), 0, "ABSENT")
	fleetNoContainer(t, "rules/ without rules/sast/")
}

// The binary is BAKED INTO THE LANE IMAGE: `opengrep --version` under the
// default Expect IS the provisioning probe, and the curl-the-installer
// fallback that made this atom cannot-run fleet-wide is gone.
func TestFleetOpengrepProbesTheBakedBinaryAndSetsTheLocale(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)

	wantState(t, runAtom(t, "fleet:opengrep-sast", ""), 0)

	c := engine.chain(`"opengrep","scan"`, "exitCode")
	wantCalls(t, c,
		// opengrep decodes source files by the locale; a lane image with none
		// set reads UTF-8 bytes as ASCII.
		[]string{"withEnvVariable", `name:"LANG"`, `value:"C.UTF-8"`},
		[]string{"withEnvVariable", `name:"LC_ALL"`, `value:"C.UTF-8"`},
		[]string{"withExec", `args:["opengrep","--version"]`},
		[]string{"withExec", `expect:ANY`, `args:["opengrep","scan","--config","rules/sast","--error","."]`},
	)
	if hasCall(c, "withExec", `"opengrep","--version"`, `expect:ANY`) {
		t.Errorf("the version probe is provisioning and must run under the default Expect:\n%s", c)
	}
	// opengrep is provisioned ONCE by the lane, from the mirror at its pin,
	// as a file — never fetched by the atom, never through curl.
	if !hasCall(c, "withFile", `path:"/usr/local/bin/opengrep"`) || engine.chain(`http(url:"`+checks.OpengrepURL+`")`) == "" {
		t.Errorf("the lane provisions opengrep from the mirror at its pin:\n%s", c)
	}
	if strings.Contains(c, `"curl","-`) || strings.Count(c, `path:"/usr/local/bin/opengrep"`) != 1 {
		t.Errorf("opengrep is laid in once and never curled:\n%s", c)
	}
}

// THE WHOLE DEFECT. "Ran N rules on 0 files: 0 findings" exits ZERO, and
// output() folds stderr in only for a NON-ZERO exit — so a refusal that reads
// only stdout on a clean exit hands the zero-file case back its green.
func TestFleetOpengrepRefusesAZeroFileScanOnEitherStream(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.stdout(`"opengrep","scan"`, "Ran 42 rules on 0 files: 0 findings.")
	wantState(t, runAtom(t, "fleet:opengrep-sast", ""), 2,
		"Ran 42 rules on 0 files", opengrepRefusal)

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.stderr(`"opengrep","scan"`, "Ran 42 rules on 0 files: 0 findings.")
	wantState(t, runAtom(t, "fleet:opengrep-sast", ""), 2,
		"Ran 42 rules on 0 files", opengrepRefusal)

	// A scan that examined files and found nothing is a pass, on either stream.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.stdout(`"opengrep","scan"`, "Ran 42 rules on 2636 files: 0 findings.")
	wantState(t, runAtom(t, "fleet:opengrep-sast", ""), 0)
}

func TestFleetOpengrepMapsTheScansExit(t *testing.T) {
	const tool = `"opengrep","scan"`
	engine.reset()
	engine.withTree(everyLaneTree)

	engine.exitCode(tool, 1)
	engine.stdout(tool, "src/x.py:12: dangerous-subprocess")
	wantState(t, runAtom(t, "fleet:opengrep-sast", ""), 1, "dangerous-subprocess")

	engine.exitCode(tool, 2)
	engine.stderr(tool, "invalid rule in rules/sast/go.yml")
	wantState(t, runAtom(t, "fleet:opengrep-sast", ""), 2, "invalid rule")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"opengrep","--version"`, "exit code: 127: opengrep: not found")
	wantState(t, runAtom(t, "fleet:opengrep-sast", ""), 2, "the atom never ran", "opengrep: not found")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`directory{entries}`, "the tree went away")
	wantState(t, runAtom(t, "fleet:opengrep-sast", ""), 2, "the tree would not enumerate")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`directory(path:"rules"){entries}`, "rules/ would not list")
	wantState(t, runAtom(t, "fleet:opengrep-sast", ""), 2, "the tree would not enumerate", "would not list")
}

// ---- fleet:witness ----

// fleet:witness's needles: each git exec the atom runs, by the words only it has.
const (
	wSnapNeedle   = `"git","config","--get","ca.snapshot"`
	wBaseNeedle   = `"rev-parse","--verify","--quiet","base-sha^{commit}"`
	wMergeNeedle  = `"git","merge-base","base-sha","HEAD"`
	wDiffNeedle   = `"--diff-filter=AMR","since0..HEAD"`
	wParentNeedle = `"rev-parse","--verify","--quiet","HEAD^"`
	wTipNeedle    = `"--diff-filter=AMR","HEAD^..HEAD"`
	wRootNeedle   = `"git","show","--pretty="`
	wOriginNeedle = `"remote","get-url","origin"`
)

// witnessAnswer is narcissus's HTTP answer to one tools/call.
type witnessAnswer struct {
	status      int
	contentType string
	body        string
	err         error
}

// witnessResult wraps a witness body (the verb's JSON) in the MCP envelope
// narcissus sends back.
func witnessResult(body string) witnessAnswer {
	text, _ := json.Marshal(body)
	return witnessAnswer{200, "application/json", `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":` + string(text) + `}]}}`, nil}
}

// answerWitness stands in for narcissus for the rest of the test: each asked
// path gets its answer, or "*"'s. It answers the request bodies it was sent.
func answerWitness(t *testing.T, answers map[string]witnessAnswer) *[]string {
	t.Helper()
	var mu sync.Mutex
	asked := &[]string{}
	prev := askWitness
	askWitness = func(_ context.Context, body string) (int, string, string, error) {
		var req struct {
			Params struct {
				Arguments struct {
					Path string `json:"path"`
				} `json:"arguments"`
			} `json:"params"`
		}
		_ = json.Unmarshal([]byte(body), &req)
		mu.Lock()
		*asked = append(*asked, body)
		mu.Unlock()
		a, ok := answers[req.Params.Arguments.Path]
		if !ok {
			a = answers["*"]
		}
		return a.status, a.contentType, a.body, a.err
	}
	t.Cleanup(func() { askWitness = prev })
	return asked
}

// scriptWitness answers a pull whose change set holds one python source, a
// TypeScript file, a vendored go file and a README, from a door clone of nereus.
func scriptWitness(tree map[string]string) {
	engine.reset()
	engine.withTree(fleetTree(map[string]string{"src/x.py": "def f():\n    return 1\n"}))
	engine.withTree(tree)
	engine.stdout(wMergeNeedle, sinceSha+"\n")
	engine.stdout(wDiffNeedle, "src/x.py\nweb/a.ts\nvendor/v/y.go\nREADME.md\n")
	engine.stdout(wOriginNeedle, "http://ourea:8215/nereus.git\n")
}

const witnessNovel = `{"verdict":"novel","novelty":{}}`

// Each changed source file the star authored is asked about, whole, from Go —
// no script, no python — and the rest of the change set is named, not asked.
func TestFleetWitnessAsksAboutEachChangedSourceFile(t *testing.T) {
	scriptWitness(nil)
	asked := answerWitness(t, map[string]witnessAnswer{"*": witnessResult(witnessNovel)})
	wantState(t, runAtom(t, "fleet:witness", "base-sha"), 0)

	if len(*asked) != 1 {
		t.Fatalf("asked about %d file(s), want the one python source:\n%v", len(*asked), *asked)
	}
	for _, w := range []string{`"method":"tools/call"`, `"name":"witness"`, `"granularity":"code"`, `"language":"python"`,
		`"id":1,`, `"path":"src/x.py"`, `"caller":"ci:gate:nereus@HEAD"`, `"query":"def f():\n    return 1\n"`} {
		if !strings.Contains((*asked)[0], w) {
			t.Errorf("the request lacks %s:\n%s", w, (*asked)[0])
		}
	}
	c := engine.chain(wDiffNeedle, "stdout")
	wantCalls(t, c,
		[]string{"withEnvVariable", `name:"GATE_BASE"`, `value:"base-sha"`},
		[]string{"withExec", `args:["git","--version"]`},
		[]string{"withExec", "expect:ANY", `args:["git","diff","--name-only","--diff-filter=AMR","since0..HEAD"]`},
	)
	if hasCall(c, "withExec", `args:["git","--version"]`, "expect:ANY") {
		t.Errorf("the git probe is provisioning and must run under the default Expect:\n%s", c)
	}
	for _, relic := range []string{`path:"/stocks"`, `"python3"`, "WITNESS_DIR"} {
		if strings.Contains(c, relic) {
			t.Errorf("the ported atom still carries %s:\n%s", relic, c)
		}
	}

	// A canonical-class match is a finding, with its checklist in the table.
	scriptWitness(nil)
	answerWitness(t, map[string]witnessAnswer{"*": witnessResult(
		`{"verdict":"convention","novelty":{"canonical":{"class":"Adapter","descriptor":"an adapter","footguns":["forgets to close"]}}}`)})
	wantState(t, runAtom(t, "fleet:witness", "base-sha"), 1,
		"narcissus — fleet:witness: findings in 1 of 1 file(s): src/x.py: matches canonical class 'Adapter' — 1 known failure mode(s) to check",
		"| src/x.py | convention | finding |", "- src/x.py · Adapter: forgets to close")

	// Every failure to hear from the witness is that file's could-not-consult.
	for name, a := range map[string]witnessAnswer{
		"HTTP 503":          {503, "text/plain", "busy", nil},
		"no connection":     {0, "", "", errors.New("dial tcp: connection refused")},
		"an erroring verb":  {200, "application/json", `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"boom"}}`, nil},
		"an unknown corpus": witnessResult(`{"verdict":"unknown"}`),
	} {
		t.Run(name, func(t *testing.T) {
			scriptWitness(nil)
			answerWitness(t, map[string]witnessAnswer{"*": a})
			wantState(t, runAtom(t, "fleet:witness", "base-sha"), 2, "could not consult 1 of 1 file(s): src/x.py: ")
		})
	}

	// An event stream carries the same envelope on its last data: line.
	scriptWitness(nil)
	novel := witnessResult(`{"verdict":"standard","recommendation":"use the one in stellar_core"}`)
	answerWitness(t, map[string]witnessAnswer{"*": {200, "text/event-stream", "event: message\ndata: " + novel.body + "\n\n", nil}})
	wantState(t, runAtom(t, "fleet:witness", "base-sha"), 1, "duplicates a standard: use the one in stellar_core")

	// A file the tree cannot give up is never asked about, and says why.
	scriptWitness(nil)
	delete(engine.tree, "src/x.py")
	asked = answerWitness(t, map[string]witnessAnswer{"*": witnessResult(witnessNovel)})
	wantState(t, runAtom(t, "fleet:witness", "base-sha"), 2, "src/x.py: could not ask: ")
	if len(*asked) != 0 {
		t.Errorf("a file that did not read was asked about")
	}
}

// The rows keep git's order whatever order the answers arrive in, and the
// origin that cannot be read names an unknown star rather than failing.
func TestFleetWitnessKeepsGitsOrderAndNamesItsStar(t *testing.T) {
	scriptWitness(map[string]string{"a.go": "package a\n", "b.py": "def g(): pass\n"})
	engine.stdout(wDiffNeedle, "b.py\na.go\nsrc/x.py\n")
	engine.exitCode(wOriginNeedle, 2)
	asked := answerWitness(t, map[string]witnessAnswer{
		"b.py": witnessResult(`{"verdict":"standard","recommendation":"b"}`),
		"*":    witnessResult(witnessNovel),
	})
	v := runAtom(t, "fleet:witness", "base-sha")
	wantState(t, v, 1, "findings in 1 of 3 file(s): b.py: duplicates a standard: b")
	if b, x := strings.Index(v.Reason, "| b.py |"), strings.Index(v.Reason, "| src/x.py |"); b < 0 || x < b || strings.Index(v.Reason, "| a.go |") > x {
		t.Errorf("the table is not in git's order:\n%s", v.Reason)
	}
	for _, body := range *asked {
		if !strings.Contains(body, `"caller":"ci:gate:unknown@HEAD"`) {
			t.Errorf("an unreadable origin must name the star unknown: %s", body)
		}
		if strings.Contains(body, `"path":"a.go"`) && !strings.Contains(body, `"language":"go"`) {
			t.Errorf("a go file is asked about as go: %s", body)
		}
	}
}

func TestFleetWitnessStandsDownOrCannotRun(t *testing.T) {
	cases := map[string]struct {
		base   string
		script func()
		state  int
		// reason is checked only off a finding or could-not-run: a pass keeps no
		// output (checks.VerdictOf), so a stand-down is told apart by what ran.
		reason  []string
		reached []string
		never   []string
		asks    int
	}{
		"a snapshot has no change set": {"base-sha", func() { engine.stdout(wSnapNeedle, "linked-worktree") }, 0, nil,
			[]string{wSnapNeedle}, []string{wBaseNeedle, wDiffNeedle}, 0},
		"a base the history lacks": {"base-sha", func() { engine.exitCode(wBaseNeedle, 1) }, 2,
			[]string{"could not read the change set: the base base-sha is not in this history"}, nil, []string{wMergeNeedle, wDiffNeedle}, 0},
		"no merge base": {"base-sha", func() { engine.exitCode(wMergeNeedle, 1) }, 2,
			[]string{"could not read the change set: git found no merge base between the base base-sha and HEAD (exit 1)"}, []string{wMergeNeedle}, []string{wDiffNeedle}, 0},
		"git cannot diff": {"base-sha", func() {
			engine.exitCode(wDiffNeedle, 1)
			engine.stderr(wDiffNeedle, "fatal: bad object")
		}, 2, []string{"could not read the change set: ", "fatal: bad object"}, nil, []string{wOriginNeedle}, 0},
		"nothing the star authored": {"base-sha", func() { engine.stdout(wDiffNeedle, "web/a.ts\nvendor/v/y.go\n") }, 0, nil,
			[]string{wDiffNeedle}, []string{wOriginNeedle}, 0},
		"the tip against its parent": {"", func() { engine.stdout(wTipNeedle, "src/x.py\n") }, 0, nil,
			[]string{wParentNeedle, wTipNeedle}, []string{wRootNeedle, wBaseNeedle}, 1},
		"a root commit carries everything": {"", func() {
			engine.exitCode(wParentNeedle, 1)
			engine.stdout(wRootNeedle, "src/x.py\n")
		}, 0, nil, []string{wRootNeedle}, []string{wTipNeedle}, 1},
		"the snapshot check never ran": {"base-sha", func() { engine.fail(wSnapNeedle, "engine gone") }, 2, []string{"never ran"}, nil, []string{wDiffNeedle}, 0},
		"the base check never ran":     {"base-sha", func() { engine.fail(wBaseNeedle, "engine gone") }, 2, []string{"never ran"}, nil, []string{wMergeNeedle}, 0},
		"the merge base never ran":     {"base-sha", func() { engine.fail(wMergeNeedle, "engine gone") }, 2, []string{"never ran"}, nil, []string{wDiffNeedle}, 0},
		"the diff never ran":           {"base-sha", func() { engine.fail(wDiffNeedle, "engine gone") }, 2, []string{"never ran"}, nil, []string{wOriginNeedle}, 0},
		"the parent check never ran":   {"", func() { engine.fail(wParentNeedle, "engine gone") }, 2, []string{"never ran"}, nil, []string{wTipNeedle}, 0},
		"the origin never read":        {"base-sha", func() { engine.fail(wOriginNeedle, "engine gone") }, 2, []string{"never ran"}, nil, nil, 0},
		"no git in the image":          {"base-sha", func() { engine.fail(`args:["git","--version"]`, "exec: git: not found") }, 2, []string{"never ran", "git: not found"}, nil, nil, 0},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			scriptWitness(nil)
			asked := answerWitness(t, map[string]witnessAnswer{"*": witnessResult(witnessNovel)})
			if c.script != nil {
				c.script()
			}
			wantState(t, runAtom(t, "fleet:witness", c.base), c.state, c.reason...)
			if len(*asked) != c.asks {
				t.Errorf("asked about %d file(s), want %d", len(*asked), c.asks)
			}
			for _, n := range c.reached {
				if engine.chain(n) == "" {
					t.Errorf("never reached %s", n)
				}
			}
			for _, n := range c.never {
				if engine.chain(n) != "" {
					t.Errorf("went on to %s", n)
				}
			}
		})
	}
}

// The witness reads history, so it gets the throwaway repository too when the
// tree is a linked worktree — and the snapshot mark is what it reads first.
func TestFleetWitnessRebuildsALinkedWorktreesRepository(t *testing.T) {
	engine.reset()
	engine.withTree(fleetTree(map[string]string{".git": "gitdir: /home/rob/x/.git/worktrees/y\n"}, ".git/HEAD"))
	engine.stdout(wMergeNeedle, sinceSha+"\n")
	answerWitness(t, map[string]witnessAnswer{"*": witnessResult(witnessNovel)})
	wantState(t, runAtom(t, "fleet:witness", "base-sha"), 0)
	wantCalls(t, engine.chain(wSnapNeedle, "stdout"),
		[]string{"withExec", `args:["git","init","-q","."]`},
		[]string{"withExec", `args:["git","config","--local","ca.snapshot","linked-worktree"]`},
		[]string{"withExec", `args:["git","add","-A"]`},
		[]string{"withExec", `args:["git","remote","add","origin","/home/rob/x.git"]`},
	)
}

// ---- fleet:hadolint ----

// hadolintPinned scripts the one thing every hadolint happy path needs: the
// version probe answering the pin. The paper engine's default stdout is "",
// which the atom reads — correctly — as a binary that is not the pinned one.
func hadolintPinned() {
	engine.stdout(`"hadolint","--version"`, "Haskell Dockerfile Linter "+checks.HadolintVersion+"\n")
}

// hadolintTool is the tool exec's distinguishing text: the fleet config path
// appears in no other exec.
const hadolintTool = `"--config","` + checks.HadolintConfigPath + `"`

// repoHadolintConfig is the repository's own file, spelled by concatenation:
// registry_test.go reads every literal in atoms_*.go — this file included —
// and refuses the name, which is the guard this test exists to exercise, not
// to trip.
const repoHadolintConfig = ".hadolint" + ".yaml"

func TestFleetHadolintIsAbsentWithoutADockerfile(t *testing.T) {
	engine.reset()
	engine.withTree(fleetTree(nil, "Dockerfile"))
	v := runAtom(t, "fleet:hadolint", "")
	wantState(t, v, 0, "ABSENT", "tracks no Dockerfile or Containerfile")
	if v.Result != "absent" {
		t.Errorf("want absent, got %q", v.Result)
	}
	fleetNoContainer(t, "no Dockerfile in the tree")

	// A vendored Dockerfile is not this repository's: the fleet exclude drops
	// it before the predicate sees it, and the atom stands down ABSENT rather
	// than linting a dependency's build recipe.
	engine.reset()
	engine.withTree(fleetTree(map[string]string{
		"vendor/go.opentelemetry.io/otel/dependencies.Dockerfile": "FROM scratch\n",
	}, "Dockerfile"))
	wantState(t, runAtom(t, "fleet:hadolint", ""), 0, "ABSENT")
	fleetNoContainer(t, "only a vendored Dockerfile")
}

// THE FLEET'S RULESET REACHES hadolint AS A FILE OUTSIDE /src, NAMED BY
// --config — which is what shuts the repository's .hadolint.yaml out — and
// the binary is fetched at its pin, mirror first, and proved before it judges.
func TestFleetHadolintProvisionsThePinAndPointsItAtTheFleetRuleset(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	hadolintPinned()

	wantState(t, runAtom(t, "fleet:hadolint", ""), 0)

	c := engine.chain(hadolintTool, "exitCode")
	if !strings.Contains(c, checks.ImageFleet) {
		t.Errorf("fleet:hadolint must run in the fleet lane image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withEnvVariable", `name:"CI"`, `value:"true"`},
		[]string{"withMountedDirectory", `path:"/src"`},
		[]string{"withWorkdir", `path:"/src"`},
		[]string{"withFile", `path:"/usr/local/bin/hadolint"`, `permissions:493`},
		[]string{"withNewFile", `path:"` + checks.HadolintConfigPath + `"`, `failure-threshold: info`, `trustedRegistries:`},
		[]string{"withExec", `args:["hadolint","--version"]`},
		[]string{"withExec", `expect:ANY`, `args:["hadolint","--no-color","--config","` + checks.HadolintConfigPath + `","--","Dockerfile"]`},
	)
	if hasCall(c, "withExec", `"hadolint","--version"`, `expect:ANY`) {
		t.Errorf("the version probe is provisioning and must run under the default Expect:\n%s", c)
	}
	if engine.chain(`http(url:"`+checks.HadolintURL+`")`, "id") == "" {
		t.Errorf("the happy path must place the release URL's file:\n%v", engine.chains())
	}
	if strings.Contains(c, "GATE_BASE") {
		t.Errorf("rule 8: fleet:hadolint must not read GATE_BASE — it would key the cache on the pull:\n%s", c)
	}
	if strings.Contains(c, "disable-ignore-pragma") || strings.Contains(c, "no-fail") {
		t.Errorf("pragmas stay honoured and the exit code stays live:\n%s", c)
	}
	// The config is placed OUTSIDE the tree: nothing writes into /src, no
	// chain reads the repository's own file, and --config never names it.
	if hasCall(c, "withNewFile", `path:"/src/`) || hasCall(c, "withExec", `"--config","`+repoHadolintConfig+`"`) {
		t.Errorf("the ruleset is the fleet's, written outside /src, and --config never names the repo's file:\n%s", c)
	}
	for _, q := range engine.chains() {
		if hasCall(q, "file", `path:"`+repoHadolintConfig+`"`) {
			t.Errorf("no chain may read the repository's %s:\n%s", repoHadolintConfig, q)
		}
	}
}

// EVERY DOCKERFILE THE REPOSITORY SHIPS, IN ONE ARGV, SORTED — and nothing
// that is not one. Rule 4 with no xargs: the population is ones per repo.
func TestFleetHadolintHandsTheWholeDockerfilePopulationToOneExec(t *testing.T) {
	engine.reset()
	engine.withTree(fleetTree(map[string]string{
		"bases/go-ci/Dockerfile":            "FROM scratch\n",
		"docker/serving/serving.Dockerfile": "FROM scratch\n",
		"Containerfile.dev":                 "FROM scratch\n",
		"src/trash/Dockerfile-old":          "FROM scratch\n", // hyphenated: not a Dockerfile
		"vendor/x/Dockerfile":               "FROM scratch\n", // vendored: the exclude's
		"docs/dockerfiles.rst":              "",
	}))
	hadolintPinned()
	wantState(t, runAtom(t, "fleet:hadolint", ""), 0)

	c := engine.chain(hadolintTool, "exitCode")
	want := `args:["hadolint","--no-color","--config","` + checks.HadolintConfigPath + `","--",` +
		`"Containerfile.dev","Dockerfile","bases/go-ci/Dockerfile","docker/serving/serving.Dockerfile"]`
	if !hasCall(c, "withExec", want) {
		t.Errorf("the tool exec must name every Dockerfile once, sorted, and nothing else; want %s in:\n%s", want, c)
	}
	if strings.Contains(c, "Dockerfile-old") || strings.Contains(c, "vendor/") {
		t.Errorf("a hyphenated name and a vendored file are not this repository's Dockerfiles:\n%s", c)
	}
	if strings.Contains(c, `"xargs"`) {
		t.Errorf("Dockerfiles number in the ones; the population is the argv, not an xargs list:\n%s", c)
	}
}

// THE TOOL'S OWN EXIT IS THE VERDICT: 0 pass, 1 findings (a rule at the
// threshold OR a Dockerfile that does not parse), anything else could-not-run.
func TestFleetHadolintMapsTheToolsExit(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	hadolintPinned()

	engine.exitCode(hadolintTool, 1)
	engine.stdout(hadolintTool, "Dockerfile:9 DL3025 warning: Use arguments JSON notation for CMD and ENTRYPOINT arguments")
	wantState(t, runAtom(t, "fleet:hadolint", ""), 1, "DL3025", "JSON notation")

	engine.exitCode(hadolintTool, 1)
	engine.stdout(hadolintTool, "Dockerfile:2:4 missing whitespace")
	wantState(t, runAtom(t, "fleet:hadolint", ""), 1, "missing whitespace")

	// A kill (137) is not an exit the tool gives: no Expect covers the signal
	// range, so the engine errors and the atom never ran.
	engine.exitCode(hadolintTool, 137)
	engine.stdout(hadolintTool, "")
	wantState(t, runAtom(t, "fleet:hadolint", ""), 2, "the atom never ran", "exit code: 137")

	// A code the tool does give past 1 stays could-not-run on its own sentence.
	engine.exitCode(hadolintTool, 127)
	wantState(t, runAtom(t, "fleet:hadolint", ""), 2, "CANNOT RUN (exit 127)")

	// Info-level notes below the threshold exit 0 and are a pass.
	engine.exitCode(hadolintTool, 0)
	engine.stdout(hadolintTool, "Dockerfile:12 DL3064 info: Potentially sensitive data should not be used in the `ARG` or `ENV` commands")
	wantState(t, runAtom(t, "fleet:hadolint", ""), 0)
}

// THE PIN IS PART OF THE QUESTION: a binary that answers another version, or
// no version, has not been proved to carry the pinned rules.
func TestFleetHadolintRefusesABinaryThatIsNotThePin(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.stdout(`"hadolint","--version"`, "Haskell Dockerfile Linter 2.12.0\n")
	wantState(t, runAtom(t, "fleet:hadolint", ""), 2,
		"CANNOT RUN", "does not answer", checks.HadolintVersion, "2.12.0", "never linted is not a Dockerfile that passed")
	if engine.chain(hadolintTool, "exitCode") != "" {
		t.Errorf("no Dockerfile may be judged by a binary that is not the pin:\n%v", engine.chains())
	}

	// The engine's default — an empty answer — is refused too.
	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "fleet:hadolint", ""), 2, "does not answer")
}

// THE BINARY IS FETCHED FROM ITS RELEASE URL, and a failed fetch is a
// could-not-run rather than a fallthrough: no second address is tried.
func TestFleetHadolintRefusesWhenTheReleaseURLFails(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	hadolintPinned()
	engine.fail("hadolint/hadolint/releases", "404")
	wantState(t, runAtom(t, "fleet:hadolint", ""), 2,
		"could not be fetched", checks.HadolintVersion, "never linted")
	fleetNoContainer(t, "the release URL did not serve the binary")
	if strings.Contains(strings.Join(engine.chains(), "\n"), "nexus.notusmi.com") {
		t.Errorf("no fleet address is tried:\n%v", engine.chains())
	}
}

func TestFleetHadolintEngineFailures(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`glob(pattern:"**")`, "the tree went away")
	wantState(t, runAtom(t, "fleet:hadolint", ""), 2, "the tree would not enumerate")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"hadolint","--version"`, "exit code: 126: cannot execute binary file")
	wantState(t, runAtom(t, "fleet:hadolint", ""), 2, "the hadolint version probe never ran", "cannot execute")

	engine.reset()
	engine.withTree(everyLaneTree)
	hadolintPinned()
	engine.failLeaf(hadolintTool, "exitCode", "the engine went away")
	wantState(t, runAtom(t, "fleet:hadolint", ""), 2, "the atom never ran", "the engine went away")
}

// ---- the lane as a whole ----

// Rule 1, read off the wire: every fleet atom's provisioning exec runs under
// the DEFAULT Expect and its tool run is the last exec under ANY. A probe
// under ANY would swallow a failed provision into the tool's exit code.
func TestFleetProbesNeverCarryAnyExit(t *testing.T) {
	probes := []string{
		`"uvx","--from","pre-commit-hooks","check-yaml","--help"`,
		`"python3","--version"`,
		`"uv","--version"`,
		`"opengrep","--version"`,
		`"hadolint","--version"`,
	}
	for _, a := range checks.Atoms {
		if !strings.HasPrefix(a.ID, "fleet:") {
			continue
		}
		engine.reset()
		engine.withTree(everyLaneTree)
		runAtom(t, a.ID, "base-sha")
		for _, q := range engine.chains() {
			for _, p := range probes {
				if hasCall(q, "withExec", p, "expect:ANY") {
					t.Errorf("%s runs a provisioning exec under ANY (%s):\n%s", a.ID, p, q)
				}
			}
		}
	}
}
