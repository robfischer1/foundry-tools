package atomtest

import (
	"context"
	"slices"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
)

// Fake stands in for the programs on PATH through the input's Exec seam: it
// answers each call from a script, records it, and fails the test for a program
// the tools container does not carry (atoms.Programs) unless Free names it.
type Fake struct {
	T      TB
	Calls  []atoms.Cmd
	Answer func(c atoms.Cmd) (string, int)
	// Free are programs a toolchain image provides and the atoms binary's tools
	// container does not (uv).
	Free []string
}

// Exec is the seam.
func (f *Fake) Exec(_ context.Context, c atoms.Cmd) (string, int) {
	f.T.Helper()
	Assert(f.T, slices.Contains(atoms.Programs, c.Name) || slices.Contains(f.Free, c.Name), "exec of %q: no layer of the tools container carries it", c.Name)
	f.Calls = append(f.Calls, c)
	if f.Answer == nil {
		return "", 0
	}
	return f.Answer(c)
}

// Ran is the program names the atom exec'd, in order.
func (f *Fake) Ran() []string {
	var names []string
	for _, c := range f.Calls {
		names = append(names, c.Name)
	}
	return names
}

// Last is the call the atom made last, or a failed test.
func (f *Fake) Last() atoms.Cmd {
	f.T.Helper()
	if len(f.Calls) == 0 {
		f.T.Fatal("no program was exec'd")
	}
	return f.Calls[len(f.Calls)-1]
}

// ToolTree is TreeIn with the exec seam set to the script.
func ToolTree(t TB, files map[string]string, answer func(atoms.Cmd) (string, int)) (atomkit.In, *Fake) {
	t.Helper()
	in := TreeIn(t, files)
	f := &Fake{T: t, Answer: answer}
	in.Exec = f.Exec
	return in, f
}

// Flagged is an argv as one string, for a needle.
func Flagged(c atoms.Cmd) string { return strings.Join(c.Args, " ") }

// DieTree is the smallest tree the dies atoms call the policy die's source,
// with extra files laid over it.
func DieTree(extra map[string]string) map[string]string {
	files := map[string]string{"policy/.manifest": "x", "fleet/repos/chaos.json": "{\n  \"a\": 1\n}\n"}
	for k, v := range extra {
		files[k] = v
	}
	return files
}
