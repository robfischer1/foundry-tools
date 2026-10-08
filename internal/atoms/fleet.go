package atoms

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/retiredverbs"
)

// THE FLEET ATOMS THAT READ A FEW NAMED FILES. Each is the port of the chain of
// the same id in package main (atoms_fleet.go and its neighbours): the same
// surface check, the same states, the same sentences, with the tree read from
// disk instead of through the Dagger Directory. The judgements and the long
// sentences are in internal/checks (atomtext.go and the lane files), so the
// two readers cannot word a finding differently.

// absentRuleset answers for a tree with no rules/sast. ABSENCE IS READ AGAINST
// INTENT: a tree nobody stamped may legitimately have no SAST and passes; one
// of the fleet's templates stamped it, so it is supposed to carry the ruleset
// that template ships, and its absence is a scan that did not happen (2).
func absentRuleset(t tree, a checks.AtomDef) checks.Verdict {
	answers, _ := t.read(checks.CopierAnswersFile)
	tpl := checks.CopierTemplate(answers)
	if tpl == "" {
		return checks.VerdictOf(a, int(checks.StatePass), a.ID+
			": ABSENT - no rules/sast in this tree, and no fleet template stamped it")
	}
	return checks.VerdictOf(a, int(checks.StateCannotRun), checks.SastAbsentStamped(a.ID, tpl))
}

// sastRulesetLanes: the SAST ruleset declares every lane this repository builds.
// A lane the ruleset never names is a lane nothing examined: exit 2 by design,
// not a finding.
func sastRulesetLanes(_ context.Context, a checks.AtomDef, in Input) checks.Verdict {
	t := in.tree()
	entries, err := t.entries(".")
	if err != nil {
		return cannotEnumerate(a, err)
	}
	if !checks.HasEntry(entries, "rules") {
		return absentRuleset(t, a)
	}
	rulesEntries, err := t.entries("rules")
	if err != nil {
		return cannotEnumerate(a, err)
	}
	if !checks.HasEntry(rulesEntries, "sast") {
		return absentRuleset(t, a)
	}
	var bodies []string
	for _, p := range t.glob("rules/sast/*.yml") {
		body, err := t.read(p)
		if err != nil {
			// A ruleset file the atom could not read is a ruleset it did not
			// examine, and guessing its languages is the partial scan this
			// atom exists to catch.
			return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - %s would not read: %v", a.ID, p, err))
		}
		bodies = append(bodies, body)
	}
	declared, missing := checks.SastLanesMissing(bodies, entries)
	if len(missing) == 0 {
		return checks.VerdictOf(a, int(checks.StatePass), "fleet:sast-ruleset-lanes: ruleset declares every lane this repo builds")
	}
	return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf(
		"sast-ruleset-lanes: rules/sast declares [%s] but this repo also builds: %s\n"+
			"A ruleset that never names a lane never examines it, and opengrep still exits 0 whenever some OTHER language matched - the partial-scan case the zero-file refusal cannot see (foundry-stocks#4949).",
		strings.Join(declared, " "), strings.Join(missing, " ")))
}

// copierAnswersIntact: .copier-answers.yml carries no renovate crash receipt.
// EXIT 1, NOT 2: a file carrying what it should not is a finding about the
// tree, not a check that could not run.
func copierAnswersIntact(_ context.Context, a checks.AtomDef, in Input) checks.Verdict {
	t := in.tree()
	entries, err := t.entries(".")
	if err != nil {
		return cannotEnumerate(a, err)
	}
	if !checks.HasEntry(entries, checks.CopierAnswersFile) {
		return checks.VerdictOf(a, int(checks.StatePass),
			"fleet:copier-answers-intact: ABSENT - this tree carries no "+checks.CopierAnswersFile+
				", so it is not copier-stamped and has no render to crash")
	}
	body, err := t.read(checks.CopierAnswersFile)
	if err != nil {
		// A file the atom could not read is a file it did not examine, and
		// guessing it is clean is how the marker got six weeks of cover.
		return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf(
			"%s: CANNOT RUN - %s would not read: %v", a.ID, checks.CopierAnswersFile, err))
	}
	if !strings.Contains(body, checks.CopierSentinel) {
		return checks.VerdictOf(a, int(checks.StatePass), "fleet:copier-answers-intact: no crash receipt in "+checks.CopierAnswersFile)
	}
	return checks.VerdictOf(a, int(checks.StateFindings), checks.CopierMarkerReport())
}

// oureaRetired reads the door's published list of keys ourea stopped reading,
// from ourea's main through the git door (the chain cloned it through Dagger;
// the binary has the door, which is where every other atom here reads the
// fleet from). An answer that is not a 200 is an error: the list is the check.
func oureaRetired(ctx context.Context, in Input) (map[string]string, error) {
	status, body, err := in.door().Get(ctx, checks.OureaDoorRepo, checks.OureaRetiredKeysFile)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("the door answered HTTP %d", status)
	}
	return checks.OureaRetiredList(string(body))
}

