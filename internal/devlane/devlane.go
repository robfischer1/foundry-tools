// Package devlane is the pure half of the Dev surface: the decisions
// `dagger call dev` makes about a tree and a verb, as functions a table can sit
// under. Nothing here touches an engine; package main's dev.go is the thin
// caller that turns these answers into containers.
//
// WHY A SURFACE AT ALL. The workstation is losing its Go and Rust toolchains, so
// the inner loop (build, test, vet, clippy, fmt, tidy, lock) has to run on the
// cluster's engine. The verbs reuse the gate's own lane images and cache volumes
// (checks.CachesForRepo); this package only decides WHAT to run in them.
package devlane

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"dagger/foundry-tools/internal/checks"
)

// Languages a Dev surface can drive.
const (
	Go   = "go"
	Rust = "rust"
)

// Ignore is what never leaves the host: the gate's own upload ignore
// (main.go's New, the list `just check` uploads under) less `.git`. The gate
// keeps `.git` because its history atoms read it; the interactive loop reads no
// history, and a primary checkout's .git can be gigabytes. A test holds this
// list to the annotation on FoundryTools.Dev so the two cannot drift.
var Ignore = []string{
	"**/node_modules", "**/.venv", "**/target", "**/__pycache__", "**/.pytest_cache",
	"**/.mypy_cache", "**/.ruff_cache", "**/dist", ".melt", ".claude", ".specify", ".furnace", ".git",
}

// Lang decides the language of a source: the explicit one when given, else what
// the root manifests say. Both manifests with no explicit choice is an error
// rather than a guess, because the wrong guess runs the wrong toolchain over
// the tree and reports on it.
func Lang(entries []string, want string) (string, error) {
	switch want {
	case Go, Rust:
		return want, nil
	case "":
	default:
		return "", fmt.Errorf("--lang %q is not go or rust", want)
	}
	hasGo, hasRust := slices.Contains(entries, "go.mod"), slices.Contains(entries, "Cargo.toml")
	switch {
	case hasGo && hasRust:
		return "", errors.New("the source has both go.mod and Cargo.toml: say which with --lang=go or --lang=rust")
	case hasGo:
		return Go, nil
	case hasRust:
		return Rust, nil
	}
	return "", errors.New("no go.mod or Cargo.toml at the source root: nothing to drive (or name --lang)")
}

// Identity is what the caches key on: the repository, never the worktree path.
// The Go module path and the Cargo package name are the same in every worktree of
// one repo. A virtual workspace has no package of its own, so the names of the
// crates Cargo.lock records as local (no `source`) stand in, and its members only
// when there is no lock. "" when the manifests say nothing; the caller then falls
// back to the shared key.
func Identity(lang, manifest, lock string) string {
	if lang == Go {
		return goModule(manifest)
	}
	var m struct {
		Package   struct{ Name string } `toml:"package"`
		Workspace struct{ Members []string }
	}
	if _, err := toml.Decode(manifest, &m); err != nil {
		return ""
	}
	if m.Package.Name != "" {
		return m.Package.Name
	}
	if local := localCrates(lock); len(local) > 0 {
		return "workspace:" + strings.Join(local, ",")
	}
	if len(m.Workspace.Members) > 0 {
		members := slices.Clone(m.Workspace.Members)
		slices.Sort(members)
		return "workspace:" + strings.Join(members, ",")
	}
	return ""
}

// localCrates are the packages a Cargo.lock records without a registry or git
// source: the workspace's own crates, sorted.
func localCrates(lock string) []string {
	var l struct {
		Package []struct{ Name, Source string }
	}
	// A lock that will not parse names no crates; the error adds nothing.
	_, _ = toml.Decode(lock, &l)
	var names []string
	for _, p := range l.Package {
		if p.Source == "" && p.Name != "" {
			names = append(names, p.Name)
		}
	}
	slices.Sort(names)
	return names
}

// goModule reads the module path off a go.mod, tolerating a quoted path and a
// trailing comment.
func goModule(manifest string) string {
	for _, line := range strings.Split(manifest, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module")
		if !ok || rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
			continue
		}
		rest, _, _ = strings.Cut(rest, "//")
		return strings.Trim(strings.TrimSpace(rest), "\"`")
	}
	return ""
}

// HasVendor says whether the tree vendors its modules, off the root entries.
func HasVendor(entries []string) bool {
	return slices.Contains(entries, "vendor/") || slices.Contains(entries, "vendor")
}

