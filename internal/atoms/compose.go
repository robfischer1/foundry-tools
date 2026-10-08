package atoms

import (
	"context"
	"fmt"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

// THE COMPOSE ATOMS THAT NEED NO CLIENT. compose:config parses specs with a
// fetched docker-compose and stays a chain; these two ask only about the file
// list and the file bodies. Both read the RAW committable tree
// (Input.Committable), not the gate population: the chains never applied the
// fleet exclude to the compose lane.

// composeSurface is the condition the compose atoms share: does this repository
// track a compose spec at all? A scan that failed is a 2, not an absence the
// repository never declared.
func composeSurface(a checks.AtomDef, in Input) (files []string, stop *checks.Verdict) {
	if in.FilesErr != nil {
		v := checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the compose-surface scan itself failed (%v). An empty surface read off a broken scan is an absence this repository never declared.", a.ID, in.FilesErr))
		return nil, &v
	}
	if len(checks.ComposeSpecs(in.Committable)) == 0 {
		v := checks.VerdictOf(a, int(checks.StatePass), a.ID+": ABSENT - this repository tracks no compose.yaml/compose.yml, so it declares no compose spec. Most of the fleet is Kubernetes YAML, which this says nothing about.")
		return nil, &v
	}
	return in.Committable, nil
}

// indent is a tool's output set in from the line that introduced it.
func indent(s, prefix string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return lines
}

// composeNoTrackedSecrets: no credential-shaped file is tracked in a repository
// that ships compose specs. The population is the BARE tracked set
// (checks.CredentialShaped carries the argument).
func composeNoTrackedSecrets(_ context.Context, a checks.AtomDef, in Input) checks.Verdict {
	files, stop := composeSurface(a, in)
	if stop != nil {
		return *stop
	}
	if hits := checks.CredentialShaped(files); len(hits) > 0 {
		return checks.VerdictOf(a, int(checks.StateFindings), a.ID+": FINDINGS - tracked files that must never be committed:\n"+
			strings.Join(indent(strings.Join(hits, "\n"), "  "), "\n"))
	}
	return checks.VerdictOf(a, int(checks.StatePass), a.ID+": no credential-shaped file is tracked")
}

// composeThirdPartyPins: zero ${PIN_} image interpolations, the BP6b ratchet
// stays closed. A count gate only stays closed if the count happened: a body
// that could not be read is a 2.
func composeThirdPartyPins(_ context.Context, a checks.AtomDef, in Input) checks.Verdict {
	files, stop := composeSurface(a, in)
	if stop != nil {
		return *stop
	}
	t := in.tree()
	bodies := map[string]string{}
	for _, f := range checks.PinScanFiles(files) {
		body, err := t.read(f)
		if err != nil {
			return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the ${PIN_} scan itself failed (could not read %s: %v). Refusing to report a closed ratchet on a scan that did not run.", a.ID, f, err))
		}
		bodies[f] = body
	}
	if hits := checks.PinInterpolations(bodies); len(hits) != 0 {
		return checks.VerdictOf(a, int(checks.StateFindings), fmt.Sprintf("%s: FINDINGS - %d ${PIN_} interpolation(s) found - the pin era does not reopen:\n%s",
			a.ID, len(hits), strings.Join(hits, "\n")))
	}
	return checks.VerdictOf(a, int(checks.StatePass), a.ID+": zero ${PIN_} interpolations; the pin era stays closed")
}
