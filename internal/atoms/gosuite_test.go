package atoms

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// gosuiteIn is an Input over a real tree on disk (the suite reads the tree for
// its gravity and Kafka facts) whose execs the fake answers.
func gosuiteIn(t *testing.T, f *golaneFake, files map[string]string) Input {
	t.Helper()
	in := treeIn(t, files)
	in.Exec = f.exec
	return in
}

const (
	gosuiteGo     = "module x\n\ngo 1.26\n"
	gosuiteRegen  = "f450dd4c17fabbc2820904727f2d07f3f4916eb7"
	gosuiteRegen2 = "824e00f7108f62fac3c5e0d730e3bded5673da5e"
	gosuiteFlow   = "package x\nimport \"git.notusmi.com/rob/stellar-core-go/kafkatopics\"\nfunc p() { kafkatopics.Record(kafkatopics.TopicSessionTurns, nil) }\n"
)

func gosuiteProv(rev string) string {
	return `{"generator":{"command":"gravity --name x-core --package x ./guest.wasm","commit":"` + rev + `"}}`
}

func gosuiteVerdict(t *testing.T, f *golaneFake, files map[string]string, race bool) checks.Verdict {
	t.Helper()
	return GoSuite(t.Context(), gosuiteIn(t, f, files), GoRaceRow, ".", race)
}

// THE COMMIT'S SUITE IS THE UNIT SUITE: the same packages, no race detector,
// no database and no build tags — and the same count as the push's run.
func TestGoSuiteRunsTheUnitSuiteAfterTheDownloadAndTheCount(t *testing.T) {
	f := &golaneFake{rules: []golaneRule{{"go list", "11\n", 0}}}
	v := gosuiteVerdict(t, f, map[string]string{"go.mod": gosuiteGo}, false)
	if v.State != 0 || !strings.HasPrefix(v.Reason, "unit suite: no race detector and no database — go:test-race runs those at the push\nregen: no package pairs") || !strings.HasSuffix(v.Reason, "\ngo:test-race: PASS") {
		t.Fatalf("%+v", v)
	}
	want := []string{"mod download", "list -f {{len .TestGoFiles}}{{len .XTestGoFiles}} ./...", "test ./..."}
	if len(f.runs) != len(want) {
		t.Fatalf("ran %v", f.runs)
	}
	for i, w := range want {
		if !strings.HasSuffix(f.runs[i], ": go "+w) {
			t.Errorf("run %d is %q, want go %s", i, f.runs[i], w)
		}
	}
}

func TestGoSuiteCountsTestFilesBeforeRunning(t *testing.T) {
	files := map[string]string{"go.mod": gosuiteGo}
	f := &golaneFake{rules: []golaneRule{{"go list", "00\n00\n", 0}}}
	v := gosuiteVerdict(t, f, files, true)
	if v.State != 1 || !strings.Contains(v.Reason, "go:test-race: FINDINGS - no test file in any package; nothing is built without tests") {
		t.Errorf("%+v", v)
	}
	for _, r := range f.runs {
		if strings.Contains(r, "go test") {
			t.Errorf("the suite ran over nothing: %v", f.runs)
		}
	}

	// a module that will not list is a red about the module
	f = &golaneFake{rules: []golaneRule{{"go list", "go: cannot find main module", 1}}}
	v = gosuiteVerdict(t, f, files, true)
	if v.State != 1 || !strings.Contains(v.Reason, "go list ./... failed, so the tests cannot be counted: go: cannot find main module") {
		t.Errorf("%+v", v)
	}

	// a program that would not start never ran
	f = &golaneFake{rules: []golaneRule{{"go list", "exec: go: not found", -1}}}
	if v = gosuiteVerdict(t, f, files, true); v.State != 2 || !strings.Contains(v.Reason, "the atom never ran: exec: go: not found") {
		t.Errorf("%+v", v)
	}
}

func TestGoSuiteStopsAtAFailedDownload(t *testing.T) {
	f := &golaneFake{rules: []golaneRule{{"mod download", "go: reading example.com/x: 404 Not Found", 1}}}
	v := gosuiteVerdict(t, f, map[string]string{"go.mod": gosuiteGo}, false)
	if v.State != 2 || !strings.Contains(v.Reason, "preflight `go mod download` exited 1: go: reading example.com/x: 404 Not Found") || len(f.runs) != 1 {
		t.Errorf("%+v ran %v", v, f.runs)
	}
}

