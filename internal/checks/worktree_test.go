package checks

import "testing"

func TestWorktreePrimaryReadsTheGitdirLine(t *testing.T) {
	got := WorktreePrimary("gitdir: /home/rob/Forge/Outputs/tartarus/.git/worktrees/rowan-x\n")
	if got != "/home/rob/Forge/Outputs/tartarus" {
		t.Errorf("got %q", got)
	}
	// A gitdir at the filesystem root has no primary to name: index 0 is
	// not a path, and must read as "no primary", not as "/".
	for _, bad := range []string{"", "ref: refs/heads/main", "gitdir: /some/where/else", "gitdir: .git/worktrees/x", "gitdir: /.git/worktrees/x"} {
		if got := WorktreePrimary(bad); got != "" {
			t.Errorf("%q: got %q, want empty", bad, got)
		}
	}
}
