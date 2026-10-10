package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// THE ZERO-UNIT EARLY EXIT. A mutation run whose change set is empty settles
// before anything is provisioned — no module download, no test server, no
// cargo fetch, no mutation tools — and settles EXACTLY as it did when it paid
// for all of them first. The goldens below are the verdicts the atoms answered
// before the change (captured at e4597be with CAPTURE_EARLY_GOLDEN=1), so the
// comparison is old against new, not new against itself.

// earlyVerdict is the part of a verdict a reader sees: everything but the
// timings the runner stamps.
type earlyVerdict struct {
	Atom     string           `json:"atom"`
	State    int              `json:"state"`
	Result   string           `json:"result"`
	Reason   string           `json:"reason"`
	Logs     []string         `json:"logs"`
	Findings []checks.Finding `json:"findings"`
	Gradings []checks.Grading `json:"gradings"`
}

func earlyJSON(t *testing.T, v checks.Verdict) string {
	t.Helper()
	b, err := json.Marshal(earlyVerdict{v.Atom, v.State, v.Result, v.Reason, v.Logs, v.Findings, v.Gradings})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// sameVerdict compares a run's verdict to its golden, or prints the verdict
// when the golden is being captured.
func sameVerdict(t *testing.T, name string, v checks.Verdict, golden string) {
	t.Helper()
	got := earlyJSON(t, v)
	if os.Getenv("CAPTURE_EARLY_GOLDEN") != "" {
		t.Logf("GOLDEN %s %s", name, got)
		return
	}
	if got != golden {
		t.Errorf("%s: the verdict moved\n got: %s\nwant: %s", name, got, golden)
	}
}

// provisioned names every chain that provisions what only a mutation run
// needs; an early exit asks for none of them.
func provisioned(needles ...string) []string {
	var hits []string
	for _, q := range engine.chains() {
		for _, n := range needles {
			if strings.Contains(q, n) {
				hits = append(hits, n)
			}
		}
	}
	return hits
}

const (
	goEarlyGolden         = `{"atom":"go:mutation","state":0,"result":"pass","reason":"test databases: live_db → TEST_DATABASE_URL\ntest brokers: live_kafka → KAFKA_BOOTSTRAP; one broker serves every package — mint unique topic and group names per test\ngo:mutation: PASS","logs":["go:mutation: this pull changes no Go file — nothing to mutate"],"findings":null,"gradings":null}`
	rustEarlyGolden       = `{"atom":"rust:mutation","state":0,"result":"pass","reason":"rust:mutation: scoped to the declared critical modules: src/lib.rs\nrust:mutation: PASS","logs":["rust:mutation: this pull touched none of the critical modules — nothing to mutate"],"findings":null,"gradings":null}`
	rustEarlyRemoveGolden = `{"atom":"rust:mutation","state":0,"result":"pass","reason":"rust:mutation: scoped to the declared critical modules: src/lib.rs\nrust:mutation: PASS","logs":["rust:mutation: this pull only REMOVED lines from the critical modules — nothing to mutate"],"findings":null,"gradings":null}`
)

// A Go pull that changes no Go file, on a star whose record binds a Postgres
// and a broker: the old verdict, and no server, no download.
func TestGoMutationWithNoChangedGoSettlesBeforeProvisioning(t *testing.T) {
	scriptGoMutation(map[string]string{
		".copier-answers.yml":           "service_name: x\n",
		"/dies/fleet/stars/x/slag.json": `{"backends":{"postgres":{},"kafka":["x.events"]}}`,
	})
	engine.stdout(`"grep","-rhoE"`, "//go:build live_db\n//go:build live_kafka\n")
	engine.stdout(goDiffNeedle, "")
	v := runAtom(t, "go:mutation", "abc123")
	sameVerdict(t, "go", v, goEarlyGolden)
	if os.Getenv("CAPTURE_EARLY_GOLDEN") != "" {
		return
	}
	if hits := provisioned("withServiceBinding", "asService", `"go","mod","download"`, `"gomutants"`); len(hits) > 0 {
		t.Errorf("an empty change set provisioned %v:\n%v", hits, engine.chains())
	}
	// The change set WAS asked, on the base the door named.
	if engine.chain(goDiffNeedle) == "" || engine.chain(mergeBaseNeedle) == "" {
		t.Errorf("the change set was never computed:\n%v", engine.chains())
	}
	// And the scope line is the record's, read without binding anything.
	if !strings.Contains(v.Reason, "live_db → TEST_DATABASE_URL") || !strings.Contains(v.Reason, "live_kafka") {
		t.Errorf("the scope line lost the record's servers: %q", v.Reason)
	}
}

// The same star with a Go change still gets its servers and its download.
func TestGoMutationWithAChangeStillProvisions(t *testing.T) {
	scriptGoMutation(map[string]string{
		".copier-answers.yml":           "service_name: x\n",
		"/dies/fleet/stars/x/slag.json": `{"backends":{"postgres":{},"kafka":["x.events"]}}`,
	})
	engine.stdout(`"grep","-rhoE"`, "//go:build live_db\n//go:build live_kafka\n")
	runAtom(t, "go:mutation", "abc123")
	c := engine.chain(goMutantsNeedle, "exitCode")
	wantCalls(t, c,
		[]string{"withExec", `args:["go","mod","download"]`},
		[]string{"withServiceBinding", `alias:"db-mutation"`},
		[]string{"withServiceBinding", `alias:"broker-mutation"`},
	)
	// Downloaded, then bound: the order the atom always had.
	if dl, bind := strings.Index(c, `"go","mod","download"`), strings.Index(c, "withServiceBinding"); dl < 0 || bind < dl {
		t.Errorf("the servers are bound before the download:\n%s", c)
	}
}

// A Rust pull that touches no critical module, and one that only removes lines
// from one: the old verdicts, and no fetch, no mutation tool.
func TestRustMutationWithNothingToMutateSettlesBeforeProvisioning(t *testing.T) {
	for _, tc := range []struct {
		name, diff, golden string
	}{
		{"untouched", "", rustEarlyGolden},
		{"removed only", "diff --git a/src/lib.rs b/src/lib.rs\n--- a/src/lib.rs\n+++ b/src/lib.rs\n@@ -1,2 +1 @@\n-fn g() {}\n fn f() {}", rustEarlyRemoveGolden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scriptRustMutation(map[string]string{".copier-answers.yml": "critical_modules: src/lib.rs\n"})
			engine.stdout(rustDiffNeedle, tc.diff)
			v := runAtom(t, "rust:mutation", "abc123")
			sameVerdict(t, "rust-"+tc.name, v, tc.golden)
			if os.Getenv("CAPTURE_EARLY_GOLDEN") != "" {
				return
			}
			if hits := provisioned(`"cargo","fetch"`, `"cargo","install"`, checks.CargoNextestURL, `"cargo","mutants"`, `"cargo","metadata"`); len(hits) > 0 {
				t.Errorf("nothing to mutate provisioned %v:\n%v", hits, engine.chains())
			}
			if engine.chain(rustDiffNeedle) == "" {
				t.Errorf("the change set was never computed:\n%v", engine.chains())
			}
		})
	}
}

