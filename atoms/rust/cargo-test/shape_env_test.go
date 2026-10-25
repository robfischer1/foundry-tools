package rustcargotest

import (
	"os"
	"slices"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// TestMain clears the executor's shape words and rebuilds env without them. In
// a shaped CI pod ATOM_CPUS and ATOM_MEM_BYTES are set for the whole process,
// and env is computed at package init, before any test can unset them, so the
// cargo bound sized to the pod (check@foundry-tools, 2026-10-25: jobs 2, want
// 6). Tests set what they need.
func TestMain(m *testing.M) {
	for _, k := range []string{"ATOM_CPUS", "ATOM_MEM_BYTES", "ATOM_SHAPE"} {
		os.Unsetenv(k)
	}
	env = slices.Concat(checks.CargoBoundedEnv(), checks.CargoLineTablesEnv(), checks.CargoMoldLinkEnv())
	os.Exit(m.Run())
}
