package atoms

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

// THE RACE RUN, SPLIT. A tree whose tests sit behind a fleet tag binds test
// servers, and `-p 1` keeps packages from sharing them: the suites reset one
// database's schema per test, and one broker serves every package. But `-p 1`
// on the whole module also ran every package that reaches no server one test
// binary at a time. MEASURED on stellar-core (gate run 1454, ae7f8a28): the race
// run spent 789 CPU-seconds over 987 s of wall on a 6-CPU container, 0.8 cores,
// and 6 of its 111 packages reach a server.
//
// So the run is two `go test` invocations over the module, one after the other:
//
//   - the SERIAL set at `-p 1`: every package that reaches a bound server (a .go
//     file of its directory names a compiled fleet tag or a server's variable),
//     and every package known to be heavy (goRaceHeavy);
//   - the rest at `-p GoRaceParallel`.
//
// Each writes its own profile; the two are merged into the one the run owes
// (GoCoverProfile). Sequential, not concurrent, so the run's memory peak stays
// the larger of the two sets', not their sum: until go:diff-coverage reads
// go:test-race's profile (after_any), two race runs share the go container.
//
// EVERY DOUBT IS THE OLD RUN: a module that will not list, a directory that
// will not read, or no package left to run in parallel answers ok false, and the
// suite runs once at `-p 1` as before.

// GoRaceParallel is how many packages the parallel set tests at once. FOUR, NOT
// THE CONTAINER'S WIDTH (Hopper64, 2026-10-22): the go container's limit is
// 20Gi, raised after signer.test alone OOM-killed a gate at 12Gi, and the
// width is raised only once the measured peak allows it.
const GoRaceParallel = 4

// goRaceHeavy are the packages whose race test binary alone peaks above about
// 2 GB, by import path, with the peak in MiB. They run in the serial set, so
// four of them never run at once. MEASURED, not guessed: every package of
// stellar-core 00f48fe6, `go test -race -p 1 -cover` with each binary run under
// `/usr/bin/time -f %M` (a pod on llm01, 2026-10-22; 111 packages): signer
// 4104 MiB, and the next heaviest assembly 1314, mcpserve/rest 958,
// predicatematch 687. The gate has seen signer.test at about 6.1 GB (gate
// 1397's OOM at 12Gi).
var goRaceHeavy = map[string]int{
	"git.notusmi.com/rob/stellar-core-go/signer": 4104,
}

// raceSplit is the race run's two package sets, as import paths.
type raceSplit struct{ serial, parallel []string }

// serverEnvs are the variables the bound servers are named by: a package whose
// code reads one reaches the server whatever its build tags.
func serverEnvs(dbs []checks.TestDB, brokers []checks.TestBroker, caches []checks.TestCache, kubes []checks.TestKube) []string {
	var out []string
	for _, d := range dbs {
		out = append(out, d.Env)
	}
	for _, b := range brokers {
		out = append(append(out, b.Env), b.Also...)
	}
	for _, c := range caches {
		out = append(out, c.Env)
	}
	for _, k := range kubes {
		out = append(out, k.URLEnv, k.TokenEnv, k.CAEnv)
	}
	return slices.DeleteFunc(out, func(s string) bool { return s == "" })
}

// splitRace lists the module's packages under the run's own tags (args are the
// single run's) and sorts them into the two sets. A package reaches the servers
// when a .go file of its directory contains any of live: the compiled tags,
// whose `//go:build` line is how a test asks for a server, and the servers'
// variables. A substring is the safe reading: it can only put more in the serial
// set.
func splitRace(ctx context.Context, in Input, cwd string, args, live []string) (raceSplit, bool) {
	list := []string{"list"}
	if i := slices.Index(args, "-tags"); i >= 0 && i+1 < len(args) {
		list = append(list, "-tags", args[i+1])
	}
	list = append(list, "-f", "{{.ImportPath}}\t{{.Dir}}", "./...")
	out, code := in.run(ctx, Cmd{Dir: cwd, Name: "go", Args: list})
	if code != 0 {
		return raceSplit{}, false
	}
	var s raceSplit
	for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
		pkg, dir, found := strings.Cut(ln, "\t")
		if !found || pkg == "" || dir == "" {
			return raceSplit{}, false
		}
		reaches, err := reachesServers(dir, live)
		if err != nil {
			return raceSplit{}, false
		}
		if reaches || goRaceHeavy[pkg] > 0 {
			s.serial = append(s.serial, pkg)
		} else {
			s.parallel = append(s.parallel, pkg)
		}
	}
	if len(s.parallel) == 0 {
		return raceSplit{}, false
	}
	return s, true
}

// reachesServers is whether a .go file directly in dir contains any marker.
func reachesServers(dir string, markers []string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return false, err
		}
		for _, m := range markers {
			if bytes.Contains(b, []byte(m)) {
				return true, nil
			}
		}
	}
	return false, nil
}

// scope is the split as the atom's reason says it.
func (s raceSplit) scope() string {
	return fmt.Sprintf("race split: %d package(s) at -p 1 (they reach a bound server, or are heavy), %d at -p %d",
		len(s.serial), len(s.parallel), GoRaceParallel)
}

// run is the two invocations, serial set first, each the single run's args with
// its own -p, its own profile and its packages for ./.... Their output is joined
// and their exit is the worse: a program that would not start (-1) wins, then
// the larger code. The profiles are merged into profile only when both runs
// left one; the caller removes it for a run that did not pass.
func (s raceSplit) run(ctx context.Context, in Input, cwd string, args, env []string, profile string) (string, int) {
	var outs, parts []string
	code := 0
	for _, set := range []struct {
		pkgs []string
		p    int
		name string
	}{{s.serial, 1, "serial"}, {s.parallel, GoRaceParallel, "parallel"}} {
		if len(set.pkgs) == 0 {
			continue
		}
		part := profile + "." + set.name
		_ = os.Remove(part)
		out, c := in.run(ctx, Cmd{Dir: cwd, Name: "go", Args: splitArgs(args, set.p, part, set.pkgs), Env: env, Both: true})
		outs = append(outs, out)
		parts = append(parts, part)
		switch {
		case c < 0 || code < 0:
			code = -1
		default:
			code = max(code, c)
		}
	}
	_ = os.Remove(profile)
	if merged, err := mergeProfiles(parts); err == nil {
		_ = os.WriteFile(profile, merged, 0o644)
	}
	for _, part := range parts {
		_ = os.Remove(part)
	}
	return strings.Join(outs, "\n"), code
}

// splitArgs is the single run's args with -p set to p, -coverprofile to
// profile, and ./... replaced by pkgs.
func splitArgs(args []string, p int, profile string, pkgs []string) []string {
	out := slices.Clone(args)
	for i := 0; i+1 < len(out); i++ {
		switch out[i] {
		case "-p":
			out[i+1] = strconv.Itoa(p)
		case "-coverprofile":
			out[i+1] = profile
		}
	}
	out = slices.DeleteFunc(out, func(a string) bool { return a == "./..." })
	return append(out, pkgs...)
}

// mergeProfiles is the profiles as one: the first whole, each later one less its
// `mode:` line. Any profile that will not read is no merged profile.
func mergeProfiles(paths []string) ([]byte, error) {
	var merged []byte
	for i, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		if i > 0 {
			if first, rest, ok := bytes.Cut(b, []byte("\n")); ok && bytes.HasPrefix(first, []byte("mode:")) {
				b = rest
			}
		}
		merged = append(merged, b...)
	}
	return merged, nil
}
