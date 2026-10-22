package atoms

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

const splitSigner = "git.notusmi.com/rob/stellar-core-go/signer"

// splitTree is a module of four packages on disk: one whose test asks for
// live_db by tag, one whose code reads the database's variable, one that
// reaches nothing, and the heavy signer (which reaches nothing either). list is
// `go list`'s answer for it.
func splitTree(t *testing.T) (in Input, list string) {
	t.Helper()
	in = treeIn(t, map[string]string{
		"go.mod":            gosuiteGo,
		"db/db_test.go":     "//go:build live_db\n\npackage db\n",
		"env/env.go":        "package env\n\nvar dsn = os.Getenv(\"TEST_DATABASE_URL\")\n",
		"pure/pure.go":      "package pure\n",
		"pure/pure_test.go": "package pure\n",
		// what is not a .go file of the directory is not read: a note that names
		// the database, and a nested package that reaches it
		"pure/NOTES.txt":        "TEST_DATABASE_URL\n",
		"pure/nested/n_test.go": "//go:build live_db\n\npackage nested\n",
		"signer/s.go":           "package signer\n",
	})
	for _, l := range [][2]string{{"x/db", "db"}, {"x/env", "env"}, {"x/pure", "pure"}, {splitSigner, "signer"}} {
		list += l[0] + "\t" + filepath.Join(in.Root, l[1]) + "\n"
	}
	return in, list
}

var splitLive = []string{"live_db", "TEST_DATABASE_URL"}

// THE SERIAL SET is every package that reaches a bound server, by tag or by
// variable, and every heavy one; the rest run in parallel. The list is asked
// under the run's own tags.
func TestSplitRaceSortsThePackages(t *testing.T) {
	in, list := splitTree(t)
	f := &golaneFake{rules: []golaneRule{{"go list", list, 0}}}
	in.Exec = f.exec
	s, ok := splitRace(t.Context(), in, in.Root, []string{"test", "-race", "-tags", "live_db", "-p", "1", "./..."}, splitLive)
	if !ok || !slices.Equal(s.serial, []string{"x/db", "x/env", splitSigner}) || !slices.Equal(s.parallel, []string{"x/pure"}) {
		t.Fatalf("%v %+v", ok, s)
	}
	if len(f.runs) != 1 || !strings.HasSuffix(f.runs[0], ": go list -tags live_db -f {{.ImportPath}}\t{{.Dir}} ./...") {
		t.Errorf("listed as %v", f.runs)
	}
	if got := s.scope(); got != "race split: 3 package(s) at -p 1 (they reach a bound server, or are heavy), 1 at -p 4" {
		t.Errorf("scope %q", got)
	}
}

// EVERY DOUBT IS THE OLD RUN: the split answers not ok, and the suite runs once
// at -p 1.
func TestSplitRaceFallsBackOnEveryDoubt(t *testing.T) {
	in, list := splitTree(t)
	for name, tc := range map[string]struct {
		out  string
		code int
		live []string
	}{
		"the list fails":         {"go: no", 1, splitLive},
		"the list never ran":     {"go: not found", -1, splitLive},
		"a line with no dir":     {"x/pure\n", 0, splitLive},
		"a dir that won't read":  {"x/gone\t" + filepath.Join(in.Root, "gone") + "\n", 0, splitLive},
		"nothing left to spread": {"x/db\t" + filepath.Join(in.Root, "db") + "\n", 0, splitLive},
		"everything reaches":     {list, 0, []string{"package"}},
	} {
		t.Run(name, func(t *testing.T) {
			in.Exec = (&golaneFake{rules: []golaneRule{{"go list", tc.out, tc.code}}}).exec
			if s, ok := splitRace(t.Context(), in, in.Root, []string{"test", "./..."}, tc.live); ok {
				t.Errorf("split as %+v", s)
			}
		})
	}
	// a .go entry that will not read is a doubt too: a link to nothing
	in.Exec = (&golaneFake{rules: []golaneRule{{"go list", list, 0}}}).exec
	if err := os.Symlink(filepath.Join(in.Root, "nowhere"), filepath.Join(in.Root, "pure", "gone.go")); err != nil {
		t.Fatal(err)
	}
	if s, ok := splitRace(t.Context(), in, in.Root, []string{"test", "./..."}, splitLive); ok {
		t.Errorf("an unreadable file split as %+v", s)
	}
}

