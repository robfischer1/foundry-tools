package checks

import (
	"reflect"
	"strings"
	"testing"
)

func TestFilesOverReadsStatLines(t *testing.T) {
	const limit = 2048 * 1024
	out := strings.Join([]string{
		"1034240 flux/infrastructure/cert-manager.yaml", // 1010 KB — under the fleet's ceiling
		"3145728 assets/model.bin",
		"12 README.md",
		"2097152 exactly/at/the/limit.bin", // 2048 KB — NOT over
		"2097153 one/byte/over.bin",
	}, "\n")
	got := FilesOver(out, limit)
	want := []string{"assets/model.bin", "one/byte/over.bin"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FilesOver = %q, want %q", got, want)
	}
}

// The 1010 KB measurement, asserted as the reason the threshold moved: infra's
// vendored cert-manager CRD bundle is a finding at 500 KiB and is not one at
// 2048 KB.
func TestFilesOverAdmitsTheCertManagerBundle(t *testing.T) {
	const line = "1034240 flux/infrastructure/cert-manager.yaml"
	if got := FilesOver(line, 500*1024); len(got) != 1 {
		t.Errorf("at 500 KiB the cert-manager bundle must be a finding, got %q", got)
	}
	if got := FilesOver(line, 2048*1024); len(got) != 0 {
		t.Errorf("at 2048 KB the cert-manager bundle must not be a finding, got %q", got)
	}
}

// stat writes its failures to stderr and output() folds stderr in on a non-zero
// exit, so error prose reaches this function. It must not be read as a file.
func TestFilesOverSkipsLinesThatAreNotSizeAndPath(t *testing.T) {
	out := strings.Join([]string{
		"stat: cannot statx 'gone.txt': No such file or directory",
		"",
		"   ",
		"notanumber path",
		"99999999 big.bin",
	}, "\n")
	got := FilesOver(out, 1024)
	want := []string{"big.bin"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FilesOver = %q, want %q", got, want)
	}
}

// The awk this replaces rebuilt $0 with OFS, which collapsed runs of spaces in
// a path. Splitting on the first space does not.
func TestFilesOverKeepsSpacesInAPath(t *testing.T) {
	got := FilesOver("4096 docs/two  spaces.md", 1)
	want := []string{"docs/two  spaces.md"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FilesOver = %q, want %q", got, want)
	}
}

// THE TWO BOUNDARIES OF THE SPLIT, named because the comparison that used to
// stand here (`cut <= 0`) carried a mutant the suite could not kill: a line
// starting with a space has cut == 0 and is skipped by the boundary OR by
// ParseInt("") failing, so `<=` and `<` behaved identically on every possible
// input. strings.Cut has no boundary to mutate; these are the cases that pin
// the behaviour it has to keep.
func TestFilesOverSkipsALineWithNoSpaceAndALineThatStartsWithOne(t *testing.T) {
	for _, line := range []string{
		"4194304",              // no space at all: not a `<size> <path>` line
		" 4194304 leading.bin", // the size field is empty
		" ",                    // one space, nothing either side
		"\t4194304 tabbed.bin", // stat writes one space, not a tab
	} {
		if got := FilesOver(line, 1); len(got) != 0 {
			t.Errorf("FilesOver(%q) = %q, want none — a line that is not `<size> <path>` is not a file", line, got)
		}
	}
	// And the line that IS that shape still answers, with one space exactly.
	if got := FilesOver("4194304 a.bin", 1); !reflect.DeepEqual(got, []string{"a.bin"}) {
		t.Errorf("FilesOver = %q, want [a.bin]", got)
	}
}

// THE HITS DOMINATE THE CODE. xargs answers 123 for the ordinary mixed run —
// one chunk matched, another did not — so a non-empty stdout is the only honest
// evidence of a conflict marker.
func TestGrepThroughXargsReadsTheOutputFirst(t *testing.T) {
	const hit = "README.md:12:<<<<<<< HEAD"
	for _, code := range []int{0, 1, 123, 2, 127} {
		if got := GrepThroughXargsState(code, hit); got != int(StateFindings) {
			t.Errorf("exit %d with a hit = %d, want findings", code, got)
		}
	}
}

func TestGrepThroughXargsCleanCodes(t *testing.T) {
	for _, code := range []int{0, 1, 123} {
		if got := GrepThroughXargsState(code, ""); got != int(StatePass) {
			t.Errorf("exit %d with no output = %d, want pass", code, got)
		}
		if got := GrepThroughXargsState(code, "  \n "); got != int(StatePass) {
			t.Errorf("exit %d with blank output = %d, want pass", code, got)
		}
	}
}

