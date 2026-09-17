package checks

import (
	"strings"
	"testing"
)

func TestGroupOfSplitsTheFleetFromWhatTheTreeContains(t *testing.T) {
	for id, want := range map[string]string{
		"fleet:detect-secrets": GroupBasic, "fleet:opengrep-sast": GroupBasic,
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
		{Atom: "fleet:check-yaml", State: 1, Result: "findings", Reason: "bad.yaml: line 3\n"},
		{Atom: "python:ruff-check", State: 0, Result: "absent", Reason: "no pyproject.toml\nsecond line"},
		{Atom: "ops:ansible", State: 2, Result: "cannot-run", Reason: "galaxy 404"},
		{Atom: "fleet:detect-secrets", State: 0, Result: "pass", Reason: ""},
	})
	var ran, omitted []string
	for _, a := range st.Ran {
		ran = append(ran, a.Atom+"/"+a.Group)
	}
	for _, a := range st.Omitted {
		omitted = append(omitted, a.Atom+"/"+a.Group)
	}
	if strings.Join(ran, ",") != "fleet:check-yaml/basic,fleet:detect-secrets/basic,go:vet/language,ops:ansible/language" {
		t.Errorf("ran %v", ran)
	}
	if strings.Join(omitted, ",") != "python:ruff-check/language" {
		t.Errorf("omitted %v", omitted)
	}
	if st.State != 2 || st.Name != "check" {
		t.Errorf("state %d name %q: the stage is its worst atom", st.State, st.Name)
	}
	if strings.Join(st.Lanes, ",") != "go,ops" {
		t.Errorf("lanes %v: the language namespaces that ran, sorted, never fleet or an omitted one", st.Lanes)
	}
	want := "── fleet:check-yaml · findings ──\nbad.yaml: line 3\n" +
		"── fleet:detect-secrets · pass ──\n\n" +
		"── go:vet · pass ──\nok vet\n" +
		"── ops:ansible · cannot-run ──\ngalaxy 404\n" +
		"── omitted: no surface here ──\npython:ruff-check — no pyproject.toml\n"
	if st.Log != want {
		t.Errorf("log\n%s\nwant\n%s", st.Log, want)
	}
	if a := st.Ran[0]; a.State != 1 || a.Result != "findings" || a.Reason != "bad.yaml: line 3\n" {
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
	commit := "fleet:check-yaml,fleet:check-added-large-files,fleet:check-merge-conflict,fleet:detect-secrets,fleet:stop-justifications," +
		"fleet:sast-ruleset-lanes,fleet:opengrep-sast,fleet:hadolint," +
		"ops:shell,ops:chezmoi,ops:yaml,ops:dup,ops:declaration,ops:specs,ops:ansible,ops:flux," +
		"go:gofmt,go:vet,go:test-race," +
		"python:ruff-check,python:ruff-format,python:forge-testkit-assertion-free,python:forge-testkit-fake-placement,python:forge-testkit-schema-budget,python:mypy,python:pytest," +
		"rust:cargo-fmt,rust:cargo-clippy,rust:cargo-test," +
		"ts:bun-gate," +
		"compose:config,compose:no-tracked-secrets,compose:third-party-pins," +
		"dies:opa-test,dies:contracts,dies:schema"
	push := "fleet:orbit-drift,go:build,go:staticcheck,go:govulncheck,python:pip-audit,rust:cargo-audit,ts:bun-audit," +
		"dies:admission-dogfood,dies:data-keys,dies:canary-visibility,fleet:witness"
	if got := ids(StagePrecommit); got != commit {
		t.Errorf("commit stage\n got %s\nwant %s", got, commit)
	}
	if got := ids(StagePrepush); got != push {
		t.Errorf("push stage\n got %s\nwant %s", got, push)
	}
}
