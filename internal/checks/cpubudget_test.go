package checks

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseCPUMax(t *testing.T) {
	for name, tc := range map[string]struct {
		in   string
		want int
		ok   bool
	}{
		"four cores":      {"400000 100000\n", 4, true},
		"twelve cores":    {"1200000 100000", 12, true},
		"fraction rounds": {"250000 100000", 3, true},
		"half a core":     {"50000 100000", 1, true},
		"unlimited":       {"max 100000", 0, false},
		"empty":           {"", 0, false},
		"one field":       {"400000", 0, false},
		"zero period":     {"400000 0", 0, false},
		"not numbers":     {"a b", 0, false},
		"negative":        {"-1 100000", 0, false},
	} {
		got, ok := ParseCPUMax(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: ParseCPUMax(%q) = %d, %v; want %d, %v", name, tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestBudgetFrom(t *testing.T) {
	for name, tc := range map[string]struct {
		env, cpuMax string
		ncpu, want  int
	}{
		"the executor's word wins":      {"4", "1200000 100000", 24, 4},
		"the cgroup when no word":       {"", "400000 100000", 24, 4},
		"the node when nothing else":    {"", "max 100000", 24, 24},
		"the node when no cgroup file":  {"", "", 24, 24},
		"a bad word falls to cgroup":    {"many", "200000 100000", 24, 2},
		"a zero word falls to cgroup":   {"0", "200000 100000", 24, 2},
		"a negative word is no word":    {"-3", "", 8, 8},
		"never below one":               {"", "", 0, 1},
		"padded word":                   {" 6 ", "", 24, 6},
		"a word above the node is kept": {"32", "", 24, 32},
	} {
		if got := BudgetFrom(tc.env, tc.cpuMax, tc.ncpu); got != tc.want {
			t.Errorf("%s: BudgetFrom(%q, %q, %d) = %d, want %d", name, tc.env, tc.cpuMax, tc.ncpu, got, tc.want)
		}
	}
}

func TestCoresPerSlot(t *testing.T) {
	for name, tc := range map[string]struct{ budget, slots, ceiling, want int }{
		"the 12-core cap keeps the old four":  {12, 2, 4, 4},
		"a 24-core node keeps the old four":   {24, 2, 4, 4},
		"eight cores are four each":           {8, 2, 4, 4},
		"four cores are two each":             {4, 2, 4, 2},
		"three cores are one each":            {3, 2, 4, 1},
		"one core is one each":                {1, 2, 4, 1},
		"no slots is the ceiling's share":     {4, 0, 4, 4},
		"a ceiling below one is one":          {4, 2, 0, 1},
		"a four-worker tool on four cores":    {4, 4, 4, 1},
		"a four-worker tool on sixteen cores": {16, 4, 4, 4},
	} {
		if got := CoresPerSlot(tc.budget, tc.slots, tc.ceiling); got != tc.want {
			t.Errorf("%s: CoresPerSlot(%d, %d, %d) = %d, want %d", name, tc.budget, tc.slots, tc.ceiling, got, tc.want)
		}
	}
}

func TestCPUBudgetReadsTheEnvironmentFirst(t *testing.T) {
	t.Setenv(CPUsEnv, "3")
	if got := CPUBudget(); got != 3 {
		t.Errorf("CPUBudget with %s=3 = %d", CPUsEnv, got)
	}
}

func TestCPUBudgetFallsBackToWhatTheNodeHas(t *testing.T) {
	t.Setenv(CPUsEnv, "")
	old := cpuMaxFile
	t.Cleanup(func() { cpuMaxFile = old })
	cpuMaxFile = "/nonexistent/cpu.max"
	if got := CPUBudget(); got < 1 {
		t.Errorf("CPUBudget = %d, want at least one", got)
	}
}

func TestCPUBudgetReadsTheCgroupFile(t *testing.T) {
	t.Setenv(CPUsEnv, "")
	old := cpuMaxFile
	t.Cleanup(func() { cpuMaxFile = old })
	cpuMaxFile = func() string {
		p := filepath.Join(t.TempDir(), "cpu.max")
		if err := os.WriteFile(p, []byte("200000 100000\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}()
	if got := CPUBudget(); got != 2 {
		t.Errorf("CPUBudget over a 2-core cgroup = %d", got)
	}
}

func TestLLDDefaultTracksTheToolchain(t *testing.T) {
	for in, want := range map[string]bool{
		"1.97.0-x86_64-unknown-linux-gnu":  true,
		"1.90.0-x86_64-unknown-linux-gnu":  true,
		"1.90.0":                           true,
		"1.89.0-x86_64-unknown-linux-gnu":  false,
		"stable-x86_64-unknown-linux-gnu":  false,
		"nightly":                          false,
		"":                                 false,
		"2.0.0-x86_64-unknown-linux-gnu":   true,
		"1.100.0":                          true,
		"1.97.0-aarch64-unknown-linux-gnu": false,
		"1.97.0-x86_64-unknown-linux-musl": false,
	} {
		if got := LLDIsDefault(in); got != want {
			t.Errorf("LLDIsDefault(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestLLDThreadsFlag(t *testing.T) {
	if got, want := LLDThreadsFlag(2), "-C link-arg=-Wl,--threads=2"; got != want {
		t.Errorf("LLDThreadsFlag(2) = %q, want %q", got, want)
	}
	if got, want := LLDThreadsFlag(0), "-C link-arg=-Wl,--threads=1"; got != want {
		t.Errorf("LLDThreadsFlag(0) = %q, want %q", got, want)
	}
	if got, want := MoldThreadsFlag(3), "-C link-arg=-Wl,--thread-count=3"; got != want {
		t.Errorf("MoldThreadsFlag(3) = %q, want %q", got, want)
	}
}

func TestRustcToolchain(t *testing.T) {
	vv := "rustc 1.97.0 (abc 2026-09-01)\nbinary: rustc\nhost: x86_64-unknown-linux-gnu\nrelease: 1.97.0\nLLVM version: 21.1.0\n"
	for name, tc := range map[string]struct{ in, want string }{
		"both":         {vv, "1.97.0-x86_64-unknown-linux-gnu"},
		"no host":      {"release: 1.97.0\n", ""},
		"no release":   {"host: x86_64-unknown-linux-gnu\n", ""},
		"empty":        {"", ""},
		"padded lines": {"host: x86_64-unknown-linux-gnu \nrelease: 1.90.1\r\n", "1.90.1-x86_64-unknown-linux-gnu"},
	} {
		if got := RustcToolchain(tc.in); got != tc.want {
			t.Errorf("%s: RustcToolchain = %q, want %q", name, got, tc.want)
		}
	}
}
