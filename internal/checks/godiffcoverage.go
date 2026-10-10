package checks

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// THE GATE'S DIFF-COVERAGE CHECK: a changed line no test executes, read off the
// coverage profile go:test-race already writes — no mutant needed.
//
// WHY IT EXISTS. Mutation moves off the pull's path into a background tier, and
// 41% of the Go lane's findings over 2026-10-03 → 10-10 (erebus.mutation_mutants)
// were NOT COVERED: code the pull wrote that no test runs. That signal never
// needed a mutant, only a profile, so it stays BLOCKING on the pull here while
// the mutation lane stops blocking.
//
// THE SEMANTICS ARE gomutants' NOT COVERED, AT LINE GRAIN, and never wider. A
// gomutants mutant is NOT COVERED when its position sits inside a block whose
// count is 0 and inside no block that ran (internal/discover FilterByCoverage,
// v0.6.1). Positions OUTSIDE every block — a top-level const or var, a switch
// case expression before its clause block begins — it treats as covered in a
// tested file, and forge-testkit-go's classifier forgives in an untested one; so
// a line is flagged here only where a never-run block has CODE on it. The
// profile is the same `go test -coverprofile` gomutants gathers: no -coverpkg,
// so each package's coverage is its own tests', and the same build tags.
//
// Two things the mutation lane cannot see and this does, both in the safe
// direction for a reader: a line whose only operator is a disabled mutator, and
// a raw-string line inside an uncovered function. Neither is a new KIND of
// failure — each sits in a block the suite never ran, beside lines the mutation
// lane already flags.

// GoDiffCoverageCause is the slug every uncovered line carries.
const GoDiffCoverageCause = "not-covered"

// GoCoverProfile is where go:test-race writes its profile, outside the tree so
// no test that reads the checkout ever sees it.
const GoCoverProfile = "/tmp/go-test-race.cover"

// GoDiffCoverageInput is everything one module's evaluation reads.
type GoDiffCoverageInput struct {
	// Dir is the module's directory, repository-relative ("." at the root).
	Dir string
	// Root is the module's absolute directory in the lane, which Packages'
	// directories are under.
	Root string
	// Diff is `git diff --unified=0 --relative` run in the module: its paths
	// are module-relative.
	Diff string
	// Packages is `go list -e -f GoPackagesFormat ./...` for the module.
	Packages string
	// Profile is the suite's coverage profile.
	Profile string
	// Exclude is the mutation lane's generated-Go exclusion, applied to the
	// same module-relative path gomutants applies it to.
	Exclude *regexp.Regexp
	// Sources are the changed files' contents, keyed module-relative.
	Sources map[string]string
}

// coverBlock is one block of a profile, by its range.
type coverBlock struct {
	file           string
	sl, sc, el, ec int
}

// profileBlock is a full profile row: file:sl.sc,el.ec statements count.
var profileBlock = regexp.MustCompile(`^(.+):(\d+)\.(\d+),(\d+)\.(\d+) \d+ (\d+)$`)

// GoDiffCoverageFiles is the module-relative files of a diff this check
// judges: changed .go that is not a test and not excluded as generated. A file
// the diff only deletes names no range and is not here.
func GoDiffCoverageFiles(diff string, exclude *regexp.Regexp) []string {
	var files []string
	for _, r := range StrykerRanges(diff) {
		f := r.File
		if !strings.HasSuffix(f, ".go") || strings.HasSuffix(f, "_test.go") || exclude.MatchString(f) || slices.Contains(files, f) {
			continue
		}
		files = append(files, f)
	}
	return files
}

// packageDirs maps each import path the listing names to its module-relative
// directory. A directory outside the module (a nested module's) is left out.
func packageDirs(listing, root string) map[string]string {
	dirs := map[string]string{}
	for _, ln := range strings.Split(listing, "\n") {
		imp, dir, _ := strings.Cut(ln, "\t")
		if rel, ok := relTo(root, strings.TrimSpace(dir)); ok {
			dirs[strings.TrimSpace(imp)] = rel
		}
	}
	return dirs
}

