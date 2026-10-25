package rustmut

import (
	"os"
	"testing"
)

// TestMain clears the executor's shape words before any test runs. In a
// shaped CI pod ATOM_CPUS and ATOM_MEM_BYTES are set for the whole process,
// and a test that sets only the CPU word would otherwise size to the pod's
// memory (check@foundry-tools under B1, 2026-10-25). Tests set what they need.
func TestMain(m *testing.M) {
	for _, k := range []string{"ATOM_CPUS", "ATOM_MEM_BYTES", "ATOM_SHAPE"} {
		os.Unsetenv(k)
	}
	os.Exit(m.Run())
}
