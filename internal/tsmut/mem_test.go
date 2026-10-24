package tsmut

import "testing"

func TestStrykerConcurrencyHonoursMemory(t *testing.T) {
	t.Setenv("ATOM_CPUS", "8")
	t.Setenv("ATOM_MEM_BYTES", "")
	if got := strykerConcurrency(); got != strykerWorkers {
		t.Errorf("no memory word: %d, want %d", got, strykerWorkers)
	}
	t.Setenv("ATOM_MEM_BYTES", "4294967296") // 4 GiB: two runners
	if got := strykerConcurrency(); got != 2 {
		t.Errorf("4 GiB: %d, want 2", got)
	}
	t.Setenv("ATOM_MEM_BYTES", "1")
	if got := strykerConcurrency(); got != 1 {
		t.Errorf("tiny memory: %d, want 1", got)
	}
}
