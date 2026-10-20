// Package pinsync moves everything that has to follow a pinned version when
// Renovate bumps one, and that Renovate's regex manager cannot reach.
//
// renovate-config's foundry-tools manager reads each `// renovate:` marker in
// internal/checks and rewrites the `<Name>Version = "…"` on the next line. Four
// things depend on that version and are not that line:
//
//  1. URL and tarball-member LITERALS that carry the version. They are
//     literals, not joins, on purpose (witlane.go: a `+` in a const block is a
//     declaration no coverage profile marks), so nothing moves them.
//  2. checks.ToolSHA256, the sha256 of every pinned asset.
//  3. tools.toml, held equal to the constants by TestToolsTomlMatchesGo.
//  4. pytools/requirements.in and its lock, where copier is held to
//     checks.CopierVersion.
//
// Run is idempotent: on a tree that is already in step it reads, fetches
// nothing and writes nothing. Renovate runs it as a postUpgradeTask.
package pinsync

import (
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
)

// URLPin is a tool fetched from a release URL: its tools.toml table, the
// constant naming its URL and the one naming its version.
type URLPin struct{ Tool, URL, Version string }

// URLPins is every `kind = "url"` tool in tools.toml. TestTheTablesCoverTheTree
// holds it equal to tools.toml's url tools.
var URLPins = []URLPin{
	{"opa", "OpaURL", "OpaVersion"},
	{"opengrep", "OpengrepURL", "OpengrepVersion"},
	{"hadolint", "HadolintURL", "HadolintVersion"},
	{"chezmoi", "ChezmoiURL", "ChezmoiVersion"},
	{"docker-compose", "ComposeURL", "ComposeVersion"},
	{"wasm-tools", "WasmToolsURL", "WasmToolsVersion"},
	{"wac", "WacURL", "WacVersion"},
	{"just", "JustURL", "JustVersion"},
	{"shellcheck", "ShellcheckURL", "ShellcheckVersion"},
	{"oras", "OrasURL", "OrasVersion"},
	{"cargo-audit", "CargoAuditURL", "CargoAuditVersion"},
	{"cargo-nextest", "CargoNextestURL", "CargoNextestVersion"},
	{"cargo-mutants", "CargoMutantsURL", "CargoMutantsVersion"},
	{"sccache", "SccacheURL", "SccacheVersion"},
	{"mold", "MoldURL", "MoldRelease"},
	{"jq", "JqURL", "JqRelease"},
	{"curl", "CurlURL", "CurlVersion"},
	{"rust-clippy", "RustClippyURL", "RustComponentsTag"},
	{"rust-rustfmt", "RustRustfmtURL", "RustComponentsTag"},
	{"rust-std", "RustStdURL", "RustComponentsTag"},
	{"rust-wasm-std", "RustStdWasmURL", "RustWasmComponentsTag"},
	{"rust-wasm-clippy", "RustWasmClippyURL", "RustWasmComponentsTag"},
	{"rust-wasm-rustfmt", "RustWasmRustfmtURL", "RustWasmComponentsTag"},
	{"python-standalone", "PythonStandaloneURL", "FleetPythonVersion"},
	{"python-3-15", "PythonStandalone315URL", "PythonStandalone315Release"},
	{"gofmt", "GofmtToolchainURL", "GofmtGoRelease"},
	{"rustc-1-97-1", "RustDist1971RustcURL", "RustDist1971Tag"},
	{"cargo-1-97-1", "RustDist1971CargoURL", "RustDist1971Tag"},
	{"rust-std-1-97-1", "RustDist1971StdURL", "RustDist1971Tag"},
	{"rust-std-wasm-1-97-1", "RustDist1971StdWasmURL", "RustDist1971Tag"},
	{"rustc-1-99-0", "RustDist1990RustcURL", "RustDist1990Tag"},
	{"cargo-1-99-0", "RustDist1990CargoURL", "RustDist1990Tag"},
	{"rust-std-1-99-0", "RustDist1990StdURL", "RustDist1990Tag"},
	{"rust-std-wasm-1-99-0", "RustDist1990StdWasmURL", "RustDist1990Tag"},
}

