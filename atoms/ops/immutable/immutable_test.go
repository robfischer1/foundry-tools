package opsimmutable

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atomlib/atomtest"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// run is the atom's check over an input, as the runner calls it.
func run(in atoms.Input) checks.Verdict { return check(context.Background(), in) }

func TestTheDeclarationIsTheRowItReplaced(t *testing.T) {
	atomtest.DeclaresItsRow(t, Def, "prepush", "none")
	if Def.ID != id {
		t.Errorf("%+v", Def)
	}
}

// job is a Job whose pod template differs by its cpu limit: the edit flux #206 made.
func job(cpu string) string {
	return "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: provision-3\n  namespace: foundry\n" +
		"spec:\n  template:\n    spec:\n      containers:\n      - name: c\n        resources: {limits: {cpu: " + cpu + "}}\n"
}

const configMap = "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: c, namespace: foundry}\ndata: {a: '%s'}\n"

// fluxRepo is a repository whose first commit is the base: a flux tree of two
// Kustomization directories, and a README. It answers the repository and the base.
func fluxRepo(t *testing.T) (dir, base string) {
	t.Helper()
	dir = atomtest.NewRepo(t)
	atomtest.Put(t, dir, "README.md", "one\n")
	atomtest.Put(t, dir, "flux/app/kustomization.yaml", "resources: [job.yaml]\n")
	atomtest.Put(t, dir, "flux/app/job.yaml", job("200m"))
	atomtest.Put(t, dir, "flux/other/kustomization.yaml", "resources: [cm.yaml]\n")
	atomtest.Put(t, dir, "flux/other/cm.yaml", strings.Replace(configMap, "%s", "1", 1))
	return dir, atomtest.CommitAll(t, dir, "base")
}

// kubectlFake renders a directory as the concatenation of its manifests, writing
// where -o names; a directory whose path holds a word in refuse is refused.
type kubectlFake struct {
	t      *testing.T
	mu     sync.Mutex
	calls  []atoms.Cmd
	refuse []string
	// silent are directories kubectl accepts and writes nothing for.
	silent []string
	// silentHead is the same for every directory named relative to the root: the
	// head's, where the base's is an absolute path in the worktree.
	silentHead bool
	probe      int
	start      bool // the program would not start
}

func (k *kubectlFake) exec(_ context.Context, c atoms.Cmd) (string, int) {
	k.mu.Lock()
	k.calls = append(k.calls, c)
	k.mu.Unlock()
	if c.Name != "kubectl" {
		k.t.Errorf("exec of %q", c.Name)
		return "", 0
	}
	if c.Args[0] == "version" {
		return "Client Version: v1.34", k.probe
	}
	dir, dest := c.Args[1], c.Args[3]
	if k.start {
		return "kubectl: gone", -1
	}
	for _, r := range k.refuse {
		if strings.Contains(dir, r) {
			return "error: no kustomization", 1
		}
	}
	if k.silentHead && !filepath.IsAbs(dir) {
		return "", 0
	}
	for _, r := range k.silent {
		if strings.Contains(dir, r) {
			return "", 0
		}
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(c.Dir, dir)
	}
	des, err := os.ReadDir(dir)
	if err != nil {
		return "error: " + err.Error(), 1
	}
	var out strings.Builder
	for _, de := range des {
		if de.Name() == "kustomization.yaml" {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(dir, de.Name()))
		out.WriteString(string(b) + "---\n")
	}
	if err := os.WriteFile(dest, []byte(out.String()), 0o644); err != nil {
		k.t.Error(err)
	}
	return "", 0
}

// immutableIn is the input the binary would read over the repository.
func immutableIn(t *testing.T, dir, base string, k *kubectlFake) atoms.Input {
	t.Helper()
	in := atoms.Collect(context.Background(), dir, base, "", time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC))
	in.Exec = k.exec
	return in
}

func worktrees(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	for _, ln := range strings.Split(atomtest.GitIn(t, dir, "worktree", "list", "--porcelain"), "\n") {
		if p, ok := strings.CutPrefix(ln, "worktree "); ok {
			out = append(out, p)
		}
	}
	return out
}

