// The build lane's record, driven through the real phase sequence.
//
// THESE RUN THE LANE, not the recorder. seal/unreached/renderLog could be
// tested on a hand-built buildLane in a tenth of the lines, and that test would
// pass forever while run() stopped calling them — which is the failure that
// matters, because the phases are only worth anything if the code path actually
// seals them. So each case scripts the engine into a real outcome and reads the
// record the lane would have posted.
package main

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/buildlane"
)

// laneFor runs the lane exactly as Build does and hands back both the verdict
// and the lane, so a test can read the record off it.
//
// A TIP CARRIES ITS CREDENTIALS, the same ones tipWith hands Build. Without
// them preflight refuses before anything else runs, and a test about the scan
// would be measuring the argument check instead.
func laneFor(t *testing.T, m *FoundryTools, tip bool) (*buildLane, int, string) {
	t.Helper()
	l := &buildLane{m: m, tip: tip, registry: "registry.notusmi.com",
		sourceBase: "https://forgejo.notusmi.com/rob", stamp: "1",
		// EXACTLY AS Build DOES, including the phase order — without it
		// Unreached is empty and every case about what a run did NOT reach
		// passes vacuously.
		phases: phases{group: buildGroup, order: buildPhases}}
	if tip {
		l.registryAuth = dag.SetSecret("registry-auth", `{"auths":{"registry.notusmi.com":{"username":"publisher","password":"hunter2"}}}`)
		l.cosignKey = dag.SetSecret("cosign-key", base64.StdEncoding.EncodeToString([]byte("-----BEGIN ENCRYPTED SIGSTORE PRIVATE KEY-----")))
		l.cosignPassphrase = dag.SetSecret("cosign-password", "pw")
	}
	code, reason := l.run(context.Background())
	return l, code, reason
}

// atomNames is the record's atoms in the order they sealed.
func atomNames(l *buildLane) []string {
	out := make([]string, 0, len(l.atoms))
	for _, a := range l.atoms {
		out = append(out, a.Atom)
	}
	return out
}

func atomNamed(t *testing.T, l *buildLane, name string) AtomResult {
	t.Helper()
	for _, a := range l.atoms {
		if a.Atom == name {
			return a
		}
	}
	t.Fatalf("no atom %q in %v", name, atomNames(l))
	return AtomResult{}
}

// A PULL SEALS EVERY PHASE IT RAN AND CLAIMS NONE IT DID NOT. Publishing,
// signing and the permit are the landing's; a pull that reported them as held
// would claim it proved something it never ran.
func TestAPullSealsThePhasesItRanAndLeavesTheRestUnreached(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	l, code, _ := laneFor(t, m, false)

	if code != buildlane.Clean {
		t.Fatalf("a clean pull settles clean, got %d", code)
	}
	want := []string{"build:preflight", "build:dependencies", "build:detect", "build:release", "build:image", "build:verify"}
	if got := atomNames(l); !equalStrings(got, want) {
		t.Fatalf("phases sealed\n want %v\n  got %v", want, got)
	}
	rec := l.record("build", code)
	if rec.State != buildlane.Clean {
		t.Fatalf("the record's state is the lane's verdict, got %d", rec.State)
	}
	wantUnreached := []string{"build:publish", "build:sign"}
	if !equalStrings(rec.Unreached, wantUnreached) {
		t.Fatalf("unreached\n want %v\n  got %v", wantUnreached, rec.Unreached)
	}
}

// A SCAN THAT FINDS SOMETHING STOPS THE LANE AT THAT PHASE, and the record says
// which one — the whole reason this exists. Before it, a red build carried an
// exit code and 400 lines of pod log.
func TestAFindingSealsTheScanAndStopsThere(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	scriptATip()
	engine.script(script{match: imageReportNeedle, leaf: "contents", value: report(grpcFinding)})
	l, code, _ := laneFor(t, m, true)

	if code != buildlane.Findings {
		t.Fatalf("a fixable finding settles findings, got %d", code)
	}
	verify := atomNamed(t, l, "build:verify")
	if verify.State != buildlane.Findings || verify.Result != "findings" {
		t.Fatalf("the scan's atom carries the finding, got state=%d result=%q", verify.State, verify.Result)
	}
	if !strings.Contains(verify.Reason, "fixable HIGH or CRITICAL") {
		t.Fatalf("the scan's atom carries its own reason, got %q", verify.Reason)
	}
	rec := l.record("build", code)
	for _, after := range []string{"build:publish", "build:sign"} {
		if contains(atomNames(l), after) {
			t.Fatalf("%s sealed although the scan stopped the lane", after)
		}
		if !contains(rec.Unreached, after) {
			t.Fatalf("%s must be UNREACHED, not absent: %v", after, rec.Unreached)
		}
	}
}

