package atoms

import (
	"context"
	"fmt"
	"os"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

// THE FLEET ATOMS THAT EXEC A SCANNER. Each is the port of the chain of the
// same id in atoms_fleet.go: the same surface checks, the same argv, the same
// exit mapping, the same sentences. What changed is only where the program
// comes from (PATH, not a container the chain built).

// opengrepRefusal is the zero-file refusal, kept as the chain wrote it.
const opengrepRefusal = "opengrep: REFUSING a zero-file scan. The ruleset matched no file in this repo, so 0 findings means NOTHING WAS EXAMINED - not that the code is clean. Check that rules/sast declares the language(s) this repo is actually written in."

// opengrepEnv is the tool's locale: opengrep decodes source files by it, and a
// process with none set reads UTF-8 bytes as ASCII.
var opengrepEnv = []string{"LANG=C.UTF-8", "LC_ALL=C.UTF-8"}

// fleetOpengrepSast: the SAST scan, which refuses a zero-file scan.
//
// THE REFUSAL IS THE POINT. "Ran N rules on 0 files: 0 findings" exits 0 and
// renders as Passed, indistinguishable from a clean scan; checks.OpengrepZeroFiles
// reads the summary line, from BOTH streams on every exit (the summary lands on
// whichever stream opengrep chose, and a zero-file scan exits zero).
//
// No `opengrep --version` probe: a program that would not start is the exec's
// own -1, a 2, which is what the chain's failed provisioning layer was.
func fleetOpengrepSast(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	t := in.tree()
	entries, err := t.entries(".")
	if err != nil {
		return cannotEnumerate(a, err)
	}
	if !checks.HasEntry(entries, "rules") {
		return absentRuleset(t, a)
	}
	rulesEntries, err := t.entries("rules")
	if err != nil {
		return cannotEnumerate(a, err)
	}
	if !checks.HasEntry(rulesEntries, "sast") {
		return absentRuleset(t, a)
	}
	out, code := in.run(ctx, Cmd{Dir: in.Root, Name: "opengrep", Args: []string{"scan", "--config", "rules/sast", "--error", "."}, Env: opengrepEnv, Both: true})
	if code < 0 {
		return neverRan(a, out)
	}
	out = strings.TrimSpace(out)
	if checks.OpengrepZeroFiles(out) {
		return checks.VerdictOf(a, int(checks.StateCannotRun), out+"\n"+opengrepRefusal)
	}
	return checks.VerdictOf(a, code, out)
}

// hadolintVersion proves the hadolint on PATH runs and is the PINNED one: the
// rule set is a property of the binary (checks.HadolintVersionOK).
func hadolintVersion(ctx context.Context, in Input) error {
	out, code := in.run(ctx, Cmd{Dir: in.Root, Name: "hadolint", Args: []string{"--version"}})
	if code != 0 {
		return fmt.Errorf("the hadolint version probe never ran: %s", out)
	}
	if !checks.HadolintVersionOK(out, checks.HadolintVersion) {
		return fmt.Errorf("the binary on disk does not answer %q, so the rules are not the pinned ones: %s", "Haskell Dockerfile Linter "+checks.HadolintVersion, out)
	}
	return nil
}

// fleetHadolint: every Dockerfile in the tree passes hadolint under the fleet's
// ruleset.
//
// NO DOCKERFILE IS ABSENT, decided from the population before hadolint is
// touched. THE FLEET'S RULESET IS WRITTEN FOR THE RUN to a scratch file and
// named by --config, which REPLACES hadolint's lookup of the repository's own
// .hadolint.yaml rather than merging with it (checks.HadolintConfigPath has the
// measurement): the file is the same bytes the chain laid at that path, so the
// ruleset is the fleet's and not the repository's. The tool's own exit code is
// the verdict, 0/1 straight through StateFor; inline pragmas are honoured.
func fleetHadolint(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	if in.FilesErr != nil {
		return cannotEnumerate(a, in.FilesErr)
	}
	dockerfiles := checks.DockerfilePopulation(in.Files)
	if len(dockerfiles) == 0 {
		return checks.VerdictOf(a, int(checks.StatePass), "fleet:hadolint: ABSENT - this repository tracks no Dockerfile or Containerfile, so it ships no image for hadolint to read")
	}
	if err := hadolintVersion(ctx, in); err != nil {
		return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - %v. A Dockerfile that was never linted is not a Dockerfile that passed.", a.ID, err))
	}
	config := tempPath("hadolint", ".yaml")
	defer func() { _ = os.Remove(config) }()
	if err := os.WriteFile(config, []byte(checks.HadolintConfig), 0o644); err != nil {
		return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the fleet's ruleset could not be written for the run: %v. A Dockerfile that was never linted is not a Dockerfile that passed.", a.ID, err))
	}
	args := append([]string{"--no-color", "--config", config, "--"}, dockerfiles...)
	out, code := in.run(ctx, Cmd{Dir: in.Root, Name: "hadolint", Args: args, Both: true})
	if code < 0 {
		return neverRan(a, out)
	}
	return checks.VerdictOf(a, code, out)
}
