package atoms

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/unitkey"
)

// THE NIGHTLY'S CARRIED RANGES, as every mutation atom reads them
// (checks/mutationranges.go says what they are). One read for the four lanes, so
// the env's grammar and the base-to-HEAD move have one home.

// Carried is a run's carried ranges, moved to HEAD.
type Carried struct {
	// Ranges are the carried lines the atom keeps, named from the root.
	Ranges map[string][]unitkey.Range
	// Moved are the kept lines whose position changed, as holds findings.
	Moved []checks.Finding
}

// Files are the carried files, sorted.
func (c *Carried) Files() []string {
	if c == nil {
		return nil
	}
	return slices.Sorted(maps.Keys(c.Ranges))
}

// Empty is no carried line.
func (c *Carried) Empty() bool { return c == nil || len(c.Ranges) == 0 }

// ReadCarried reads checks.EnvMutationRanges and moves it from since to HEAD,
// asking git at the root. keep names the files the atom grades (its language,
// its module); probe stamps the moved findings. nil with a nil error is none
// asked; an error is the atom's could-not-run.
func ReadCarried(ctx context.Context, in Input, since, probe string, keep func(string) bool) (*Carried, error) {
	raw := os.Getenv(checks.EnvMutationRanges)
	if raw == "" {
		return nil, nil
	}
	asked, err := checks.ParseMutationRanges(raw)
	if err != nil {
		return nil, err
	}
	diff, code := in.run(ctx, Cmd{Dir: in.Root, Name: "git", Args: []string{"-C", in.Root, "diff", "-M", "--unified=0", "--no-color", "--no-ext-diff", since, "HEAD"}})
	if code != 0 {
		return nil, carriedDiffError{since: since, out: LastLine(diff)}
	}
	mapped, moved := checks.MapMutationRanges(asked, diff)
	c := &Carried{Ranges: map[string][]unitkey.Range{}}
	for f, rs := range mapped {
		if keep(f) {
			c.Ranges[f] = rs
		}
	}
	for _, m := range moved {
		f, _, _ := strings.Cut(m.Subject, ":")
		if keep(f) {
			m.Probe = probe
			c.Moved = append(c.Moved, m)
		}
	}
	return c, nil
}

// carriedDiffError is a mapping diff git would not answer.
type carriedDiffError struct{ since, out string }

func (e carriedDiffError) Error() string {
	return "git could not diff the carried ranges' base " + e.since + " against HEAD: " + e.out
}

// Plus is c with more lines to grade, named from the root: the source a
// change's tests run (checks/testcovered.go). They widen the scope exactly as
// carried lines do and move no finding. Nothing added is c, nil included.
func (c *Carried) Plus(r map[string][]unitkey.Range) *Carried {
	if len(r) == 0 {
		return c
	}
	if c == nil {
		c = &Carried{}
	}
	c.Ranges = checks.UnionMutationRanges(c.Ranges, r)
	return c
}

// Into adds the moved findings to a verdict that graded (state 0 or 1); a
// could-not-run carries nothing, because nothing it says was measured.
func (c *Carried) Into(v checks.Verdict) checks.Verdict {
	if c != nil && v.State < 2 {
		v.Findings = append(v.Findings, c.Moved...)
	}
	return v
}

// WithCarried is diff with the carried lines added: the union of the lines diff
// adds and the carried ones, rendered with their text from the tree at root.
// Nothing carried is diff, byte for byte.
func (c *Carried) WithCarried(diff, root string) string {
	if c.Empty() {
		return diff
	}
	return checks.RenderMutationRanges(checks.UnionMutationRanges(unitkey.ParseRanges(diff), c.Ranges), TreeLines(root))
}

// TreeLines answers line n (1-indexed) of a file under root, "" past its end or
// for a file that does not read. Each file is read once.
func TreeLines(root string) func(file string, n int) string {
	cache := map[string][]string{}
	return func(file string, n int) string {
		lines, ok := cache[file]
		if !ok {
			b, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(file))) // unreadable: no lines
			lines = strings.Split(string(b), "\n")
			cache[file] = lines
		}
		if n < 1 || n > len(lines) {
			return ""
		}
		return lines[n-1]
	}
}
