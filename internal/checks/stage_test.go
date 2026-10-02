package checks

import (
	"strings"
	"testing"
)

func TestGroupOfSplitsTheFleetFromWhatTheTreeContains(t *testing.T) {
	for id, want := range map[string]string{
		"fleet:hadolint": GroupBasic, "fleet:opengrep-sast": GroupBasic,
		"go:vet": GroupLanguage, "python:ruff-check": GroupLanguage, "ops:ansible": GroupLanguage,
		"compose:config": GroupLanguage, "dies:opa-test": GroupLanguage, "fleetish:x": GroupLanguage,
	} {
		if got := GroupOf(id); got != want {
			t.Errorf("%s: %s, want %s", id, got, want)
		}
	}
}

func TestSettleStageOrdersBasicThenLanguageAndOmitsTheAbsent(t *testing.T) {
	st := SettleStage("check", []Verdict{
		{Atom: "go:vet", State: 0, Result: "pass", Reason: "ok vet"},
		{Atom: "fleet:check-yaml", State: 1, Result: "findings", Reason: "fleet:check-yaml: FINDINGS (exit 1)\nbad.yaml: line 3\n"},
		{Atom: "python:ruff-check", State: 0, Result: "absent", Reason: "python:ruff-check: ABSENT — no pyproject.toml\nsecond line"},
		{Atom: "rust:cargo-fmt", State: 0, Result: "absent", Reason: "ABSENT — no Cargo.toml"},
		{Atom: "python:mypy", State: 0, Result: "absent", Reason: "python:mypy: ABSENT — no pyproject.toml"},
		{Atom: "ops:ansible", State: 2, Result: "cannot-run", Reason: "galaxy 404"},
		{Atom: "fleet:hadolint", State: 0, Result: "pass", Reason: ""},
	})
	var ran, omitted []string
	for _, a := range st.Ran {
		ran = append(ran, a.Atom+"/"+a.Group)
	}
	for _, a := range st.Omitted {
		omitted = append(omitted, a.Atom+"/"+a.Group)
	}
	if strings.Join(ran, ",") != "fleet:check-yaml/basic,fleet:hadolint/basic,go:vet/language,ops:ansible/language" {
		t.Errorf("ran %v", ran)
	}
	if strings.Join(omitted, ",") != "python:ruff-check/language,rust:cargo-fmt/language,python:mypy/language" {
		t.Errorf("omitted %v", omitted)
	}
	if st.State != 2 || st.Name != "check" {
		t.Errorf("state %d name %q: the stage is its worst atom", st.State, st.Name)
	}
	if strings.Join(st.Lanes, ",") != "go,ops" {
		t.Errorf("lanes %v: the language namespaces that ran, sorted, never fleet or an omitted one", st.Lanes)
	}
	want := "── fleet:check-yaml · findings ──\nFINDINGS (exit 1)\nbad.yaml: line 3\n" +
		"── fleet:hadolint · pass ──\n\n" +
		"── go:vet · pass ──\nok vet\n" +
		"── ops:ansible · cannot-run ──\ngalaxy 404\n" +
		"── omitted: no surface here ──\n" +
		"python:ruff-check, python:mypy — ABSENT — no pyproject.toml\n" +
		"rust:cargo-fmt — ABSENT — no Cargo.toml\n"
	if st.Log != want {
		t.Errorf("log\n%s\nwant\n%s", st.Log, want)
	}
	if a := st.Ran[0]; a.State != 1 || a.Result != "findings" || a.Reason != "fleet:check-yaml: FINDINGS (exit 1)\nbad.yaml: line 3\n" {
		t.Errorf("a ran atom carries its own state, result and reason: %+v", a)
	}
}

func TestSettleStageAbsentNeverSettlesAndFindingsAreOne(t *testing.T) {
	if st := SettleStage("check", []Verdict{{Atom: "go:vet", State: 2, Result: "absent", Reason: "x"}}); st.State != 0 || len(st.Ran) != 0 || len(st.Lanes) != 0 {
		t.Errorf("an absent atom settles nothing: %+v", st)
	}
	if st := SettleStage("check", []Verdict{{Atom: "go:vet", State: 1, Result: "findings"}, {Atom: "go:build", State: 0, Result: "pass"}}); st.State != 1 {
		t.Errorf("findings and a pass settle on findings: %d", st.State)
	}
	if st := SettleStage("check", nil); st.State != 0 || st.Log != "" || st.Ran != nil || st.Omitted != nil {
		t.Errorf("no verdicts is an empty clean stage: %+v", st)
	}
}

func TestUnprefixedDropsOnlyTheAtomsOwnName(t *testing.T) {
	got := unprefixed("go:vet", "go:vet: FINDINGS (exit 1)\nmain.go:6: go:vet: quoted\ngo:vetx: other")
	if got != "FINDINGS (exit 1)\nmain.go:6: go:vet: quoted\ngo:vetx: other" {
		t.Errorf("unprefixed %q", got)
	}
}

