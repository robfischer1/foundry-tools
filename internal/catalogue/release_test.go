package catalogue

import (
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// THE RELEASE ATOMS' ROWS, as the module as a whole sees them (the rows of the
// atoms that live in files included). WHAT A FORK ALWAYS KEEPS: the build, the
// test suites, the audits, the release builds, SAST and cargo fmt, so no release
// atom stands down on a fork. Each runs in its toolchain's lane, names its star
// by the record when the answers file does not, and only the python one is about
// the project's manifest.
func TestTheReleaseAtomsKeepTheirRows(t *testing.T) {
	for _, tc := range []struct {
		id       string
		lane     checks.Lane
		image    string
		manifest bool
	}{
		{"go:release", checks.LaneGo, checks.ImageGo, false},
		{"python:release", checks.LanePython, checks.ImagePython, true},
		{"rust:release", checks.LaneRust, checks.ImageRust, false},
		{"ts:release", checks.LaneTS, checks.ImageTS, false},
	} {
		a := checks.AtomByID(tc.id)
		if a.Stage != checks.StagePrepush || a.Lane != tc.lane || a.Image != tc.image || a.NotOnForks != "" ||
			a.SubsumedBy != "" || !a.NeedsDies || a.NeedsManifest != tc.manifest {
			t.Errorf("%s: %+v", tc.id, a)
		}
		if got := checks.GroupOf(tc.id); got != checks.GroupLanguage {
			t.Errorf("%s: group %s", tc.id, got)
		}
	}
}
