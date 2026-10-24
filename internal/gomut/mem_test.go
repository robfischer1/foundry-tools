package gomut

import "testing"

func TestGoWorkersHonoursMemory(t *testing.T) {
	t.Setenv("ATOM_CPUS", "8")
	t.Setenv("ATOM_MEM_BYTES", "")
	if got := goWorkers(); got != goMutationWorkers {
		t.Errorf("no memory word: %d, want %d", got, goMutationWorkers)
	}
	t.Setenv("ATOM_MEM_BYTES", "6442450944") // 6 GiB at 2 GiB a worker
	if got := goWorkers(); got != 3 {
		t.Errorf("6 GiB: %d, want 3", got)
	}
	t.Setenv("ATOM_MEM_BYTES", "1")
	if got := goWorkers(); got != 1 {
		t.Errorf("tiny memory: %d, want 1", got)
	}
}
