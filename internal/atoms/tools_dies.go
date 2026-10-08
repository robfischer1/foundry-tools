package atoms

import (
	"context"
	"fmt"
	"os"
	"strings"

	"dagger/foundry-tools/internal/checks"
)

// THE DIES ATOMS THAT EXEC OPA. Each opens, as the chain did, with the tree's
// shape (diesShape: absent anywhere but the policy die's source) and then with
// the proof that the opa on PATH is the pinned one (opaProvisioned), before it
// asks opa anything.

// diesOpaTest: the rego unit and invariant suite passes.
//
// A ZERO-TEST RUN IS REFUSED: `opa test` over a policy tree containing no test
// at all exits 0, which renders as a clean suite and is not one. opa's own
// codes do not line up with ours (a failing assertion is 2 and a parse error is
// 1), so checks.OpaTestState folds both to findings and only a code opa does
// not use becomes a 2.
func diesOpaTest(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	if stop := diesShape(in.tree(), a); stop != nil {
		return *stop
	}
	if stop := opaProvisioned(ctx, a, in); stop != nil {
		return *stop
	}
	out, code := in.run(ctx, Cmd{Dir: in.Root, Name: "opa", Args: []string{"test", "policy/", "-v"}})
	if code < 0 {
		return checks.VerdictOf(a, int(checks.StateCannotRun), a.ID+": CANNOT RUN - the atom never ran: "+out)
	}
	state, reason := checks.OpaTestState(code, out)
	switch state {
	case int(checks.StatePass):
		return checks.VerdictOf(a, state, a.ID+": "+reason)
	case int(checks.StateFindings):
		return checks.VerdictOf(a, state, a.ID+": FINDINGS - "+reason+"\n"+out)
	default:
		return checks.VerdictOf(a, int(checks.StateCannotRun), a.ID+": CANNOT RUN - "+reason+"\n"+out)
	}
}

// diesAdmissionDogfood: the admission domain admits this repo's own star shape.
//
// A MISSING FIXTURE IS A 2: the atom's whole content is "the domain admitted
// THIS input", and with no input there is no claim to make. Go reads the
// result (checks.OpaDenySet), and a shape it cannot read is a 2 rather than a
// deny count nobody computed.
func diesAdmissionDogfood(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	t := in.tree()
	if stop := diesShape(t, a); stop != nil {
		return *stop
	}
	// policy/ is proved present by diesShape, so its listing is an answer
	// rather than a risk, and a directory is what has to be there.
	domains, err := t.entries("policy")
	if err != nil {
		return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - policy/ could not be listed (%v).", a.ID, err))
	}
	if !checks.HasEntry(domains, "admission") {
		return checks.VerdictOf(a, int(checks.StateCannotRun), a.ID+": CANNOT RUN - policy/admission is absent, so there is no admission domain to ask.")
	}
	if stop := requirePaths(t, a, [][2]string{
		{"tests/fixtures/ouranos-self.json", "tests/fixtures/ouranos-self.json is absent, so there is no own-star shape to submit."},
	}); stop != nil {
		return *stop
	}
	if stop := opaProvisioned(ctx, a, in); stop != nil {
		return *stop
	}
	out, code := in.run(ctx, Cmd{Dir: in.Root, Name: "opa", Args: []string{
		"eval", "-d", "policy/admission", "-i", "tests/fixtures/ouranos-self.json",
		"data.admission.deny", "--format", "json",
	}})
	if code < 0 {
		return checks.VerdictOf(a, int(checks.StateCannotRun), a.ID+": CANNOT RUN - the atom never ran: "+out)
	}
	if code != 0 {
		return checks.VerdictOf(a, int(checks.StateCannotRun), a.ID+": CANNOT RUN - opa eval did not complete.\n"+out)
	}
	deny, err := checks.OpaDenySet(out)
	if err != nil {
		return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - opa eval answered a shape this atom cannot read (%v), so there is no deny set to count.", a.ID, err))
	}
	if len(deny) > 0 {
		return checks.VerdictOf(a, int(checks.StateFindings), fmt.Sprintf("%s: FINDINGS - deny count = %d\n%s",
			a.ID, len(deny), strings.Join(indent(strings.Join(deny, "\n"), "  "), "\n")))
	}
	return checks.VerdictOf(a, int(checks.StatePass), a.ID+": deny count = 0; the admission domain admits our own star shape")
}

