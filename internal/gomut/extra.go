package gomut

import (
	"context"
	"maps"
	"os/exec"
	"slices"
	"strings"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/gitextra"
	"dagger/foundry-tools/internal/unitkey"
)

// THE NIGHTLY'S EXTRA SCOPE in the Go atom (checks/mutationranges.go says what
// it is). With checks.EnvMutationRanges unset every line below is skipped and the
// atom is exactly the per-pull gate it was: nothing here reads, runs or renders.
//
// SET, it widens the three reads that scope a run, and only those: the changed
// file list (so a night with no new Go still grades its carried survivors), the
// -U0 diff the grading keys are made from (so R covers the carried lines and an
// untouched unit's key is the same every night: the store answers it and nothing
// runs), and the diff gomutants reads (through gitextra, its only door).

// extraScope is the module's carried ranges, mapped to HEAD.
type extraScope struct {
	// root are the ranges named from the repository root, for the grading keys.
	root map[string][]unitkey.Range
	// mod are the same ranges named from the module, for gomutants and the
	// changed list.
	mod map[string][]unitkey.Range
	// moved are the carried lines whose position changed, as findings.
	moved []checks.Finding
}

// lookGit is exec.LookPath for the real git, a variable so a test can say.
var lookGit = func() (string, error) { return exec.LookPath("git") }

// readExtra reads and maps the module's extra ranges; nil with an empty reason
// is none asked. A reason is a could-not-run.
func (m *module) readExtra(ctx context.Context, since string) (*extraScope, string) {
	c, err := atoms.ReadCarried(ctx, m.in, since, id, func(f string) bool {
		return strings.HasSuffix(f, ".go") && (m.dir == "." || strings.HasPrefix(f, m.dir+"/"))
	})
	if err != nil {
		return nil, err.Error()
	}
	if c == nil {
		return nil, ""
	}
	return &extraScope{root: c.Ranges, mod: checks.ModuleMutationRanges(c.Ranges, m.dir), moved: c.Moved}, ""
}

// withExtraFiles adds the carried files to the changed list (module-relative,
// one per line), each once.
func (x *extraScope) withExtraFiles(changed string) string {
	if x == nil || len(x.mod) == 0 {
		return changed
	}
	have := strings.Fields(changed)
	for _, f := range slices.Sorted(maps.Keys(x.mod)) {
		if !slices.Contains(have, f) {
			have = append(have, f)
		}
	}
	return strings.Join(have, "\n")
}

// keyDiff is the diff the grading keys read: the real one, or with carried
// ranges the union of its added lines and theirs, rendered.
func (x *extraScope) keyDiff(diff string) string {
	if x == nil || len(x.root) == 0 {
		return diff
	}
	return checks.RenderMutationRanges(checks.UnionMutationRanges(unitkey.ParseRanges(diff), x.root), nil)
}

// gitEnv links the git helper into bin and answers the variables gomutants'
// exec carries for it; none without carried ranges.
func (x *extraScope) gitEnv(bin string) ([]string, error) {
	if x == nil || len(x.mod) == 0 {
		return nil, nil
	}
	real, err := lookGit()
	if err != nil {
		return nil, err
	}
	if _, err := linkHelper(bin, "git"); err != nil {
		return nil, err
	}
	return []string{gitextra.EnvRealGit + "=" + real, gitextra.EnvRanges + "=" + checks.MutationRangesArg(x.mod)}, nil
}
