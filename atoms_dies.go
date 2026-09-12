package main

import (
	"context"
	"fmt"
	"strings"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/checks/scripts"
	"dagger/foundry-tools/internal/dagger"
)

// THE DIES LANE: the policy die's source, ported off ci / contracts / schema.
//
// Six atoms, and THE SPLIT BETWEEN THE FIRST TWO AND THE NEXT TWO IS THE
// ARGUMENT. `opa test` and the dogfood eval grade the SOURCE; data-keys and
// canary-visibility grade the ARTIFACT, and the gap between them is measured
// rather than theoretical: a source tree can pass 312 assertions and build a
// bundle that admits everything. diesBundle carries the whole of it.
//
// build.yml and fleet-bundle.yml are deliberately NOT here. They publish rather
// than validate, and the bundle recipe lane owns them.

func init() {
	register("dies:opa-test", diesOpaTest)
	register("dies:admission-dogfood", diesAdmissionDogfood)
	register("dies:data-keys", diesDataKeys)
	register("dies:canary-visibility", diesCanaryVisibility)
	register("dies:contracts", diesContracts)
	register("dies:schema", diesSchema)
}

// diesShape is the condition every dies: atom shares, decided IN GO from the
// Directory before any container runs.
//
// TWO MARKERS, BOTH REQUIRED, because either alone is ambiguous: policy/ turns
// up in more than one repo in this fleet, and fleet/stars/ is a name a fleet
// inventory could reasonably take. Together they name foundry-dies — the repo
// that owns the policy bundle AND the star roster the bundle is built from —
// and nothing else in custody carries both. Everywhere else these atoms report
// ABSENT and say why, rather than going quiet.
//
// THE ROOT LISTING IS READ FIRST so a missing marker is an ABSENCE rather than
// a descent into a directory that is not there: Directory("fleet").Entries on a
// tree with no fleet/ is an error, and an error is not an answer about the
// tree's shape.
func diesShape(ctx context.Context, r *run, a checks.AtomDef) *checks.Verdict {
	absent := checks.VerdictOf(a, 0, a.ID+": ABSENT - this tree is not the policy die's source. It needs both policy/.manifest and fleet/stars/, and this one does not carry both.")

	roots, err := r.src.Entries(ctx)
	if err != nil {
		v := checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - the repository root could not be read (%v), so this tree's shape is unknown rather than wrong.", a.ID, err))
		return &v
	}
	if !checks.HasEntry(roots, "policy") || !checks.HasEntry(roots, "fleet") {
		return &absent
	}
	if _, err := r.src.File("policy/.manifest").Contents(ctx); err != nil {
		return &absent
	}
	fleet, err := r.src.Directory("fleet").Entries(ctx)
	if err != nil {
		v := checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - fleet/ is in the root listing but could not be read (%v).", a.ID, err))
		return &v
	}
	if !checks.HasEntry(fleet, "stars") {
		return &absent
	}
	return nil
}

// present answers whether the tree carries a path, WITHOUT conflating "not
// there" with "could not look". Glob returns an empty list for the first and an
// error for the second, which is the distinction every guard on this page needs
// and the one a `[ -f x ]` in shell could never make.
func present(ctx context.Context, r *run, path string) (bool, error) {
	matches, err := r.src.Glob(ctx, path)
	if err != nil {
		return false, err
	}
	return len(matches) > 0, nil
}

// requirePaths is the run of `[ -f … ] || CANNOT RUN` each dies atom opened
// with. Each entry keeps the original sentence, because each says something
// different about what cannot be claimed without that file.
func requirePaths(ctx context.Context, r *run, a checks.AtomDef, required [][2]string) *checks.Verdict {
	for _, req := range required {
		ok, err := present(ctx, r, req[0])
		if err != nil {
			v := checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - the tree could not be scanned for %s (%v).", a.ID, req[0], err))
			return &v
		}
		if !ok {
			v := checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - "+req[1])
			return &v
		}
	}
	return nil
}