func TestServerEnvsNamesEveryBoundVariable(t *testing.T) {
	got := serverEnvs(
		[]checks.TestDB{{Env: "TEST_DATABASE_URL"}},
		[]checks.TestBroker{{Env: "KAFKA_BOOTSTRAP", Also: []string{"TEST_KAFKA_BROKERS"}}},
		[]checks.TestCache{{Env: "TEST_REDIS_URL"}, {}},
		[]checks.TestKube{{URLEnv: "TEST_KUBE_URL", TokenEnv: "TEST_KUBE_TOKEN", CAEnv: "TEST_KUBE_CA_FILE"}},
	)
	want := []string{"TEST_DATABASE_URL", "KAFKA_BOOTSTRAP", "TEST_KAFKA_BROKERS", "TEST_REDIS_URL", "TEST_KUBE_URL", "TEST_KUBE_TOKEN", "TEST_KUBE_CA_FILE"}
	if !slices.Equal(got, want) {
		t.Errorf("%v", got)
	}
}

func TestSplitArgsSetsPProfileAndPackages(t *testing.T) {
	args := []string{"test", "-race", "-tags", "live_db", "-p", "1", "-coverprofile", "/c/root.out", "./..."}
	got := splitArgs(args, 4, "/c/root.out.parallel", []string{"x/a", "x/b"})
	want := []string{"test", "-race", "-tags", "live_db", "-p", "4", "-coverprofile", "/c/root.out.parallel", "x/a", "x/b"}
	if !slices.Equal(got, want) || args[5] != "1" {
		t.Errorf("%v (the run's own args: %v)", got, args)
	}
}

// splitExec answers each `go test` with code by its -p, writing body to its
// -coverprofile when body is set.
func splitExec(runs *[][]string, codes map[string]int, bodies map[string]string) Exec {
	return func(_ context.Context, c Cmd) (string, int) {
		*runs = append(*runs, c.Args)
		p := c.Args[slices.Index(c.Args, "-p")+1]
		prof := c.Args[slices.Index(c.Args, "-coverprofile")+1]
		if b, ok := bodies[p]; ok {
			_ = os.WriteFile(prof, []byte(b), 0o644)
		}
		return "out -p " + p, codes[p]
	}
}

// THE TWO RUNS, serial set first, each with its own -p and profile; the profiles
// merged into the one the run owes, the parts gone; the exit the worse of the two.
func TestRaceSplitRunsBothSetsAndMergesTheirProfiles(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, "root.out")
	args := []string{"test", "-race", "-tags", "live_db", "-p", "1", "-coverprofile", profile, "./..."}
	s := raceSplit{serial: []string{"x/db"}, parallel: []string{"x/a", "x/b"}}
	bodies := map[string]string{"1": "mode: atomic\nx/db/a.go:1.1,2.2 1 1\n", "4": "mode: atomic\nx/a/a.go:1.1,2.2 1 0\n"}
	for _, tc := range []struct {
		name  string
		codes map[string]int
		want  int
	}{
		{"both pass", map[string]int{}, 0},
		{"the parallel set fails", map[string]int{"4": 1}, 1},
		{"the serial set fails", map[string]int{"1": 1}, 1},
		{"one would not start", map[string]int{"1": -1, "4": 1}, -1},
		{"the second would not start", map[string]int{"4": -1}, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var runs [][]string
			in := Input{Root: dir, Exec: splitExec(&runs, tc.codes, bodies)}
			out, code := s.run(t.Context(), in, dir, args, nil, profile)
			if code != tc.want || out != "out -p 1\nout -p 4" {
				t.Errorf("code %d out %q", code, out)
			}
			if len(runs) != 2 || !slices.Equal(runs[0][len(runs[0])-1:], []string{"x/db"}) || !slices.Equal(runs[1][len(runs[1])-2:], []string{"x/a", "x/b"}) {
				t.Errorf("ran %v", runs)
			}
			b, err := os.ReadFile(profile)
			if err != nil || string(b) != "mode: atomic\nx/db/a.go:1.1,2.2 1 1\nx/a/a.go:1.1,2.2 1 0\n" {
				t.Errorf("merged %q %v", b, err)
			}
			for _, part := range []string{profile + ".serial", profile + ".parallel"} {
				if _, err := os.Stat(part); err == nil {
					t.Errorf("%s left behind", part)
				}
			}
		})
	}
	// a part that was never written is no merged profile, and no stale one
	_ = os.WriteFile(profile, []byte("stale"), 0o644)
	var runs [][]string
	in := Input{Root: dir, Exec: splitExec(&runs, nil, map[string]string{"1": "mode: atomic\n"})}
	if _, code := s.run(t.Context(), in, dir, args, nil, profile); code != 0 {
		t.Fatal(code)
	}
	if _, err := os.Stat(profile); err == nil {
		t.Error("a merged profile from one part")
	}
	// an empty set is not run
	runs = nil
	in = Input{Root: dir, Exec: splitExec(&runs, nil, bodies)}
	if _, code := (raceSplit{parallel: []string{"x/a"}}).run(t.Context(), in, dir, args, nil, profile); code != 0 || len(runs) != 1 {
		t.Errorf("ran %v", runs)
	}
}

