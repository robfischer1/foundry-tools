package main

import (
	"regexp"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/bundlelane"
)

// derivedTable is what the derive prints: a table the lane grades and lays.
const derivedTable = `{"keys":{"v|s|_|_|_":{"d":{"e":"allow","r":0,"f":"VERB_GRANT"},"c":{}}},"reasons":[[]],"prefix":{"athena":"plan"},"caps":{"athena":{"task_list":1}}}`

// withDerive is a foundry-dies tree that carries the table's derive.
func withDerive(t *testing.T, extra map[string]string) *FoundryTools {
	t.Helper()
	tree := map[string]string{
		bundlelane.DecisionsDerive: "package authz_v2_table",
		"fleet/derive.rego":        "package fleet",
	}
	for k, v := range extra {
		tree[k] = v
	}
	return bundleOn(t, tree)
}

// scriptTheDerive answers the derive with out, and the built die's data.json
// with built. The probe that reads the built die is re-scripted after it: it
// carries the same needle, and the last matching script wins.
func scriptTheDerive(out, built string) {
	engine.stdout(`"`+bundlelane.DecisionsQuery+`"`, out)
	engine.stdout(`"/work/bundle.tar.gz"`, built)
	engine.stdout(`"/work/probe-session.json"`, `{"result":[{"expressions":[{"value":["search"]}]}]}`)
	scriptTheFleet()
}

// srcMount is a tree mount in a recorded chain, its arguments in either order.
var srcMount = regexp.MustCompile(`withMountedDirectory\([^)]*path:"/src"`)

// srcMounts counts the tree mounts a chain carries: the lane's own, and one more
// once the table is laid into it.
func srcMounts(chain string) int { return len(srcMount.FindAllString(chain, -1)) }

const policyDataWithTable = `{"authz_audience":{"star_only":{"chaos":["graph_commit_graph"],"athena":["task_create"]}},"grants":{"decisions":` + derivedTable + `}}`

// The table is derived after the suite, over the catalog roster, and laid in
// the tree both twins are built from.
func TestTheDecisionTableIsDerivedAndBuiltIntoThePolicyDie(t *testing.T) {
	m := withDerive(t, nil)
	scriptAGreenBundle()
	scriptTheDerive(derivedTable, policyDataWithTable)
	bundles(t, m)
	settledOn(t, "0", "published and signed")
	derive := engine.chain(`"` + bundlelane.DecisionsQuery + `"`)
	if derive == "" {
		t.Fatal("the derive never ran")
	}
	for _, want := range []string{`"-d"`, `"policy"`, `"tools/authz_table"`, `"/work/decisions-stage"`} {
		if !strings.Contains(derive, want) {
			t.Errorf("the derive chain lacks %s:\n%s", want, derive)
		}
	}
	for _, want := range []string{`path:"` + bundlelane.DecisionsCacheDir + `"`, "DECISIONS_KEY"} {
		if !strings.Contains(derive, want) {
			t.Errorf("the derive chain lacks the cache (%s):\n%s", want, derive)
		}
	}
	if build := engine.chain(`"/work/bundle-signed.tar.gz"`, `"--signing-key"`); srcMounts(build) != 2 {
		t.Errorf("the signed twin was not built from the tree the table was laid in (%d mounts):\n%s", srcMounts(build), build)
	}
	if suite := engine.chain(`"-v"`, `"--timeout"`); srcMounts(suite) != 1 {
		t.Error("the policy suite ran with the table laid: its mocks of data.grants would read it")
	}
}

func TestATreeWithoutTheDeriveShipsNoTable(t *testing.T) {
	m := bundleOn(t, nil)
	scriptAGreenBundle()
	bundles(t, m)
	settledOn(t, "0", "published and signed")
	if engine.chain(`"`+bundlelane.DecisionsQuery+`"`) != "" {
		t.Error("a tree without the derive was derived")
	}
}

func TestADecisionTableTheDieDoesNotCarryIsAFinding(t *testing.T) {
	m := withDerive(t, nil)
	scriptAGreenBundle()
	scriptTheDerive(derivedTable, policyData)
	bundles(t, m)
	settledOn(t, "1", "carries no data.grants.decisions")
	nothingPushed(t)
}

func TestADeriveThatFailsOrAnswersNothingPublishesNothing(t *testing.T) {
	for name, tc := range map[string]struct {
		out  string
		code int
		want string
	}{
		"opa refused": {"rego_type_error", 1, "did not derive"},
		"empty table": {`{"keys":{},"prefix":{},"caps":{}}`, 0, "has no keys"},
	} {
		t.Run(name, func(t *testing.T) {
			m := withDerive(t, nil)
			scriptAGreenBundle()
			scriptTheDerive(tc.out, policyDataWithTable)
			engine.exitCode(`"`+bundlelane.DecisionsQuery+`"`, tc.code)
			bundles(t, m)
			settledOn(t, "1", tc.want)
			nothingPushed(t)
		})
	}
}

func TestACommittedDecisionTableIsRefused(t *testing.T) {
	m := withDerive(t, map[string]string{bundlelane.DecisionsPath: derivedTable})
	scriptAGreenBundle()
	scriptTheDerive(derivedTable, policyDataWithTable)
	bundles(t, m)
	settledOn(t, "1", "is committed, and the lane derives it")
	nothingPushed(t)
}

// Every way the derive's inputs can be missing or unreadable stops the lane
// before anything is built from them: a tree the engine cannot read or a derive
// it cannot run is could-not-run, a roster the tree does not hold is a finding.
func TestTheDeriveNeedsItsRosterAndAnEngine(t *testing.T) {
	digestFails := func(t *testing.T, m *FoundryTools) {
		t.Helper()
		engine.failLeaf(`directory(path:"policy")`, "digest", "engine went away")
		bundles(t, m)
		settledOn(t, "2", "could not be digested")
		nothingPushed(t)
	}
	t.Run("the inputs cannot be digested", func(t *testing.T) {
		m := withDerive(t, nil)
		scriptAGreenBundle()
		scriptTheDerive(derivedTable, policyDataWithTable)
		digestFails(t, m)
	})
	for name, tc := range map[string]struct {
		tree map[string]string
		fail string
		code string
		want string
	}{
		"the derive cannot be probed": {nil, `"` + bundlelane.DecisionsDerive + `"`, "2", "could not be probed"},
		"the catalog cannot be read":  {nil, `"` + bundlelane.DeclaredGlob + `"`, "2", ""},
		"the derive never ran":        {nil, `"` + bundlelane.DecisionsQuery + `"`, "2", "the decision table's derive never ran"},
		"no fleet/derive.rego":        {map[string]string{"fleet/derive.rego": ""}, "", "1", "fleet/derive.rego is absent"},
		"a catalog entry misnamed":    {map[string]string{"fleet/repos/ares.json": `{"name":"zeus"}`}, "", "1", "name must be"},
		"no fleet/data.json to stage": {map[string]string{"fleet/data.json": ""}, "", "1", "fleet/data.json is absent"},
	} {
		t.Run(name, func(t *testing.T) {
			m := withDerive(t, tc.tree)
			scriptAGreenBundle()
			scriptTheDerive(derivedTable, policyDataWithTable)
			if tc.fail != "" {
				engine.fail(tc.fail, "engine went away")
			}
			bundles(t, m)
			settledOn(t, tc.code, tc.want)
			nothingPushed(t)
		})
	}
}