// THE PUSH'S RUN IS THE RACE RUN against the tree's databases, one at a time,
// writing the profile go:diff-coverage reads.
func TestGoSuiteRaceCompilesTheTreesTagsSeriallyAndWritesTheProfile(t *testing.T) {
	f := &golaneFake{rules: []golaneRule{
		{"go list", "11\n", 0},
		{"grep -rhoE", "//go:build live_db\n//go:build live_db_novector\n//go:build integration\n", 0},
	}}
	v := gosuiteVerdict(t, f, map[string]string{"go.mod": gosuiteGo, "x_test.go": "//go:build live_db\n\npackage x\n"}, true)
	if v.State != 0 || !strings.Contains(v.Reason, "live_db → TEST_DATABASE_URL, live_db_novector → TEST_NOVECTOR_DATABASE_URL; tags left uncompiled (not in the fleet's vocabulary): integration") {
		t.Fatalf("%+v", v)
	}
	last := f.runs[len(f.runs)-1]
	if !strings.HasSuffix(last, ": go test -race -tags live_db,live_db_novector -p 1 -coverprofile "+checks.GoCoverProfile+" ./...") {
		t.Errorf("the suite ran as %q", last)
	}

	// NO TAGGED SUITE: no tags at all — not an empty -tags word — and no -p 1.
	f = &golaneFake{rules: []golaneRule{{"go list", "11\n", 0}, {"grep -rhoE", "", 1}}}
	v = gosuiteVerdict(t, f, map[string]string{"go.mod": gosuiteGo}, true)
	last = f.runs[len(f.runs)-1]
	if v.State != 0 || !strings.Contains(v.Reason, "test databases: none — no test file sits behind a tag the fleet names") ||
		strings.Contains(last, "-tags") || strings.Contains(last, "-p 1") || !strings.Contains(last, "go test -race -coverprofile") {
		t.Errorf("%+v ran %q", v, last)
	}
}

// A BROKER ALONE STILL TAGS AND SERIALISES the race run.
func TestGoSuiteRaceCompilesABrokerOnlyTreesTag(t *testing.T) {
	f := &golaneFake{rules: []golaneRule{{"go list", "11\n", 0}, {"grep -rhoE", "//go:build live_kafka\n", 0}}}
	v := gosuiteVerdict(t, f, map[string]string{"go.mod": gosuiteGo, "flows.go": gosuiteFlow}, true)
	last := f.runs[len(f.runs)-1]
	if v.State != 0 || !strings.Contains(v.Reason, "test brokers: live_kafka") || !strings.Contains(last, "go test -race -tags live_kafka -p 1 -coverprofile") {
		t.Errorf("%+v ran %q", v, last)
	}
}

// kubeEnv is a suite environment that answers the named variables and nothing else.
func kubeEnv(pairs ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return func(k string) string { return m[k] }
}

func gosuiteVerdictEnv(t *testing.T, f *golaneFake, getenv func(string) string, race bool) checks.Verdict {
	t.Helper()
	in := gosuiteIn(t, f, map[string]string{"go.mod": gosuiteGo})
	in.Getenv = getenv
	return GoSuite(t.Context(), in, GoRaceRow, ".", race)
}

var kubeBound = []string{"TEST_KUBE_URL", "https://kube:6443", "TEST_KUBE_TOKEN", "tok", "TEST_KUBE_CA_FILE", "/run/test-kube/ca.crt"}

// AN API SERVER ALONE STILL TAGS AND SERIALISES the race run, and the reason says
// which variables the suite was handed, WHEN THE LANE BOUND ALL THREE.
func TestGoSuiteRaceCompilesAKubeOnlyTreesTagWhenAllThreeVariablesAreBound(t *testing.T) {
	f := &golaneFake{rules: []golaneRule{{"go list", "11\n", 0}, {"grep -rhoE", "//go:build live_kube\n", 0}}}
	v := gosuiteVerdictEnv(t, f, kubeEnv(kubeBound...), true)
	last := f.runs[len(f.runs)-1]
	if v.State != 0 || !strings.Contains(v.Reason, "test kubes: live_kube → TEST_KUBE_URL, TEST_KUBE_TOKEN, TEST_KUBE_CA_FILE") ||
		!strings.HasSuffix(last, ": go test -race -tags live_kube -p 1 -coverprofile "+checks.GoCoverProfile+" ./...") {
		t.Errorf("%+v ran %q", v, last)
	}
	if strings.Contains(v.Reason, "uncompiled") {
		t.Errorf("the scope line calls a compiled tag uncompiled:\n%s", v.Reason)
	}
}