// RepoKey is the repository identity the caches key on: the caller's --repo
// when given, else what the manifest names.
func RepoKey(repo, lang, manifest, lock string) string {
	if repo != "" {
		return repo
	}
	return Identity(lang, manifest, lock)
}

// goValueFlags are the `go test`/`go build`/`go vet` flags whose NEXT argument is
// their value, so that value is never mistaken for a package.
var goValueFlags = map[string]bool{
	"-run": true, "-skip": true, "-bench": true, "-count": true, "-timeout": true, "-tags": true,
	"-p": true, "-parallel": true, "-cpu": true, "-coverprofile": true, "-covermode": true,
	"-coverpkg": true, "-vet": true, "-ldflags": true, "-gcflags": true, "-o": true, "-mod": true,
	"-exec": true, "-fuzz": true, "-fuzztime": true, "-benchtime": true, "-outputdir": true,
	"-asmflags": true, "-modfile": true, "-overlay": true, "-pkgdir": true, "-toolexec": true,
	"-blockprofile": true, "-cpuprofile": true, "-memprofile": true, "-mutexprofile": true,
	"-trace": true, "-C": true,
}

// packageLike says whether one argument names a package pattern.
func packageLike(arg string) bool {
	switch {
	case arg == "." || arg == "..":
		return true
	case strings.HasPrefix(arg, "./"), strings.HasPrefix(arg, "../"), strings.HasSuffix(arg, "..."):
		return true
	}
	first, _, slashed := strings.Cut(arg, "/")
	return slashed && strings.Contains(first, ".")
}

// goArgsEnd is where the go command's own arguments stop: at `-args`, past which
// everything belongs to the test binary. len(args) when there is none.
func goArgsEnd(args []string) int {
	for i, a := range args {
		if a == "-args" || a == "--args" {
			return i
		}
	}
	return len(args)
}

// hasPackage says whether the go command's own arguments already name a package.
// One pass with a flag saying "the previous argument was a flag that takes a
// value", so the loop ends with the arguments and nothing moves its index.
func hasPackage(args []string) bool {
	skip := false
	for _, a := range args[:goArgsEnd(args)] {
		if skip {
			skip = false
			continue
		}
		if strings.HasPrefix(a, "-") {
			name, _, hasValue := strings.Cut(a, "=")
			skip = goValueFlags[name] && !hasValue
			continue
		}
		if packageLike(a) {
			return true
		}
	}
	return false
}

// withDefaultPackage adds ./... to the go command's own arguments when none
// names a package, ahead of `-args`.
func withDefaultPackage(args []string) []string {
	if hasPackage(args) {
		return args
	}
	end := goArgsEnd(args)
	return slices.Concat(args[:end], []string{"./..."}, args[end:])
}

// hasRaceFlag says whether the caller already decided the race detector.
func hasRaceFlag(args []string) bool {
	for _, a := range args[:goArgsEnd(args)] {
		name := strings.SplitN(strings.TrimLeft(a, "-"), "=", 2)[0]
		if name == "race" && strings.HasPrefix(a, "-") {
			return true
		}
	}
	return false
}

// GoArgv is the command for a Go verb. race is `test`'s default and the caller's
// to turn off (--race=false) or to spell for themselves (-race, -race=false in
// the passthrough, which wins).
func GoArgv(verb string, args []string, race bool) ([]string, error) {
	switch verb {
	case "build", "vet":
		return slices.Concat([]string{"go", verb}, withDefaultPackage(args)), nil
	case "test":
		head := []string{"go", "test"}
		if race && !hasRaceFlag(args) {
			head = append(head, "-race")
		}
		return slices.Concat(head, withDefaultPackage(args)), nil
	}
	return nil, fmt.Errorf("%s is not a Go verb (build, vet, test, fmt, tidy)", verb)
}

// cargoScoped says whether the caller already chose which packages cargo acts
// on, in cargo's own arguments (before any `--`).
func cargoScoped(args []string) bool {
	for _, a := range cargoOwn(args) {
		switch {
		case a == "--package", a == "--workspace", a == "--all", a == "--manifest-path":
			return true
		case strings.HasPrefix(a, "--package="), strings.HasPrefix(a, "--manifest-path="):
			return true
		case strings.HasPrefix(a, "-p") && !strings.HasPrefix(a, "--"):
			// -p crate and -pcrate, but not --profile.
			return true
		}
	}
	return false
}