func TestStopsOnAFindingOrACannotRunButNeverOnAnAbsence(t *testing.T) {
	for _, c := range []struct {
		v    Verdict
		want bool
	}{
		{Verdict{State: 0, Result: "pass"}, false},
		{Verdict{State: 1, Result: "findings"}, true},
		{Verdict{State: 2, Result: "cannot-run"}, true},
		{Verdict{State: 0, Result: "absent"}, false},
		{Verdict{State: 2, Result: "absent"}, false},
	} {
		if got := Stops(c.v); got != c.want {
			t.Errorf("%+v: stops %v, want %v", c.v, got, c.want)
		}
	}
}

func TestTailAfterIsEveryAtomPastTheOneThatStopped(t *testing.T) {
	sel := []AtomDef{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	for i, want := range map[int]string{0: "b,c", 1: "c", 2: ""} {
		if got := strings.Join(TailAfter(sel, i), ","); got != want {
			t.Errorf("TailAfter(%d) = %q, want %q", i, got, want)
		}
	}
	if got := TailAfter(sel, 2); got != nil {
		t.Errorf("the last atom leaves nothing unreached: %v", got)
	}
}

func TestAStageThatStoppedSaysWhatItNeverReached(t *testing.T) {
	st := SettleStage("push", []Verdict{
		{Atom: "go:staticcheck", State: 1, Result: "findings", Reason: "x.go:1: unused"},
	}, "go:build", "go:test-race")
	if strings.Join(st.Unreached, ",") != "go:build,go:test-race" {
		t.Errorf("unreached %v", st.Unreached)
	}
	want := "── go:staticcheck · findings ──\nx.go:1: unused\n" +
		"── not reached: the stage stopped before them ──\ngo:build, go:test-race\n"
	if st.Log != want {
		t.Errorf("log\n%s\nwant\n%s", st.Log, want)
	}
	if st.State != 1 {
		t.Errorf("state %d: the stage settles on the atom that stopped it", st.State)
	}
	if st := SettleStage("push", []Verdict{{Atom: "go:build", State: 0, Result: "pass"}}); len(st.Unreached) != 0 || strings.Contains(st.Log, "not reached") {
		t.Errorf("a stage that ran everything names nothing unreached: %+v", st)
	}
}

func TestLogTailKeepsTheEndAndSaysWhatItDropped(t *testing.T) {
	if got := LogTail("abcdef", 6); got != "abcdef" {
		t.Errorf("a log at the limit is whole: %q", got)
	}
	if got := LogTail("abcdef", 4); got != "… 2 earlier byte(s) of the log dropped …\ncdef" {
		t.Errorf("tail %q", got)
	}
}

// THE STAGES, PINNED. Rob, 2026-09-17: the commit runs the basic checks and the
// language checks — format, lint, tests; the push runs the complex checks —
// staticcheck, vulncheck, build — beside mutation. A change to this table is a
// change to what a commit costs and what a push proves.
func TestTheCommitAndPushStagesHoldTheirAtoms(t *testing.T) {
	ids := func(stage string) string {
		var out []string
		for _, a := range AtomsForStage(stage) {
			out = append(out, a.ID)
		}
		return strings.Join(out, ",")
	}
	commit := "fleet:check-yaml,fleet:check-added-large-files,fleet:check-merge-conflict,fleet:stop-justifications," +
		"fleet:sast-ruleset-lanes,fleet:copier-answers-intact,fleet:ourea-config-retired-keys,fleet:retired-verbs,fleet:opengrep-sast,fleet:hadolint," +
		"ops:shell,ops:chezmoi,ops:yaml,ops:dup,ops:declaration,ops:specs,ops:metrics,ops:ansible,ops:flux,ops:kube-linter," +
		"go:gofmt,go:vet,go:test," +
		"python:ruff-check,python:ruff-format,python:forge-testkit-assertion-free,python:forge-testkit-fake-placement,python:forge-testkit-schema-budget,python:mypy,python:pytest," +
		"rust:cargo-fmt,rust:cargo-clippy,rust:cargo-test," +
		"ts:bun-gate," +
		"compose:config,compose:no-tracked-secrets,compose:third-party-pins," +
		"dies:opa-test,dies:contracts,dies:schema,dies:findings,dies:schemas,dies:canonical"
	push := "fleet:orbit-drift,template:render-matrix,go:staticcheck,go:govulncheck,go:build,go:release,go:test-race,python:pip-audit,python:release,rust:release,rust:cargo-audit,ts:bun-audit,ts:release," +
		"dies:admission-dogfood,dies:data-keys,dies:canary-visibility,fleet:witness"
	if got := ids(StagePrecommit); got != commit {
		t.Errorf("commit stage\n got %s\nwant %s", got, commit)
	}
	if got := ids(StagePrepush); got != push {
		t.Errorf("push stage\n got %s\nwant %s", got, push)
	}
}