// A STAND-DOWN IS CLEAN AND BUILT NOTHING, and the phases after it are
// unreached rather than passed — a distinction the exit code cannot draw at all.
func TestAStandDownSealsDetectAndReachesNoFurther(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	published(permittedSha)
	engine.stdout("--name-only", "README.md\ndocs/guide.md\n.claude/settings.json\n")
	l, code, _ := laneFor(t, m, false)

	if code != buildlane.Clean {
		t.Fatalf("a stand-down is clean, got %d", code)
	}
	detect := atomNamed(t, l, "build:detect")
	if !strings.HasPrefix(detect.Reason, "stood down:") {
		t.Fatalf("detect must say it stood down, got %q", detect.Reason)
	}
	if contains(atomNames(l), "build:image") {
		t.Fatal("a stand-down built nothing, so build:image must not seal")
	}
	// The detect atom's log is the line the pod log carried: the stand-down's
	// reason, said once before the phase sealed.
	if logs := detect.Logs; len(logs) == 0 || !strings.Contains(logs[len(logs)-1], "is inert") {
		t.Errorf("detect's log does not carry its reason: %q", logs)
	}
	if !contains(l.record("build", code).Unreached, "build:image") {
		t.Fatal("build:image must read as unreached on a stand-down")
	}
}

// A LANE THAT CANNOT START SEALS ONE PHASE AND NOTHING ELSE. Every later phase
// is unreached, which is the honest account of a run that never began.
func TestAPreflightRefusalSealsOnlyPreflight(t *testing.T) {
	engine.reset()
	l, code, _ := laneFor(t, &FoundryTools{Source: dag.Directory()}, false)

	if code != buildlane.CouldNotRun {
		t.Fatalf("an unfetched tree could not run, got %d", code)
	}
	if got := atomNames(l); !equalStrings(got, []string{"build:preflight"}) {
		t.Fatalf("only preflight seals, got %v", got)
	}
	rec := l.record("build", code)
	if len(rec.Unreached) != len(buildPhases)-1 {
		t.Fatalf("every other phase is unreached, got %v", rec.Unreached)
	}
}

// EVERY ATOM CARRIES A LOG LIST, never a nil one. An absent list and an empty
// list read differently to anyone looking at the JSON, and "this phase printed
// nothing" is a fact worth being able to see.
func TestEveryAtomCarriesALogListEvenWhenItPrintedNothing(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	l, _, _ := laneFor(t, m, false)

	if len(l.atoms) == 0 {
		t.Fatal("the lane sealed no atoms at all")
	}
	for _, a := range l.atoms {
		if a.Logs == nil {
			t.Fatalf("%s carries a nil log list", a.Atom)
		}
	}
}

// THE PHASE THAT PRINTED A LINE KEEPS IT, and the phase after it does not. A
// recorder that never drained would hand every later atom the whole run's
// output, which is worse than no logs at all — it would read as evidence.
func TestAPhasesLinesBelongToThatPhaseAlone(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	l, _, _ := laneFor(t, m, false)

	pre := atomNamed(t, l, "build:preflight")
	if len(pre.Logs) != 1 || !strings.Contains(pre.Logs[0], "pull-time build") {
		t.Fatalf("preflight keeps the line it printed, got %v", pre.Logs)
	}
	for _, a := range l.atoms {
		if a.Atom == "build:preflight" {
			continue
		}
		for _, line := range a.Logs {
			if strings.Contains(line, "pull-time build") {
				t.Fatalf("%s inherited preflight's line: %q", a.Atom, line)
			}
		}
	}
}

// THE THREE STATES MAP ONTO THE GATE'S OWN VOCABULARY, so one reader folds both
// lanes. A fourth state does not exist and must not be invented here.
func TestTheLanesStatesReadAsTheGatesResults(t *testing.T) {
	for _, c := range []struct {
		code int
		want string
	}{
		{buildlane.Clean, "pass"},
		{buildlane.Findings, "findings"},
		{buildlane.CouldNotRun, "cannot-run"},
	} {
		if got := phaseResult(c.code); got != c.want {
			t.Errorf("phaseResult(%d) = %q, want %q", c.code, got, c.want)
		}
	}
}

