package atoms

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

// template:render-matrix — every case in this template's ci-matrix.toml still
// renders. A template bug does not break the template: it propagates into every
// repo stamped afterward and surfaces in someone else's, which is why it is a
// check on the template and not on the stamped repos.
//
// THE TREE MUST BE A REAL REPOSITORY. The matrix renders the template AT ITS GIT
// HEAD (--vcs-ref=HEAD is load-bearing, foundry#130); without a repository copier
// resolves some other tree, and a green from that would be about something else.
// A linked worktree, whose `.git` is a file naming a gitdir this process may not
// have, fails ON PURPOSE: an index can be rebuilt, a history cannot.
//
// IT WRITES, SO EVERY RENDER GOES TO A DIRECTORY OF THIS RUN'S OWN. copier renders
// into its destination (and clones the template into its own temp), never into
// the template; the destination is created under the temp directory, graded where
// it stands and removed, so the tree under check is exactly what it was. The
// chain rendered to /out/<case> for the same reason.
//
// copier IS THE VENV'S, at checks.CopierVersion. The chain ran `uvx --from
// copier==<version> copier ...`, which resolved the pinned version from the
// index on every cold cache; the program and every argument after it are the
// chain's (checks.CopierArgv is the one spelling of both).

// copierProgram is the index of the program in checks.CopierArgv's `uvx --from
// copier==X copier copy ...`: what runs is everything from there.
const copierProgram = 3

// templateRenderMatrix: see the file's header.
func templateRenderMatrix(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	t := in.tree()
	entries, err := t.entries(".")
	if err != nil {
		return checks.VerdictOf(a, int(checks.StateCannotRun), a.ID+": CANNOT RUN - the repository root could not be read: "+err.Error())
	}
	if !checks.HasEntry(entries, "ci-matrix.toml") {
		return checks.VerdictOf(a, int(checks.StatePass), a.ID+": ABSENT - no ci-matrix.toml at the repository root, so this repo declares no render matrix.")
	}
	const noRepository = ": CANNOT RUN - no .git in the tree under check. The matrix renders the template AT ITS GIT HEAD (--vcs-ref=HEAD is load-bearing, foundry#130); without a repository copier resolves some other tree, and a green from that would be a green about something else."
	if !checks.HasEntry(entries, ".git") {
		return checks.VerdictOf(a, int(checks.StateCannotRun), a.ID+noRepository)
	}
	if fi, err := os.Lstat(filepath.Join(in.Root, ".git")); err == nil && fi.Mode().IsRegular() {
		return checks.VerdictOf(a, int(checks.StateCannotRun), a.ID+noRepository+" Here .git is a FILE, not a directory: this tree came from a linked worktree, and the gitdir it names is a host path that does not exist inside the container. That is a refusal on purpose — an index can be rebuilt, a history cannot.")
	}
	text, err := t.read("ci-matrix.toml")
	if err != nil {
		return checks.VerdictOf(a, int(checks.StateCannotRun), a.ID+": CANNOT RUN - ci-matrix.toml could not be read: "+err.Error())
	}
	matrix, err := checks.ParseMatrix(text)
	if err != nil {
		return checks.VerdictOf(a, int(checks.StateCannotRun), "::error::"+err.Error())
	}
	if stop := probeTool(ctx, a, in, "copier", "--version"); stop != nil {
		return *stop
	}
	out := tempPath("render", "")
	defer func() { _ = os.RemoveAll(out) }()

	names := make([]string, 0, len(matrix.Cases))
	problems := make(map[string][]string, len(matrix.Cases))
	for _, c := range matrix.Cases {
		names = append(names, c.Name)
		found, err := renderCase(ctx, in, filepath.Join(out, c.Name), c, matrix.Parse)
		if err != nil {
			return neverRan(a, err.Error())
		}
		problems[c.Name] = found
	}
	state, report := checks.MatrixReport(names, problems)
	return checks.VerdictOf(a, state, report)
}

// renderCase renders one case into dest and grades what came out. The error
// return is the RUN's — a render that would not start, a destination that would
// not read; everything the template did wrong is a problem in the slice.
func renderCase(ctx context.Context, in Input, dest string, c checks.MatrixCase, parse []string) ([]string, error) {
	argv, err := checks.CopierArgv(in.Root, dest, c.Answers)
	if err != nil {
		return []string{err.Error()}, nil
	}
	rendered, code := in.run(ctx, Cmd{Dir: in.Root, Name: argv[copierProgram], Args: argv[copierProgram+1:], Env: pythonEnv, Both: true})
	if code < 0 {
		return nil, errors.New(rendered)
	}
	if code != 0 {
		return []string{"copier render failed:\n    " + strings.Join(tailLines(rendered, 12), "\n    ")}, nil
	}
	files, all, err := renderedPaths(dest)
	if err != nil {
		return nil, err
	}
	problems := checks.RenderedPathProblems(files)

	// THE EXPECTATIONS MATCH DIRECTORIES TOO, and that is the point of the
	// whole-directory guards a matrix carries: `absent = [".forgejo"]` is how a
	// workflow tree nobody listed comes back noticed.
	for _, pattern := range c.Present {
		if len(globMatches(pattern, all)) == 0 {
			problems = append(problems, checks.PresentProblem(pattern))
		}
	}
	for _, pattern := range c.Absent {
		if hits := globMatches(pattern, all); len(hits) > 0 {
			problems = append(problems, checks.AbsentProblem(pattern, trimDirs(hits)))
		}
	}
	parsed, err := parseProblems(dest, parse, all)
	if err != nil {
		return nil, err
	}
	scanned, err := suppressionProblems(dest, files)
	if err != nil {
		return nil, err
	}
	return append(append(problems, parsed...), scanned...), nil
}

