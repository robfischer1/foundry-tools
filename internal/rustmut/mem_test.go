package rustmut

import "testing"

func TestRustCoresHonoursMemory(t *testing.T) {
	t.Setenv("ATOM_CPUS", "8")
	t.Setenv("ATOM_MEM_BYTES", "")
	if got := rustCores(); got != 4 {
		t.Errorf("no memory word: %d, want 4", got)
	}
	t.Setenv("ATOM_MEM_BYTES", "8589934592") // 8 GiB: four rustc jobs, two mutants
	if got := rustCores(); got != 2 {
		t.Errorf("8 GiB: %d, want 2", got)
	}
	t.Setenv("ATOM_MEM_BYTES", "1")
	if got := rustCores(); got != 1 {
		t.Errorf("tiny memory: %d, want 1", got)
	}
}
