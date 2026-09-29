// The cast lane's record, driven through the real phase sequence.
//
// THESE RUN THE LANE, not the recorder — the same reason build_record_test.go
// gives: seal/unreached could be tested on a hand-built castLane in a tenth of
// the lines, and that test would pass forever while run() quietly stopped
// calling them. Each case scripts the engine into a real outcome and reads the
// record the lane would have posted.
package main

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/buildlane"
)

// castLaneFor runs the lane exactly as Cast does and hands back the lane so a
// test can read the record off it.
func castLaneFor(t *testing.T, m *FoundryTools, dryRun bool) (*castLane, int, string) {
	t.Helper()
	l := &castLane{
		m: m, doorbellURL: castDoorbell,
		hades: "https://hades:8102", hadesID: "spiffe://notusmi.com/star/hades",
		dryRun: dryRun,
		stamp:  "1",
		phases: phases{group: castGroup, order: castPhases},
	}
	if !dryRun {
		l.spire = dag.LoadSocketFromID("spire-agent-socket")
		l.registryToken = dag.SetSecret("registry-token", "tok")
	}
	code, reason := l.run(context.Background())
	return l, code, reason
}

func castAtoms(l *castLane) []string {
	out := make([]string, 0, len(l.atoms))
	for _, a := range l.atoms {
		out = append(out, a.Atom)
	}
	return out
}

func castAtomNamed(t *testing.T, l *castLane, name string) AtomResult {
	t.Helper()
	for _, a := range l.atoms {
		if a.Atom == name {
			return a
		}
	}
	t.Fatalf("no atom %q in %v", name, castAtoms(l))
	return AtomResult{}
}

// A WHOLE CAST SEALS EVERY PHASE, in order, and reaches the end.
func TestACastSealsEveryPhaseItRan(t *testing.T) {
	m := castOn(t, nil)
	scriptACast(castPin + "\ntongs\n")
	l, code, _ := castLaneFor(t, m, false)

	if code != buildlane.Clean {
		t.Fatalf("a scripted cast settles clean, got %d", code)
	}
	want := []string{"cast:preflight", "cast:record", "cast:cosign", "cast:payload",
		"cast:pin", "cast:stage", "cast:mint", "cast:verify", "cast:ring"}
	if got := castAtoms(l); !equalStrings(got, want) {
		t.Fatalf("phases sealed\n want %v\n  got %v", want, got)
	}
	rec := l.record("cast", code)
	if len(rec.Unreached) != 0 {
		t.Fatalf("a whole cast reaches everything, got unreached %v", rec.Unreached)
	}
	if mint := castAtomNamed(t, l, "cast:mint"); !strings.Contains(mint.Reason, "mold minted") {
		t.Fatalf("the mint carries what mold answered, got %q", mint.Reason)
	}
}

// A DRY RUN STOPS AT THE PIN, and the stage, the mint and the signature read as
// UNREACHED rather than passed. That is the distinction this lane most needs a
// record for: reporting a signature as held would claim the one thing a dry run
// deliberately does not do.
func TestADryRunReachesThePinAndClaimsNoSignature(t *testing.T) {
	m := castOn(t, nil)
	scriptACast(castPin + "\ntongs\n")
	l, code, _ := castLaneFor(t, m, true)

	if code != buildlane.Clean {
		t.Fatalf("a dry run settles clean, got %d", code)
	}
	if !contains(castAtoms(l), "cast:pin") {
		t.Fatalf("a dry run pins for real: %v", castAtoms(l))
	}
	rec := l.record("cast", code)
	for _, after := range []string{"cast:stage", "cast:mint", "cast:verify"} {
		if contains(castAtoms(l), after) {
			t.Fatalf("a dry run must not seal %s", after)
		}
		if !contains(rec.Unreached, after) {
			t.Fatalf("%s must read as UNREACHED, got %v", after, rec.Unreached)
		}
	}
}

// A LANE WITHOUT ITS CREDENTIALS SEALS ONE PHASE AND CLAIMS NOTHING ELSE.
func TestACastWithoutCredentialsSealsOnlyPreflight(t *testing.T) {
	m := castOn(t, nil)
	l := &castLane{m: m, hades: "https://hades:8102", stamp: "1",
		phases: phases{group: castGroup, order: castPhases}}
	code, _ := l.run(context.Background())

	if code != buildlane.CouldNotRun {
		t.Fatalf("no credentials is could-not-run, got %d", code)
	}
	if got := castAtoms(l); !equalStrings(got, []string{"cast:preflight"}) {
		t.Fatalf("only preflight seals, got %v", got)
	}
	if n := len(l.record("cast", code).Unreached); n != len(castPhases)-1 {
		t.Fatalf("every other phase is unreached, got %d", n)
	}
}

// A REPO WITH NO cosign.pub STOPS AT THE KEY, having read its record first —
// which is the phase-level fact the exit code cannot carry.
func TestACastWithNoKeyStopsAtCosignHavingReadItsRecord(t *testing.T) {
	m := castOn(t, map[string]string{"cosign.pub": ""})
	scriptACast(castPin + "\ntongs\n")
	l, code, _ := castLaneFor(t, m, false)

	if code != buildlane.Findings {
		t.Fatalf("a missing key is findings, got %d", code)
	}
	if !contains(castAtoms(l), "cast:record") {
		t.Fatalf("the dies record is read before the key: %v", castAtoms(l))
	}
	key := castAtomNamed(t, l, "cast:cosign")
	if key.State != buildlane.Findings || !strings.Contains(key.Reason, "no cosign.pub") {
		t.Fatalf("cast:cosign carries the finding, got state=%d reason=%q", key.State, key.Reason)
	}
	for _, after := range []string{"cast:payload", "cast:pin", "cast:stage", "cast:mint", "cast:verify"} {
		if contains(castAtoms(l), after) {
			t.Fatalf("%s sealed although the key stopped the lane", after)
		}
	}
}

// THE BELL NEVER VOTES. ring answers a string and never a code, because the
// hosts' delivery timer delivers whether or not the doorbell answered — so a
// doorbell that did not ring is recorded, clean, and the cast still holds.
func TestADoorbellThatDidNotRingIsRecordedAndStillClean(t *testing.T) {
	m := castOn(t, nil)
	scriptACast(castPin + "\ntongs\n")
	engine.exitCode(castDoorbell, 7)
	l, code, _ := castLaneFor(t, m, false)

	if code != buildlane.Clean {
		t.Fatalf("a silent doorbell must not cost the cast, got %d", code)
	}
	bell := castAtomNamed(t, l, "cast:ring")
	if bell.State != buildlane.Clean {
		t.Fatalf("the bell never votes, got state %d", bell.State)
	}
	if !strings.Contains(bell.Reason, "did not ring") {
		t.Fatalf("the bell records what happened, got %q", bell.Reason)
	}
}