// opaClient provisions opa AT THE PINNED VERSION, and the pin is load-bearing.
//
// The rego language version is a property of the binary: a suite written for v1
// semantics graded by a different major answers a different question, and "the
// policy suite passed" would then be a true statement about the wrong language.
// The workflow this ports pinned 1.18.0 by hand (foundry-dies
// .forgejo/workflows/ci.yml:47-53); the pin lives in checks.OpaVersion now.
//
// THE VERSION IS CHECKED RATHER THAN ASSUMED, so a mirror that served something
// else — or an image that starts shipping its own opa on PATH — cannot silently
// supply a different language. The old body used the check to SKIP the download
// when the right binary was already on disk; with the file placed by the engine
// there is nothing to skip, and the same probe now asserts what was fetched.
func (r *run) opaClient(ctx context.Context) (*dagger.Container, error) {
	f, err := fetchTool(ctx, checks.OpaMirror, checks.OpaURL)
	if err != nil {
		return nil, fmt.Errorf("opa %s could not be fetched from the mirror or from upstream: %w", checks.OpaVersion, err)
	}
	ctr := r.lane(checks.ImageFleet).
		WithFile("/usr/local/bin/opa", f, dagger.ContainerWithFileOpts{Permissions: 0o755})

	out, code, err := output(ctx, ctr.WithExec([]string{"opa", "version"}, anyExit))
	if err != nil {
		return nil, fmt.Errorf("the opa version probe never ran: %w", err)
	}
	if code != 0 {
		return nil, fmt.Errorf("opa is on disk but does not run (exit %d): %s", code, out)
	}
	if !checks.OpaVersionOK(out, checks.OpaVersion) {
		return nil, fmt.Errorf("the binary on disk does not answer %q, so the rego semantics are not the pinned ones: %s", "Version: "+checks.OpaVersion, out)
	}
	return ctr, nil
}

// diesOpa is opaClient with the atom's own refusal attached.
func diesOpa(ctx context.Context, r *run, a checks.AtomDef) (*dagger.Container, *checks.Verdict) {
	ctr, err := r.opaClient(ctx)
	if err != nil {
		v := checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - %v. A policy suite that never ran is not a policy suite that passed.", a.ID, err))
		return nil, &v
	}
	return ctr, nil
}

// diesBundle builds the artifact and takes its data document out, because THE
// SOURCE TREE IS NOT A PROXY FOR THE ARTIFACT.
//
// That is the whole reason ci.yml's third gate exists, and it is worth keeping
// verbatim: `opa test policy/` passes on a tree whose BUILT BUNDLE is empty.
// Directory (--data) mode loads every *.json and merges by top-level key; bundle
// mode reads ONLY files literally named data.json. A tree using arbitrary JSON
// names tests green and builds an artifact with data.json == {}, which makes
// star_only undefined, which makes the visibility comprehension collect nothing,
// which makes EVERY verb visible to EVERY principal. Fail-open, silent, and
// green the whole way down.
//
// A BUILD THAT DID NOT COMPLETE IS A 2; A BUILD THAT PRODUCED A BUNDLE WITH NO
// data.json IS A 1. The first is provisioning — opa could not do its job, and
// the build exec carries the DEFAULT Expect so its failure is a Dagger error.
// The second IS the defect above, arriving exactly as described, and calling it
// "could not run" would file the finding as an absence.
//
// THE REVISION IS BEST-EFFORT, exactly as the shell's `|| echo unknown` was. It
// is stamped into the bundle's manifest and nothing here grades it, so a tree
// git cannot resolve a HEAD in — a linked worktree, which gitReady gives a
// throwaway repository with no commit — builds as "unknown" rather than not at
// all.
func diesBundle(ctx context.Context, r *run, a checks.AtomDef, ctr *dagger.Container) (*dagger.Container, string, *checks.Verdict) {
	rev := "unknown"
	if out, code, err := output(ctx, r.gitReady(ctx, ctr).WithExec([]string{"git", "rev-parse", "HEAD"}, anyExit)); err == nil && code == 0 {
		if head := strings.TrimSpace(out); head != "" {
			rev = head
		}
	}

	built := ctr.WithExec([]string{
		"opa", "build", "-b", "policy/", "-o", "/tmp/dies-bundle.tar.gz",
		"--revision", rev, "--ignore", "*_test.rego",
	})

	data, code, err := output(ctx, built.WithExec(
		[]string{"tar", "xzOf", "/tmp/dies-bundle.tar.gz", "/data.json"}, anyExit))
	if err != nil {
		v := checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - opa build did not produce a bundle, so there is no artifact to interrogate.\n%v", a.ID, err))
		return nil, "", &v
	}
	if code != 0 {
		v := checks.VerdictOf(a, 1, a.ID+": FINDINGS - the built bundle carries NO data.json member at all. Bundle mode reads only files literally named data.json, so a tree using arbitrary JSON names tests green and ships an empty data document - star_only undefined, the visibility comprehension collecting nothing, every verb visible to every principal.\n"+data)
		return nil, "", &v
	}
	return built, data, nil
}