// A TREE THAT CARRIES live_kube ON A LANE THAT BOUND NO API SERVER CANNOT RUN: the
// atom never compiles the suite with nothing behind it (the pod path binds no
// kube-apiserver), and the detail names exactly the variables that are absent.
func TestGoSuiteRaceCannotRunLiveKubeWhereAVariableIsNotBound(t *testing.T) {
	for _, tc := range []struct {
		name    string
		env     []string
		missing string
	}{
		{"none bound", nil, "TEST_KUBE_URL, TEST_KUBE_TOKEN, TEST_KUBE_CA_FILE"},
		{"the URL missing", []string{"TEST_KUBE_TOKEN", "tok", "TEST_KUBE_CA_FILE", "/ca"}, "TEST_KUBE_URL"},
		{"the token missing", []string{"TEST_KUBE_URL", "u", "TEST_KUBE_CA_FILE", "/ca"}, "TEST_KUBE_TOKEN"},
		{"the CA missing", []string{"TEST_KUBE_URL", "u", "TEST_KUBE_TOKEN", "tok"}, "TEST_KUBE_CA_FILE"},
		{"one set but empty", []string{"TEST_KUBE_URL", "u", "TEST_KUBE_TOKEN", "", "TEST_KUBE_CA_FILE", "/ca"}, "TEST_KUBE_TOKEN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &golaneFake{rules: []golaneRule{{"go list", "11\n", 0}, {"grep -rhoE", "//go:build live_kube\n", 0}}}
			v := gosuiteVerdictEnv(t, f, kubeEnv(tc.env...), true)
			want := "go:test-race: CANNOT RUN - live_kube: " + tc.missing + " not bound on this lane (the pod path binds no kube-apiserver; see atoms/go/test-race Needs)"
			if v.State != 2 || !strings.Contains(v.Reason, want) {
				t.Errorf("%+v\nwant %q", v, want)
			}
			for _, r := range f.runs {
				if strings.Contains(r, "go test") {
					t.Errorf("the suite ran with nothing bound: %v", f.runs)
				}
			}
		})
	}
}

// NO live_kube IN THE TREE: the variables are not read for a tag nothing carries,
// so a lane that binds all three adds no tag, and one that binds none is not refused.
func TestGoSuiteRaceAddsNoKubeTagWhenTheTreeCarriesNone(t *testing.T) {
	for _, env := range [][]string{kubeBound, nil} {
		f := &golaneFake{rules: []golaneRule{{"go list", "11\n", 0}, {"grep -rhoE", "", 1}}}
		v := gosuiteVerdictEnv(t, f, kubeEnv(env...), true)
		last := f.runs[len(f.runs)-1]
		if v.State != 0 || strings.Contains(last, "live_kube") || strings.Contains(last, "-tags") ||
			!strings.Contains(v.Reason, "test kubes: none — no test file sits behind a tag the fleet names") {
			t.Errorf("env %v: %+v ran %q", env, v, last)
		}
	}
}

// THE UNIT RUN (the commit's go:test) reads no variables and compiles no tag.
func TestGoSuiteUnitRunIgnoresTheKubeVariables(t *testing.T) {
	f := &golaneFake{rules: []golaneRule{{"go list", "11\n", 0}}}
	v := gosuiteVerdictEnv(t, f, kubeEnv(), false)
	if v.State != 0 || strings.Contains(f.runs[len(f.runs)-1], "-tags") {
		t.Errorf("%+v ran %v", v, f.runs)
	}
}

// go test's exit is the state, its whole output the reason's evidence; a suite
// that would not start never ran.
func TestGoSuiteReadsGoTestsExit(t *testing.T) {
	files := map[string]string{"go.mod": gosuiteGo}
	f := &golaneFake{rules: []golaneRule{{"go list", "11\n", 0}, {"go test", "--- FAIL: TestX (0.00s)\nFAIL\n", 1}}}
	v := gosuiteVerdict(t, f, files, false)
	if v.State != 1 || !strings.Contains(v.Reason, "go:test-race: FINDINGS (exit 1)") || !strings.Contains(v.Reason, "--- FAIL: TestX") {
		t.Errorf("%+v", v)
	}
	f = &golaneFake{rules: []golaneRule{{"go list", "11\n", 0}, {"go test", "exec: go: not found", -1}}}
	if v = gosuiteVerdict(t, f, files, false); v.State != 2 || !strings.Contains(v.Reason, "the atom never ran") {
		t.Errorf("%+v", v)
	}
	f = &golaneFake{rules: []golaneRule{{"go list", "11\n", 0}, {"go test", "signal: killed", 137}}}
	if v = gosuiteVerdict(t, f, files, false); v.State != 2 {
		t.Errorf("%+v", v)
	}
}

