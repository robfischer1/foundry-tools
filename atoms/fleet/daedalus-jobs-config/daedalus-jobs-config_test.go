package fleetdaedalusjobsconfig

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atomlib/atomtest"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

const refusal = `REFPOD_PEER_ID="": a refPod table authorises one caller, and the lanes are armed with none named`

func images(tag string) string {
	return "images:\n  - name: registry.notusmi.com/rob/naiad\n    newTag: 1792543862-30d36bb # {\"$imagepolicy\": \"flux-system:naiad:tag\"}\n" +
		"  - name: registry.notusmi.com/rob/daedalus\n    newTag: " + tag + " # {\"$imagepolicy\": \"flux-system:daedalus:tag\"}\n" +
		"    digest: sha256:fa23\n  - name: registry.notusmi.com/rob/glaucus\n    newTag: 1792597308-56dd868\n"
}

// script answers `go install` and the installed checker from a table.
type script struct {
	installOut  string
	installCode int
	checkOut    string
	checkCode   int
	// what the atom ran
	installArgs   string
	installedInto string
	checkName     string
	checkArgs     string
}

func (s *script) exec(_ context.Context, c atoms.Cmd) (string, int) {
	if c.Name == "go" {
		s.installArgs = strings.Join(c.Args, " ")
		for _, e := range c.Env {
			if strings.HasPrefix(e, "GOBIN=") {
				s.installedInto = strings.TrimPrefix(e, "GOBIN=")
			}
		}
		return s.installOut, s.installCode
	}
	s.checkName, s.checkArgs = c.Name, strings.Join(c.Args, " ")
	return s.checkOut, s.checkCode
}

func run(t *testing.T, s *script, files map[string]string) checks.Verdict {
	t.Helper()
	in := atomtest.TreeIn(t, files)
	in.Exec = s.exec
	return check(context.Background(), in)
}

func TestRDL1_TheDeclarationValidatesAndNamesItsRow(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrecommit || Def.Lane != checks.LaneAny || Def.Toolchain != "go" ||
		Def.Surface != "any:"+jobs || Def.Group != checks.GroupOf(id) {
		t.Errorf("%+v", Def)
	}
}

// The #5748 data: the parser's refusal comes back word for word as a finding.
func TestRDL1_ARefusedConfigIsAFindingWithTheParsersWords(t *testing.T) {
	s := &script{checkOut: "jobs: refused — a setting did not parse; the runner stays off: " + refusal, checkCode: 1}
	v := run(t, s, map[string]string{jobs: "data: {}", pins: images("1792622669-4b68a21")})
	atomtest.Expect(t, v, atomtest.StateOf(1), atomtest.Findings, refusal, "4b68a21")
	if !strings.Contains(s.installArgs, "install git.notusmi.com/rob/daedalus/cmd/jobs-config-check@4b68a21") {
		t.Errorf("installed %q: not the checker at the build flux deploys", s.installArgs)
	}
	if !strings.HasSuffix(s.checkName, "/jobs-config-check") || s.checkArgs != jobs {
		t.Errorf("ran %q %q", s.checkName, s.checkArgs)
	}
	if !strings.HasPrefix(s.checkName, s.installedInto) {
		t.Errorf("ran %q, installed into %q", s.checkName, s.installedInto)
	}
}

func TestRDL1_AConfigTheRunnerBootsOnPasses(t *testing.T) {
	s := &script{checkOut: "jobs-config-check: prime/daedalus-jobs.yaml parses; the runner would boot on it", checkCode: 0}
	atomtest.Expect(t, run(t, s, map[string]string{jobs: "data: {}", pins: images("1792622669-4b68a21")}), atomtest.StateOf(0), atomtest.Pass, "boot on it")
}

// A parser that could not look is could-not-run, never a pass.
func TestRDL1_CouldNotLookIsCannotRun(t *testing.T) {
	s := &script{checkOut: "jobs-config-check: could not look: x", checkCode: 2}
	atomtest.Expect(t, run(t, s, map[string]string{jobs: "data: {}", pins: images("1792622669-4b68a21")}), atomtest.StateOf(2), atomtest.Cannot, "could not look")
}

// A build that predates the checker (a rollback) must not stop the rollback.
func TestRDL1_ABuildWithoutTheCheckerIsAbsent(t *testing.T) {
	s := &script{installOut: "go: git.notusmi.com/rob/daedalus@4b68a21 (v0.0.0-x) found, but does not contain package git.notusmi.com/rob/daedalus/cmd/jobs-config-check", installCode: 1}
	v := run(t, s, map[string]string{jobs: "data: {}", pins: images("1792622669-4b68a21")})
	atomtest.Expect(t, v, atomtest.StateOf(0), atomtest.Absent, "predates")
	if s.checkName != "" {
		t.Errorf("ran %q after a failed install", s.checkName)
	}
}

// Any other install failure is the proxy's bad hour: could-not-run, not a finding.
func TestRDL1_AnInstallFailureIsCannotRun(t *testing.T) {
	s := &script{installOut: "go: reading x: 502 Bad Gateway", installCode: 1}
	atomtest.Expect(t, run(t, s, map[string]string{jobs: "data: {}", pins: images("1792622669-4b68a21")}), atomtest.StateOf(2), atomtest.Cannot, "502 Bad Gateway")
	s = &script{installOut: "no go", installCode: -1}
	atomtest.Expect(t, run(t, s, map[string]string{jobs: "data: {}", pins: images("1792622669-4b68a21")}), atomtest.StateOf(2), atomtest.Cannot, "no go")
}

func TestRDL1_AnUnreadableImagePinIsCannotRun(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"no kustomization": {jobs: "data: {}"},
		"no daedalus pin":  {jobs: "data: {}", pins: "images:\n  - name: registry.notusmi.com/rob/naiad\n    newTag: 1-abcdef0\n"},
		"tag has no sha":   {jobs: "data: {}", pins: images("latest")},
	} {
		s := &script{}
		v := run(t, s, files)
		if v.State != 2 || s.installArgs != "" {
			t.Errorf("%s: state %d, installed %q\n%s", name, v.State, s.installArgs, v.Reason)
		}
	}
}

func TestRDL1_ACheckerThatWillNotStartIsCannotRun(t *testing.T) {
	s := &script{checkOut: "fork/exec: no such file", checkCode: -1}
	atomtest.Expect(t, run(t, s, map[string]string{jobs: "data: {}", pins: images("1792622669-4b68a21")}), atomtest.StateOf(2), atomtest.Cannot, "no such file")
}

// A tree without the ConfigMap is absent, and says so in the words the index keeps.
func TestRDL1_ATreeWithoutTheConfigMapIsAbsent(t *testing.T) {
	s := &script{}
	v := run(t, s, map[string]string{"README.md": "x"})
	atomtest.Expect(t, v, atomtest.StateOf(0), atomtest.Absent, "carries no "+jobs)
	if s.installArgs != "" || s.checkName != "" {
		t.Errorf("ran %q / %q over a tree without the ConfigMap", s.installArgs, s.checkName)
	}
}

// A work directory the atom cannot make its scratch in is could-not-run, and no program starts.
func TestRDL1_AnUnwritableWorkDirIsCannotRun(t *testing.T) {
	t.Setenv("ATOM_WORK", t.TempDir()+"/not-there")
	s := &script{}
	v := run(t, s, map[string]string{jobs: "data: {}", pins: images("1792622669-4b68a21")})
	atomtest.Expect(t, v, atomtest.StateOf(2), atomtest.Cannot, "the atom never ran")
	if s.installArgs != "" {
		t.Errorf("installed %q with no scratch directory", s.installArgs)
	}
}
