package atoms

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// gosuiteIn is an Input over a real tree on disk (the suite reads the tree for
// its gravity and Kafka facts) whose execs the fake answers.
func gosuiteIn(t *testing.T, f *golaneFake, files map[string]string) Input {
	t.Helper()
	coverRootIn(t, t.TempDir())
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

// A REDIS SUITE ALONE COMPILES ITS TAG, SERIALLY. The cache is one server and
// its isolation unit is the keyspace, so the packages of the run do not overlap;
// and the tag is in the -tags word, or the suite would compile OUT and the lane
// would report green over tests that never built.
func TestGoSuiteRaceCompilesALiveRedisTagSerially(t *testing.T) {
	f := &golaneFake{rules: []golaneRule{
		{"go list", "11\n", 0},
		{"grep -rhoE", "//go:build live_redis\n", 0},
	}}
	v := gosuiteVerdict(t, f, map[string]string{"go.mod": gosuiteGo, "x_test.go": "//go:build live_redis\n\npackage x\n"}, true)
	if v.State != 0 || !strings.Contains(v.Reason, "test caches: live_redis → TEST_REDIS_URL") || strings.Contains(v.Reason, "uncompiled") {
		t.Fatalf("%+v", v)
	}
	if last := f.runs[len(f.runs)-1]; !strings.HasSuffix(last, ": go test -race -tags live_redis -p 1 -coverprofile "+GoCoverProfile(GoRaceRow.ID, ".")+" ./...") {
		t.Errorf("the suite ran as %q", last)
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
	if !strings.HasSuffix(last, ": go test -race -tags live_db,live_db_novector -p 1 -coverprofile "+GoCoverProfile(GoRaceRow.ID, ".")+" ./...") {
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
		!strings.HasSuffix(last, ": go test -race -tags live_kube -p 1 -coverprofile "+GoCoverProfile(GoRaceRow.ID, ".")+" ./...") {
		t.Errorf("%+v ran %q", v, last)
	}
	if strings.Contains(v.Reason, "uncompiled") {
		t.Errorf("the scope line calls a compiled tag uncompiled:\n%s", v.Reason)
	}
}

// A TREE THAT CARRIES live_kube ON A LANE THAT BOUND NO API SERVER CANNOT RUN: the
// atom never compiles the suite with nothing behind it (a pod binds a
// kube-apiserver only for an atom whose Needs name it), and the detail names
// exactly the variables that are absent.
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
			want := "go:test-race: CANNOT RUN - live_kube: " + tc.missing + " not bound on this lane (the pod path binds one for an atom whose Needs name service:etcd and service:kube-apiserver; see atoms/go/test-race Needs)"
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
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gravity"), []byte("gravity-bytes"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GRAVITY", "")
	t.Setenv("PATH", dir)
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
	if env != "CLASSESCORE_REGEN_REQUIRED=1 STAMP_REGEN_REQUIRED=1 GRAVITY_SHA256="+gosuiteSum("gravity-bytes")+" "+strings.Join(suiteScrub, " ") {
		t.Errorf("the suite's exec carries %q", env)
	}
	for _, e := range f.envs[:len(f.envs)-1] {
		if len(e) != 0 {
			t.Errorf("only the suite's exec carries the variables: %v", e)
		}
	}
}

// THE WIT SNAPSHOT TEST IS REQUIRED where the tree holds snapshots: its variable rides the
// suite's exec, and the scope line says so.
func TestGoSuiteRequiresTheWitSnapshotTestWhereTheTreeHoldsSnapshots(t *testing.T) {
	f := &golaneFake{rules: []golaneRule{{"go list", "11\n", 0}}}
	v := gosuiteVerdict(t, f, map[string]string{
		"go.mod":                      gosuiteGo,
		"genregistry/wit/SOURCE.json": `{"tool":"wasm-tools 1.261.0","command":"wasm-tools component wit --json {pkg}/guest.wasm"}`,
	}, false)
	if v.State != 0 || !strings.Contains(v.Reason, "wit: GENREGISTRY_WIT_REQUIRED=1 makes a missing wasm-tools a failure") {
		t.Fatalf("%+v", v)
	}
	env := strings.Join(f.envs[len(f.envs)-1], " ")
	if env != "GENREGISTRY_WIT_REQUIRED=1 "+strings.Join(suiteScrub, " ") {
		t.Errorf("the suite's exec carries %q", env)
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

// A WIT snapshot record the suite cannot read, or a tree it cannot walk, is a could-not-run
// like an unreadable provenance: the snapshot test cannot be required of what is not seen.
func TestGoSuiteCannotRunWhenTheTreeWillNotShowItsWitSnapshotRecord(t *testing.T) {
	f := &golaneFake{rules: []golaneRule{{"go list", "11\n", 0}}}
	in := gosuiteIn(t, f, map[string]string{"go.mod": gosuiteGo})
	put(t, in.Root, "pkg/wit/other", "")
	if err := os.Symlink("nowhere", filepath.Join(in.Root, "pkg", "wit", "SOURCE.json")); err != nil {
		t.Fatal(err)
	}
	v := GoSuite(t.Context(), in, GoRaceRow, ".", false)
	if v.State != 2 || !strings.Contains(v.Reason, "CANNOT RUN - the tree's provenance could not be read") {
		t.Errorf("%+v", v)
	}
	if _, err := witSnapshotPlan(Input{Root: "/nonexistent-tree-for-the-suite"}); !errors.Is(err, errGoGravityRead) {
		t.Errorf("a tree that will not walk: %v", err)
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
		caches int
		lines  []string
	}{
		{"no tagged suite and no flow", map[string]string{"go.mod": gosuiteGo}, "", golaneRule{"grep", "", 1}, 0, 0, 0, []string{
			"test databases: none — no test file sits behind a tag the fleet names",
			"test brokers: none — the tree states no kafka flow",
			"test caches: none — no test file sits behind a tag the fleet names",
			"test kubes: none — no test file sits behind a tag the fleet names",
		}},
		{"tags unreadable", map[string]string{"go.mod": gosuiteGo}, "", golaneRule{"grep", "grep: x: Permission denied", 2}, 0, 0, 0, []string{
			"test databases: none — the tree's build tags could not be read",
			"test brokers: none",
			"test caches: none — the tree's build tags could not be read",
			"test kubes: none — the tree's build tags could not be read",
		}},
		{"grep would not start", map[string]string{"go.mod": gosuiteGo}, "", golaneRule{"grep", "exec: grep: not found", -1}, 0, 0, 0, []string{
			"test databases: none — the tree's build tags could not be read",
			"test brokers: none",
		}},
		{"flows unreadable", map[string]string{"go.mod": gosuiteGo}, "gone.go", golaneRule{"grep", "", 1}, 0, 0, 0, []string{
			"test brokers: none — the tree's kafka flows could not be read (gone.go (",
		}},
		{"flows without a tagged suite", map[string]string{"go.mod": gosuiteGo, "flows.go": gosuiteFlow}, "", golaneRule{"grep", "//go:build live_db\n", 0}, 1, 0, 0, []string{
			"test databases: live_db → TEST_DATABASE_URL",
			"test brokers: none — the tree states kafka flows but no test file sits behind a tag the fleet names",
		}},
		{"both", map[string]string{"go.mod": gosuiteGo, "flows.go": gosuiteFlow}, "", golaneRule{"grep", "//go:build live_db\n//go:build live_kafka\n", 0}, 1, 1, 0, []string{
			"test databases: live_db → TEST_DATABASE_URL",
			"test brokers: live_kafka → KAFKA_BOOTSTRAP, TEST_KAFKA_BROKERS; one broker serves every package — mint unique topic and group names per test",
		}},
		{"a redis suite alone", map[string]string{"go.mod": gosuiteGo}, "", golaneRule{"grep", "//go:build live_redis\n", 0}, 0, 0, 1, []string{
			"test databases: none — no test file sits behind a tag the fleet names",
			"test caches: live_redis → TEST_REDIS_URL; one server per lane",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &golaneFake{rules: []golaneRule{tc.grep}}
			in := gosuiteIn(t, f, tc.files)
			if tc.listed != "" {
				in.Files = append(in.Files, tc.listed)
			}
			dbs, brokers, caches, _, scope := GoTestServers(t.Context(), in, in.Root)
			if len(dbs) != tc.dbs || len(brokers) != tc.brokes || len(caches) != tc.caches {
				t.Errorf("dbs %v brokers %v caches %v", dbs, brokers, caches)
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
	if _, _, _, _, scope := GoTestServers(t.Context(), in, in.Root); !strings.Contains(scope, "test brokers: none — the tree's kafka flows could not be read (the tree would not enumerate: gone)") {
		t.Errorf("the scope:\n%s", scope)
	}
}

// The suite's exec blanks the pod's per-atom variables: with ATOM_WORK set,
// WorkDir ignores TMPDIR and a test that makes TMPDIR unwritable ("a copy that
// cannot be made is a could-not-run") passed instead of failing as designed.
func TestGoSuiteRunsTheSuiteWithoutThePodsAtomVariables(t *testing.T) {
	f := &golaneFake{rules: []golaneRule{{"go list", "11\n", 0}}}
	if v := gosuiteVerdict(t, f, map[string]string{"go.mod": gosuiteGo}, false); v.State != 0 {
		t.Fatalf("%+v", v)
	}
	got := strings.Join(f.envs[len(f.envs)-1], " ")
	for _, k := range []string{"ATOM_WORK=", "ATOM_OUT=", "ATOM_TREE="} {
		if !strings.Contains(got, k) {
			t.Errorf("%s is not blanked: %q", k, got)
		}
	}
}

// DEF-B4-PERF-3: `-p 1` serialises the COMPILE and LINK of every package as
// well as the test runs, so a cold build cache paid the whole race build on one
// core (glaucus: 112 s serial, 28 s at the machine's width). The race run is
// preceded by a build-only pass over the same flags that fills the cache in
// parallel — `-exec true` compiles and links every test binary and runs none, so
// no database is touched and no result is cached. Its outcome is not the atom's:
// a build that fails fails again, with go test's own words, in the real run.
func TestDEF_B4_PERF_3_RaceRunIsPrecededByAParallelBuildOnlyPass(t *testing.T) {
	f := &golaneFake{rules: []golaneRule{
		{"go list", "11\n", 0},
		{"grep -rhoE", "//go:build live_db\n", 0},
	}}
	v := gosuiteVerdict(t, f, map[string]string{"go.mod": gosuiteGo}, true)
	if v.State != 0 {
		t.Fatalf("%+v", v)
	}
	// the warm pass precedes the suite; the split's package listing may sit
	// between them (gosuite_split.go)
	n := len(f.runs)
	warm, real := f.runs[n-2], f.runs[n-1]
	if strings.Contains(warm, ": go list -tags") {
		warm = f.runs[n-3]
	}
	if !strings.HasSuffix(warm, ": go test -race -tags live_db -cover -exec true ./...") {
		t.Errorf("the pass before the suite is %q", warm)
	}
	if !strings.Contains(real, "-p 1 -coverprofile") {
		t.Errorf("the suite is %q", real)
	}

	// A failing build-only pass is not the verdict: the suite's own run is.
	f = &golaneFake{rules: []golaneRule{{"-exec true", "build failed", 1}, {"go list", "11\n", 0}, {"grep -rhoE", "", 1}}}
	if v = gosuiteVerdict(t, f, map[string]string{"go.mod": gosuiteGo}, true); v.State != 0 || strings.Contains(v.Reason, "build failed") {
		t.Errorf("the pass leaked into the verdict: %+v", v)
	}

	// The commit's unit suite builds what it runs: no pass.
	f = &golaneFake{rules: []golaneRule{{"go list", "11\n", 0}}}
	gosuiteVerdict(t, f, map[string]string{"go.mod": gosuiteGo}, false)
	for _, r := range f.runs {
		if strings.Contains(r, "-exec true") {
			t.Errorf("the unit suite warmed a cache it does not use: %q", r)
		}
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

// gosuiteRegenTree is a tree with one package that opted in to the regen test.
var gosuiteRegenTree = map[string]string{
	"go.mod":                      gosuiteGo,
	"classescore/provenance.json": gosuiteProv(gosuiteRegen),
	"classescore/regen_test.go":   "package classescore\n",
}

func gosuiteSum(b string) string {
	h := sha256.Sum256([]byte(b))
	return hex.EncodeToString(h[:])
}

// UT-GRAVITY-SHA-1. THE STAGED GENERATOR ENTERS go test's CACHE KEY. go test
// keys a variable the test reads by value, and never sees a binary outside the
// module, so the suite's exec carries the sha256 of the bytes it will run:
// the file on PATH, or the one GRAVITY names, hashed here and not trusted from
// a table.
func TestGoSuiteExportsTheHashOfTheStagedGravity(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gravity"), []byte("gravity-bytes"), 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "gravity-elsewhere")
	if err := os.WriteFile(other, []byte("other-bytes"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, gravity, path, want string }{
		{"on PATH", "", dir, gosuiteSum("gravity-bytes")},
		{"GRAVITY names it", other, dir, gosuiteSum("other-bytes")},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("GRAVITY", c.gravity)
			t.Setenv("PATH", c.path)
			f := &golaneFake{rules: []golaneRule{{"go list", "11\n", 0}}}
			v := gosuiteVerdict(t, f, gosuiteRegenTree, false)
			if v.State != 0 {
				t.Fatalf("%+v", v)
			}
			env := f.envs[len(f.envs)-1]
			if !slices.Contains(env, "GRAVITY_SHA256="+c.want) {
				t.Errorf("the suite's exec carries %v, want GRAVITY_SHA256=%s", env, c.want)
			}
			for _, e := range f.envs[:len(f.envs)-1] {
				if len(e) != 0 {
					t.Errorf("only the suite's exec carries it: %v", e)
				}
			}
		})
	}
}

// UT-GRAVITY-SHA-2. A tree with no regen test pays nothing: the variable is not
// set, so a star that does not use gravity keeps its cache key.
func TestGoSuiteSetsNoGravityHashWithoutARegenTest(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gravity"), []byte("gravity-bytes"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GRAVITY", "")
	t.Setenv("PATH", dir)
	f := &golaneFake{rules: []golaneRule{{"go list", "11\n", 0}}}
	if v := gosuiteVerdict(t, f, map[string]string{"go.mod": gosuiteGo}, false); v.State != 0 {
		t.Fatalf("%+v", v)
	}
	for _, e := range f.envs {
		if strings.Contains(strings.Join(e, " "), "GRAVITY_SHA256") {
			t.Errorf("a tree with no regen test got %v", e)
		}
	}
}

// UT-GRAVITY-SHA-3. A generator that resolves and cannot be read is a
// could-not-run: running the suite without the hash would serve a stale
// "(cached)" for a generator nobody hashed.
func TestGoSuiteCannotRunWhenTheStagedGravityCannotBeRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gravity"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GRAVITY", "")
	t.Setenv("PATH", dir)
	real := readStaged
	readStaged = func(string) ([]byte, error) { return nil, errors.New("input/output error") }
	t.Cleanup(func() { readStaged = real })
	f := &golaneFake{rules: []golaneRule{{"go list", "11\n", 0}}}
	v := gosuiteVerdict(t, f, gosuiteRegenTree, false)
	if v.State != 2 || !strings.Contains(v.Reason, "CANNOT RUN - the staged gravity could not be hashed: input/output error") {
		t.Fatalf("%+v", v)
	}
	for _, r := range f.runs {
		if strings.Contains(r, "go test") {
			t.Errorf("the suite ran: %v", f.runs)
		}
	}
}

// gravityRevs are the prebuilts the registry holds, one provisioned tool each.
const (
	gravityOld = "68d8146b88e53e619bfbc0a36b9d70ef9c3bcf06"
	gravityNew = "aeb03bfd4a6f14e29b1a91dcdfad05e02695db39"
)

// stageGravities lays, in one PATH directory, the executor's provisioned
// gravity tools (one per prebuilt rev, as tools.toml names them) and returns it.
func stageGravities(t *testing.T, revs ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, rev := range revs {
		if err := os.WriteFile(filepath.Join(dir, checks.GravityTool(rev)), []byte("bytes-of-"+rev), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func gosuiteRegenTreeAt(rev string) map[string]string {
	return map[string]string{
		"go.mod":                      gosuiteGo,
		"classescore/provenance.json": gosuiteProv(rev),
		"classescore/regen_test.go":   "package classescore\n",
	}
}

// UT-GRAVITY-REV-1. THE GENERATOR IS THE TREE'S PIN, NOT THE POD'S: with both
// prebuilts provisioned, a tree pinned at either runs that one. GRAVITY names it
// for go test, and GRAVITY_SHA256 is the hash of those bytes.
func TestGoSuiteStagesTheGravityTheTreeItselfPins(t *testing.T) {
	for _, rev := range []string{gravityOld, gravityNew} {
		t.Run(rev[:7], func(t *testing.T) {
			dir := stageGravities(t, gravityOld, gravityNew)
			t.Setenv("GRAVITY", "")
			t.Setenv("PATH", dir)
			f := &golaneFake{rules: []golaneRule{{"go list", "11\n", 0}}}
			v := gosuiteVerdict(t, f, gosuiteRegenTreeAt(rev), false)
			if v.State != 0 {
				t.Fatalf("%+v", v)
			}
			env := f.envs[len(f.envs)-1]
			for _, want := range []string{"GRAVITY=" + filepath.Join(dir, checks.GravityTool(rev)), "GRAVITY_SHA256=" + gosuiteSum("bytes-of-"+rev)} {
				if !slices.Contains(env, want) {
					t.Errorf("the suite's exec carries %v, want %s", env, want)
				}
			}
		})
	}
}

// UT-GRAVITY-REV-2. A person's GRAVITY outranks the tool the pin would pick.
func TestGoSuiteKeepsAGravityTheCallerNamed(t *testing.T) {
	dir := stageGravities(t, gravityOld)
	other := filepath.Join(t.TempDir(), "mine")
	if err := os.WriteFile(other, []byte("mine"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GRAVITY", other)
	t.Setenv("PATH", dir)
	f := &golaneFake{rules: []golaneRule{{"go list", "11\n", 0}}}
	if v := gosuiteVerdict(t, f, gosuiteRegenTreeAt(gravityOld), false); v.State != 0 {
		t.Fatalf("%+v", v)
	}
	env := f.envs[len(f.envs)-1]
	if !slices.Contains(env, "GRAVITY_SHA256="+gosuiteSum("mine")) {
		t.Errorf("the suite's exec carries %v", env)
	}
	for _, e := range env {
		if strings.HasPrefix(e, "GRAVITY=") {
			t.Errorf("the caller's GRAVITY is already in the environment, not re-set: %v", env)
		}
	}
}

// UT-GRAVITY-REV-3. A rev with no provisioned prebuilt and no gravity on PATH
// is a could-not-run that says how to add it, and the suite never starts.
func TestGoSuiteCannotRunWhenNoGravityIsStagedForThePinnedRev(t *testing.T) {
	t.Setenv("GRAVITY", "")
	t.Setenv("PATH", stageGravities(t, gravityOld, gravityNew))
	f := &golaneFake{rules: []golaneRule{{"go list", "11\n", 0}}}
	v := gosuiteVerdict(t, f, gosuiteRegenTreeAt(gosuiteRegen), false)
	if v.State != 2 || !strings.Contains(v.Reason, "CANNOT RUN - the tree pins gravity "+gosuiteRegen) || !strings.Contains(v.Reason, "checks.GravityPrebuilt") {
		t.Fatalf("%+v", v)
	}
	for _, r := range f.runs {
		if strings.Contains(r, "go test") {
			t.Errorf("the suite ran: %v", f.runs)
		}
	}
}

// UT-GRAVITY-REV-4. THE DAGGER LANE STAGES ONE `gravity` at the tree's rev and
// no per-rev tool: it is used as it is, GRAVITY left alone.
func TestGoSuiteUsesTheGravityOnPathWhenNoPerRevToolIsThere(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gravity"), []byte("staged"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GRAVITY", "")
	t.Setenv("PATH", dir)
	f := &golaneFake{rules: []golaneRule{{"go list", "11\n", 0}}}
	if v := gosuiteVerdict(t, f, gosuiteRegenTreeAt(gravityOld), false); v.State != 0 {
		t.Fatalf("%+v", v)
	}
	env := f.envs[len(f.envs)-1]
	if !slices.Contains(env, "GRAVITY_SHA256="+gosuiteSum("staged")) {
		t.Errorf("the suite's exec carries %v", env)
	}
	for _, e := range env {
		if strings.HasPrefix(e, "GRAVITY=") {
			t.Errorf("GRAVITY set although the PATH one is the pin's: %v", env)
		}
	}
}

// THE CALLER'S ARGUMENTS fold into the race run (GoSuiteFor).
func TestParseSuiteArgs(t *testing.T) {
	for name, c := range map[string]struct {
		in   []string
		want SuiteArgs
	}{
		"none":             {nil, SuiteArgs{}},
		"package":          {[]string{"./internal/store/"}, SuiteArgs{Packages: true, Rest: []string{"./internal/store/"}}},
		"flags pass":       {[]string{"-run", "TestX", "-count=1", "-v"}, SuiteArgs{Rest: []string{"-run", "TestX", "-count=1", "-v"}}},
		"race implied":     {[]string{"-race", "--race=true", "-v"}, SuiteArgs{Rest: []string{"-v"}}},
		"tags separate":    {[]string{"-tags", "live,live_db"}, SuiteArgs{Tags: []string{"live", "live_db"}}},
		"tags joined":      {[]string{"--tags=a b", "-tags=a"}, SuiteArgs{Tags: []string{"a", "b"}}},
		"tags no value":    {[]string{"-tags"}, SuiteArgs{}},
		"p is the callers": {[]string{"-p", "4", "./..."}, SuiteArgs{P: true, Packages: true, Rest: []string{"-p", "4", "./..."}}},
		"args to binary":   {[]string{"./p", "-args", "-race", "./x", "-tags", "z"}, SuiteArgs{Packages: true, Rest: []string{"./p", "-args", "-race", "./x", "-tags", "z"}}},
	} {
		got := ParseSuiteArgs(c.in)
		if !slices.Equal(got.Tags, c.want.Tags) || got.Packages != c.want.Packages || got.P != c.want.P || !slices.Equal(got.Rest, c.want.Rest) {
			t.Errorf("%s: %+v, want %+v", name, got, c.want)
		}
	}
}

func TestUnionTags(t *testing.T) {
	for _, c := range []struct {
		word   string
		caller []string
		want   string
	}{{"", nil, ""}, {"live_db", nil, "live_db"}, {"", []string{"live"}, "live"}, {"live_db,live_kafka", []string{"live", "live_db"}, "live_db,live_kafka,live"}} {
		if got := UnionTags(c.word, c.caller); got != c.want {
			t.Errorf("%q + %v = %q, want %q", c.word, c.caller, got, c.want)
		}
	}
}

func TestSuiteArgsNoteNamesTheUnboundTagsOnly(t *testing.T) {
	s := SuiteArgs{Tags: []string{"live_db", "live_kafka", "live_redis", "live_kube", "live", "other"}}
	got := s.Note(checks.TestDBs[:1], checks.TestBrokers[:1], checks.TestCaches[:1], checks.TestKubes[:1])
	if !strings.Contains(got, "live, other") || strings.Contains(got, "live_db") || strings.Contains(got, "live_kafka") || strings.Contains(got, "live_redis") || strings.Contains(got, "live_kube") {
		t.Errorf("%q", got)
	}
	if got := (SuiteArgs{Tags: []string{"live_db"}}).Note(checks.TestDBs[:1], nil, nil, nil); got != "" {
		t.Errorf("bound tag noted: %q", got)
	}
}

func TestGoSuiteForKeepsTheCallersPAndWritesNoProfile(t *testing.T) {
	f := &golaneFake{rules: []golaneRule{{"go list", "11\n", 0}, {"grep -rhoE", "//go:build live_db\n", 0}}}
	in := gosuiteIn(t, f, map[string]string{"go.mod": gosuiteGo})
	v := GoSuiteFor(t.Context(), in, GoRaceRow, ".", ParseSuiteArgs([]string{"-p", "2", "./a/..."}))
	last := f.runs[len(f.runs)-1]
	if v.State != 0 || !strings.HasSuffix(last, ": go test -race -tags live_db -p 2 ./a/...") {
		t.Errorf("%+v ran %q", v, last)
	}
	for _, r := range f.runs {
		if strings.Contains(r, "-exec true") || strings.Contains(r, "-coverprofile") {
			t.Errorf("the gate's extras ran for a dev run: %q", r)
		}
	}
}

func TestPackageLike(t *testing.T) {
	for arg, want := range map[string]bool{
		"-v": false, "-run": false, "TestX": false, ".": true, "..": true, "./x": true, "../x": true,
		"x/...": true, "example.com/x": true, "internal/store": false,
	} {
		if got := PackageLike(arg); got != want {
			t.Errorf("%q: %v, want %v", arg, got, want)
		}
	}
}

// coverRootIn points GoCoverRoot at dir for the test.
func coverRootIn(t *testing.T, dir string) {
	t.Helper()
	old := GoCoverRoot
	GoCoverRoot = dir
	t.Cleanup(func() { GoCoverRoot = old })
}

// THE PROFILE IS PER ATOM AND PER MODULE, under GoCoverRoot: two atoms that run
// the race suite at once in one pod, and two modules of one tree, never write
// one file.
func TestGoCoverProfileIsPerAtomAndPerModule(t *testing.T) {
	root := t.TempDir()
	coverRootIn(t, root)
	for _, tc := range []struct{ owner, dir, want string }{
		{"go:test-race", ".", "go__test-race/root.out"},
		{"go:test-race", "", "go__test-race/root.out"},
		{"go:test-race", "styx", "go__test-race/styx.out"},
		{"go:test-race", "tools/forge/", "go__test-race/tools%2Fforge.out"},
		{"go:diff-coverage", ".", "go__diff-coverage/root.out"},
	} {
		if got := GoCoverProfile(tc.owner, tc.dir); got != filepath.Join(root, tc.want) {
			t.Errorf("GoCoverProfile(%q, %q) = %s, want %s", tc.owner, tc.dir, got, filepath.Join(root, tc.want))
		}
	}
	if fi, err := os.Stat(filepath.Join(root, "go__diff-coverage")); err != nil || !fi.IsDir() {
		t.Errorf("the owner's directory was not made: %v", err)
	}
}

// A ROOT THAT CANNOT BE MADE (a workstation has no /work) falls back to the same
// layout under the temporary directory, rather than a suite that cannot write.
func TestGoCoverProfileFallsBackWhereTheRootCannotBeMade(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	coverRootIn(t, filepath.Join(file, "cover"))
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	want := filepath.Join(tmp, "go-cover", "go__test-race", "root.out")
	if got := GoCoverProfile("go:test-race", "."); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if fi, err := os.Stat(filepath.Dir(want)); err != nil || !fi.IsDir() {
		t.Errorf("the fallback directory was not made: %v", err)
	}
}

// A RED SUITE LEAVES NO PROFILE, and no run reads one an earlier run left: go
// test writes the profile of a failing run too, and go:diff-coverage reads the
// one go:test-race left while it is there.
func TestGoSuiteLeavesAProfileOnlyForASuiteThatPassed(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		kept bool
	}{
		{"a pass keeps what the suite wrote", 0, true},
		{"findings remove it", 1, false},
		{"a suite that never ran removes it", -1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &golaneFake{rules: []golaneRule{{"go list", "11\n", 0}, {"grep -rhoE", "", 1}}}
			in := gosuiteIn(t, f, map[string]string{"go.mod": gosuiteGo})
			profile := GoCoverProfile(GoRaceRow.ID, ".")
			if err := os.WriteFile(profile, []byte("stale"), 0o644); err != nil {
				t.Fatal(err)
			}
			in.Exec = func(ctx context.Context, c Cmd) (string, int) {
				out, code := f.exec(ctx, c)
				if c.Name == "go" && len(c.Args) > 1 && c.Args[0] == "test" && slices.Contains(c.Args, "-coverprofile") {
					if _, err := os.Stat(profile); err == nil {
						t.Errorf("the run started over the profile an earlier run left")
					}
					_ = os.WriteFile(profile, []byte("mode: atomic\n"), 0o644)
					return "", tc.code
				}
				return out, code
			}
			GoSuite(t.Context(), in, GoRaceRow, ".", true)
			if _, err := os.Stat(profile); (err == nil) != tc.kept {
				t.Errorf("profile kept: %v, want %v", err == nil, tc.kept)
			}
		})
	}
}

// THE RACE RUN go:diff-coverage MAKES writes under its own name, never over
// the profile go:test-race is writing in the same pod.
func TestGoRaceSuiteAsWritesItsOwnersProfile(t *testing.T) {
	f := &golaneFake{rules: []golaneRule{{"go list", "11\n", 0}, {"grep -rhoE", "", 1}}}
	in := gosuiteIn(t, f, map[string]string{"go.mod": gosuiteGo})
	v := GoRaceSuiteAs(t.Context(), in, GoRaceRow, ".", "go:diff-coverage")
	last := f.runs[len(f.runs)-1]
	if v.State != 0 || v.Atom != GoRaceRow.ID || !strings.HasSuffix(last, ": go test -race -coverprofile "+GoCoverProfile("go:diff-coverage", ".")+" ./...") {
		t.Errorf("%+v ran %q", v, last)
	}
}
