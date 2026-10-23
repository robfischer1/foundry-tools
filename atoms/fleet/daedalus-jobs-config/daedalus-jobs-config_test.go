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
	// greenOut and greenCode answer the checker when it is run over the green file.
	greenOut  string
	greenCode int
	ran       []string
	installs  []string
	// what the atom ran
	installArgs   string
	installedInto string
	checkName     string
	checkArgs     string
}

func (s *script) exec(_ context.Context, c atoms.Cmd) (string, int) {
	if c.Name == "go" {
		s.installArgs = strings.Join(c.Args, " ")
		s.installs = append(s.installs, s.installArgs)
		for _, e := range c.Env {
			if strings.HasPrefix(e, "GOBIN=") {
				s.installedInto = strings.TrimPrefix(e, "GOBIN=")
			}
		}
		return s.installOut, s.installCode
	}
	s.checkName, s.checkArgs = c.Name, strings.Join(c.Args, " ")
	s.ran = append(s.ran, s.checkArgs)
	if s.checkArgs == jobsGreen {
		return s.greenOut, s.greenCode
	}
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
		Def.Surface != "any:prime/daedalus-jobs*.yaml" || Def.Group != checks.GroupOf(id) {
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

// imagesBoth is the pin list with a pin for each colour.
func imagesBoth(blue, green string) string {
	return images(blue) + "  - name: registry.notusmi.com/rob/daedalus-green\n    newName: registry.notusmi.com/rob/daedalus\n    newTag: " + green + " # {\"$imagepolicy\": \"flux-system:daedalus-green:tag\"}\n    digest: sha256:fb34\n"
}

// BG1: the green ConfigMap is parsed by the build the green pin names, which is
// not necessarily the build blue runs.
func TestBG1_TheGreenConfigMapIsParsedAtTheGreenPin(t *testing.T) {
	s := &script{checkOut: "ok blue", greenOut: "ok green"}
	v := run(t, s, map[string]string{jobs: "data: {}", jobsGreen: "data: {}", pins: imagesBoth("1792622669-4b68a21", "1792700000-9999999")})
	atomtest.Expect(t, v, atomtest.StateOf(0), atomtest.Pass, "ok blue", "ok green")
	if len(s.installs) != 2 || !strings.HasSuffix(s.installs[0], "@4b68a21") || !strings.HasSuffix(s.installs[1], "@9999999") {
		t.Errorf("installs = %q: blue at blue's build, green at green's", s.installs)
	}
	if len(s.ran) != 2 || s.ran[0] != jobs || s.ran[1] != jobsGreen {
		t.Errorf("ran %q over", s.ran)
	}
}

// A refusal in the green map is a finding that names the green file and build.
func TestBG1_AGreenRefusalIsAFindingNamingTheGreenFile(t *testing.T) {
	s := &script{checkOut: "ok blue", greenOut: "jobs: refused — " + refusal, greenCode: 1}
	v := run(t, s, map[string]string{jobs: "data: {}", jobsGreen: "data: {}", pins: imagesBoth("1792622669-4b68a21", "1792700000-9999999")})
	atomtest.Expect(t, v, atomtest.StateOf(1), atomtest.Findings, refusal, jobsGreen, "9999999")
}

// A green map with no green pin cannot be judged: could-not-run, never a pass.
func TestBG1_AGreenMapWithNoGreenPinIsCannotRun(t *testing.T) {
	s := &script{}
	v := run(t, s, map[string]string{jobs: "data: {}", jobsGreen: "data: {}", pins: images("1792622669-4b68a21")})
	if v.State != 2 {
		t.Errorf("state %d\n%s", v.State, v.Reason)
	}
}

// A tree with the blue map alone is judged as before.
func TestBG1_NoGreenMapIsTheOldBehaviour(t *testing.T) {
	s := &script{checkOut: "ok blue"}
	atomtest.Expect(t, run(t, s, map[string]string{jobs: "data: {}", pins: images("1792622669-4b68a21")}), atomtest.StateOf(0), atomtest.Pass, "ok blue")
	if len(s.ran) != 1 {
		t.Errorf("ran %q", s.ran)
	}
}