func TestOpsImmutable(t *testing.T) {
	t.Run("an edit to a Job's pod template is a finding, read against the merge base", func(t *testing.T) {
		dir, base := fluxRepo(t)
		atomtest.Put(t, dir, "flux/app/job.yaml", job("1"))
		atomtest.CommitAll(t, dir, "raise the limit")
		k := &kubectlFake{t: t}
		v := run(immutableIn(t, dir, base, k))
		atomtest.Expect(t, v, atomtest.StateOf(1), atomtest.Findings, "ops:immutable: FINDINGS", "flux/app: Job/foundry/provision-3: spec.template", "force: enabled")
		if strings.Contains(v.Reason, "flux/other") {
			t.Errorf("a path whose inputs did not change was compared:\n%s", v.Reason)
		}
		var rendered []string
		for _, c := range k.calls[1:] {
			if c.Dir != dir {
				t.Errorf("kubectl ran in %q", c.Dir)
			}
			rendered = append(rendered, c.Args[1])
		}
		if len(rendered) != 2 || rendered[0] != "flux/app" || !strings.HasSuffix(rendered[1], "/flux/app") || rendered[1] == "flux/app" {
			t.Errorf("rendered %v: want the head's directory, then the base worktree's", rendered)
		}
		if got := worktrees(t, dir); len(got) != 1 {
			t.Errorf("the base worktree was left registered: %v", got)
		}
		if _, err := os.Stat(rendered[1]); err == nil {
			t.Errorf("the base worktree directory %s was left behind", rendered[1])
		}
	})
	t.Run("an edit elsewhere in a touched path passes, and says what it compared", func(t *testing.T) {
		dir, base := fluxRepo(t)
		atomtest.Put(t, dir, "flux/other/cm.yaml", strings.Replace(configMap, "%s", "2", 1))
		atomtest.CommitAll(t, dir, "change data")
		v := run(immutableIn(t, dir, base, &kubectlFake{t: t}))
		atomtest.Expect(t, v, atomtest.StateOf(0), atomtest.Pass, "PASS - 1 path(s) compared against "+base+", the 1 of 2 whose inputs changed")
	})
	t.Run("a deletion counts as a change to a path's inputs", func(t *testing.T) {
		dir, base := fluxRepo(t)
		if err := os.Remove(filepath.Join(dir, "flux/app/job.yaml")); err != nil {
			t.Fatal(err)
		}
		atomtest.CommitAll(t, dir, "drop the job")
		atomtest.Expect(t, run(immutableIn(t, dir, base, &kubectlFake{t: t})), atomtest.StateOf(0), atomtest.Pass, "PASS - 1 path(s) compared")
	})
	t.Run("a pull that touches no Flux path renders nothing", func(t *testing.T) {
		dir, base := fluxRepo(t)
		atomtest.Put(t, dir, "README.md", "two\n")
		atomtest.CommitAll(t, dir, "docs")
		k := &kubectlFake{t: t}
		atomtest.Expect(t, run(immutableIn(t, dir, base, k)), atomtest.StateOf(0), atomtest.Pass, "no Flux path's inputs changed against "+base+"; 2 path(s), nothing to compare")
		if len(k.calls) != 1 {
			t.Errorf("kubectl rendered with nothing to compare: %d calls", len(k.calls))
		}
	})
	t.Run("the merge base, not the tip of the base, is what is compared", func(t *testing.T) {
		dir, fork := fluxRepo(t)
		atomtest.GitIn(t, dir, "checkout", "-q", "-b", "pull")
		atomtest.Put(t, dir, "README.md", "pull\n")
		atomtest.CommitAll(t, dir, "pull docs")
		atomtest.GitIn(t, dir, "checkout", "-q", "main")
		atomtest.Put(t, dir, "flux/app/job.yaml", job("1")) // main moved on after the pull branched
		mainTip := atomtest.CommitAll(t, dir, "landing")
		atomtest.GitIn(t, dir, "checkout", "-q", "pull")
		k := &kubectlFake{t: t}
		v := run(immutableIn(t, dir, mainTip, k))
		atomtest.Expect(t, v, atomtest.StateOf(0), atomtest.Pass, "against "+fork+"; 2 path(s), nothing to compare")
	})
	t.Run("a head that does not build, and a base that never did, are noted and skipped", func(t *testing.T) {
		dir, base := fluxRepo(t)
		atomtest.Put(t, dir, "flux/app/job.yaml", job("1"))
		atomtest.Put(t, dir, "flux/other/cm.yaml", strings.Replace(configMap, "%s", "2", 1))
		atomtest.CommitAll(t, dir, "both")
		for _, tc := range []struct{ refuse, note string }{
			{"flux/app", "flux/app: head does not build (ops:flux reports it)"},
			{"/flux/other", "flux/other: no buildable base (new or broken there), skipped"},
		} {
			k := &kubectlFake{t: t, refuse: []string{tc.refuse}}
			v := run(immutableIn(t, dir, base, k))
			if tc.refuse == "flux/app" {
				atomtest.Expect(t, v, atomtest.StateOf(0), atomtest.Pass, tc.note)
				continue
			}
			atomtest.Expect(t, v, atomtest.StateOf(1), atomtest.Findings, tc.note, "flux/app: Job/foundry/provision-3: spec.template")
		}
	})
	t.Run("the states that are not a verdict about the tree", func(t *testing.T) {
		dir, base := fluxRepo(t)
		atomtest.Put(t, dir, "flux/app/job.yaml", job("1"))
		atomtest.CommitAll(t, dir, "raise")
		for _, tc := range []struct {
			name    string
			mutate  func(in *atoms.Input, k *kubectlFake)
			state   int
			result  string
			needles []string
		}{
			{"no base is absent", func(in *atoms.Input, _ *kubectlFake) { in.Base = "" }, 0, atomtest.Absent, []string{"no base to compare against"}},
			{"no Flux tree is absent", func(in *atoms.Input, _ *kubectlFake) { in.Files = []string{"main.go"} }, 0, atomtest.Absent, []string{"no Flux tree here"}},
			{"a Flux tree with nothing to build is absent", func(in *atoms.Input, _ *kubectlFake) { in.Files = []string{"flux/README.md"} }, 0, atomtest.Absent, []string{"carries no Kustomization CR and no kustomization.yaml"}},
			{"a population that failed", func(in *atoms.Input, _ *kubectlFake) { in.FilesErr = atomtest.ErrBoom }, 2, atomtest.Cannot, []string{"the tree would not enumerate"}},
			{"a base the history does not reach", func(in *atoms.Input, _ *kubectlFake) { in.Base = strings.Repeat("a", 40) }, 2, atomtest.Cannot, []string{"CANNOT RUN - the base " + strings.Repeat("a", 40) + " is not in this history"}},
			{"a kubectl that fails its probe", func(_ *atoms.Input, k *kubectlFake) { k.probe = 127 }, 2, atomtest.Cannot, []string{"kubectl could not be provisioned: exited 127"}},
			{"a kubectl that would not start", func(_ *atoms.Input, k *kubectlFake) { k.start = true }, 2, atomtest.Cannot, []string{"CANNOT RUN - the atom never ran: kubectl: gone"}},
			{"a manifest of a cluster that will not read", func(in *atoms.Input, _ *kubectlFake) {
				in.Files = append(in.Files, "flux/clusters/c/ks.yaml")
				in.Committable = in.Files
			}, 2, atomtest.Cannot, []string{"flux/clusters/c/ks.yaml could not be read"}},
			{"a kustomization.yaml that will not read", func(in *atoms.Input, _ *kubectlFake) {
				if err := os.Remove(filepath.Join(dir, "flux/app/kustomization.yaml")); err != nil {
					t.Fatal(err)
				}
			}, 2, atomtest.Cannot, []string{"flux/app/kustomization.yaml could not be read"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				k := &kubectlFake{t: t}
				in := immutableIn(t, dir, base, k)
				tc.mutate(&in, k)
				atomtest.Expect(t, run(in), atomtest.StateOf(tc.state), tc.result, tc.needles...)
				if tc.name == "a kustomization.yaml that will not read" {
					atomtest.GitIn(t, dir, "checkout", "--", "flux/app/kustomization.yaml")
				}
			})
		}
	})
	t.Run("a history that lost its objects is a 2", func(t *testing.T) {
		dir, base := fluxRepo(t)
		atomtest.Put(t, dir, "flux/app/job.yaml", job("1"))
		head := atomtest.CommitAll(t, dir, "raise")
		in := immutableIn(t, dir, base, &kubectlFake{t: t})
		// A repository whose objects went missing after the merge base resolved.
		objs := filepath.Join(dir, ".git", "objects", head[:2])
		if err := os.Rename(objs, objs+".gone"); err != nil {
			t.Fatal(err)
		}
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "CANNOT RUN")
	})
}

