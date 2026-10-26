// Package fleetdaedalusjobsconfig is the atom fleet:daedalus-jobs-config: flux's
// prime/daedalus-jobs.yaml parses under the daedalus build that flux deploys. One
// file holds the atom and both runners execute it.
package fleetdaedalusjobsconfig

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom. The checker is daedalus' own, built by the lane's go. Surface and
// Toolchain are string literals on purpose: daedalus reads a Def with go/ast and
// understands nothing else (catalogue/literal_surface_test.go).
var Def = atomkit.Def{
	ID: "fleet:daedalus-jobs-config", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupBasic,
	Desc:      "prime/daedalus-jobs.yaml parses under daedalus' own settings parser, built at the daedalus commit prime/images/kustomization.yaml deploys, or it prints the parser's refusal (flux#5748: REFPOD_LANES without REFPOD_PEER_ID kept the runner off and stalled fleet CI).",
	Surface:   "any:prime/daedalus-jobs*.yaml",
	Toolchain: "go",
	Check:     check,
}

const (
	id    = "fleet:daedalus-jobs-config"
	jobs  = "prime/daedalus-jobs.yaml"
	pins  = "prime/images/kustomization.yaml"
	image = "registry.notusmi.com/rob/daedalus"
	// BG1: the green colour's ConfigMap is parsed by the build ITS pin names.
	jobsGreen  = "prime/daedalus-jobs-green.yaml"
	imageGreen = "registry.notusmi.com/rob/daedalus-green"
	// pkg is daedalus' checker: internal/config's parser behind a main.
	pkg = "git.notusmi.com/rob/daedalus/cmd/jobs-config-check"
	// noPackage is go's word for a module version that predates the checker.
	noPackage = "does not contain package"
	// module is the module pkg lives in: the one path this install fetches direct.
	module = "git.notusmi.com/rob/daedalus"
)

// installEnv is the environment of the one `go install`. GONOPROXY=module sends
// the daedalus module straight to the forge and leaves every other module on the
// lane's GOPROXY.
//
// THE PIN IS A COMMIT, AND NO PROXY ON THE LANE CAN ANSWER ONE. The door's goproxy
// serves branches and tags only (`@v/2708758.info` and the full sha both 404 "is
// not a branch or tag", 2026-10-26), so `door|proxy.golang.org,direct` asked
// proxy.golang.org five times per run for a module it can never hold (it cannot
// resolve git.notusmi.com) before reaching `direct`. proxy.golang.org timed out 3
// times in 7 days and one of those held flux#6921's gate. MEASURED 2026-10-26 on
// dev01, from a pod carrying pistis.ci/executor=atomrun (the executor-egress
// policy) with the gate's DNS and fleet trust, with a warm module cache: the lane's
// env made 5 proxy.golang.org gets for daedalus@2708758; with this GONOPROXY it made
// 0 to proxy.golang.org and 0 to the door. The forge answered go-get in 16 ms and
// `git ls-remote` in 56 ms, and the checker built. Inside the cluster
// git.notusmi.com is the in-cluster forge, not the SSO portal that checks.GoPrivate
// guards against. A cold cache still fetches public deps through the lane's GOPROXY.
//
// ONE MODULE, NOT THE HOST. stellar-core-go and the other fleet modules daedalus
// needs are pinned at tags, which the door serves, so they keep the lane's path.
// GOPRIVATE stays the lane's empty value. This setting reaches nothing but this
// command.
func installEnv(bin string) []string {
	return []string{"GOBIN=" + bin, "GOFLAGS=-mod=mod", "GOTOOLCHAIN=local", "GONOPROXY=" + module}
}

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrecommit, Lane: checks.LaneAny}

// tagSHA is the commit in an image tag: <unix-ts>-<sha>.
var tagSHA = regexp.MustCompile(`^\d+-([0-9a-f]{7,40})$`)

