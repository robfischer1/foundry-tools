package main

import (
	"context"
	"fmt"
	"strings"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE COMPOSE LANE: the host stacks, ported off the act-runner's validate.yml.
//
// nas01-stacks and llm01-stacks ARE the boxes: every compose spec, the
// Caddyfile, the runner config. Their `validate.yml` was the only thing that
// had ever validated any of it, and the act-runner it ran on is being removed —
// so these three atoms are what "validated by the gate or not at all" means for
// those repos.
//
// WHAT A GREEN HERE MEANS, kept from the workflow's own header: "this parses and
// its schema is valid", nothing stronger. It does not say the file matches what
// is RUNNING on the box; that is drift, not syntax, and no gate can see it from
// here.

func init() {
	register("compose:config", composeConfig)
	register("compose:no-tracked-secrets", composeNoTrackedSecrets)
	register("compose:third-party-pins", composeThirdPartyPins)
}

// composeSurface is the condition every compose: atom shares — does this
// repository track a compose spec at all? It answers the tracked tree, that
// tree's files, the specs among them, and a verdict when the atom is already
// finished.
//
// THE SURFACE IS TRACKED FILES, NOT A TREE WALK: a compose file sitting in a
// gitignored scratch directory is not a spec this repository ships, and no
// repo's gate should turn on one. The engine's own gitignore filter answers the
// same set the old body's `git ls-files` did, without building a throwaway
// repository to ask git — and `.git` itself is excluded because a linked
// worktree's is a dangling file rather than a directory.
//
// AND THE SCAN'S OWN FAILURE IS READ, because "no compose file" and "the scan
// broke" are the same empty list. That conflation is the exact defect the ported
// workflows had to fix twice in their own bodies (nas01-stacks
// validate.yml:63-79, llm01-stacks validate.yml:56-70) and the one that turned
// 57 repos into false cannot-runs on ca-sweep-manual-1788973171. An ABSENT read
// off a broken scan is an absence this repository never declared. In shell that
// was grep's third exit code; here it is the Glob error, and Go will not let the
// caller drop it.
func composeSurface(ctx context.Context, r *run, a checks.AtomDef) (tracked *dagger.Directory, files, specs []string, stop *checks.Verdict) {
	tracked = r.src.Filter(dagger.DirectoryFilterOpts{
		Gitignore: true,
		Exclude:   []string{".git"},
	})
	files, err := tracked.Glob(ctx, "**")
	if err != nil {
		v := checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - the compose-surface scan itself failed (%v). An empty surface read off a broken scan is an absence this repository never declared.", a.ID, err))
		return nil, nil, nil, &v
	}
	specs = checks.ComposeSpecs(files)
	if len(specs) == 0 {
		v := checks.VerdictOf(a, 0, a.ID+": ABSENT - this repository tracks no compose.yaml/compose.yml, so it declares no compose spec. Most of the fleet is Kubernetes YAML, which this says nothing about.")
		return nil, nil, nil, &v
	}
	return tracked, files, specs, nil
}

// composeClient provisions the parser, PINNED, and probes it.
//
// THE LANE IMAGES CARRY NO COMPOSE CLIENT — they are language CI images, and
// none of the four ships docker or the compose plugin (checks.ComposeVersion's
// comment records the measurement). The old body preferred an existing
// `docker compose` or `docker-compose` when one happened to be there; that
// branch is DELETED, because on these four images it never fires and a branch
// that never fires is a second parser nobody has ever graded with. The pinned
// client is fetched the way oras is: the Nexus mirror first, upstream second,
// and a failure of both is the caller's state 2 rather than a fallthrough.
//
// NO DAEMON IS INVOLVED. `config` is a pure client-side parse — it reads the
// file, resolves extends/include and validates the schema — so nothing here
// needs a docker socket, which is what lets the atom run in a container that has
// no access to one.
//
// The `version` probe is its own exec under the default Expect: a client that
// downloaded but does not run is a provisioning failure, and verdict()/output()
// file it as 2.
func (r *run) composeClient(ctx context.Context) (*dagger.Container, error) {
	f, err := fetchTool(ctx, checks.ComposeMirror, checks.ComposeURL)
	if err != nil {
		return nil, err
	}
	return r.lane(checks.ImageFleet).
		WithFile("/usr/local/bin/docker-compose", f, dagger.ContainerWithFileOpts{Permissions: 0o755}).
		WithExec([]string{"docker-compose", "version"}), nil
}

// Every tracked compose spec parses and its schema validates.
//
// --no-interpolate IS LOAD-BEARING. These files use ${VAR:?message} to make a
// missing variable a DEPLOY-TIME error, which is correct on the box and fatal
// anywhere no variable is set. Without it the gate would fail on every file for
// the wrong reason. Schema validation still runs.
//
// THE env_file TARGETS ARE STUBBED FIRST. env_file targets are secrets and are
// correctly absent from the repo, but `compose config` hard-errors on a missing
// env_file before it ever reaches the schema. Nothing here reads a VALUE —
// --no-interpolate is set — so an empty file is enough. The scan that finds them
// is checks.EnvFileRefs over the tracked YAML read in Go; its failure is a
// CANNOT RUN, because the shell version's `|| true` collapsed grep's three exit
// codes into an empty list, printed "nothing to stub", stubbed nothing, and
// handed the parse a tree missing every file it was supposed to create — a green
// step reporting a clean scan it never performed (nas01-stacks
// validate.yml:63-79).
//
// ONE EXEC PER SPEC, and the table is folded in Go. The old body's `while read`
// loop was one opaque `sh -c`; per spec the engine caches each parse on its own,
// and a file that did not move is not re-parsed.
func composeConfig(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("compose:config")
	tracked, files, specs, stop := composeSurface(ctx, r, a)
	if stop != nil {
		return *stop
	}

	bodies, err := readBodies(ctx, tracked, checks.ComposeYAMLFiles(files))
	if err != nil {
		return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - the env_file scan failed (%v). Refusing to report 'nothing to stub' from a scan that did not run, and then to parse a tree missing every file it was supposed to create.", a.ID, err))
	}

	// The stubs go into the TREE rather than into the container's own
	// filesystem: /src is a mount, the parse runs with /src as its workdir, and
	// a relative env_file resolves inside the mount. A target the repository
	// actually tracks is left alone — the old body's `[ -e "$p" ] || touch`.
	src, tracking := r.src, map[string]bool{}
	for _, f := range files {
		tracking[strings.TrimPrefix(f, "./")] = true
	}
	stubs := []string{}
	for _, p := range checks.EnvFileRefs(bodies) {
		if tracking[p] {
			continue
		}
		src = src.WithNewFile(p, "")
		stubs = append(stubs, p)
	}

	ctr, err := r.composeClient(ctx)
	if err != nil {
		return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - the pinned docker/compose client (v%s) could not be fetched from the mirror or from upstream. Refusing to report a parsed tree that was never parsed.\n%v", a.ID, checks.ComposeVersion, err))
	}
	if len(stubs) > 0 {
		// The lane already mounted r.src at /src; the stubbed tree replaces
		// that mount rather than layering a second one at the same target,
		// because two mounts on one path is a shadowing rule this file should
		// not be relying on. A tree that needed no stub keeps the lane's own
		// mount, and its cache key with it.
		ctr = ctr.WithoutMount("/src").WithMountedDirectory("/src", src)
	}

	lines := []string{fmt.Sprintf("%d env_file reference(s) stubbed", len(stubs))}
	fail := false
	for _, f := range specs {
		out, code, err := output(ctx, ctr.WithExec(
			[]string{"docker-compose", "-f", f, "config", "--no-interpolate", "--quiet"}, anyExit))
		if err != nil {
			return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - the parse of %s never ran: %v", a.ID, f, err))
		}
		if code == 0 {
			lines = append(lines, fmt.Sprintf("%-48sOK", f))
			continue
		}
		fail = true
		lines = append(lines, fmt.Sprintf("%-48sFAIL", f))
		lines = append(lines, indent(out, "      ")...)
	}
	table := strings.Join(lines, "\n")
	if fail {
		return checks.VerdictOf(a, 1, a.ID+": FINDINGS - a tracked compose spec does not parse:\n"+table)
	}
	return checks.VerdictOf(a, 0, a.ID+": every tracked compose spec parses\n"+table)
}

// No credential-shaped file is tracked in a repository that ships compose specs.
//
// THE IGNORE RULE IS ASSERTED, NOT TRUSTED, and the population is the BARE
// tracked set rather than the gate population — checks.CredentialShaped carries
// the argument. NO CONTAINER RUNS: the question is entirely about the file list,
// which the engine already answered, so the old body's `grep` over a temp file
// is a regexp in Go and the atom needs no image at all.
func composeNoTrackedSecrets(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("compose:no-tracked-secrets")
	_, files, _, stop := composeSurface(ctx, r, a)
	if stop != nil {
		return *stop
	}
	hits := checks.CredentialShaped(files)
	if len(hits) > 0 {
		return checks.VerdictOf(a, 1, a.ID+": FINDINGS - tracked files that must never be committed:\n"+
			strings.Join(indent(strings.Join(hits, "\n"), "  "), "\n"))
	}
	return checks.VerdictOf(a, 0, a.ID+": no credential-shaped file is tracked")
}

// Zero ${PIN_} image interpolations — the BP6b ratchet stays closed.
//
// The ratchet (BDTH, Rob-ratified 2026-08-08) was fully tightened the night it
// landed: the literal lane took every third-party image off ${PIN_}, the staged
// :stable conversion took all 32 first-party stars off it, and compose/pins.env
// is deleted. A COUNT gate stays closed where a list gate reopens — ANY ${PIN_}
// image interpolation is a red merge, and the pin era does not reopen
// (nas01-stacks validate.yml:149-176).
//
// A COUNT GATE ONLY STAYS CLOSED IF THE COUNT HAPPENED. grep's rc 1 was the
// ANSWER this gate wants and rc >=2 a refusal; here the read of each body is the
// thing that can fail, and a body that could not be read is a CANNOT RUN rather
// than a ratchet reporting closed on a scan that did not run.
func composeThirdPartyPins(ctx context.Context, r *run) checks.Verdict {
	a := checks.AtomByID("compose:third-party-pins")
	tracked, files, _, stop := composeSurface(ctx, r, a)
	if stop != nil {
		return *stop
	}
	bodies, err := readBodies(ctx, tracked, checks.PinScanFiles(files))
	if err != nil {
		return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - the ${PIN_} scan itself failed (%v). Refusing to report a closed ratchet on a scan that did not run.", a.ID, err))
	}
	hits := checks.PinInterpolations(bodies)
	if n := len(hits); n != 0 {
		return checks.VerdictOf(a, 1, fmt.Sprintf("%s: FINDINGS - %d ${PIN_} interpolation(s) found - the pin era does not reopen:\n%s",
			a.ID, n, strings.Join(hits, "\n")))
	}
	return checks.VerdictOf(a, 0, a.ID+": zero ${PIN_} interpolations; the pin era stays closed")
}

// readBodies reads each named file out of the tree. A file the engine listed
// and then could not read is an ERROR, never an empty body: an empty body
// matches nothing, which is the clean-scan-that-never-ran this module exists to
// refuse.
func readBodies(ctx context.Context, dir *dagger.Directory, files []string) (map[string]string, error) {
	bodies := make(map[string]string, len(files))
	for _, f := range files {
		body, err := dir.File(f).Contents(ctx)
		if err != nil {
			return nil, fmt.Errorf("could not read %s: %w", f, err)
		}
		bodies[f] = body
	}
	return bodies, nil
}

// indent is the `sed 's/^/  /'` every one of these bodies ended with: a tool's
// output, set in from the line that introduced it.
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
