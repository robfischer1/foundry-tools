package pymut

import "testing"

func TestXdistShareHonoursMemory(t *testing.T) {
	t.Setenv("ATOM_CPUS", "8")
	t.Setenv("ATOM_MEM_BYTES", "")
	if got := xdistShare(2); got != "PYTEST_XDIST_AUTO_NUM_WORKERS=4" {
		t.Errorf("no memory word: %s", got)
	}
	t.Setenv("ATOM_MEM_BYTES", "17179869184") // 16 GiB at 4 GiB a process: 4, two workers
	if got := xdistShare(2); got != "PYTEST_XDIST_AUTO_NUM_WORKERS=2" {
		t.Errorf("16 GiB: %s", got)
	}
	t.Setenv("ATOM_MEM_BYTES", "1")
	if got := xdistShare(2); got != "PYTEST_XDIST_AUTO_NUM_WORKERS=1" {
		t.Errorf("tiny memory: %s", got)
	}
}