// Follower is a literal constant that carries a version constant's value.
// Retag marks a release URL whose tag is not the version, so a new version
// lives under a tag that has to be looked up.
type Follower struct {
	Const, Version string
	Retag          bool
}

// Followers is every literal in internal/checks that spells a renovate-moved
// version. TestEveryLiteralThatCarriesAVersionFollowsIt holds it complete.
var Followers = []Follower{
	{Const: "ShellcheckURL", Version: "ShellcheckVersion"},
	{Const: "ShellcheckMember", Version: "ShellcheckVersion"},
	{Const: "WasmToolsURL", Version: "WasmToolsVersion"},
	{Const: "WasmToolsMember", Version: "WasmToolsVersion"},
	{Const: "JustURL", Version: "JustVersion"},
	{Const: "WacURL", Version: "WacVersion"},
	{Const: "CargoAuditURL", Version: "CargoAuditVersion"},
	{Const: "CargoAuditMember", Version: "CargoAuditVersion"},
	{Const: "CargoNextestURL", Version: "CargoNextestVersion"},
	{Const: "CargoMutantsURL", Version: "CargoMutantsVersion"},
	{Const: "SccacheURL", Version: "SccacheVersion"},
	{Const: "SccacheMember", Version: "SccacheVersion"},
	{Const: "CurlURL", Version: "CurlVersion"},
	{Const: "PythonStandaloneURL", Version: "FleetPythonVersion", Retag: true},
}

// SumsVar is the map of asset URL to sha256.
const SumsVar = "ToolSHA256"

// Env is what a run reads, writes and fetches with.
type Env struct {
	Root      string       // the repository
	HTTP      *http.Client // asset downloads and the GitHub API
	GitHubAPI string       // https://api.github.com
	Token     string       // a GitHub token, if the environment has one
	Write     bool         // false: answer what would change, write and fetch nothing
	Log       io.Writer
	// RunLock runs the lock's recorded `uv pip compile …` in dir.
	RunLock func(ctx context.Context, dir string, args []string) error
	// Relock regenerates the python lock even when no coupled pin moved: the
	// post-upgrade task for renovate's pip_requirements, which bumps a lock
	// line in place under its predecessor's hashes.
	Relock bool
}

// Result is what a run changed (or, without Write, would change), as paths
// relative to the repository.
type Result struct {
	Changed []string
}

// Run brings every follower, checksum, tools.toml entry and coupled python pin
// into step with the version constants.
func (e *Env) Run(ctx context.Context) (Result, error) {
	var res Result
	src, err := loadGoSource(filepath.Join(e.Root, "internal", "checks"))
	if err != nil {
		return res, err
	}
	raw, err := os.ReadFile(filepath.Join(e.Root, "tools.toml"))
	if err != nil {
		return res, err
	}
	doc := parseTOML(raw)
	followed, err := e.syncFollowers(ctx, src)
	if err != nil {
		return res, err
	}
	summed, err := e.syncURLPins(ctx, src, doc)
	if err != nil {
		return res, err
	}
	if err := syncUVTools(src, doc); err != nil {
		return res, err
	}
	out := map[string][]byte{}
	for _, f := range mergeSorted(followed, summed) {
		out[filepath.Join("internal", "checks", f)] = src.files[f]
	}
	if string(doc.bytes()) != string(raw) {
		out["tools.toml"] = doc.bytes()
	}
	lock, err := e.pythonPins(src, out)
	if err != nil {
		return res, err
	}
	for _, name := range slices.Sorted(maps.Keys(out)) {
		if e.Write {
			if err := os.WriteFile(filepath.Join(e.Root, name), out[name], 0o644); err != nil {
				return res, err
			}
		}
		res.Changed = append(res.Changed, filepath.ToSlash(name))
	}
	if lock == nil {
		return res, nil
	}
	res.Changed = append(res.Changed, "pytools/requirements.txt")
	if !e.Write {
		return res, nil
	}
	return res, lock(ctx)
}