// A scan that was killed, or whose binary was not there, has not found nothing
// — it has not looked. 124 is a 255, 125 a signal, 126/127 a binary that would
// not run, 137 an OOM kill.
func TestGrepThroughXargsBrokenScanIsCannotRun(t *testing.T) {
	for _, code := range []int{124, 125, 126, 127, 137, 143, 255} {
		if got := GrepThroughXargsState(code, ""); got != int(StateCannotRun) {
			t.Errorf("exit %d with no output = %d, want cannot-run", code, got)
		}
	}
}

// The mapping that keeps a two-valued hook's finding from reading as a crash.
func TestToolThroughXargsState(t *testing.T) {
	for _, tc := range []struct {
		code int
		want State
	}{
		{0, StatePass},
		{123, StateFindings},
		{1, StateCannotRun},
		{124, StateCannotRun},
		{125, StateCannotRun},
		{126, StateCannotRun},
		{127, StateCannotRun},
		{137, StateCannotRun},
	} {
		if got := ToolThroughXargsState(tc.code); got != int(tc.want) {
			t.Errorf("ToolThroughXargsState(%d) = %d, want %v", tc.code, got, tc.want)
		}
	}
}

// A finding must never be flattened into a could-not-run, which is what
// StateFor alone would do with xargs' 123.
func TestToolThroughXargsRescuesTheFindingStateForWouldLose(t *testing.T) {
	if StateFor(123) != StateCannotRun {
		t.Fatal("premise changed: StateFor(123) is no longer cannot-run")
	}
	if ToolThroughXargsState(123) != int(StateFindings) {
		t.Error("xargs' 123 must reach the verdict as FINDINGS for a two-valued hook")
	}
}

func TestSecretsPopulationDropsFixtures(t *testing.T) {
	in := []string{
		"bases/x.yaml",
		"testdata/secret.json",
		"internal/checks/testdata/a.txt",
		"tests/fixtures/creds.yaml",
		"tests/unit/test_thing.py",
		"docs/testdata.md",
		"src/tests/fixtures/nope.txt",
	}
	want := []string{
		"bases/x.yaml",
		"tests/unit/test_thing.py",
		"docs/testdata.md",
		"src/tests/fixtures/nope.txt", // ^tests/fixtures/ is root-anchored
	}
	if got := SecretsPopulation(in); !reflect.DeepEqual(got, want) {
		t.Errorf("SecretsPopulation = %q, want %q", got, want)
	}
}

// The 200+ false findings on foundry-stocks came from a "./" prefix the
// baseline's keys do not carry. Nothing here adds one.
func TestSecretsPopulationDoesNotRewritePaths(t *testing.T) {
	got := SecretsPopulation([]string{"bases/x.yaml"})
	if len(got) != 1 || got[0] != "bases/x.yaml" {
		t.Errorf("SecretsPopulation rewrote the path: %q", got)
	}
}

func TestSastLanesMissingMirrorsTheShell(t *testing.T) {
	rules := []string{
		"rules:\n  - id: py-thing\n    languages: [python]\n",
		"rules:\n  - id: go-thing\n    languages: [ \"go\" , generic ]\n",
	}
	declared, missing := SastLanesMissing(rules, []string{"go.mod", "pyproject.toml", "Cargo.toml", "package.json", "rules/"})
	wantDeclared := []string{"generic", "go", "python"}
	if !reflect.DeepEqual(declared, wantDeclared) {
		t.Errorf("declared = %q, want %q", declared, wantDeclared)
	}
	wantMissing := []string{"rust(Cargo.toml)", "typescript-or-javascript(package.json)"}
	if !reflect.DeepEqual(missing, wantMissing) {
		t.Errorf("missing = %q, want %q", missing, wantMissing)
	}
}

// EVERY DECLARATION ON A LINE COUNTS, not the first one. Flow-style YAML puts
// several rules on one line, and reading only the first would silently under-
// report the declared set — the ruleset would look like it misses a lane it
// actually names, or (worse, one edit later) like it names one it misses.
// `FindAllString(line, -1)` is what says "all of them"; this is the case that
// kills a mutant which caps it (gremlins 200:56, LIVED on PR #31).
func TestSastLanesMissingReadsEveryDeclarationOnOneLine(t *testing.T) {
	line := `rules: [{id: a, languages: [go]}, {id: b, languages: [rust]}, {id: c, languages: [python]}]`
	declared, missing := SastLanesMissing([]string{line}, []string{"go.mod", "Cargo.toml", "pyproject.toml"})
	if !reflect.DeepEqual(declared, []string{"go", "python", "rust"}) {
		t.Errorf("declared = %q, want [go python rust] — every array on the line counts", declared)
	}
	if len(missing) != 0 {
		t.Errorf("missing = %q, want none", missing)
	}
}

