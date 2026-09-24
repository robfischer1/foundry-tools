package checks

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"text/template"
)

// BOTH CONTROLS MUST BE VALID GO AND MUST STAY UNDER-TESTED. They are strings
// here and source files in the lane, so a bad escape is a control that does not
// build — which reads as CanaryUnknown, the answer that distrusts, and would
// quietly turn the package-main exclusion on forever. Parsing them proves the
// strings are Go; the package clause proves each one is the shape it claims.
func TestTheControlsAreValidGoOfTheShapeTheyClaim(t *testing.T) {
	for _, c := range []struct{ name, code, test, pkg string }{
		{"harness", GoMutationCanaryCode, GoMutationCanaryTest, "canary"},
		{"package-main", GoMutationMainCanaryCode, GoMutationMainCanaryTest, "main"},
	} {
		t.Run(c.name, func(t *testing.T) {
			for what, src := range map[string]string{"code": c.code, "test": c.test} {
				f, err := parser.ParseFile(token.NewFileSet(), c.name+"_"+what+".go", src, 0)
				if err != nil {
					t.Fatalf("the %s control's %s is not Go: %v", c.name, what, err)
				}
				if f.Name.Name != c.pkg {
					t.Errorf("the %s control's %s is package %q, want %q", c.name, what, f.Name.Name, c.pkg)
				}
			}
			// The mutant: `+` is what gremlins mutates, and the test must run Add
			// WITHOUT asserting its result. An assertion would kill the mutant and
			// the control would answer broken for a reason that is not the bug.
			if !strings.Contains(c.code, "return a + b") {
				t.Errorf("the %s control lost its mutable operator:\n%s", c.name, c.code)
			}
			if !strings.Contains(c.test, "Add(2, 3)") {
				t.Errorf("the %s control's test does not run Add, so its mutant is NOT COVERED rather than LIVED:\n%s", c.name, c.test)
			}
			for _, assertion := range []string{"t.Error", "t.Fatal", "if got"} {
				if strings.Contains(c.test, assertion) {
					t.Errorf("the %s control's test asserts (%s) — an honest LIVED needs a test that notices nothing:\n%s", c.name, assertion, c.test)
				}
			}
		})
	}
	// THE PACKAGE-MAIN CONTROL NEEDS A `func main`, or it is not a main package
	// and gremlins resolves it like any other — the bug it exists to detect
	// would not fire.
	if !strings.Contains(GoMutationMainCanaryCode, "func main()") {
		t.Errorf("the package-main control has no main function:\n%s", GoMutationMainCanaryCode)
	}
	// Two modules, two names: they are written side by side into one container,
	// and a shared module path is a cache collision waiting to answer the wrong
	// control's question.
	if GoMutationCanaryMod == GoMutationMainCanaryMod {
		t.Errorf("both controls declare the same module: %q", GoMutationCanaryMod)
	}
}

// GoMainFilesFormat is a text/template that go list executes, so it is pinned
// against text/template here rather than against a toolchain: the mistake it
// guards is `{{.Dir}}` inside the range, which resolves against the FILE NAME
// and yields paths with no directory at all.
func TestGoMainFilesFormatNamesEveryFileOfAMainPackageAndNoOther(t *testing.T) {
	tmpl, err := template.New("golist").Parse(GoMainFilesFormat)
	if err != nil {
		t.Fatalf("the go list template does not parse: %v", err)
	}
	type pkg struct {
		Name, Dir string
		GoFiles   []string
	}
	for _, c := range []struct {
		name string
		in   pkg
		want string
	}{
		{"a main package in a subdirectory", pkg{"main", "/src/cmd/tool", []string{"main.go", "flags.go"}},
			"/src/cmd/tool/main.go\n/src/cmd/tool/flags.go\n"},
		{"a main package at the module root", pkg{"main", "/src", []string{"main.go"}}, "/src/main.go\n"},
		{"a library says nothing", pkg{"lib", "/src/lib", []string{"lib.go", "more.go"}}, ""},
		// A package called main whose files are all excluded by build tags: the
		// template answers nothing, and an empty answer is an answer.
		{"a main package with no files", pkg{"main", "/src/cmd/tagged", nil}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out strings.Builder
			if err := tmpl.Execute(&out, c.in); err != nil {
				t.Fatal(err)
			}
			if out.String() != c.want {
				t.Errorf("rendered %q, want %q", out.String(), c.want)
			}
		})
	}
}

// ParseGoMainFiles turns what go list printed into the paths gremlins uses in
// its report: relative to the module, which is not the repository when the
// module is nested.
func TestParseGoMainFilesReadsPathsRelativeToTheModule(t *testing.T) {
	out := "/src/cmd/tool/main.go\n/src/cmd/tool/flags.go\n\n  /src/main.go  \n"
	got := ParseGoMainFiles(out, "/src")
	want := GoMainFiles{"cmd/tool/main.go": true, "cmd/tool/flags.go": true, "main.go": true}
	if len(got) != len(want) {
		t.Fatalf("read %v, want %v", got, want)
	}
	for k := range want {
		if !got[k] {
			t.Errorf("%q was not read from %q", k, out)
		}
	}
	// A NESTED MODULE'S PATHS ARE RELATIVE TO THE MODULE. gremlins names
	// cmd/forge/main.go, never tools/forge/cmd/forge/main.go, so a set keyed the
	// other way matches nothing and the exclusion silently does not apply.
	nested := ParseGoMainFiles("/src/tools/forge/cmd/forge/main.go\n", "/src/tools/forge")
	if len(nested) != 1 || !nested["cmd/forge/main.go"] {
		t.Errorf("a nested module read %v, want cmd/forge/main.go", nested)
	}
	// A trailing slash on the root is the same root.
	if slashed := ParseGoMainFiles("/src/cmd/tool/main.go\n", "/src/"); !slashed["cmd/tool/main.go"] {
		t.Errorf("a root with a trailing slash read %v", slashed)
	}
	// A line outside the module is not this module's file. go list -e prints
	// package-load errors, and a path from elsewhere must not become an
	// exclusion — an over-wide set hides real mutants.
	outside := ParseGoMainFiles("/other/cmd/tool/main.go\n/src\n/srcfake/main.go\n", "/src")
	if len(outside) != 0 {
		t.Errorf("read %v from lines outside the module, want none", outside)
	}
	// AN EMPTY ANSWER IS AN ANSWER, and it is not nil: a module with no main
	// package excludes nothing, which is different from a lane that could not
	// look (GoMutationRun.MainFilesErr carries that).
	if empty := ParseGoMainFiles("", "/src"); empty == nil || len(empty) != 0 {
		t.Errorf("an empty listing read %v, want an empty non-nil set", empty)
	}
}

// GoMutationCanary is the reader for BOTH controls, and the two answers it can
// read off a one-mutant module are the two the gate acts on. Anything else is
// unknown, which distrusts rather than clears.
func TestGoMutationCanaryReadsTheControl(t *testing.T) {
	for out, want := range map[string]string{
		"Mutation testing completed\nKilled: 0, Lived: 1, Not covered: 0\n": CanaryOK,
		"Killed: 1, Lived: 0, Not covered: 0":                               CanaryBroken,
		"go: cannot find main module":                                       CanaryUnknown,
		"":                                                                  CanaryUnknown,
	} {
		if got := GoMutationCanary(out); got != want {
			t.Errorf("%q: %s, want %s", out, got, want)
		}
	}
}
