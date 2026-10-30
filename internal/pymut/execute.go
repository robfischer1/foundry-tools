package pymut

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// execute runs cosmic-ray on each worker's share of the session, merges the
// shares and scores the merged session.
func (g *grading) execute(ctx context.Context, workers int, sessions, selectEnv []string, selectLog string) checks.Verdict {
	executed := concurrently(workers, func(i int) step {
		w := g.worker(i)
		body, err := readFile(sessions[i])
		if err == nil {
			err = writeFile(filepath.Join(w, session), body, 0o644)
		}
		if err != nil {
			return step{out: err.Error(), code: -1, cmd: atoms.Cmd{Name: "cosmic-ray"}}
		}
		// Each worker selects into a log of its own; they are joined below.
		// selectEnv ends with the joined log's name; the worker's own replaces it.
		env := append(slicesClone(selectEnv[:len(selectEnv)-1]), "FORGE_MUT_SELECT_LOG="+selectLog+"."+strconv.Itoa(i+1), "FORGE_MUT_WORKER="+strconv.Itoa(i+1), xdistShare(workers))
		return g.do(ctx, w, env, "uv", "run", "cosmic-ray", "exec", pythonMutationConfig, session)
	})
	status := 0
	merge := []string{"merge", session}
	var selections strings.Builder
	for i, w := range executed {
		if !w.ran() {
			return g.neverRan(w)
		}
		if status == 0 {
			status = w.code
		}
		part := filepath.Join(g.work, "parts", fmt.Sprintf("w%d.sqlite", i+1))
		body, err := readFile(filepath.Join(g.worker(i), session))
		if err == nil {
			err = writeAll(map[string]string{part: string(body)})
		}
		if err != nil {
			return g.settle(2, "CANNOT RUN - the atom never ran: "+err.Error())
		}
		merge = append(merge, part)
		// A worker that selected nothing wrote no log.
		log, _ := readFile(selectLog + "." + strconv.Itoa(i+1))
		selections.Write(log)
	}
	if err := writeFile(selectLog, []byte(selections.String()), 0o644); err != nil {
		return g.settle(2, "CANNOT RUN - the atom never ran: "+err.Error())
	}
	merged := g.testkit(ctx, selectEnv, merge...)
	if !merged.ran() {
		return g.neverRan(merged)
	}
	// A worker that left jobs unscored is a broken exec, whatever its own exit
	// said: the score must not read a sample nobody chose.
	if status == 0 {
		status = merged.code
	}
	if status != 0 {
		return g.settle(2, fmt.Sprintf("CANNOT RUN - cosmic-ray exec exited %d — a broken run, not a survivor report\n%s", status, lastLines(merged.out, 20)))
	}
	// Exit 3 is a mutant run that pruned its collection and then matched none of
	// its selected tests: its outcome is not a measurement. Any other failure is a
	// summary that could not be read, and exec's status stands.
	selection := g.testkit(ctx, selectEnv, "selection", selectLog)
	if !selection.ran() {
		return g.neverRan(selection)
	}
	if selection.code == 3 {
		return g.cannot("a mutant run pruned its test collection and then matched none of its selected tests, so its outcome is not a measurement", selection)
	}

	// SCORE: the honest report, stdout and stderr apart.
	var stderr string
	report := atoms.Cmd{Dir: g.tree, Name: "uv", Env: selectEnv, Stderr: &stderr, Args: []string{"run", "--no-project", "--isolated", "--refresh-package", "forge-testkit",
		"--with", checks.PythonMutationTestkit, "forge-testkit-mutation", "report", session, "--fail-under", strconv.Itoa(checks.PythonMutationFailUnder)}}
	stdout, rc := atoms.RunCmd(ctx, g.in, report)
	if !atoms.ToolRan(rc) {
		return g.neverRan(step{out: stdout + stderr, code: rc, cmd: report})
	}
	return g.settle(checks.PythonReportVerdict(rc, stdout, stderr))
}
