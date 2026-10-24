package atoms

import (
	"context"
	"fmt"
	"os"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

// KubeLintPlan is what ops:kube-linter runs: the targets, and the derived
// config to run them under ("" runs under the tree's own). Scoped is true when
// Targets are fewer than the full set asked for, and Note says why or why not.
// Skip is "nothing to lint".
type KubeLintPlan struct {
	Targets []string
	Config  string
	Note    string
	Skip    bool
}

// PlanKubeLinter narrows the targets a pull lints (checks.ScopeKubeLinter has
// the rule). Anything it cannot establish - no base, a base the history does
// not reach, a diff or file that will not read, a config that will not derive -
// answers the full set: it fails toward more lint, never toward less. The caller
// removes Config when it is not "".
func PlanKubeLinter(ctx context.Context, in Input, targets []string) KubeLintPlan {
	full := func(why string) KubeLintPlan { return KubeLintPlan{Targets: targets, Note: "full tree: " + why} }
	mb, why := scopeBase(ctx, in)
	if mb == "" {
		return full(why)
	}
	out, code := in.run(ctx, Cmd{Dir: in.Root, Name: "git", Args: []string{"diff", "-z", "--name-status", "--no-renames", mb + "..HEAD"}})
	if code != 0 {
		return full(fmt.Sprintf("the change set did not compute (git exit %d)", code))
	}
	changes, ok := checks.ParseKubeNameStatus(out)
	if !ok {
		return full("the change set did not parse")
	}
	t := in.Tree()
	scope := checks.ScopeKubeLinter(checks.KubeScopeIn{
		Targets: targets, Files: in.Files, Changes: changes, Head: t.Read,
		Base: func(p string) (string, error) {
			out, code := in.run(ctx, Cmd{Dir: in.Root, Name: "git", Args: []string{"show", mb + ":" + p}})
			if code != 0 {
				return "", fmt.Errorf("git show %s:%s exited %d", mb, p, code)
			}
			return out, nil
		},
	})
	if scope.Full {
		return full(scope.Why)
	}
	if len(scope.Targets) == 0 {
		return KubeLintPlan{Skip: true, Note: "nothing to lint: no changed file is read by a lint target"}
	}
	cfg, err := derivedKubeConfig(t)
	if err != nil {
		return full(err.Error())
	}
	return KubeLintPlan{Targets: scope.Targets, Config: cfg, Note: "scoped to the targets that build the changed pins"}
}

// scopeBase is the commit the change is measured from, or "" and why not.
//
// A pull's or candidate's gate names a base, and the change is measured from
// its merge base (ChangeBase). A TIP gate - the door's run on main after a
// landing - names none, and it is where the cost is: a pin bump lands with no
// gate of its own, so this is the only lint it gets. A landing is a two-parent
// merge, so the tip's change is its diff against the first parent. Anything
// that is not a merge (a root commit, a fast-forward, a hook's snapshot) has
// no such change and keeps the full tree.
func scopeBase(ctx context.Context, in Input) (string, string) {
	if in.Base != "" {
		mb, err := ChangeBase(ctx, in, in.Root)
		if err != nil || mb == "" {
			return "", "the merge base could not be established"
		}
		return mb, ""
	}
	if _, code := in.run(ctx, Cmd{Dir: in.Root, Name: "git", Args: []string{"rev-parse", "--verify", "--quiet", "HEAD^2"}}); code != 0 {
		return "", "no base to measure the change from"
	}
	out, code := in.run(ctx, Cmd{Dir: in.Root, Name: "git", Args: []string{"rev-parse", "--verify", "--quiet", "HEAD^1"}})
	if p := strings.TrimSpace(out); code == 0 && p != "" {
		return p, ""
	}
	return "", "the merge's first parent could not be resolved"
}

// derivedKubeConfig writes the tree's kube-linter config with the cross-object
// checks excluded, and answers its path.
func derivedKubeConfig(t Tree) (string, error) {
	src, err := t.Read(".kube-linter.yaml")
	if err != nil {
		src, _ = t.Read(".kube-linter.yml") // neither: the defaults, less the excluded checks
	}
	derived, err := checks.KubeLinterConfigWith(src, checks.KubeLinterCrossObject)
	if err != nil {
		return "", err
	}
	p := TempPath("kube-linter", ".yaml")
	if err := os.WriteFile(p, []byte(derived), 0o600); err != nil {
		return "", fmt.Errorf("the derived kube-linter config could not be written: %w", err)
	}
	return p, nil
}