// The rego unit and invariant suite passes.
//
// A ZERO-TEST RUN IS REFUSED, which the workflow did not do and this module
// cannot skip: `opa test` over a policy tree containing no test at all exits 0
// (measured against an empty directory, 2026-09-10). That renders as a clean
// suite and is not one — it is opengrep matching zero files wearing different
// clothes, and it gets the same answer.
//
// OPA'S OWN CODES ARE THREE-VALUED TOO, and they do not line up with this
// module's: a failing assertion is exit 2 and a rego parse error is exit 1 (both
// measured). Passing either through would file a real finding as CANNOT RUN, so
// both fold to 1 and only a code opa does not use becomes a 2. That fold is
// checks.OpaTestState, where a table test holds every branch — the `case` it
// replaces was untestable shell.
func diesOpaTest(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("dies:opa-test")
	if stop := diesShape(ctx, r, a); stop != nil {
		return *stop
	}
	ctr, stop := diesOpa(ctx, r, a)
	if stop != nil {
		return *stop
	}
	out, code, err := output(ctx, ctr.WithExec([]string{"opa", "test", "policy/", "-v"}, anyExit))
	if err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - the atom never ran: "+err.Error())
	}
	state, reason := checks.OpaTestState(code, out)
	switch state {
	case 0:
		return checks.VerdictOf(a, 0, a.ID+": "+reason)
	case 1:
		return checks.VerdictOf(a, 1, a.ID+": FINDINGS - "+reason+"\n"+out)
	default:
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - "+reason+"\n"+out)
	}
}

// The admission domain admits this repo's own star shape.
//
// The domain that judges every star's slag is asked about the one star whose
// shape lives in the same repo as the rule. A deny here means the policy has
// drifted from the fleet it governs, and it shows up on the repo that OWNS the
// rule rather than on whichever star was poured next — the same
// alarm-asymmetry argument contracts.yml makes at length.
//
// A MISSING FIXTURE IS A 2. The atom's whole content is "the domain admitted
// THIS input"; with no input there is no claim to make, and exiting 0 would make
// one anyway.
//
// GO READS THE RESULT, NOT jq AND NOT python3. The workflow's runner image
// carried jq; the lane images are not promised to, and the old body reached for
// python3 to count the length of a JSON array. Both are gone: the value is the
// same one, read by encoding/json in checks.OpaDenySet, and a shape this atom
// cannot read is a 2 rather than a deny count nobody computed.
func diesAdmissionDogfood(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("dies:admission-dogfood")
	if stop := diesShape(ctx, r, a); stop != nil {
		return *stop
	}

	// policy/ is proved present by diesShape, so its listing is an answer
	// rather than a risk — and a directory is what has to be there.
	domains, err := r.src.Directory("policy").Entries(ctx)
	if err != nil {
		return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - policy/ could not be listed (%v).", a.ID, err))
	}
	if !checks.HasEntry(domains, "admission") {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - policy/admission is absent, so there is no admission domain to ask.")
	}
	if stop := requirePaths(ctx, r, a, [][2]string{
		{"tests/fixtures/ouranos-self.json", "tests/fixtures/ouranos-self.json is absent, so there is no own-star shape to submit."},
	}); stop != nil {
		return *stop
	}

	ctr, stop := diesOpa(ctx, r, a)
	if stop != nil {
		return *stop
	}
	out, code, err := output(ctx, ctr.WithExec([]string{
		"opa", "eval", "-d", "policy/admission", "-i", "tests/fixtures/ouranos-self.json",
		"data.admission.deny", "--format", "json",
	}, anyExit))
	if err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - the atom never ran: "+err.Error())
	}
	if code != 0 {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - opa eval did not complete.\n"+out)
	}
	deny, err := checks.OpaDenySet(out)
	if err != nil {
		return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - opa eval answered a shape this atom cannot read (%v), so there is no deny set to count.", a.ID, err))
	}
	if len(deny) > 0 {
		return checks.VerdictOf(a, 1, fmt.Sprintf("%s: FINDINGS - deny count = %d\n%s",
			a.ID, len(deny), strings.Join(indent(strings.Join(deny, "\n"), "  "), "\n")))
	}
	return checks.VerdictOf(a, 0, a.ID+": deny count = 0; the admission domain admits our own star shape")
}

