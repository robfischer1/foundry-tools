package fleetbunpackagetest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

type script struct {
	calls []atoms.Cmd
	exit  int
	out   string
}

func (s *script) exec(_ context.Context, c atoms.Cmd) (string, int) {
	s.calls = append(s.calls, c)
	return s.out, s.exit
}

func (s *script) argvs() []string {
	var out []string
	for _, c := range s.calls {
		out = append(out, strings.Join(append([]string{c.Name}, c.Args...), " "))
	}
	return out
}

func tree(s *script, files ...string) atomkit.In {
	return atomkit.In{Root: "/src", Files: files, Exec: s.exec}
}

func expect(t *testing.T, v checks.Verdict, state checks.State, needles ...string) {
	t.Helper()
	if v.State != int(state) {
		t.Errorf("state %d, want %d:\n%s\n%v", v.State, state, v.Reason, v.Logs)
	}
	said := v.Reason + "\n" + strings.Join(v.Logs, "\n")
	for _, n := range needles {
		if !strings.Contains(said, n) {
			t.Errorf("lacks %q:\n%s", n, said)
		}
	}
}

func TestTheDeclarationValidates(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrecommit || Def.Lane != checks.LaneAny || Def.Toolchain != "bun" || Def.Surface != "any:**/*.test.ts" {
		t.Errorf("%+v", Def)
	}
}

func TestAPackageLocalTestDirectoryRunsUnderBun(t *testing.T) {
	s := &script{out: "17 pass\n"}
	v := check(context.Background(), tree(s, "payload/pi-cerberus/index.ts", "payload/pi-cerberus/index.test.ts", "payload/pi-cerberus/git.test.ts", "src/main.rs"))
	expect(t, v, checks.StatePass, "bun test ran in payload/pi-cerberus")
	if got := s.argvs(); len(got) != 1 || got[0] != "bun test payload/pi-cerberus/" {
		t.Errorf("ran %v", got)
	}
	if s.calls[0].Dir != "/src" || !s.calls[0].Both {
		t.Errorf("%+v", s.calls[0])
	}
}

func TestAFailingTestIsAFindingAndAnythingElseIsCouldNotRun(t *testing.T) {
	files := []string{"pkg/a.test.ts"}
	for _, c := range []struct {
		exit  int
		state checks.State
	}{{1, checks.StateFindings}, {127, checks.StateCannotRun}, {-1, checks.StateCannotRun}, {137, checks.StateCannotRun}} {
		s := &script{exit: c.exit, out: "bun: boom"}
		expect(t, check(context.Background(), tree(s, files...)), c.state)
	}
	s := &script{exit: 1, out: "1 fail"}
	expect(t, check(context.Background(), tree(s, files...)), checks.StateFindings, "bun test pkg exited 1", "1 fail")
}

func TestNoTestFileOrARootPackageJSONIsAbsentAndSaysWhy(t *testing.T) {
	s := &script{}
	expect(t, check(context.Background(), tree(s, "src/main.go")), checks.StatePass, "ABSENT - this tree holds no TypeScript or JavaScript test file")
	expect(t, check(context.Background(), tree(s, "package.json", "pkg/a.test.ts")), checks.StatePass, "ABSENT", "root package.json", "ts:bun-gate")
	expect(t, check(context.Background(), tree(s, "tests/test_x.py", "data_test_1.json")), checks.StatePass, "ABSENT - this tree holds no TypeScript")
	if len(s.calls) != 0 {
		t.Errorf("an absent atom ran %v", s.argvs())
	}
}

func TestATestUnderItsOwnPackageJSONIsNamedNotGraded(t *testing.T) {
	s := &script{}
	v := check(context.Background(), tree(s, "web/package.json", "web/a.test.ts"))
	expect(t, v, checks.StatePass, "ABSENT", "NOT GRADED", "web")
	if len(s.calls) != 0 {
		t.Errorf("ran %v", s.argvs())
	}
	// beside a package-local one, the nested one is named on the pass line
	v = check(context.Background(), tree(s, "web/package.json", "web/a.test.ts", "ext/b.test.ts"))
	expect(t, v, checks.StatePass, "bun test ran in ext", "NOT GRADED", "web")
	if got := s.argvs(); len(got) != 1 || got[0] != "bun test ext/" {
		t.Errorf("ran %v", got)
	}
}

func TestNestedDirectoriesCollapseAndRootTestsRunEverything(t *testing.T) {
	s := &script{}
	expect(t, check(context.Background(), tree(s, "ext/a.test.ts", "ext/deep/b.test.ts", "other/c.spec.js")), checks.StatePass, "ext, other")
	if got := s.argvs(); len(got) != 2 || got[0] != "bun test ext/" || got[1] != "bun test other/" {
		t.Errorf("ran %v", got)
	}
	s = &script{}
	expect(t, check(context.Background(), tree(s, "a.test.ts", "ext/b.test.ts")), checks.StatePass, "bun test ran in .")
	if got := s.argvs(); len(got) != 1 || got[0] != "bun test" {
		t.Errorf("ran %v", got)
	}
}

func TestATsconfigBesideAPackageLocalTestDirIsCouldNotRunNeverASkip(t *testing.T) {
	for _, tsc := range []string{"ext/tsconfig.json", "tsconfig.json"} {
		s := &script{}
		expect(t, check(context.Background(), tree(s, "ext/a.test.ts", tsc)), checks.StateCannotRun, "CANNOT RUN", tsc, "pins a tsc")
		if len(s.calls) != 0 {
			t.Errorf("ran %v", s.argvs())
		}
	}
}

func TestATreeThatCannotBeEnumeratedIsCouldNotRun(t *testing.T) {
	in := atomkit.In{FilesErr: errors.New("boom")}
	expect(t, check(context.Background(), in), checks.StateCannotRun)
}

func TestFixturesAndTemplatesAreNotPackages(t *testing.T) {
	s := &script{}
	for _, p := range []string{
		"tests/golden/testdata/fleet/delta/1/__tests__/a.test.ts",
		"web/fixtures/a.test.ts",
		"vendor/x/a.test.js",
		"template/{% if not app %}__tests__{% endif %}/a.test.ts",
		"template/{{ name }}/a.test.ts",
	} {
		expect(t, check(context.Background(), tree(s, p)), checks.StatePass, "ABSENT - this tree holds no TypeScript or JavaScript test file")
	}
	// a real package beside a fixture still runs, and only it
	expect(t, check(context.Background(), tree(s, "ext/a.test.ts", "ext/testdata/b.test.ts")), checks.StatePass, "bun test ran in ext")
	if got := s.argvs(); len(got) != 1 || got[0] != "bun test ext/" {
		t.Errorf("ran %v", got)
	}
}