// The bracket is found by the regexp, not by counting characters: no space
// before it, several after it, and a nested `[` inside all read the same.
func TestSastLanesMissingReadsTheArrayWhateverPrecedesTheBracket(t *testing.T) {
	for _, body := range []string{
		"languages:[go]",
		"languages:  [go]",
		"languages:\t[ go ]",
		"    languages: [go]\n",
	} {
		declared, missing := SastLanesMissing([]string{body}, []string{"go.mod"})
		if !reflect.DeepEqual(declared, []string{"go"}) {
			t.Errorf("%q: declared = %q, want [go]", body, declared)
		}
		if len(missing) != 0 {
			t.Errorf("%q: missing = %q, want none", body, missing)
		}
	}
}

// A commented-out rule is not coverage. `grep -vE "^[[:space:]]*#"` dropped
// those lines and so does this.
func TestSastLanesMissingIgnoresCommentedRules(t *testing.T) {
	rules := []string{"  # languages: [rust]\nrules:\n    languages: [go]\n"}
	declared, missing := SastLanesMissing(rules, []string{"go.mod", "Cargo.toml"})
	if !reflect.DeepEqual(declared, []string{"go"}) {
		t.Errorf("declared = %q, want [go]", declared)
	}
	if !reflect.DeepEqual(missing, []string{"rust(Cargo.toml)"}) {
		t.Errorf("missing = %q, want [rust(Cargo.toml)]", missing)
	}
}

// Either name covers the node lane, which is why package.json is not in the
// manifest table.
func TestSastLanesMissingAcceptsEitherNodeName(t *testing.T) {
	for _, lang := range []string{"typescript", "javascript"} {
		_, missing := SastLanesMissing([]string{"languages: [" + lang + "]"}, []string{"package.json"})
		if len(missing) != 0 {
			t.Errorf("%s declared, still missing %q", lang, missing)
		}
	}
}

// A repository that builds nothing the ruleset misses is a pass, and a
// ruleset directory with no readable rule in it still answers about the
// manifests that are there.
func TestSastLanesMissingEdges(t *testing.T) {
	declared, missing := SastLanesMissing(nil, nil)
	if len(declared) != 0 || len(missing) != 0 {
		t.Errorf("empty tree: declared %q missing %q", declared, missing)
	}
	declared, missing = SastLanesMissing(nil, []string{"go.mod"})
	if len(declared) != 0 {
		t.Errorf("declared = %q, want none", declared)
	}
	if !reflect.DeepEqual(missing, []string{"go(go.mod)"}) {
		t.Errorf("missing = %q, want [go(go.mod)]", missing)
	}
	declared, missing = SastLanesMissing([]string{"languages: [go]"}, []string{"go.mod"})
	if !reflect.DeepEqual(declared, []string{"go"}) || len(missing) != 0 {
		t.Errorf("declared %q missing %q, want [go] and none", declared, missing)
	}
}

// The missing list is ordered, so one tree answers the same sentence every run.
func TestSastLanesMissingIsOrdered(t *testing.T) {
	root := []string{"go.mod", "Cargo.toml", "pyproject.toml", "package.json"}
	want := []string{"go(go.mod)", "rust(Cargo.toml)", "python(pyproject.toml)", "typescript-or-javascript(package.json)"}
	for i := 0; i < 8; i++ {
		if _, missing := SastLanesMissing(nil, root); !reflect.DeepEqual(missing, want) {
			t.Fatalf("missing = %q, want %q", missing, want)
		}
	}
}

// foundry-stocks#4415: the summary line that exits 0 having examined nothing.
func TestOpengrepZeroFiles(t *testing.T) {
	if !OpengrepZeroFiles("Ran 42 rules on 0 files: 0 findings.") {
		t.Error("the zero-file summary must be recognised")
	}
	if !OpengrepZeroFiles("scan complete\nRan 1 rules on 0 files\n") {
		t.Error("the zero-file summary must be recognised mid-output")
	}
	for _, out := range []string{
		"Ran 42 rules on 2636 files: 0 findings.",
		"Ran 42 rules on 10 files: 3 findings.",
		"",
		"0 files",
	} {
		if OpengrepZeroFiles(out) {
			t.Errorf("%q must not be read as a zero-file scan", out)
		}
	}
}

func TestHasEntryTakesEitherShape(t *testing.T) {
	entries := []string{"go.mod", "rules/", "orbit.toml", ".secrets.baseline"}
	for _, name := range []string{"go.mod", "rules", "orbit.toml", ".secrets.baseline"} {
		if !HasEntry(entries, name) {
			t.Errorf("HasEntry(%q) = false", name)
		}
	}
	for _, name := range []string{"Cargo.toml", "rule", "rules/sast", ""} {
		if HasEntry(entries, name) {
			t.Errorf("HasEntry(%q) = true", name)
		}
	}
}