// renderedPaths is every file under dest and every path (a directory with its
// trailing slash), slash-separated and relative to it: the chain's Glob("**").
func renderedPaths(dest string) (files, all []string, err error) {
	err = filepath.WalkDir(dest, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Rel cannot fail for a path the walk found under dest.
		rel, _ := filepath.Rel(dest, p)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			all = append(all, filepath.ToSlash(rel)+"/")
			return nil
		}
		files = append(files, filepath.ToSlash(rel))
		all = append(all, filepath.ToSlash(rel))
		return nil
	})
	return files, all, err
}

// globRE turns a glob into a regexp the way the engine's glob reads one: `**`
// crosses directories (and `**/` may match nothing), `*` and `?` do not cross a
// slash. A matrix uses literal paths and `**/*.ext`, and nothing else.
func globRE(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if c == '*' && i+1 < len(pattern) && pattern[i+1] == '*' {
			i++
			if i+1 < len(pattern) && pattern[i+1] == '/' {
				i++
				b.WriteString(`(?:.*/)?`)
				continue
			}
			b.WriteString(`.*`)
			continue
		}
		if c == '*' {
			b.WriteString(`[^/]*`)
			continue
		}
		if c == '?' {
			b.WriteString(`[^/]`)
			continue
		}
		b.WriteString(regexp.QuoteMeta(string(c)))
	}
	b.WriteString("$")
	// The pattern is quoted but for its wildcards, so it always compiles.
	return regexp.MustCompile(b.String())
}

// globMatches is the paths a pattern names. A DIRECTORY MATCHES ITS OWN NAME
// (measured against the engine 2026-09-15: glob(".forgejo") over a tree holding
// .forgejo/workflows/ci.yml answers [".forgejo/"]), so reading the name as no
// match would pass a guard the engine fails.
func globMatches(pattern string, all []string) []string {
	re := globRE(pattern)
	var hits []string
	for _, p := range all {
		if re.MatchString(p) || (strings.HasSuffix(p, "/") && re.MatchString(strings.TrimSuffix(p, "/"))) {
			hits = append(hits, p)
		}
	}
	return hits
}

// trimDirs renders a glob's hits the way the script printed them: the paths
// themselves, a directory without its trailing separator.
func trimDirs(hits []string) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, strings.TrimSuffix(h, "/"))
	}
	return out
}

// parseProblems reads every file a `parse` glob matched and answers what would
// not parse. An unknown suffix is a problem, not a silent skip.
func parseProblems(dest string, parse, all []string) ([]string, error) {
	var problems []string
	for _, pattern := range parse {
		hits := globMatches(pattern, all)
		sort.Strings(hits)
		for _, rel := range hits {
			if strings.HasSuffix(rel, "/") {
				continue
			}
			body, err := os.ReadFile(filepath.Join(dest, rel))
			if err != nil {
				return nil, err
			}
			if problem := checks.ParseRendered(rel, string(body)); problem != "" {
				problems = append(problems, problem)
			}
		}
	}
	return problems, nil
}

// suppressionProblems scans the rendered tree with the fleet's own suppression
// scan: one definition, not a second copy of the rule.
func suppressionProblems(dest string, files []string) ([]string, error) {
	tree := map[string]string{}
	for _, rel := range files {
		if checks.LanguageOf(rel) == "" {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dest, rel))
		if err != nil {
			return nil, err
		}
		tree[rel] = string(body)
	}
	found, err := checks.ScanTree(tree)
	if err != nil {
		return nil, err
	}
	problems := make([]string, 0, len(found))
	for _, f := range found {
		problems = append(problems, checks.SuppressionProblem(f))
	}
	return problems, nil
}

// tailLines is the script's own cut of a failed render: the last n lines of what
// copier said, which is where the reason is. A CLAMP, NOT A BRANCH: the `>` and
// `>=` forms of the comparison answer identically for every input, so the
// boundary mutant of the branch form is equivalent and no test can clear it.
func tailLines(out string, n int) []string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return lines[len(lines)-min(n, len(lines)):]
}
