package checks

import (
	"slices"
	"strconv"
	"testing"
)

func TestTheGateBoundsAreCargosOwnEnvironment(t *testing.T) {
	if RustGateJobs != 6 || RustGateJobs <= 0 || RustGateJobs > 8 {
		t.Errorf("RustGateJobs = %d: a gate atom must stay a fraction of dev01's 24 cores", RustGateJobs)
	}
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
