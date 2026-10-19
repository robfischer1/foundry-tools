package diesrenderauthor

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/atomtest"
	"dagger/foundry-tools/internal/checks"
)

// The Def carries the identity the catalogue row will, and validates.
func TestTheDeclarationValidates(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrepush || Def.Lane != checks.LaneAny || Def.Group != checks.GroupOf(id) ||
		Def.Toolchain != "none" || Def.Surface != "any:**" || len(Def.Needs) != 0 {
		t.Errorf("%+v", Def)
	}
}

// gitIn runs git in dir with env added and answers its trimmed output; a failure is fatal.
func gitIn(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// as is the environment that makes a commit's author and committer.
func as(author, committer string) []string {
	return []string{"GIT_AUTHOR_NAME=a", "GIT_AUTHOR_EMAIL=" + author, "GIT_COMMITTER_NAME=c", "GIT_COMMITTER_EMAIL=" + committer}
}

const human = "rob@example.invalid"

// commit is one commit of the pull: the files it writes and who makes it.
type commit struct {
	files             map[string]string
	author, committer string
}

// repo is a die tree whose main is one commit, with the pull's commits on top;
// it answers the input at the pull's tip and the branch point.
func repo(t *testing.T, pull ...commit) (atomkit.In, string) {
	t.Helper()
	in := atomtest.TreeIn(t, atomtest.DieTree(map[string]string{Rendered[0]: "{}", Rendered[1]: "{}"}))
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	gitIn(t, in.Root, nil, "init", "-q", "-b", "main")
	gitIn(t, in.Root, nil, "add", "-A")
	gitIn(t, in.Root, as(human, human), "commit", "-q", "-m", "main")
	base := gitIn(t, in.Root, nil, "rev-parse", "HEAD")
	for i, c := range pull {
		for p, body := range c.files {
			atomtest.Put(t, in.Root, p, body)
		}
		gitIn(t, in.Root, nil, "add", "-A")
		gitIn(t, in.Root, as(c.author, c.committer), "commit", "-q", "--allow-empty", "-m", "pull "+string(rune('a'+i)))
	}
	return in, base
}

var render = map[string]string{Rendered[0]: `{"tx": 2}`, Rendered[1]: `{"tx": 2}`}

func TestARenderPullChangesOnlyTheRenderedDocuments(t *testing.T) {
	for _, c := range []struct {
		name   string
		pull   []commit
		state  checks.State
		result string
		words  []string
	}{
		{"a pull with no themis commit is not a render", []commit{{map[string]string{"policy/x.rego": "package x"}, human, human}},
			checks.StatePass, atomtest.Pass, []string{"not a render"}},
		{"a themis render of the two documents holds", []commit{{render, Themis, Themis}},
			checks.StatePass, atomtest.Pass, []string{"only the rendered grant documents"}},
		{"a themis commit that writes anything else is refused", []commit{{map[string]string{Rendered[0]: `{"tx": 3}`, "policy/git_edge/git_edge.rego": "package git_edge"}, Themis, Themis}},
			checks.StateFindings, atomtest.Findings, []string{"policy/git_edge/git_edge.rego"}},
		{"another commit riding a render pull is refused", []commit{{render, Themis, Themis}, {map[string]string{"fleet/repos/x.json": "{}"}, human, human}},
			checks.StateFindings, atomtest.Findings, []string{"fleet/repos/x.json"}},
		{"themis as committer alone makes it a render", []commit{{map[string]string{"README.md": "x"}, human, "THEMIS@pantheon.foundry"}},
			checks.StateFindings, atomtest.Findings, []string{"README.md"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			in, base := repo(t, c.pull...)
			in.Base = base
			atomtest.Expect(t, check(context.Background(), in), c.state, c.result, c.words...)
		})
	}
}

// With no base the tip is read against its parent.
func TestTheTipWithNoBaseIsReadAgainstItsParent(t *testing.T) {
	in, _ := repo(t, commit{map[string]string{"README.md": "x"}, human, human}, commit{render, Themis, Themis})
	atomtest.Expect(t, check(context.Background(), in), checks.StatePass, atomtest.Pass, "HEAD~1")
}

// A base the history does not reach is a could-not-run, not a pass.
func TestABaseOutsideTheHistoryCannotRun(t *testing.T) {
	in, _ := repo(t, commit{render, Themis, Themis})
	in.Base = "0000000000000000000000000000000000000000"
	atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, "not in this history")
}

// A git that will not list the commits or the paths is a could-not-run.
func TestAGitThatWillNotListCannotRun(t *testing.T) {
	for _, c := range []struct{ name, fails, words string }{
		{"the commits", "log", "commits since"},
		{"the paths", "diff", "paths changed since"},
	} {
		t.Run(c.name, func(t *testing.T) {
			in, _ := atomtest.ToolTree(t, atomtest.DieTree(nil), func(cmd atoms.Cmd) (string, int) {
				switch cmd.Args[2] {
				case c.fails:
					return "fatal: broken", 128
				case "log":
					return Themis + "\x00" + Themis + "\n", 0
				}
				return "", 0
			})
			atomtest.Expect(t, check(context.Background(), in), checks.StateCannotRun, atomtest.Cannot, c.words, "broken")
		})
	}
}

// A tree that is not the policy die's source is absent.
func TestATreeThatIsNotTheDieIsAbsent(t *testing.T) {
	in := atomtest.TreeIn(t, map[string]string{"README.md": "x"})
	atomtest.Expect(t, check(context.Background(), in), checks.StatePass, "absent", "not the policy die")
}
