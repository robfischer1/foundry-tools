package checks

import "testing"

func TestUT_492_AbsentReasonFor(t *testing.T) {
	want := "rust:cargo-fmt: ABSENT — no Cargo.toml at the repository root, so this repo does not build the rust lane. Nothing was checked and nothing needed to be."
	if got := AbsentReasonFor("rust:cargo-fmt", LaneRust, false); got != want {
		t.Fatalf("got %q", got)
	}
}

func TestUT_493_ForkReasonFor(t *testing.T) {
	want := "go:vet: ABSENT — fleet/repos/gravity.json is kind vendored, a fork of an upstream, and this atom does not grade a fork: " + ForkFleetStyle +
		". The fork's build, tests and audits still run. Nothing was checked here and nothing needed to be."
	if got := ForkReasonFor("go:vet", "gravity", ForkFleetStyle); got != want {
		t.Fatalf("got %q", got)
	}
}

// TestUT_494_TheWrappersCallTheFunctions: for every row, the sentence the row's
// verdict carries is the sentence the function states.
func TestUT_494_TheWrappersCallTheFunctions(t *testing.T) {
	for _, a := range Atoms() {
		if got, want := absentReason(a), AbsentReasonFor(a.ID, a.Lane, a.NeedsManifest); got != want {
			t.Errorf("%s: absentReason %q, AbsentReasonFor %q", a.ID, got, want)
		}
		if got, want := AbsentVerdict(a).Reason, AbsentReasonFor(a.ID, a.Lane, a.NeedsManifest); got != want {
			t.Errorf("%s: AbsentVerdict %q", a.ID, got)
		}
		if got, want := ForkVerdict(a, "gravity").Reason, ForkReasonFor(a.ID, "gravity", a.NotOnForks); got != want {
			t.Errorf("%s: ForkVerdict %q", a.ID, got)
		}
	}
}

// The three sentences, word for word, as they stood before the functions were
// split out (R20: the wording does not change).
func TestAbsentReasonWordingIsUnchanged(t *testing.T) {
	for _, c := range []struct {
		id       string
		lane     Lane
		manifest bool
		want     string
	}{
		{"ts:visual", LaneTS, true, "ts:visual: ABSENT — no package.json at the repository root, so this repo declares no ts project for it to read. The lane's lint and tests still run. Nothing was checked here and nothing needed to be."},
		{"go:vet", LaneGo, false, "go:vet: ABSENT — no go.mod anywhere in the tree (vendor/, testdata/ and _ or . directories do not count), so this repo carries no go to check. Nothing was checked and nothing needed to be."},
		{"python:ruff-check", LanePython, false, "python:ruff-check: ABSENT — no .py file anywhere in the tree (vendor/, testdata/ and _ or . directories do not count), so this repo carries no python to check. Nothing was checked and nothing needed to be."},
	} {
		if got := AbsentReasonFor(c.id, c.lane, c.manifest); got != c.want {
			t.Errorf("%s: got %q", c.id, got)
		}
	}
}