// THE REGEN TESTS RUN INSTEAD OF SKIPPING where the tree has packages gravity
// generated: the variables ride the suite's exec, one per package that opted in.
func TestGoSuiteRequiresTheRegenTestsOfPackagesThatOptedIn(t *testing.T) {
	f := &golaneFake{rules: []golaneRule{{"go list", "11\n", 0}}}
	v := gosuiteVerdict(t, f, map[string]string{
		"go.mod":                      gosuiteGo,
		"classescore/provenance.json": gosuiteProv(gosuiteRegen),
		"classescore/regen_test.go":   "package classescore\n",
		"stamp/provenance.json":       gosuiteProv(gosuiteRegen),
		"stamp/regen_test.go":         "package stamp\n",
		"alarm/provenance.json":       gosuiteProv(gosuiteRegen2), // no regen test: not held
	}, false)
	if v.State != 0 || !strings.Contains(v.Reason, "gravity "+gosuiteRegen+" staged on PATH") || !strings.Contains(v.Reason, "CLASSESCORE_REGEN_REQUIRED=1, STAMP_REGEN_REQUIRED=1") {
		t.Fatalf("%+v", v)
	}
	env := strings.Join(f.envs[len(f.envs)-1], " ")
	if env != "CLASSESCORE_REGEN_REQUIRED=1 STAMP_REGEN_REQUIRED=1" {
		t.Errorf("the suite's exec carries %q", env)
	}
	for _, e := range f.envs[:len(f.envs)-1] {
		if len(e) != 0 {
			t.Errorf("only the suite's exec carries the variables: %v", e)
		}
	}
}

func TestGoSuiteFilesAProvenanceItCannotHonourAsAFinding(t *testing.T) {
	f := &golaneFake{rules: []golaneRule{{"go list", "11\n", 0}}}
	v := gosuiteVerdict(t, f, map[string]string{
		"go.mod":            gosuiteGo,
		"a/provenance.json": gosuiteProv(gosuiteRegen), "a/regen_test.go": "",
		"b/provenance.json": gosuiteProv(gosuiteRegen2), "b/regen_test.go": "",
	}, false)
	if v.State != 1 || !strings.Contains(v.Reason, "FINDINGS - a pins gravity "+gosuiteRegen) || !strings.Contains(v.Reason, "b pins 824e00f") {
		t.Errorf("%+v", v)
	}

	v = gosuiteVerdict(t, f, map[string]string{"go.mod": gosuiteGo, "a/provenance.json": "{", "a/regen_test.go": ""}, false)
	if v.State != 1 || !strings.Contains(v.Reason, "FINDINGS") || !strings.Contains(v.Reason, "a/provenance.json is not JSON") {
		t.Errorf("%+v", v)
	}
}

func TestGoSuiteCannotRunWhenTheTreeWillNotShowItsProvenance(t *testing.T) {
	f := &golaneFake{rules: []golaneRule{{"go list", "11\n", 0}}}

	// a provenance.json that points nowhere will not read
	in := gosuiteIn(t, f, map[string]string{"go.mod": gosuiteGo})
	put(t, in.Root, "a/other", "")
	if err := os.Symlink("nowhere", filepath.Join(in.Root, "a", "provenance.json")); err != nil {
		t.Fatal(err)
	}
	v := GoSuite(t.Context(), in, GoRaceRow, ".", false)
	if v.State != 2 || !strings.Contains(v.Reason, "CANNOT RUN - the tree's provenance could not be read") {
		t.Errorf("%+v", v)
	}

	// a tree that will not walk
	v = GoSuite(t.Context(), Input{Root: "/nonexistent-tree-for-the-suite", Exec: f.exec}, GoRaceRow, ".", false)
	if v.State != 2 || !strings.Contains(v.Reason, "CANNOT RUN - the tree's provenance could not be read") {
		t.Errorf("%+v", v)
	}
}

