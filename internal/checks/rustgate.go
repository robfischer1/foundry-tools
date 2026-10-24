package checks

import (
	"os"
	"strconv"
	"strings"
)

// THE GATE'S MEMORY AND CPU BOUND for the atoms that COMPILE (clippy, test, the
// wit guest), as the environment variables the atoms set on their cargo execs.
// The knobs are cargo's own; the atoms that use them share these, so the one
// number has one home. (The release build and the mutation lane keep their
// own: package main.)
const (
	// RustGateJobs is CARGO_BUILD_JOBS for the gate's compiling atoms. Why six:
	// unset, cargo takes nproc (24 on dev01) and a gate runs up to atomsInFlight
	// (4) atoms at once, so 96 rustc and linker processes could hold debug=2
	// codegen at the same time. That is the 14G anon spike and the 16:16Z dev01
	// OOM (edison47, 2026-10-15). Six is a quarter of the node per atom. Build
	// time at six is not yet measured.
	RustGateJobs = 6
	// RustGateJobs is the FALLBACK: it applies only where the executor named no
	// shape (no ATOM_CPUS). Under a shape the jobs are the shape's cores, see
	// RustGateJobsNow.
	// RustGateDebug is the debuginfo level of the dev and test profiles in the
	// clippy and test atoms: line-tables-only keeps file:line in a backtrace and
	// drops the type and variable info only a debugger reads. NOT applied to the
	// release build: that binary is the shipped artifact.
	RustGateDebug = "line-tables-only"
	// RustGateLinkThreads is mold's --thread-count for the test link, and
	// RustGateMoldThreadsFlag its literal (kept in step by a test). mold 1.10.1
	// has no environment knob, so the flag rides CARGO_BUILD_RUSTFLAGS, the
	// lowest of cargo's rustflags sources: a star's own RUSTFLAGS or target
	// rustflags still win, losing only the cap.
	RustGateLinkThreads     = 4
	RustGateMoldThreadsFlag = "-C link-arg=-Wl,--thread-count=4"
)

// CargoBoundedEnv bounds the jobs and turns the incremental cache off:
// CARGO_BUILD_JOBS (unset it is nproc) and CARGO_INCREMENTAL=0 (the dev
// profile's incremental cache is a second copy of every workspace crate's
// codegen, written and mapped per unit, and a gate's target volume is rebuilt
// from a stamped tree anyway).
func CargoBoundedEnv() []string {
	return []string{"CARGO_BUILD_JOBS=" + strconv.Itoa(RustGateJobsNow()), "CARGO_INCREMENTAL=0"}
}

// shapeCPUs is the executor's shape cores (ATOM_CPUS) when it names a positive
// number, else 0.
func shapeCPUs() int {
	n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(CPUsEnv)))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// RustGateJobsNow is CARGO_BUILD_JOBS for this run: under a shape (ATOM_CPUS
// set) min(ATOM_CPUS, floor(ATOM_MEM_BYTES / 1 GiB)), at least one; with no
// ATOM_CPUS the RustGateJobs fallback, held to the memory word when there is one.
func RustGateJobsNow() int {
	budget := RustGateJobs
	if n := shapeCPUs(); n > 0 {
		budget = n
	}
	return MemCap(budget, os.Getenv(MemEnv), RustWorkerMem)
}

// CargoLineTablesEnv is the debuginfo clippy and test need and no more.
func CargoLineTablesEnv() []string {
	return []string{"CARGO_PROFILE_DEV_DEBUG=" + RustGateDebug, "CARGO_PROFILE_TEST_DEBUG=" + RustGateDebug}
}

// CargoMoldLinkEnv caps the threads mold links with: RustGateLinkThreads, and
// under a shape no more than the jobs it leaves (a 2-core shape links on 2).
func CargoMoldLinkEnv() []string {
	if shapeCPUs() == 0 {
		return []string{"CARGO_BUILD_RUSTFLAGS=" + RustGateMoldThreadsFlag}
	}
	return []string{"CARGO_BUILD_RUSTFLAGS=" + MoldThreadsFlag(min(RustGateLinkThreads, RustGateJobsNow()))}
}

// MoldRun is argv run with mold as the linker for the whole process tree
// (LD_PRELOAD), so no RUSTFLAGS are overridden and a star's own
// .cargo/config.toml still applies. mold is in the lane image.
func MoldRun(argv []string) []string {
	return append([]string{"mold", "-run"}, argv...)
}
