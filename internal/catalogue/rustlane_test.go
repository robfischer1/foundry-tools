package catalogue

import (
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// The rows of the rust suite atoms, as the module as a whole sees them (the rows
// of the atoms that live in files included): clippy is the fleet's lint set and
// stands down on a fork; the suite does not, and neither is an atom whose subject
// is the project.
func TestTheRustSuiteAtomsKeepTheirRows(t *testing.T) {
	for _, tc := range []struct {
		id   string
		fork string
	}{
		{"rust:cargo-clippy", checks.ForkFleetStyle},
		{"rust:cargo-test", ""},
	} {
		a := checks.AtomByID(tc.id)
		if a.Stage != checks.StagePrecommit || a.Lane != checks.LaneRust || a.Image != checks.ImageRust || a.NotOnForks != tc.fork ||
			a.SubsumedBy != "" || a.NeedsDies || a.NeedsManifest {
			t.Errorf("%s: %+v", tc.id, a)
		}
	}
}