func TestOpsImmutableHoldsTheWorktreesItMakesToOne(t *testing.T) {
	dir, base := fluxRepo(t)
	atomtest.Put(t, dir, "flux/app/job.yaml", job("1"))
	atomtest.CommitAll(t, dir, "raise")
	for range 2 {
		run(immutableIn(t, dir, base, &kubectlFake{t: t}))
	}
	got := worktrees(t, dir)
	sort.Strings(got)
	if len(got) != 1 {
		t.Errorf("worktrees after two runs: %v", got)
	}
}

// The failures of the pieces the atom leans on, each in the step that meets it.
func TestOpsImmutableNamesWhereItStopped(t *testing.T) {
	raised := func(t *testing.T) (dir, base string) {
		dir, base = fluxRepo(t)
		atomtest.Put(t, dir, "flux/app/job.yaml", job("1"))
		atomtest.CommitAll(t, dir, "raise")
		return dir, base
	}
	// dropObject removes one loose object, as a repository that lost it would.
	dropObject := func(t *testing.T, dir, sha string) {
		t.Helper()
		if err := os.Remove(filepath.Join(dir, ".git", "objects", sha[:2], sha[2:])); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("a tree git cannot read makes the change list a 2", func(t *testing.T) {
		dir, base := raised(t)
		in := immutableIn(t, dir, base, &kubectlFake{t: t})
		dropObject(t, dir, atomtest.GitIn(t, dir, "rev-parse", "HEAD^{tree}"))
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "the changed files against "+base+" would not list (exit ")
	})
	t.Run("a base that cannot be checked out is a 2", func(t *testing.T) {
		dir, base := raised(t)
		in := immutableIn(t, dir, base, &kubectlFake{t: t})
		dropObject(t, dir, atomtest.GitIn(t, dir, "rev-parse", base+":flux/app/job.yaml"))
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "the base "+base+" could not be checked out (exit ")
		if got := worktrees(t, dir); len(got) != 1 {
			t.Errorf("a failed checkout left worktrees: %v", got)
		}
	})
	t.Run("a head kubectl accepted and wrote nothing for is a 2, not a pass", func(t *testing.T) {
		dir, base := raised(t)
		in := immutableIn(t, dir, base, &kubectlFake{t: t, silentHead: true})
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "CANNOT RUN - open ")
	})
	t.Run("a base kubectl accepted and wrote nothing for is a 2, not a pass", func(t *testing.T) {
		dir, base := raised(t)
		// Only the absolute path is the base's.
		k := &kubectlFake{t: t, silent: []string{string(filepath.Separator) + "flux/app"}}
		atomtest.Expect(t, run(immutableIn(t, dir, base, k)), atomtest.StateOf(2), atomtest.Cannot, "CANNOT RUN - open ")
	})
	t.Run("a stream that is not YAML names the path", func(t *testing.T) {
		dir, base := fluxRepo(t)
		atomtest.Put(t, dir, "flux/app/job.yaml", "a: [\n")
		atomtest.CommitAll(t, dir, "broken")
		atomtest.Expect(t, run(immutableIn(t, dir, base, &kubectlFake{t: t})), atomtest.StateOf(2), atomtest.Cannot, "CANNOT RUN - flux/app: ")
	})
	t.Run("the paths come from the Kustomization CRs when the tree has them", func(t *testing.T) {
		dir, _ := fluxRepo(t)
		atomtest.Put(t, dir, "flux/clusters/c/ks.yaml", "apiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nspec:\n  path: ./flux/app\n")
		base := atomtest.CommitAll(t, dir, "a CR")
		atomtest.Put(t, dir, "README.md", "two\n")
		atomtest.CommitAll(t, dir, "docs")
		atomtest.Expect(t, run(immutableIn(t, dir, base, &kubectlFake{t: t})), atomtest.StateOf(0), atomtest.Pass, "1 path(s), nothing to compare")
	})
	t.Run("a directory a kustomization reaches into that has none of its own is still an input", func(t *testing.T) {
		dir, _ := fluxRepo(t)
		atomtest.Put(t, dir, "flux/app/kustomization.yaml", "resources: [job.yaml, ../shared]\n")
		atomtest.Put(t, dir, "flux/shared/x.yaml", "a: 1\n")
		base := atomtest.CommitAll(t, dir, "shared")
		atomtest.Put(t, dir, "flux/shared/x.yaml", "a: 2\n")
		atomtest.CommitAll(t, dir, "edit shared")
		atomtest.Expect(t, run(immutableIn(t, dir, base, &kubectlFake{t: t})), atomtest.StateOf(0), atomtest.Pass, "PASS - 1 path(s) compared against "+base)
	})
}