// zeroBlocks reads a profile into the blocks that never ran, per
// module-relative file.
//
// A RANGE THAT APPEARS TWICE COUNTS ONCE, AT ITS HIGHEST COUNT, as gomutants
// reads it: a position inside any block that ran is covered. A block with no
// statement in it is kept, as gomutants keeps it — an empty `default:` spans
// no code, so it is never a finding anyway.
func zeroBlocks(profile string, dirs map[string]string) map[string][]coverBlock {
	counts := map[coverBlock]int{}
	for _, ln := range strings.Split(profile, "\n") {
		m := profileBlock.FindStringSubmatch(strings.TrimSpace(ln))
		if m == nil {
			continue
		}
		imp, name := path.Split(m[1])
		dir, ok := dirs[strings.TrimSuffix(imp, "/")]
		if !ok {
			continue
		}
		// The digits matched, so these parse.
		n := [5]int{}
		for i, s := range []string{m[2], m[3], m[4], m[5], m[6]} {
			n[i], _ = strconv.Atoi(s)
		}
		b := coverBlock{path.Join(dir, name), n[0], n[1], n[2], n[3]}
		// A count is never negative, so the zero an unseen range reads is
		// the floor max needs.
		counts[b] = max(counts[b], n[4])
	}
	zero := map[string][]coverBlock{}
	for b, c := range counts {
		if c == 0 {
			zero[b.file] = append(zero[b.file], b)
		}
	}
	return zero
}

// blockSegment is the part of line l (1-based) that block b spans: from its
// start column on its first line, up to its end column on its last. Columns are
// the profile's — 1-based bytes, the end one past the block's last byte.
func blockSegment(lines []string, b coverBlock, l int) string {
	if l > len(lines) {
		return ""
	}
	s := lines[l-1]
	if l == b.el {
		s = s[:min(b.ec-1, len(s))]
	}
	if l == b.sl {
		s = s[min(b.sc-1, len(s)):]
	}
	return s
}

// hasCode reports whether a segment holds code rather than layout: anything
// left once a trailing // comment, whitespace and bare brackets and separators
// are gone. A `}` closing an uncovered block carries no mutant and no statement.
func hasCode(seg string) bool {
	if i := strings.Index(seg, "//"); i >= 0 {
		seg = seg[:i]
	}
	return strings.Trim(seg, " \t{}()[],;") != ""
}

// GoUncoveredLines answers one finding per changed line that a never-run block
// has code on, in file then line order, subjects repository-relative.
func GoUncoveredLines(in GoDiffCoverageInput) []Finding {
	zero := zeroBlocks(in.Profile, packageDirs(in.Packages, in.Root))
	ranges := map[string][]StrykerRange{}
	for _, r := range StrykerRanges(in.Diff) {
		ranges[r.File] = append(ranges[r.File], r)
	}
	files := GoDiffCoverageFiles(in.Diff, in.Exclude)
	slices.Sort(files)
	var out []Finding
	for _, f := range files {
		lines := strings.Split(in.Sources[f], "\n")
		var hit []int
		for _, b := range zero[f] {
			for _, r := range ranges[f] {
				for l := max(r.From, b.sl); l <= min(r.To, b.el); l++ {
					if hasCode(blockSegment(lines, b, l)) {
						hit = append(hit, l)
					}
				}
			}
		}
		slices.Sort(hit)
		for _, l := range slices.Compact(hit) {
			out = append(out, Finding{
				Verdict: VerdictViolated,
				Subject: fmt.Sprintf("%s:%d", path.Join(in.Dir, f), l),
				Cause:   GoDiffCoverageCause,
				Detail:  "this pull changed the line and no test executes it: the suite's coverage profile counts its block 0 times",
				Probe:   goDiffCoverageAtom,
			})
		}
	}
	return out
}

// goDiffCoverageAtom names what produced a coverage finding.
const goDiffCoverageAtom = "go:diff-coverage"

// GoDiffCoverageVerdict settles one module: a pass over the files it judged, or
// a finding per uncovered line, capped as every atom's findings are.
func GoDiffCoverageVerdict(a AtomDef, files int, found []Finding) Verdict {
	if len(found) == 0 {
		return VerdictOf(a, 0, fmt.Sprintf("%s: every changed line in %d Go file(s) ran under the suite", a.ID, files))
	}
	subjects := make([]string, len(found))
	for i, f := range found {
		subjects[i] = "  " + f.Subject
	}
	v := VerdictOf(a, 1, fmt.Sprintf("%s: %d changed line(s) no test executes — NOT COVERED, the sharper half of what the mutation lane reports:\n%s",
		a.ID, len(found), strings.Join(subjects, "\n")))
	v.Findings = capFindings(found, a.ID)
	return v
}