func TestMergeProfilesKeepsOneModeLine(t *testing.T) {
	dir := t.TempDir()
	a, b, c := filepath.Join(dir, "a"), filepath.Join(dir, "b"), filepath.Join(dir, "c")
	_ = os.WriteFile(a, []byte("mode: atomic\nA 1 1\n"), 0o644)
	_ = os.WriteFile(b, []byte("mode: atomic\nB 1 0\n"), 0o644)
	_ = os.WriteFile(c, []byte("C 1 1\n"), 0o644)
	got, err := mergeProfiles([]string{a, b, c})
	if err != nil || string(got) != "mode: atomic\nA 1 1\nB 1 0\nC 1 1\n" {
		t.Errorf("%q %v", got, err)
	}
	if _, err := mergeProfiles([]string{a, filepath.Join(dir, "none")}); err == nil {
		t.Error("a missing part merged")
	}
}

// THROUGH THE SUITE: a tagged tree whose packages split runs the two sets and
// says so; the profile the run owes is the merge.
func TestGoSuiteRaceRunsTheSplit(t *testing.T) {
	in, list := splitTree(t)
	coverRootIn(t, t.TempDir())
	f := &golaneFake{rules: []golaneRule{
		{"go list -tags", list, 0},
		{"go list", "11\n", 0},
		{"grep -rhoE", "//go:build live_db\n", 0},
	}}
	var tests []string
	in.Exec = func(ctx context.Context, c Cmd) (string, int) {
		out, code := f.exec(ctx, c)
		if c.Name == "go" && slices.Contains(c.Args, "-coverprofile") {
			tests = append(tests, strings.Join(c.Args, " "))
			_ = os.WriteFile(c.Args[slices.Index(c.Args, "-coverprofile")+1], []byte("mode: atomic\n"), 0o644)
		}
		return out, code
	}
	v := GoSuite(t.Context(), in, GoRaceRow, ".", true)
	profile := GoCoverProfile(GoRaceRow.ID, ".")
	want := []string{
		"test -race -tags live_db -p 1 -coverprofile " + profile + ".serial x/db x/env " + splitSigner,
		"test -race -tags live_db -p 4 -coverprofile " + profile + ".parallel x/pure",
	}
	if v.State != 0 || !slices.Equal(tests, want) || !strings.Contains(v.Reason, "race split: 3 package(s) at -p 1") {
		t.Fatalf("%+v ran %q", v, tests)
	}
	if b, err := os.ReadFile(profile); err != nil || string(b) != "mode: atomic\n" {
		t.Errorf("profile %q %v", b, err)
	}
}
