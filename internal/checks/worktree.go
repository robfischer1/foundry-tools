package checks

import "strings"

// WorktreePrimary reads a linked worktree's `.git` FILE — one line,
// `gitdir: <primary>/.git/worktrees/<name>` — and answers the primary
// checkout's path, or "" when the content is not that shape. The caller
// reconstructs `origin` from it so a repository-keyed exemption survives a
// push made from a worktree.
func WorktreePrimary(gitdirFile string) string {
	line := strings.TrimSpace(gitdirFile)
	if !strings.HasPrefix(line, "gitdir:") {
		return ""
	}
	path := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	primary, _, found := strings.Cut(path, "/.git/worktrees/")
	if !found || primary == "" {
		// Not a worktree's gitdir, or one rooted at "/" — neither names a
		// primary checkout an origin could be built from.
		return ""
	}
	return primary
}