// canaryProbe asks the built bundle which of search and the canary verb a
// principal of the given type may see. The input is a file opa is pointed at,
// exactly as the chain's probe was; a probe that did not evaluate is the
// refusal's reason.
func canaryProbe(ctx context.Context, in Input, bundle, principal, canary string) ([]string, string) {
	input := bundle + "." + principal + ".json"
	defer func() { _ = os.Remove(input) }()
	body := fmt.Sprintf(`{"principal":{"type":%q,"subject":"s"},"verbs":["search",%q]}`, principal, canary)
	if err := place(input, []byte(body)); err != nil {
		return nil, err.Error()
	}
	out, code := in.run(ctx, Cmd{Dir: in.Root, Name: "opa", Args: []string{
		"eval", "-b", bundle, "-i", input, "--format", "json", "data.authz.visible.allowed",
	}})
	if code != 0 {
		return nil, out
	}
	allowed, err := checks.OpaAllowed(out)
	if err != nil {
		return nil, err.Error()
	}
	return allowed, ""
}

// diesCanaryVisibility: the BUILT bundle still hides a curated verb from a
// session principal.
//
// THE CANARY IS READ OFF THE ROSTER, NOT NAMED: the bundle's own data.json is
// asked for chaos's first star_only verb, and THAT one is proved. BOTH
// DIRECTIONS ARE ASSERTED: a curated verb visible to a session principal is the
// fail-open, and one invisible to a star principal is curation that narrowed
// both audiences instead of one.
func diesCanaryVisibility(ctx context.Context, a checks.AtomDef, in Input) checks.Verdict {
	if stop := diesShape(in.tree(), a); stop != nil {
		return *stop
	}
	if stop := opaProvisioned(ctx, a, in); stop != nil {
		return *stop
	}
	bundle := tempPath("dies-bundle", ".tar.gz")
	defer func() { _ = os.Remove(bundle) }()
	data, stop := diesBundle(ctx, a, in, bundle)
	if stop != nil {
		return *stop
	}
	canary := checks.DiesCanary(data)
	if canary == "" {
		return checks.VerdictOf(a, int(checks.StateFindings), a.ID+": FINDINGS - chaos has no star_only row in the built bundle - the roster did not survive the build.")
	}
	refuse := func(principal, why string) checks.Verdict {
		return checks.VerdictOf(a, int(checks.StateCannotRun), fmt.Sprintf("%s: CANNOT RUN - the %s-principal probe against the built bundle did not evaluate.\n%s", a.ID, principal, why))
	}
	session, why := canaryProbe(ctx, in, bundle, "session", canary)
	if why != "" {
		return refuse("session", why)
	}
	star, why := canaryProbe(ctx, in, bundle, "star", canary)
	if why != "" {
		return refuse("star", why)
	}
	seen := fmt.Sprintf("canary: %s (chaos's first star_only verb, read off the bundle)\nsession: %v\nstar:    %v", canary, session, star)

	if len(session) != 1 || session[0] != "search" {
		return checks.VerdictOf(a, int(checks.StateFindings), a.ID+": FINDINGS - a curated verb is VISIBLE to a session principal in the built bundle - the roster did not survive the build.\n"+seen)
	}
	for _, verb := range star {
		if verb == canary {
			return checks.VerdictOf(a, int(checks.StatePass), a.ID+": the built bundle still hides what it should\n"+seen)
		}
	}
	return checks.VerdictOf(a, int(checks.StateFindings), a.ID+": FINDINGS - a curated verb is INVISIBLE to a star principal - curation narrowed both audiences, not one.\n"+seen)
}