// EACH SCOPE LINE IS ITS OWN, on every branch of the reads: the database line
// and the broker line both say why, and a reason one of them carries must not
// pass for the other's. Held on go:test-race (withTestDatabases and
// withTestBrokers, bound) and on go:mutation's early exit (the reads alone).
func TestTheTestServerScopeLinesSayWhyOnEveryBranch(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tree  map[string]string
		grep  func()
		lines []string
	}{
		{"no service name", map[string]string{".copier-answers.yml": "critical_modules: \n"}, nil, []string{
			"test databases: none — no service_name in .copier-answers.yml",
			"test brokers: none — no service_name in .copier-answers.yml",
		}},
		{"no record", map[string]string{
			".copier-answers.yml":           "service_name: nobody\n",
			"/dies/fleet/stars/x/slag.json": `{"backends":{"postgres":{}}}`, // someone else's
		}, nil, []string{
			"test databases: none — no record at fleet/stars/nobody/slag.json",
			"test brokers: none",
		}},
		{"tags unreadable", map[string]string{
			".copier-answers.yml":           "service_name: x\n",
			"/dies/fleet/stars/x/slag.json": `{"backends":{"postgres":{}}}`,
		}, func() { engine.exitCode(`"grep","-rhoE"`, 2) }, []string{
			"test databases: none — the tree's build tags could not be read",
			"test brokers: none",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, atom := range []string{"go:test-race", "go:mutation"} {
				engine.reset()
				engine.withTree(everyLaneTree)
				engine.withTree(tc.tree)
				engine.stdout(`"go","list"`, "11\n")
				engine.stdout(mergeBaseNeedle, sinceSha+"\n")
				engine.stdout(goDiffNeedle, "")
				if tc.grep != nil {
					tc.grep()
				}
				v := runAtom(t, atom, "abc123")
				for _, want := range tc.lines {
					if !strings.Contains(v.Reason, want) {
						t.Errorf("%s: reason lacks %q:\n%s", atom, want, v.Reason)
					}
				}
			}
		})
	}
}

// A BROKER ALONE STILL TAGS AND SERIALISES go:test-race: withTestBrokers hands
// the brokers it bound back, and the run compiles their tag under -p 1.
func TestGoTestRaceCompilesABrokerOnlyRecordsTag(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{
		".copier-answers.yml":           "service_name: x\n",
		"/dies/fleet/stars/x/slag.json": `{"backends":{"kafka":["x.events"]}}`,
	})
	engine.stdout(`"go","list"`, "11\n")
	engine.stdout(`"grep","-rhoE"`, "//go:build live_kafka\n")
	wantState(t, runAtom(t, "go:test-race", ""), 0, "test brokers: live_kafka")
	c := engine.chain(`"go","test","-race"`, "exitCode")
	wantCalls(t, c,
		[]string{"withServiceBinding", `alias:"broker"`},
		[]string{"withExec", `args:["go","test","-race","-tags","live_kafka","-p","1","-coverprofile","/tmp/go-test-race.cover","./..."]`},
	)
}
