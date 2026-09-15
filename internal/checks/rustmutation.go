package checks

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
)

// THE RUST MUTATION GATE'S DECISIONS, in Go. They were foundry-stocks'
// ci/lib/mutation/rust.sh resolve and score phases (the workspace mapping was a
// python3 heredoc inside resolve); the atom (atoms_rust.go rustMutation) now
// runs git, cargo metadata and cargo mutants as plain execs and settles here.
//
// THERE IS NO PERCENTAGE THRESHOLD. cargo-mutants exits 2 on ANY viable
// survivor and rustc drops the unviable for free, so the exit code is the
// answer and the counts are for the reader. Every other non-zero exit — a
// baseline that did not build, a timeout, a diff that did not match the tree
// (5) — measured nothing, and is a could-not-run, never a pass.
//
// Only what the atom reached was ported. rust.sh also took a full mode, a
// workdir, a named package list and a gate switch; the atom set none of them,
// and a repo has no say in any of them (MutationScope).

// RustMutationSpecs is the pathspec a pull's diff is taken over: the declared
// critical modules when there are any, otherwise every rust source outside the
// integration tests. A plain `*` in a git pathspec crosses `/`.
func RustMutationSpecs(mods string) []string {
	if specs := strings.Fields(mods); len(specs) > 0 {
		return specs
	}
	return []string{"*.rs", ":!tests/"}
}

// RustDiffAddsLines reports whether a unified diff adds any line. A diff that
// only removes lines stands down before cargo-mutants' baseline build: it would
// find nothing to mutate ("No mutants to filter", exit 0) after spending
// 20-40s to say so. A `+++` file header is not an added line.
func RustDiffAddsLines(diff string) bool {
	for _, ln := range strings.Split(diff, "\n") {
		if len(ln) >= 2 && ln[0] == '+' && ln[1] != '+' {
			return true
		}
	}
	return false
}

// RustTouchedMembers names every workspace member a pull's files belong to,
// sorted, for cargo mutants -p.
//
// cargo-mutants at a workspace root that is also a package mutates THAT
// package and no other, as cargo does, and -D only filters the mutants it
// generated: stellar-core-rust#8 changed src/ and tools/kafka-topics-gen, the
// lane scored 69 mutants all from src/, and `-p kafka-topics-gen` over the same
// diff scored 26 more (foundry/foundry-stocks#9293). So each file is mapped to
// the member whose manifest directory holds it — the DEEPEST one, since
// tools/gen/ inside the root package's directory belongs to tools/gen.
//
// A single-package crate names nothing and runs as it always has. A file no
// member holds is compiled by none and names nothing. metadata is
// `cargo metadata --no-deps --format-version 1`; files are relative to root,
// the directory cargo ran in. Metadata that does not parse is an error, never
// a fallback to the root package: the fallback IS the bug.
func RustTouchedMembers(metadata []byte, root string, files []string) ([]string, error) {
	var meta struct {
		Packages []struct {
			Name         string `json:"name"`
			ID           string `json:"id"`
			ManifestPath string `json:"manifest_path"`
		} `json:"packages"`
		WorkspaceMembers []string `json:"workspace_members"`
	}
	if err := json.Unmarshal(metadata, &meta); err != nil {
		return nil, fmt.Errorf("cargo metadata did not parse: %w", err)
	}
	members := map[string]bool{}
	for _, id := range meta.WorkspaceMembers {
		members[id] = true
	}
	type member struct{ dir, name string }
	var dirs []member
	for _, p := range meta.Packages {
		if members[p.ID] {
			dirs = append(dirs, member{path.Dir(path.Clean(p.ManifestPath)), p.Name})
		}
	}
	if len(dirs) < 2 {
		return nil, nil
	}
	sort.SliceStable(dirs, func(i, j int) bool { return len(dirs[i].dir) > len(dirs[j].dir) })

	touched := map[string]bool{}
	for _, rel := range files {
		if rel == "" {
			continue
		}
		file := path.Join(root, rel)
		for _, d := range dirs {
			if strings.HasPrefix(file, d.dir+"/") {
				touched[d.name] = true
				break
			}
		}
	}
	names := make([]string, 0, len(touched))
	for n := range touched {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, nil
}

// RustMutationRun is what one cargo mutants run left behind: its exit, its
// output, and the four outcome lists it writes under mutants.out/ (a list it
// did not write reads as empty).
type RustMutationRun struct {
	Status                            int
	Log                               string
	Missed, Caught, Unviable, Timeout string
}

// RustMutationVerdict settles a cargo mutants run: 0 when every viable mutant
// was caught, 1 when any survived, 2 for every other exit. The reason is the
// verdict's line, then the table and the survivor and timeout lists.
func RustMutationVerdict(run RustMutationRun) (int, string) {
	missed, caught := lineCount(run.Missed), lineCount(run.Caught)
	unviable, timeout := lineCount(run.Unviable), lineCount(run.Timeout)
	viable := missed + caught
	pct := 0
	if viable > 0 {
		pct = caught * 100 / viable
	}

	var b strings.Builder
	fmt.Fprintf(&b, "### Mutation gate — rust (diff)\n\n| caught | missed | unviable | timeout | kill rate |\n|---|---|---|---|---|\n| %d | %d | %d | %d | %d%% of %d viable |\n",
		caught, missed, unviable, timeout, pct, viable)
	if missed > 0 {
		b.WriteString("\n**Survivors** — each is a HYPOTHESIS, not a finding. A mutant that lives may be a real test gap, or it may be equivalent to the original, which is undecidable in general. Read it before you write a test for it; if it is genuinely equivalent, the answer is an `exclude_re` entry in `.cargo/mutants.toml` with the reason beside it.\n\n```\n")
		b.WriteString(withNewline(run.Missed))
		b.WriteString("```\n")
	}
	if timeout > 0 {
		b.WriteString("\n**Timed out** — neither caught nor survived; the suite never answered. Usually a mutant that made a loop unbounded.\n\n```\n")
		b.WriteString(withNewline(run.Timeout))
		b.WriteString("```\n")
	}
	summary := b.String()

	if run.Status == 0 {
		return 0, fmt.Sprintf("every viable mutant was caught (%d of %d)\n\n%s", caught, viable, summary)
	}
	if run.Status == 2 {
		return 1, fmt.Sprintf("%d viable mutant(s) survived the suite — see the survivor list below\n\n%s", missed, summary)
	}
	return 2, fmt.Sprintf("CANNOT RUN - cargo mutants exited %d — a broken run, not a survivor report (a baseline that did not build, a timeout, a diff that did not match the tree): %s",
		run.Status, firstErrorLine(run.Log))
}

// lineCount is `grep -c .`: the lines that hold at least one character.
func lineCount(s string) int {
	n := 0
	for _, ln := range strings.Split(s, "\n") {
		if ln != "" {
			n++
		}
	}
	return n
}

func withNewline(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

// firstErrorLine is the first line of a run's output that says "error" in any
// case, cut to 160 characters: the cause, not its fallout.
func firstErrorLine(log string) string {
	for _, ln := range strings.Split(log, "\n") {
		if strings.Contains(strings.ToLower(ln), "error") {
			if r := []rune(ln); len(r) > 160 {
				return string(r[:160])
			}
			return ln
		}
	}
	return ""
}
