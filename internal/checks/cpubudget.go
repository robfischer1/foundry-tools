package checks

import (
	"os"
	"runtime"
	"strconv"
	"strings"
)

// THE CPU AN ATOM MAY USE, as one number the mutation tools size themselves by.
// Mutation tools read the node's core count (nproc: 24 on dev01), not the
// container's CPU quota: on 2026-10-19 a mutation-bg pod limited to 12 cores
// burned 11.9 of them, each of four rust-lld links fanning to 17 threads, and
// the node's CPU pressure went back to ~46% (it was ~3% with the lane off the
// node). The quota is a ceiling the kernel enforces by throttling; a tool that
// sizes to the node pays for the throttling in wall clock and in the siblings'
// latency. The budget is what the tool should size to instead.

const (
	// CPUsEnv is the executor's word for the budget: the container's CPU limit,
	// in whole cores. It wins over the cgroup, which an atom run in a child
	// cgroup (cpu.max "max") cannot see.
	CPUsEnv = "ATOM_CPUS"
	// MemEnv is the executor's word for the pod's memory budget, in bytes. Absent
	// or not a positive number, memory caps nothing.
	MemEnv = "ATOM_MEM_BYTES"
	// RustWorkerMem is what one rustc or cargo job is sized to hold: 1 GiB (measured: rust lanes peak at 0.5-1.4 GiB a job).
	RustWorkerMem int64 = 1 << 30
	// lldDefaultMinor is the first rust release whose x86_64-unknown-linux-gnu
	// target links with rust-lld by default (1.90, 2025-09).
	lldDefaultMinor = 90
)

// cpuMaxFile is the container's own cgroup v2 quota, a variable so a test can
// point it elsewhere.
var cpuMaxFile = "/sys/fs/cgroup/cpu.max"

// ParseCPUMax reads a cgroup v2 cpu.max ("<quota> <period>", or "max <period>")
// as whole cores, rounded up. ok is false for no quota or an unreadable file.
func ParseCPUMax(s string) (cores int, ok bool) {
	f := strings.Fields(s)
	if len(f) != 2 {
		return 0, false
	}
	quota, err1 := strconv.Atoi(f[0])
	period, err2 := strconv.Atoi(f[1])
	if err1 != nil || err2 != nil || quota <= 0 || period <= 0 {
		return 0, false
	}
	return (quota + period - 1) / period, true
}

// BudgetFrom is the budget given the environment word, the cgroup file's text
// and the node's core count: the word if it is a positive number, else the
// cgroup's quota, else the node. Never below one.
func BudgetFrom(atomCPUs, cpuMax string, ncpu int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(atomCPUs)); err == nil && n > 0 {
		return n
	}
	if n, ok := ParseCPUMax(cpuMax); ok {
		return n
	}
	return max(1, ncpu)
}

// CPUBudget is the number of cores this atom may use, read from the process.
func CPUBudget() int {
	raw, _ := os.ReadFile(cpuMaxFile)
	return BudgetFrom(os.Getenv(CPUsEnv), string(raw), runtime.NumCPU())
}

// MemCap is budget held to what the memory budget (the MemEnv text) holds at
// perWorker bytes each: min(budget, floor(mem / perWorker)), never below one.
// With no usable memory word, or no usable perWorker, it is budget unchanged.
func MemCap(budget int, memBytes string, perWorker int64) int {
	mem, err := strconv.ParseInt(strings.TrimSpace(memBytes), 10, 64)
	if err != nil || mem <= 0 || perWorker <= 0 {
		return budget
	}
	return max(1, min(budget, int(min(mem/perWorker, int64(budget)))))
}

// MemBudget is CPUBudget held to the pod's memory at perWorker bytes a worker.
func MemBudget(perWorker int64) int {
	return MemCap(CPUBudget(), os.Getenv(MemEnv), perWorker)
}

// CoresPerSlot is what each of slots concurrent units of work gets from the
// budget, at most the ceiling the unit was measured to use and at least one. A
// budget that covers every slot's ceiling leaves the unit at its ceiling, so a
// node or a quota with room changes nothing.
func CoresPerSlot(budget, slots, ceiling int) int {
	share := budget
	if slots > 1 {
		share = budget / slots
	}
	return max(1, min(ceiling, share))
}

// RustcToolchain is "<release>-<host>" off what `rustc -vV` printed, "" when it
// said neither. The atom's own environment does not name the toolchain (rustup's
// proxy sets RUSTUP_TOOLCHAIN for the children it starts, not for us), so the
// compiler is asked.
func RustcToolchain(vv string) string {
	var release, host string
	for _, ln := range strings.Split(vv, "\n") {
		if v, ok := strings.CutPrefix(ln, "release: "); ok {
			release = strings.TrimSpace(v)
		}
		if v, ok := strings.CutPrefix(ln, "host: "); ok {
			host = strings.TrimSpace(v)
		}
	}
	if release == "" || host == "" {
		return ""
	}
	return release + "-" + host
}

// LLDIsDefault says the rust toolchain named (RustcToolchain: a release, a
// dash and a host triple) links x86_64 linux-gnu with rust-lld by default. A
// channel name, an older release or another host answers false: the flag below
// is only for a linker that knows it.
func LLDIsDefault(toolchain string) bool {
	release, host, _ := strings.Cut(toolchain, "-")
	if host != "" && host != "x86_64-unknown-linux-gnu" {
		return false
	}
	parts := strings.Split(release, ".")
	if len(parts) < 2 {
		return false
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return false
	}
	return major > 1 || (major == 1 && minor >= lldDefaultMinor)
}

// LLDThreadsFlag is the rustc link arg that caps lld's threads. lld sizes its
// pool from the node's hardware threads and has no environment knob.
func LLDThreadsFlag(n int) string {
	return "-C link-arg=-Wl,--threads=" + strconv.Itoa(max(1, n))
}

// MoldThreadsFlag is the same cap for mold, which has no environment knob
// either.
func MoldThreadsFlag(n int) string {
	return "-C link-arg=-Wl,--thread-count=" + strconv.Itoa(max(1, n))
}
