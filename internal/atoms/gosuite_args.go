package atoms

import (
	"fmt"
	"slices"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

// THE CALLER'S ARGUMENTS TO THE RACE SUITE (dev:go-test-live). The atom runs what the
// race atom (go:test-race) runs, so the tree decides the servers and the tags; a caller who
// asks for more is folded in, never fought:
//
//   - -race is implied: any spelling of it is dropped.
//   - -tags is UNIONED with the tree's computed tags, so `-tags live` still
//     compiles a suite behind a tag the fleet does not name. The reason says so
//     (Note): no server is bound for that tag and its suite reads whatever DSN
//     variable it likes, which nothing sets.
//   - -p is the caller's when given; otherwise -p 1 stands with any tags.
//   - a package pattern replaces ./...; every other flag passes through.
//   - everything after -args goes to the test binary untouched.

// SuiteArgs is a caller's arguments split for the race suite.
type SuiteArgs struct {
	// Tags are the caller's build tags, in the order given, each once.
	Tags []string
	// Packages says the caller named a package, which replaces ./....
	Packages bool
	// P says the caller set -p.
	P bool
	// Rest are the other arguments, in order, for `go test`.
	Rest []string
}

// ParseSuiteArgs reads a caller's arguments.
func ParseSuiteArgs(args []string) SuiteArgs {
	var s SuiteArgs
	skip := false // the previous argument was a go flag that takes this one as its value
	for i := 0; i < len(args); i++ {
		a := args[i]
		if skip {
			skip = false
			s.Rest = append(s.Rest, a)
			continue
		}
		if a == "-args" {
			s.Rest = append(s.Rest, args[i:]...)
			break
		}
		flag, val, hasVal := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(a, "-"), "-"), "=")
		switch {
		case !strings.HasPrefix(a, "-"):
			if PackageLike(a) {
				s.Packages = true
			}
			s.Rest = append(s.Rest, a)
		case flag == "race":
			// implied; a bare -race takes no value, so nothing else is consumed.
		case flag == "tags":
			if !hasVal && i+1 < len(args) {
				i++
				val = args[i]
			}
			for _, t := range strings.FieldsFunc(val, func(r rune) bool { return r == ',' || r == ' ' }) {
				if !slices.Contains(s.Tags, t) {
					s.Tags = append(s.Tags, t)
				}
			}
		case flag == "p":
			s.P = true
			s.Rest = append(s.Rest, a)
			skip = !hasVal
		default:
			s.Rest = append(s.Rest, a)
			skip = !hasVal && GoValueFlag(flag)
		}
	}
	return s
}

// UnionTags is the tree's computed -tags word with the caller's tags added, in
// that order, each once; "" when both are empty.
func UnionTags(word string, caller []string) string {
	var tags []string
	if word != "" {
		tags = strings.Split(word, ",")
	}
	for _, t := range caller {
		if !slices.Contains(tags, t) {
			tags = append(tags, t)
		}
	}
	return strings.Join(tags, ",")
}

// Note is the line the reason adds for the caller's tags that the fleet does
// not bind a server for: "" when there is none. A tag the fleet names but the
// tree does not carry behind a `//go:build` line is also unbound.
func (s SuiteArgs) Note(dbs []checks.TestDB, brokers []checks.TestBroker, caches []checks.TestCache, kubes []checks.TestKube) string {
	bound := map[string]bool{}
	for _, d := range dbs {
		bound[d.Tag] = true
	}
	for _, b := range brokers {
		bound[b.Tag] = true
	}
	for _, c := range caches {
		bound[c.Tag] = true
	}
	for _, k := range kubes {
		bound[k.Tag] = true
	}
	var odd []string
	for _, t := range s.Tags {
		if !bound[t] {
			odd = append(odd, t)
		}
	}
	if len(odd) == 0 {
		return ""
	}
	return fmt.Sprintf("\ncaller tags compiled with no server bound: %s — a suite behind one reads its own DSN variable, which nothing here sets", strings.Join(odd, ", "))
}

// goValueFlags are the `go build`/`go test`/`go vet` flags whose NEXT argument
// is their value, so that value is never mistaken for a package.
var goValueFlags = map[string]bool{
	"run": true, "skip": true, "bench": true, "count": true, "timeout": true, "tags": true,
	"p": true, "parallel": true, "cpu": true, "coverprofile": true, "covermode": true,
	"coverpkg": true, "vet": true, "ldflags": true, "gcflags": true, "o": true, "mod": true,
	"exec": true, "fuzz": true, "fuzztime": true, "benchtime": true, "outputdir": true,
	"asmflags": true, "modfile": true, "overlay": true, "pkgdir": true, "toolexec": true,
	"blockprofile": true, "cpuprofile": true, "memprofile": true, "mutexprofile": true,
	"trace": true, "C": true,
}

// GoValueFlag says whether a go flag, in either dash spelling and without an
// attached =value, takes the next argument as its value (-o ./bin/x).
func GoValueFlag(flag string) bool { return goValueFlags[strings.TrimLeft(flag, "-")] }

// NamesPackage says whether the go command's own arguments name a package: a
// package-like word that is not the value of the flag before it, so
// `-o ./bin/x` keeps the default ./... while `-o ./bin/x ./cmd/x` replaces it.
// The caller cuts the arguments at -args first.
func NamesPackage(args []string) bool {
	skip := false
	for _, a := range args {
		if skip {
			skip = false
			continue
		}
		if strings.HasPrefix(a, "-") {
			name, _, hasValue := strings.Cut(a, "=")
			skip = !hasValue && GoValueFlag(name)
			continue
		}
		if PackageLike(a) {
			return true
		}
	}
	return false
}

// PackageLike says whether one argument names a Go package pattern: ".", "..",
// "./x", "../x", "x/...", or a path whose first element is a host (a.b/c).
// Flag-shaped arguments never are. Whether a word is a flag's value is the
// argument list's question (NamesPackage), not this one's.
func PackageLike(arg string) bool {
	if strings.HasPrefix(arg, "-") {
		return false
	}
	switch {
	case arg == "." || arg == "..":
		return true
	case strings.HasPrefix(arg, "./"), strings.HasPrefix(arg, "../"), strings.HasSuffix(arg, "..."):
		return true
	}
	first, _, slashed := strings.Cut(arg, "/")
	return slashed && strings.Contains(first, ".")
}