// syncFollowers moves each follower literal to its version.
func (e *Env) syncFollowers(ctx context.Context, src *goSource) ([]string, error) {
	var edits []edit
	for _, f := range Followers {
		lit, old, file, err := src.literal(f.Const)
		if err != nil {
			return nil, err
		}
		version, err := src.eval(f.Version)
		if err != nil {
			return nil, err
		}
		next, moved, err := follow(old, version)
		if err == nil && moved && f.Retag && e.Write {
			next, err = e.retag(ctx, next)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Const, err)
		}
		if moved {
			fmt.Fprintf(e.Log, "pinsync: %s %s -> %s\n", f.Const, old, next)
			edits = append(edits, edit{file: file, lit: lit, value: next})
		}
	}
	return src.apply(edits)
}

// syncURLPins puts each URL tool's sha256 in ToolSHA256 and its spec, version
// and sha256 in tools.toml. tools.toml's spec is the record of the URL the sum
// was taken for: a URL that still equals it keeps its sum and fetches nothing.
// A dry run that would fetch answers the sums' file without fetching.
func (e *Env) syncURLPins(ctx context.Context, src *goSource, doc *tomlDoc) ([]string, error) {
	file, sums, err := src.mapEntries(SumsVar)
	if err != nil {
		return nil, err
	}
	var edits []edit
	var unfetched []string
	for _, p := range URLPins {
		u, err := src.eval(p.URL)
		if err != nil {
			return nil, err
		}
		version, err := src.eval(p.Version)
		if err != nil {
			return nil, err
		}
		lit, ok := sums[p.URL]
		if !ok {
			return nil, fmt.Errorf("%s has no %s line; add it by hand, this command moves only pins that exist", SumsVar, p.URL)
		}
		sum, err := src.evalExpr(lit, 0)
		if err != nil {
			return nil, err
		}
		table := "tool." + p.Tool
		spec, ok := doc.get(table, "spec")
		if !ok {
			return nil, fmt.Errorf("tools.toml has no [%s] spec", table)
		}
		if spec != u {
			if !e.Write {
				fmt.Fprintf(e.Log, "pinsync: %s moved to %s and needs its sha256 fetched\n", p.Tool, u)
				unfetched = append(unfetched, file)
				sum = "" // unknown until fetched; the spec alone marks tools.toml
			} else {
				fetched, err := e.sumOf(ctx, u)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", p.Tool, err)
				}
				fmt.Fprintf(e.Log, "pinsync: %s %s sha256 %s\n", p.Tool, u, fetched)
				edits = append(edits, edit{file: file, lit: lit, value: fetched})
				sum = fetched
			}
		}
		for _, kv := range [][2]string{{"spec", u}, {"version", version}, {"sha256", sum}} {
			if kv[1] == "" {
				continue
			}
			if err := doc.set(table, kv[0], kv[1]); err != nil {
				return nil, err
			}
		}
	}
	files, err := src.apply(edits)
	return mergeSorted(files, unfetched), err
}

// syncUVTools puts each python lane tool's version in its `kind = "uv"` table.
func syncUVTools(src *goSource, doc *tomlDoc) error {
	rows, err := src.sliceFields("PythonLaneTools", "Name", "Version")
	if err != nil {
		return err
	}
	for _, row := range rows {
		name, err := src.evalExpr(row["Name"], 0)
		if err != nil {
			return fmt.Errorf("PythonLaneTools: %w", err)
		}
		version, err := src.evalExpr(row["Version"], 0)
		if err != nil {
			return fmt.Errorf("PythonLaneTools %s: %w", name, err)
		}
		if kind, _ := doc.get("tool."+name, "kind"); kind != "uv" {
			return fmt.Errorf("tools.toml [tool.%s] is not kind = \"uv\"", name)
		}
		if err := doc.set("tool."+name, "version", version); err != nil {
			return err
		}
	}
	return nil
}

func mergeSorted(a, b []string) []string {
	out := slices.Concat(a, b)
	slices.Sort(out)
	return slices.Compact(out)
}

// ExecLock runs the lock command with the process's own environment.
func ExecLock(ctx context.Context, dir string, args []string) error {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir, cmd.Stderr = dir, os.Stderr // uv also prints the lock to stdout
	return cmd.Run()
}
