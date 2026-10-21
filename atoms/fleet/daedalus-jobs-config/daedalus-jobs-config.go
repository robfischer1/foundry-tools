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

// Def is the atom. The checker is daedalus' own, built by the lane's go.
var Def = atomkit.Def{
	ID: "fleet:daedalus-jobs-config", Stage: checks.StagePrecommit, Lane: checks.LaneAny,
	Group:     checks.GroupBasic,
	Desc:      "prime/daedalus-jobs.yaml parses under daedalus' own settings parser, built at the daedalus commit prime/images/kustomization.yaml deploys, or it prints the parser's refusal (flux#5748: REFPOD_LANES without REFPOD_PEER_ID kept the runner off and stalled fleet CI).",
	Surface:   "any:" + jobs,
	Toolchain: "go",
	Check:     check,
}

const (
	id    = "fleet:daedalus-jobs-config"
	jobs  = "prime/daedalus-jobs.yaml"
	pins  = "prime/images/kustomization.yaml"
	image = "registry.notusmi.com/rob/daedalus"
	// pkg is daedalus' checker: internal/config's parser behind a main.
	pkg = "git.notusmi.com/rob/daedalus/cmd/jobs-config-check"
	// noPackage is go's word for a module version that predates the checker.
	noPackage = "does not contain package"
)

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
	sha, why := deployedCommit(in.Root)
	if why != "" {
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - "+why)
	}
	bin, err := os.MkdirTemp(atoms.WorkDir(), "jobs-config-check-")
	if err != nil {
		return atoms.CouldNotStart(row, err.Error())
	}
	defer func() { _ = os.RemoveAll(bin) }()
	out, code := atoms.RunCmd(ctx, in, atoms.Cmd{Dir: in.Root, Name: "go", Args: []string{"install", pkg + "@" + sha},
		Env: []string{"GOBIN=" + bin, "GOFLAGS=-mod=mod", "GOTOOLCHAIN=local"}, Both: true})
	switch {
	case code < 0:
		return atoms.CouldNotStart(row, out)
	case code != 0 && strings.Contains(out, noPackage):
		return checks.VerdictOf(row, int(checks.StatePass), id+": ABSENT - daedalus@"+sha+" predates cmd/jobs-config-check, so there is no parser to run over "+jobs)
	case code != 0:
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - `go install "+pkg+"@"+sha+"` exited "+strconv.Itoa(code)+": "+atoms.LastLine(out))
	}
	out, code = atoms.RunCmd(ctx, in, atoms.Cmd{Dir: in.Root, Name: filepath.Join(bin, "jobs-config-check"), Args: []string{jobs}, Both: true})
	switch {
	case code < 0:
		return atoms.CouldNotStart(row, out)
	case code == 1:
		return checks.VerdictOf(row, int(checks.StateFindings), id+": FINDINGS - daedalus@"+sha+" refuses "+jobs+"\n"+out)
	case code != 0:
		return checks.VerdictOf(row, int(checks.StateCannotRun), id+": CANNOT RUN - jobs-config-check exited "+strconv.Itoa(code)+": "+out)
	}
	return checks.VerdictOf(row, int(checks.StatePass), id+": "+out)
}

// deployedCommit is the daedalus commit the tree's image pin names. why is the
// reason it could not be read, "" when sha is good.
func deployedCommit(root string) (sha, why string) {
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