// EACH SCOPE LINE IS ITS OWN, on every branch of the reads: the database line
// and the broker line both say why, and a reason one of them carries must not
// pass for the other's.
func TestTheTestServerScopeLinesSayWhyOnEveryBranch(t *testing.T) {
	for _, tc := range []struct {
		name   string
		files  map[string]string
		listed string // a path the tree lists and does not hold
		grep   golaneRule
		dbs    int
		brokes int
		lines  []string
	}{
		{"no tagged suite and no flow", map[string]string{"go.mod": gosuiteGo}, "", golaneRule{"grep", "", 1}, 0, 0, []string{
			"test databases: none — no test file sits behind a tag the fleet names",
			"test brokers: none — the tree states no kafka flow",
			"test kubes: none — no test file sits behind a tag the fleet names",
		}},
		{"tags unreadable", map[string]string{"go.mod": gosuiteGo}, "", golaneRule{"grep", "grep: x: Permission denied", 2}, 0, 0, []string{
			"test databases: none — the tree's build tags could not be read",
			"test brokers: none",
			"test kubes: none — the tree's build tags could not be read",
		}},
		{"grep would not start", map[string]string{"go.mod": gosuiteGo}, "", golaneRule{"grep", "exec: grep: not found", -1}, 0, 0, []string{
			"test databases: none — the tree's build tags could not be read",
			"test brokers: none",
		}},
		{"flows unreadable", map[string]string{"go.mod": gosuiteGo}, "gone.go", golaneRule{"grep", "", 1}, 0, 0, []string{
			"test brokers: none — the tree's kafka flows could not be read (gone.go (",
		}},
		{"flows without a tagged suite", map[string]string{"go.mod": gosuiteGo, "flows.go": gosuiteFlow}, "", golaneRule{"grep", "//go:build live_db\n", 0}, 1, 0, []string{
			"test databases: live_db → TEST_DATABASE_URL",
			"test brokers: none — the tree states kafka flows but no test file sits behind a tag the fleet names",
		}},
		{"both", map[string]string{"go.mod": gosuiteGo, "flows.go": gosuiteFlow}, "", golaneRule{"grep", "//go:build live_db\n//go:build live_kafka\n", 0}, 1, 1, []string{
			"test databases: live_db → TEST_DATABASE_URL",
			"test brokers: live_kafka → KAFKA_BOOTSTRAP; one broker serves every package — mint unique topic and group names per test",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &golaneFake{rules: []golaneRule{tc.grep}}
			in := gosuiteIn(t, f, tc.files)
			if tc.listed != "" {
				in.Files = append(in.Files, tc.listed)
			}
			dbs, brokers, _, scope := GoTestServers(t.Context(), in, in.Root)
			if len(dbs) != tc.dbs || len(brokers) != tc.brokes {
				t.Errorf("dbs %v brokers %v", dbs, brokers)
			}
			for _, want := range tc.lines {
				if !strings.Contains(scope, want) {
					t.Errorf("the scope lacks %q:\n%s", want, scope)
				}
			}
		})
	}
	// a tree that would not enumerate has no flows to read
	in := Input{Root: t.TempDir(), FilesErr: errTreeGone, Exec: (&golaneFake{rules: []golaneRule{{"grep", "", 1}}}).exec}
	if _, _, _, scope := GoTestServers(t.Context(), in, in.Root); !strings.Contains(scope, "test brokers: none — the tree's kafka flows could not be read (the tree would not enumerate: gone)") {
		t.Errorf("the scope:\n%s", scope)
	}
}

// AN INPUT WITH NO ENVIRONMENT OF ITS OWN READS THE PROCESS'S: the adapter binds
// the three variables on the container the atom runs in, so that is where the
// real atom finds them; a set Getenv wins over it.
func TestInputGetenvReadsTheProcessUnlessTold(t *testing.T) {
	t.Setenv("TEST_KUBE_URL", "https://kube:6443")
	if got := (Input{}).getenv("TEST_KUBE_URL"); got != "https://kube:6443" {
		t.Errorf("the process's variable = %q", got)
	}
	in := Input{Getenv: func(string) string { return "told" }}
	if got := in.getenv("TEST_KUBE_URL"); got != "told" {
		t.Errorf("a set Getenv = %q", got)
	}
}