// The BUILT bundle carries every data root the policy reads, non-empty.
//
// The artifact gate, asked of the artifact. An empty or partial data document is
// the silent fail-open diesBundle describes, so the documents the policy
// actually reads are asserted PRESENT and NON-EMPTY, by name
// (checks.DiesRequiredDataRoots).
//
// A data.json THAT WILL NOT PARSE IS A 2, and that is a deliberate change from
// the shell, where the python traceback fell through a `|| exit 1` and was filed
// as a finding. The dogfood atom's own comment states the rule this follows: a
// shape the atom cannot read is a could-not-run, not a count nobody computed.
func diesDataKeys(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("dies:data-keys")
	if stop := diesShape(ctx, r, a); stop != nil {
		return *stop
	}
	ctr, stop := diesOpa(ctx, r, a)
	if stop != nil {
		return *stop
	}
	_, data, stop := diesBundle(ctx, r, a, ctr)
	if stop != nil {
		return *stop
	}

	missing, stars, err := checks.DiesDataKeys(data)
	if err != nil {
		return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - the built bundle's data.json is a shape this atom cannot read (%v), so there is no data document to grade.", a.ID, err))
	}
	if len(missing) > 0 {
		return checks.VerdictOf(a, 1, fmt.Sprintf("%s: FINDINGS - ::error::bundle data.json is missing/empty: %v. Bundle mode reads ONLY files named data.json - check the policy/<root>/data.json layout.", a.ID, missing))
	}
	return checks.VerdictOf(a, 0, fmt.Sprintf("%s: data roots ok; star_only carries %d stars", a.ID, stars))
}

// The BUILT bundle still hides a curated verb from a session principal.
//
// THE CANARY IS READ OFF THE ROSTER, NOT NAMED, and that is the post-mortem's
// own recommendation (2026-08-22). The first canary named graph_subscribe, a
// verb chaos retired in F2; the second named graph_nodes. A named canary goes
// stale the day its verb leaves the roster and the gate then goes red on a
// roster that is MORE correct — twice now. So the built bundle's own data.json
// is asked for chaos's first star_only verb and THAT one is proved: whatever
// chaos curates first is, by construction, curated. chaos because it is the star
// with the largest curated surface, and an empty chaos row is itself the failure
// — nothing curated means the roster did not survive the build.
//
// BOTH DIRECTIONS ARE ASSERTED. A curated verb visible to a session principal is
// the fail-open. A curated verb INVISIBLE to a star principal is the opposite
// error and just as wrong: curation that narrowed both audiences instead of one.
//
// THE PROBE INPUT IS A FILE, NOT A REDIRECT. `--stdin-input` needed a `<` and a
// `<` needs a shell; `-i /tmp/probe-<principal>.json` asks opa the same question
// with the file placed by the engine, which is also why each probe is its own
// cacheable exec.
func diesCanaryVisibility(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("dies:canary-visibility")
	if stop := diesShape(ctx, r, a); stop != nil {
		return *stop
	}
	ctr, stop := diesOpa(ctx, r, a)
	if stop != nil {
		return *stop
	}
	built, data, stop := diesBundle(ctx, r, a, ctr)
	if stop != nil {
		return *stop
	}

	canary := checks.DiesCanary(data)
	if canary == "" {
		return checks.VerdictOf(a, 1, a.ID+": FINDINGS - chaos has no star_only row in the built bundle - the roster did not survive the build.")
	}

	probe := func(principal string) ([]string, *checks.Verdict) {
		refuse := func(why string) *checks.Verdict {
			v := checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - the %s-principal probe against the built bundle did not evaluate.\n%s", a.ID, principal, why))
			return &v
		}
		path := "/tmp/probe-" + principal + ".json"
		in := fmt.Sprintf(`{"principal":{"type":%q,"subject":"s"},"verbs":["search",%q]}`, principal, canary)
		out, code, err := output(ctx, built.
			WithNewFile(path, in).
			WithExec([]string{
				"opa", "eval", "-b", "/tmp/dies-bundle.tar.gz", "-i", path,
				"--format", "json", "data.authz.visible.allowed",
			}, anyExit))
		if err != nil {
			return nil, refuse(err.Error())
		}
		if code != 0 {
			return nil, refuse(out)
		}
		allowed, err := checks.OpaAllowed(out)
		if err != nil {
			return nil, refuse(err.Error())
		}
		return allowed, nil
	}

	session, stop := probe("session")
	if stop != nil {
		return *stop
	}
	star, stop := probe("star")
	if stop != nil {
		return *stop
	}
	seen := fmt.Sprintf("canary: %s (chaos's first star_only verb, read off the bundle)\nsession: %v\nstar:    %v", canary, session, star)

	if len(session) != 1 || session[0] != "search" {
		return checks.VerdictOf(a, 1, a.ID+": FINDINGS - a curated verb is VISIBLE to a session principal in the built bundle - the roster did not survive the build.\n"+seen)
	}
	for _, verb := range star {
		if verb == canary {
			return checks.VerdictOf(a, 0, a.ID+": the built bundle still hides what it should\n"+seen)
		}
	}
	return checks.VerdictOf(a, 1, a.ID+": FINDINGS - a curated verb is INVISIBLE to a star principal - curation narrowed both audiences, not one.\n"+seen)
}