// cargoOwn is the part of the passthrough that is cargo's, before any `--`.
func cargoOwn(args []string) []string {
	if i := slices.Index(args, "--"); i >= 0 {
		return args[:i]
	}
	return args
}

// targetSelectors are the flags that choose what clippy lints, so the default
// --all-targets stands down for them.
var targetSelectors = []string{"--all-targets", "--lib", "--bins", "--bin", "--tests", "--test", "--benches", "--bench", "--examples", "--example"}

// clippyLints is the fleet's lint set, named after `--` so a crate's own lint
// table cannot narrow it (atoms_rust.go rustCargoClippy says why).
var clippyLints = []string{"-W", "clippy::all", "-D", "warnings"}

// RustArgv is the command for a Rust verb. Without a package selection of its
// own, check/build/test/clippy act on the whole workspace, as the gate does.
func RustArgv(verb string, args []string) ([]string, error) {
	switch verb {
	case "check", "build", "test":
		head := []string{"cargo", verb}
		if !cargoScoped(args) {
			head = append(head, "--workspace")
		}
		return slices.Concat(head, args), nil
	case "clippy":
		head := []string{"cargo", "clippy"}
		if !cargoScoped(args) {
			head = append(head, "--workspace")
		}
		own := cargoOwn(args)
		if !slices.ContainsFunc(own, func(a string) bool { return slices.Contains(targetSelectors, a) }) {
			head = append(head, "--all-targets")
		}
		out := slices.Concat(head, args)
		if !slices.Contains(args, "--") {
			out = slices.Concat(out, []string{"--"}, clippyLints)
		}
		return out, nil
	case "fmt":
		return slices.Concat([]string{"cargo", "fmt", "--all"}, args), nil
	case "lock":
		return slices.Concat([]string{"cargo", "generate-lockfile"}, args), nil
	case "update":
		return slices.Concat([]string{"cargo", "update"}, args), nil
	}
	return nil, fmt.Errorf("%s is not a Rust verb (check, build, test, clippy, fmt, lock, update)", verb)
}

// verbs are the verbs each language answers to.
var verbs = map[string][]string{
	Go:   {"build", "vet", "test", "fmt", "tidy", "vendor"},
	Rust: {"check", "build", "test", "clippy", "fmt", "lock", "update"},
}

// Applies says whether a verb exists for a language, with the sentence to give
// when it does not.
func Applies(lang, verb string) error {
	if slices.Contains(verbs[lang], verb) {
		return nil
	}
	return fmt.Errorf("%s does not exist for a %s source (it has: %s)", verb, lang, strings.Join(verbs[lang], ", "))
}

// SettleCode is the exit the call ends on for a tool's own exit code: 0 stays 0,
// a signal-range or not-found code (126 and up: no binary, a kill, an OOM) is
// the lane's could-not-run, 2, and every other non-zero code is the tool saying
// "no", 1. The call never exits 0 on a failed tool.
func SettleCode(code int) int {
	switch {
	case code == 0:
		return 0
	case code >= 126:
		return 2
	}
	return 1
}

// StreamLimit bounds each of a failed tool's streams in the reason. Stdout and
// stderr are tailed separately: one 64KB window over both let a large stderr
// push cargo's failing-test names, which are on stdout, out of the reason.
const StreamLimit = 32 << 10

// Reason is a failed tool's text as the settle exec prints it: the tail of
// stdout, then the tail of stderr, each bounded on its own.
func Reason(stdout, stderr string) string {
	out, errs := checks.LogTail(stdout, StreamLimit), checks.LogTail(stderr, StreamLimit)
	if out != "" && errs != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out + errs
}

// Refuse is the sentence for a source whose repository identity cannot be
// found: the cargo target is never keyed on nothing (which would be the
// fleet-shared volume), so the caller names one.
func Refuse(repo string) error {
	if repo != "" {
		return nil
	}
	return errors.New("cannot tell which repository this is (no Go module path or Cargo package name): pass --repo=<name>, so the build cache is not shared with another repository's")
}

// Nonce makes a verb's exec fresh: the env value that keys it afresh. "" is no
// nonce, and the engine's cache answers an unchanged tree.
func Nonce(fresh bool, now int64) string {
	if !fresh {
		return ""
	}
	return strconv.FormatInt(now, 10)
}
