package checks

import (
	"slices"
	"strconv"
	"testing"
)

func TestTheGateBoundsAreCargosOwnEnvironment(t *testing.T) {
	// The fallback only: a pod with a shape sizes by it (TestGateJobsFollowTheShape),
	// and the shape's ceiling (12 cores) is the bound there.
	if RustGateJobs != 6 {
		t.Errorf("RustGateJobs = %d: the no-shape fallback is the measured 6", RustGateJobs)
	}
	t.Setenv(CPUsEnv, "")
	t.Setenv(MemEnv, "")
	if want := "-C link-arg=-Wl,--thread-count=" + strconv.Itoa(RustGateLinkThreads); RustGateMoldThreadsFlag != want {
		t.Errorf("mold flag %q out of step with RustGateLinkThreads, want %q", RustGateMoldThreadsFlag, want)
	}
	for name, tc := range map[string]struct{ got, want []string }{
		"bounded":    {CargoBoundedEnv(), []string{"CARGO_BUILD_JOBS=6", "CARGO_INCREMENTAL=0"}},
		"lean":       {CargoLineTablesEnv(), []string{"CARGO_PROFILE_DEV_DEBUG=line-tables-only", "CARGO_PROFILE_TEST_DEBUG=line-tables-only"}},
		"mold links": {CargoMoldLinkEnv(), []string{"CARGO_BUILD_RUSTFLAGS=-C link-arg=-Wl,--thread-count=4"}},
	} {
		if !slices.Equal(tc.got, tc.want) {
			t.Errorf("%s: %v, want %v", name, tc.got, tc.want)
		}
	}
}

func TestMoldRunWrapsTheWholeProcessTree(t *testing.T) {
	if got := MoldRun([]string{"cargo", "test"}); !slices.Equal(got, []string{"mold", "-run", "cargo", "test"}) {
		t.Errorf("%v", got)
	}
}

func TestGateJobsFollowTheShape(t *testing.T) {
	const gib = 1 << 30
	for name, tc := range map[string]struct {
		cpus, mem string
		jobs      int
		mold      string
	}{
		"no shape, the fallback":    {"", "", 6, "4"},
		"no shape, memory binds":    {"", strconv.Itoa(4 * gib), 2, "4"},
		"C4 is twelve cores":        {"12", strconv.Itoa(10 * gib), 5, "4"},
		"B4 memory is plenty":       {"8", strconv.Itoa(20 * gib), 8, "4"},
		"the shape is wider than 6": {"12", strconv.Itoa(30 * gib), 12, "4"},
		"cores bind":                {"2", strconv.Itoa(30 * gib), 2, "2"},
		"cores, no memory word":     {"3", "", 3, "3"},
		"tiny memory is still one":  {"4", "1", 1, "1"},
		"junk cpus is no shape":     {"many", "", 6, "4"},
		"zero cpus is no shape":     {"0", "", 6, "4"},
	} {
		t.Setenv(CPUsEnv, tc.cpus)
		t.Setenv(MemEnv, tc.mem)
		if got, want := CargoBoundedEnv()[0], "CARGO_BUILD_JOBS="+strconv.Itoa(tc.jobs); got != want {
			t.Errorf("%s: %s, want %s", name, got, want)
		}
		if got, want := CargoMoldLinkEnv()[0], "CARGO_BUILD_RUSTFLAGS=-C link-arg=-Wl,--thread-count="+tc.mold; got != want {
			t.Errorf("%s: %s, want %s", name, got, want)
		}
	}
}