// diesContractFixtures is the detection proof, as a table.
//
// NINE MUST FAIL AND FOUR MUST PASS. The live check cannot prove the checker
// DETECTS anything while it is green, so the fixtures are run first (the
// retiring pair and its control joined 2026-09-11 with check_contracts'
// `retiring`, pending's mirror for a member the authority dropped while a
// consumer still carries it). Without the controls the failure loop could be
// satisfied by a checker that simply fails everything — including a pending
// entry whose grounds genuinely still hold, which is a legitimate deferral. A
// gate that cannot fail is a gate that is not there.
//
// THE FIXTURES TOUCH NO NETWORK, by the fixture manifest's own design, which is
// what lets the detection proof stand while the door is down.
var diesContractFixtures = []struct {
	name       string
	expectFail bool
}{
	{"lagging", true},
	{"undeclared", true},
	{"bad_pending", true},
	{"unreadable", true},
	{"expired_pending", true},
	{"undated_pending", true},
	{"old_shape_pending", true},
	{"bad_retiring", true},
	{"expired_retiring", true},
	{"agreeing", false},
	{"holding_pending", false},
	{"unmeasurable_pending", false},
	{"retiring", false},
}

// tomlpy runs a python program that has to read TOML.
//
// THROUGH uv, UNCONDITIONALLY. The old body tried the image's own python3 first
// and only fell back to `uv run --with tomli` when `import tomllib` failed —
// two provisioning paths, of which CI exercised one. The image's system python3
// is not promised to be >= 3.11, and check_contracts.py already imports tomli
// when tomllib is missing, so the fallback is the only path that is always
// correct and it is now the only path there is.
func tomlpy(args ...string) []string {
	return append([]string{"uv", "run", "--no-project", "--quiet", "--with", "tomli>=2.0", "python3"}, args...)
}

// schemapy runs the slag-schema validator, with jsonschema resolved the same
// way and for the same reason.
//
// jsonschema COMES THROUGH uv, NOT pip. The workflow's `python3 -m pip install`
// assumed the act image's interpreter; the lane images are uv-managed, where
// that install is refused outright as an externally-managed environment.
func schemapy(args ...string) []string {
	return append([]string{"uv", "run", "--no-project", "--quiet", "--with", "jsonschema>=4.20", "python3"}, args...)
}

