package checks

import (
	"sort"
	"strings"
	"testing"
)

// wantSweep is the sweep's roster, written out rather than derived, so that
// adding or losing an atom is a deliberate edit to this list and not a silent
// change in a count. CA F9 named these five.
var wantSweep = []string{
	"sweep:digest-pins",
	"sweep:kube-linter",
	"sweep:kubeconform",
	"sweep:portfolio-sbom",
	"sweep:template-render-matrix",
}

// THE ACCEPTANCE, ASSERTED. CA F9's success criterion is an absence — "no
// `stage: sweep` atom appears in any pull's path" — and an absence nobody
// tests is an absence that comes back. The gate reads the pull-path set; this
// says that set can never contain one.
func TestNoSweepAtomOnThePullPath(t *testing.T) {
	for _, a := range PullPathAtoms() {
		if a.Stage == StageSweep {
			t.Errorf("sweep atom %q reached the pull-path set", a.ID)
		}
		if strings.HasPrefix(a.ID, "sweep:") {
			t.Errorf("atom %q is in the sweep namespace but reached the pull-path set", a.ID)
		}
	}
}

// The empty stage is the one a door passes when it has nothing particular to
// say, so it is the one that has to be right: it means every PULL stage, not
// every stage in the table.
func TestTheEmptyStageIsThePullPathNotEverything(t *testing.T) {
	all := AtomsForStage("")
	if len(all) == len(Atoms) {
		t.Fatal("the empty stage selected the whole table — a door asking for the vector would be handed the sweep")
	}
	if len(all)+len(SweepAtoms()) != len(Atoms) {
		t.Fatalf("pull-path %d + sweep %d != %d atoms — some atom belongs to neither",
			len(all), len(SweepAtoms()), len(Atoms))
	}
	for _, a := range all {
		if !IsPullPath(a.Stage) {
			t.Errorf("atom %q with stage %q is not a pull stage", a.ID, a.Stage)
		}
	}
}

func TestSweepCarriesExactlyItsRoster(t *testing.T) {
	got := make([]string, 0, len(SweepAtoms()))
	for _, a := range SweepAtoms() {
		got = append(got, a.ID)
	}
	sort.Strings(got)
	if len(got) != len(wantSweep) {
		t.Fatalf("sweep carries %v, want %v", got, wantSweep)
	}
	for i := range got {
		if got[i] != wantSweep[i] {
			t.Fatalf("sweep carries %v, want %v", got, wantSweep)
		}
	}
}

func TestIsPullPathRefusesTheSweep(t *testing.T) {
	for _, tc := range []struct {
		stage string
		want  bool
	}{
		{StagePrecommit, true},
		{StagePrepush, true},
		{StageSweep, false},
		{"", false},
		{"nightly", false},
	} {
		if got := IsPullPath(tc.stage); got != tc.want {
			t.Errorf("IsPullPath(%q) = %v, want %v", tc.stage, got, tc.want)
		}
	}
}

// A sweep atom runs unattended on a clock, which is precisely where a silent
// pass does the most damage: nobody is watching the run, so a body that exits 0
// because its tool never arrived reads as a clean fleet for as long as it takes
// somebody to look. Every one of them must carry an explicit CANNOT RUN.
func TestEverySweepAtomCanRefuse(t *testing.T) {
	for _, a := range SweepAtoms() {
		if !strings.Contains(a.Script, "CANNOT RUN") {
			t.Errorf("sweep atom %q has no CANNOT RUN branch — an unattended check that cannot refuse will report a clean fleet it never examined", a.ID)
		}
		if !strings.Contains(a.Script, "exit 2") {
			t.Errorf("sweep atom %q never exits 2, so it has only two states", a.ID)
		}
	}
}

// A sweep atom that finds no surface must SAY SO rather than pass in silence —
// the same rule the lane atoms carry, restated here because a sweep runs
// against every repo in custody and most repos have no surface for most of
// these.
func TestEverySweepAtomAnnouncesAbsence(t *testing.T) {
	for _, a := range SweepAtoms() {
		if !strings.Contains(a.Script, "ABSENT") {
			t.Errorf("sweep atom %q never reports ABSENT; on a repo with no surface it would exit 0 in silence, which is indistinguishable from a scan that found nothing", a.ID)
		}
	}
}

// The two atoms that judge pinning are themselves pinned by digest. Anything
// else is the joke telling itself.
func TestThePinCheckersAreThemselvesPinned(t *testing.T) {
	for _, a := range SweepAtoms() {
		switch a.ID {
		case "sweep:kubeconform", "sweep:kube-linter":
			if !strings.Contains(a.Image, "@sha256:") {
				t.Errorf("sweep atom %q runs on a floating image %q", a.ID, a.Image)
			}
		}
	}
}