// THE RENDERED LOG NAMES WHAT NEVER RAN, because a person reading the record
// needs the same distinction the structure carries.
func TestTheRenderedLogNamesWhatWasNeverReached(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	l, code, _ := laneFor(t, m, false)

	log := l.record("build", code).Log
	if !strings.Contains(log, "build:verify: pass") {
		t.Fatalf("the log names each phase and its verdict:\n%s", log)
	}
	if !strings.Contains(log, "never reached: build:publish, build:sign") {
		t.Fatalf("the log names what never ran:\n%s", log)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// THE BASES PATH IS A FANOUT, ONE ATOM PER BASE, and nothing is unreached: a
// base that fails does not spare the others — they are independent images and
// the run grades all of them — so "the ones that ran" and "the ones there were"
// are the same list.
func TestEveryBaseSealsItsOwnAtomAndNothingIsUnreached(t *testing.T) {
	m := basesOn(t, baseTree())
	engine.stdout("--name-only", "stellar-boot/main.go\n")
	engine.script(script{match: trivyReport, leaf: "contents", value: cleanReport})
	l, code, _ := laneFor(t, m, false)

	if code != buildlane.Clean {
		t.Fatalf("clean bases settle clean, got %d", code)
	}
	for _, b := range []string{"go", "rust"} {
		a := atomNamed(t, l, baseAtom(b))
		if a.State != buildlane.Clean {
			t.Fatalf("%s: state %d", a.Atom, a.State)
		}
	}
	rec := l.record("build", code)
	if len(rec.Unreached) != 0 {
		t.Fatalf("a fanout leaves nothing unreached, got %v", rec.Unreached)
	}
	// AND THE STAR PHASES ARE NOT CLAIMED. A bases run never had an image to
	// publish, so naming build:publish at all — held or unreached — would
	// describe a step this shape of run does not contain.
	for _, p := range []string{"build:image", "build:publish", "build:sign"} {
		if contains(atomNames(l), p) {
			t.Fatalf("a bases run sealed the star phase %s", p)
		}
	}
}

// A BASE THAT FAILS DOES NOT STOP THE ONES AFTER IT, and each atom carries its
// own verdict — which is the difference between a fanout and a sequence, and
// the reason Unreached is empty for this path.
func TestABaseThatFailsLeavesTheOthersGraded(t *testing.T) {
	m := basesOn(t, baseTree())
	engine.stdout("--name-only", "stellar-boot/main.go\n")
	engine.script(script{match: trivyReport, leaf: "contents", value: report(grpcFinding)})
	l, code, _ := laneFor(t, m, false)

	if code != buildlane.Findings {
		t.Fatalf("a finding in a base settles findings, got %d", code)
	}
	for _, b := range []string{"go", "rust"} {
		if !contains(atomNames(l), baseAtom(b)) {
			t.Fatalf("%s was not graded: %v", baseAtom(b), atomNames(l))
		}
	}
}

// EVERY CASE BELOW ANSWERS A SURVIVING MUTANT the lane reported on 68432ba:
// build_record.go:143 (the reason's em-dash), :151 (the never-reached line) and
// :174 twice (shortSha's boundary). They are written as behaviour, but the
// reason each exists is that nothing distinguished the code from its mutant.

// A PHASE WITH NOTHING TO SAY RENDERS WITHOUT A DASH, and one with a reason
// renders with it. build:release seals empty on the common path, so a log that
// printed a bare dash for it would be in front of every reader of every build.
func TestTheRenderedLogOnlyDashesAPhaseThatGaveAReason(t *testing.T) {
	l := &buildLane{phases: phases{group: buildGroup}}
	l.seal("build:release", buildlane.Clean, "")
	l.seal("build:image", buildlane.Clean, "built ares")

	log := l.renderLog(nil)
	if !strings.Contains(log, "build:release: pass\n") || strings.Contains(log, "build:release: pass —") {
		t.Fatalf("a phase with no reason gets no dash:\n%s", log)
	}
	if !strings.Contains(log, "build:image: pass — built ares") {
		t.Fatalf("a phase with a reason keeps it:\n%s", log)
	}
}

// AND A RUN THAT REACHED EVERYTHING SAYS NOTHING ABOUT WHAT IT DID NOT. The
// trailing line is evidence when it is there, so it must be absent when there
// is nothing to report rather than present and empty.
func TestTheRenderedLogOmitsTheNeverReachedLineWhenNothingWasMissed(t *testing.T) {
	l := &buildLane{phases: phases{group: buildGroup}}
	l.seal("build:preflight", buildlane.Clean, "ready")

	if got := l.renderLog(nil); strings.Contains(got, "never reached") {
		t.Fatalf("nothing was missed, so nothing is named:\n%s", got)
	}
	if got := l.renderLog([]string{"build:sign"}); !strings.Contains(got, "never reached: build:sign") {
		t.Fatalf("what was missed is named:\n%s", got)
	}
}

// THE SHA IS SHORTENED AT TWELVE, and both sides of that are pinned: a sha
// already twelve long is handed back whole, and thirteen is the first that is
// cut. Every other coordinate in the fleet is written at twelve.
func TestTheShaIsShortenedAtTwelveExactly(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"shorter than twelve", "0123456789", "0123456789"},
		{"exactly twelve", "0123456789ab", "0123456789ab"},
		{"thirteen, the first that is cut", "0123456789abc", "0123456789ab"},
		{"a full sha", "0123456789abcdef0123456789abcdef01234567", "0123456789ab"},
		{"empty", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := shortSha(c.in); got != c.want {
				t.Fatalf("shortSha(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// A DETECT THAT COULD NOT READ THE HISTORY SAYS SO IN ITS OWN LOG, and seals
// could-not-run with the same reason.
func TestADetectThatCannotReadTheHistoryLogsWhy(t *testing.T) {
	m := buildOn(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	published(permittedSha)
	engine.fail("--is-ancestor", "the engine went away")
	l, code, _ := laneFor(t, m, false)
	if code != buildlane.CouldNotRun {
		t.Fatalf("got %d, want could-not-run", code)
	}
	detect := atomNamed(t, l, "build:detect")
	if !strings.HasPrefix(detect.Reason, "could not run: the history could not be read") {
		t.Errorf("detect sealed %q", detect.Reason)
	}
	if logs := detect.Logs; len(logs) == 0 || logs[len(logs)-1] != detect.Reason {
		t.Errorf("detect's log does not carry its reason: %q", logs)
	}
}