// Every copy of every shared closed set agrees — and the checker is proved to
// detect first.
//
// THE DOOR IS PROBED BEFORE THE LIVE CHECK, and the probe target is READ OUT OF
// THE MANIFEST rather than named — the same lesson as the canary above.
// check_contracts.py raises ContractError on an unreachable copy and main()
// returns 1 for it, which is right for a gate whose runner sat on the same
// network as the door and wrong for an atom: a copy that could not be FETCHED is
// not a copy that DISAGREES, and reporting one as the other sends a reader to
// reconcile lists that may be identical. So an unreachable door is a 2 here and
// every other answer stays the checker's own.
//
// THE PROBE IS A FILE NOW, not a heredoc: internal/checks/scripts/
// dies_door_probe.py, embedded, byte for byte what the shell wrote to
// /tmp/dies-door-probe.py. Rule 6 — a script that IS the tool stays the tool —
// and a python program in a Go string is a program nothing can lint.
func diesContracts(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("dies:contracts")
	if stop := diesShape(ctx, r, a); stop != nil {
		return *stop
	}
	if stop := requirePaths(ctx, r, a, [][2]string{
		{"tools/check_contracts.py", "tools/check_contracts.py is absent, so there is no checker to run."},
		{"tests/contracts/fixtures.toml", "tests/contracts/fixtures.toml is absent, and a gate that cannot prove it detects is a gate that is not there."},
		{"contracts/contracts.toml", "contracts/contracts.toml is absent, so there is no live manifest to check."},
	}); stop != nil {
		return *stop
	}

	probe, err := scripts.Dies.ReadFile("dies_door_probe.py")
	if err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - the embedded door probe could not be read: "+err.Error())
	}

	// `uv --version` is the provisioning probe, on its own exec under the
	// default Expect: an image without uv is a could-not-run, not a finding.
	// The probe SCRIPT lands after the fixtures so that editing it does not
	// invalidate thirteen cached fixture runs that never read it.
	ctr := r.lane(checks.ImageFleet).
		WithExec([]string{"uv", "--version"})

	lines := []string{}
	bad := false
	for _, f := range diesContractFixtures {
		_, code, err := output(ctx, ctr.WithExec(tomlpy(
			"tools/check_contracts.py",
			"--manifest", "tests/contracts/fixtures.toml",
			"--contract", f.name,
		), anyExit))
		if err != nil {
			return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - the %s fixture never ran: %v", a.ID, f.name, err))
		}
		line, isBad := checks.ContractFixtureVerdict(f.name, f.expectFail, code)
		lines = append(lines, line)
		bad = bad || isBad
	}
	if bad {
		return checks.VerdictOf(a, 1, a.ID+": FINDINGS - the fixtures no longer prove the gate detects:\n"+strings.Join(lines, "\n"))
	}

	ctr = ctr.WithNewFile("/tmp/dies-door-probe.py", string(probe))
	out, code, err := output(ctx, ctr.WithExec(tomlpy("/tmp/dies-door-probe.py"), anyExit))
	if err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - the door reachability probe never ran: "+err.Error())
	}
	if code != 0 {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - the door's raw API is unreachable, so the remote copies cannot be read. A copy that could not be fetched is not a copy that agrees.\n"+out)
	}

	return verdict(ctx, a, ctr.WithExec(tomlpy("tools/check_contracts.py"), anyExit))
}

// The slag schema is a valid Draft 2020-12 document and every v2 record
// satisfies it.
//
// TWO ASSERTIONS ABOUT THE SCHEMA, and the second is the one check_schema does
// not make. `required` naming a property that is not DEFINED is legal to the
// metaschema and, under additionalProperties false, makes the schema reject
// EVERY document — so pour would refuse every well-formed melt, and the failure
// would surface at a pour rather than here.
//
// AND THE v2 RECORDS ARE VALIDATED, which v1's never were: nothing in CI ever
// checked a slag record against the schema, so the schema drifted silently. The
// filename and meta.name rules ride along because a record named anything other
// than <name>.slag beside its own directory is one the loader will not find.
//
// THE PROVISION IS PROBED BEFORE THE GATE RUNS — `uv --version`, then an
// `import jsonschema` under the same resolver, each its own exec under the
// default Expect — so a resolver that could not reach an index is a 2 rather
// than a schema finding nobody made. Refusing to report a validated schema that
// was never validated is the whole of that probe.
//
// The validator itself is internal/checks/scripts/dies_schema.py, embedded,
// byte for byte the PYSCHEMA heredoc it replaces.
func diesSchema(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("dies:schema")
	if stop := diesShape(ctx, r, a); stop != nil {
		return *stop
	}
	if stop := requirePaths(ctx, r, a, [][2]string{
		{"schema/slag.schema.json", "schema/slag.schema.json is absent, so there is no payload to validate."},
		{"schema/slag-v2.schema.json", "schema/slag-v2.schema.json is absent, so the v2 records cannot be discriminated."},
	}); stop != nil {
		return *stop
	}

	body, err := scripts.Dies.ReadFile("dies_schema.py")
	if err != nil {
		return checks.VerdictOf(a, 2, a.ID+": CANNOT RUN - the embedded schema validator could not be read: "+err.Error())
	}

	return verdict(ctx, a, r.lane(checks.ImageFleet).
		WithExec([]string{"uv", "--version"}).
		WithExec(schemapy("-c", "import jsonschema")).
		WithNewFile("/tmp/dies-schema.py", string(body)).
		WithExec(schemapy("/tmp/dies-schema.py"), anyExit))
}