// oureaConfigRetiredKeys: the ourea ConfigMap names no key the door has stopped
// reading. A prime/ with no ConfigMap in it is NOT absence — prime/ is the
// fleet's flux tree and nothing else's, so the ConfigMap moved.
func oureaConfigRetiredKeys(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	t := in.tree()
	entries, err := t.entries(".")
	if err != nil {
		return cannotEnumerate(a, err)
	}
	if !checks.HasEntry(entries, checks.OureaConfigDir) {
		return checks.VerdictOf(a, int(checks.StatePass), string(a.ID)+
			": ABSENT - this tree carries no "+checks.OureaConfigDir+"/, so it is not the fleet's flux tree and holds no ourea ConfigMap")
	}
	prime, err := t.entries(checks.OureaConfigDir)
	if err != nil {
		return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf(
			"%s: CANNOT RUN - %s/ would not enumerate: %v", a.ID, checks.OureaConfigDir, err))
	}
	if !checks.HasEntry(prime, checks.OureaConfigFile) {
		return checks.VerdictOf(a, int(checks.StateCannotRun), string(a.ID)+
			": CANNOT RUN - this is the fleet's flux tree ("+checks.OureaConfigDir+"/ is here) and "+checks.OureaConfigPath+
			" is not in it: the door's ConfigMap moved, and this atom grades nothing until oureaConfigPath says where")
	}
	body, err := t.read(checks.OureaConfigPath)
	if err != nil {
		return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf(
			"%s: CANNOT RUN - %s would not read: %v", a.ID, checks.OureaConfigPath, err))
	}
	cfg, err := checks.OureaConfigKeys(body)
	if err != nil {
		return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf(
			"%s: CANNOT RUN - %s would not parse: %v", a.ID, checks.OureaConfigPath, err))
	}
	retired, err := oureaRetired(ctx, in)
	if err != nil {
		// THE LIST IS THE CHECK: a pass over a ConfigMap graded against
		// nothing is the zero-file scan this module exists to delete.
		return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf(
			"%s: CANNOT RUN - ourea's %s would not read, so nothing says which keys are retired: %v",
			a.ID, checks.OureaRetiredKeysFile, err))
	}
	named := checks.OureaRetiredNamed(cfg, retired)
	if len(named) == 0 {
		return checks.VerdictOf(a, int(checks.StatePass), string(a.ID)+
			fmt.Sprintf(": %s names none of ourea's %d retired keys", checks.OureaConfigKey, len(retired)))
	}
	return checks.VerdictOf(a, int(checks.StateFindings), checks.OureaRetiredReport(named, retired))
}

// retiredVerbsLedger is the ledger's one home: the root of foundry-stocks.
const retiredVerbsLedger = "retired-verbs.toml"

// retiredVerbs: no unit in the composition surface names a retired MCP verb.
// One repository's business (a tree without the ledger is ABSENT), and a ledger
// with no unit beside it is not a pass: the surface moved, and "0 files, 0
// findings" is the zero-file scan.
func retiredVerbs(_ context.Context, a checks.AtomDef, in Input) checks.Verdict {
	t := in.tree()
	entries, err := t.entries(".")
	if err != nil {
		return cannotEnumerate(a, err)
	}
	if !checks.HasEntry(entries, retiredVerbsLedger) {
		return checks.VerdictOf(a, int(checks.StatePass), string(a.ID)+
			": ABSENT - this tree carries no "+retiredVerbsLedger+", so it is not the fleet's composition source and holds no unit this ledger grades")
	}
	body, err := t.read(retiredVerbsLedger)
	if err != nil {
		return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf(
			"%s: CANNOT RUN - %s would not read: %v", a.ID, retiredVerbsLedger, err))
	}
	ledger, err := retiredverbs.Parse(body)
	if err != nil {
		return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf(
			"%s: CANNOT RUN - %s is not a ledger this atom can grade against: %v", a.ID, retiredVerbsLedger, err))
	}
	paths, err := t.files()
	if err != nil {
		return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf(
			"%s: CANNOT RUN - the composition surface would not enumerate: %v", a.ID, err))
	}
	var units []string
	for _, p := range paths {
		if retiredverbs.Scanned(p) {
			units = append(units, p)
		}
	}
	if len(units) == 0 {
		return checks.VerdictOf(a, int(checks.StateCannotRun), string(a.ID)+
			": CANNOT RUN - "+retiredVerbsLedger+" is here and no unit is: nothing under skills/, agents/, commands/, hooks/, settings/, governance/ or rules/ was found to grade, so the composition surface moved and this atom grades nothing until it knows where")
	}
	sort.Strings(units)
	var hits []retiredverbs.Hit
	for _, p := range units {
		text, err := t.read(p)
		if err != nil {
			return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf(
				"%s: CANNOT RUN - %s would not read: %v", a.ID, p, err))
		}
		hits = append(hits, retiredverbs.Find(p, text, ledger)...)
	}
	if len(hits) == 0 {
		return checks.VerdictOf(a, int(checks.StatePass), fmt.Sprintf(
			"%s: %d unit(s) name none of the %d retired verbs", a.ID, len(units), len(ledger)))
	}
	return checks.VerdictOf(a, int(checks.StateFindings), retiredverbs.Report(a.ID, hits, ledger))
}