// check builds daedalus' checker at the commit flux deploys and runs it over the
// ConfigMap.
//
// THE PARSER IS THE CODE THAT BOOTS, NEVER A COPY. The image tag in
// prime/images/kustomization.yaml ends in the daedalus commit the image was built
// from, and `go install <pkg>@<commit>` builds the checker from exactly that
// source, so a PR that moves the image and the settings together is judged by the
// new parser, and one that moves only the settings by the deployed one.
//
// INSTALL AND RUN ARE TWO STEPS. `go run` exits 1 for a build or fetch failure as
// it does for the program's own 1, and an atom that reads 1 as a finding turns a
// proxy's bad hour into a red the sweep will not re-ask (foundry-tools#29, as
// Provision words it). The install fails as could-not-run; only the checker's 1 is
// a finding, and its 2 (could not look) is could-not-run again.
//
// A BUILD THAT PREDATES THE CHECKER IS ABSENT, not red: rolling the image back to
// before the checker existed must not need the check that the rollback removes.
func check(ctx context.Context, in atomkit.In) checks.Verdict {
	// The index asks this Check over an empty tree for the sentence it carries as
	// its absent reason, so the answer over a tree without the ConfigMap is it.
	if _, err := os.Stat(filepath.Join(in.Root, jobs)); err != nil {
		return checks.VerdictOf(row, int(checks.StatePass), id+": ABSENT - this tree carries no "+jobs+", so it holds no runner settings to parse")
	}
	v := checkFile(ctx, in, jobs, image)
	if v.State != int(checks.StatePass) {
		return v
	}
	// BG1: the green colour's map, when the tree has one, is judged by the build the
	// green pin names (the colours do not run the same build while one is the dev slot).
	if _, err := os.Stat(filepath.Join(in.Root, jobsGreen)); err != nil {
		return v
	}
	g := checkFile(ctx, in, jobsGreen, imageGreen)
	if g.State != int(checks.StatePass) {
		return g
	}
	// Both pass. A colour whose build predates the checker is absent, and says nothing
	// to join; if both are, the answer is the green one's.
	var said []string
	for _, p := range []checks.Verdict{v, g} {
		if p.Result != "absent" {
			said = append(said, strings.Join(p.Logs, "\n"))
		}
	}
	if len(said) == 0 {
		return g
	}
	return checks.VerdictOf(row, int(checks.StatePass), strings.Join(said, "\n"))
}

// checkFile parses one colour's ConfigMap under the build its image pin names.
func checkFile(ctx context.Context, in atomkit.In, file, img string) checks.Verdict {
	sha, why := deployedCommit(in.Root, img)
	if why != "" {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - "+why)
	}
	bin, err := os.MkdirTemp(atoms.WorkDir(), "jobs-config-check-")
	if err != nil {
		return atoms.CouldNotStart(row, err.Error())
	}
	defer func() { _ = os.RemoveAll(bin) }()
	out, code := atoms.RunCmd(ctx, in, atoms.Cmd{Dir: in.Root, Name: "go", Args: []string{"install", pkg + "@" + sha},
		Env: installEnv(bin), Both: true})
	switch {
	case code < 0:
		return atoms.CouldNotStart(row, out)
	case code != 0 && strings.Contains(out, noPackage):
		return checks.VerdictOf(row, int(checks.StatePass), id+": ABSENT - daedalus@"+sha+" predates cmd/jobs-config-check, so there is no parser to run over "+file)
	case code != 0:
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - `go install "+pkg+"@"+sha+"` exited "+strconv.Itoa(code)+": "+atoms.LastLine(out))
	}
	out, code = atoms.RunCmd(ctx, in, atoms.Cmd{Dir: in.Root, Name: filepath.Join(bin, "jobs-config-check"), Args: []string{file}, Both: true})
	switch {
	case code < 0:
		return atoms.CouldNotStart(row, out)
	case code == 1:
		return checks.VerdictOf(row, int(checks.StateFindings), id+": FINDINGS - daedalus@"+sha+" refuses "+file+"\n"+out)
	case code != 0:
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - jobs-config-check exited "+strconv.Itoa(code)+": "+out)
	}
	return checks.VerdictOf(row, int(checks.StatePass), id+": "+out)
}

// deployedCommit is the daedalus commit the tree's image pin names. why is the
// reason it could not be read, "" when sha is good.
func deployedCommit(root, image string) (sha, why string) {
	body, err := os.ReadFile(filepath.Join(root, pins))
	if err != nil {
		return "", "could not read " + pins + ": " + err.Error()
	}
	ours := false
	for _, line := range strings.Split(string(body), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "- name:"):
			ours = strings.TrimSpace(strings.TrimPrefix(t, "- name:")) == image
		case ours && strings.HasPrefix(t, "newTag:"):
			tag, _, _ := strings.Cut(strings.TrimSpace(strings.TrimPrefix(t, "newTag:")), " ")
			m := tagSHA.FindStringSubmatch(tag)
			if m == nil {
				return "", pins + " pins " + image + " at " + tag + ", which is not <unix-ts>-<sha>"
			}
			return m[1], ""
		}
	}
	return "", pins + " pins no " + image
}
