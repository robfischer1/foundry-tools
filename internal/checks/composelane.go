package checks

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// The compose lane's JUDGEMENTS, as pure functions over strings.
//
// Every one of these was a `grep -E` in a shell body, and grep is three-valued
// — 0 selected, 1 selected nothing, >=2 the scan broke. Both ported workflows
// had to learn that the hard way (nas01-stacks validate.yml:63-79, llm01-stacks
// validate.yml:56-70): a `|| true` collapsed all three into an empty list, so
// "nothing matched" and "the scan did not run" rendered identically and a green
// step reported a clean scan it never performed. In Go the third value is an
// error the caller cannot swallow by accident, and the first two are a slice
// whose length is the answer. The atom decides; these only look.

var composeSpecRE = regexp.MustCompile(`(^|/)(docker-)?compose\.ya?ml$`)

// ComposeSpecs is THE COMPOSE SURFACE: which of this repository's tracked
// files are compose specs. An empty result is the ABSENT every compose: atom
// shares — this repository declares no compose spec — and it is only that when
// the list handed in came from a scan that actually ran.
//
// THE INPUT IS TRACKED FILES, NOT A TREE WALK. A compose file sitting in a
// gitignored scratch directory is not a spec this repository ships, and no
// repo's gate should turn on one.
func ComposeSpecs(files []string) []string {
	out := []string{}
	for _, f := range cleanPaths(files) {
		if composeSpecRE.MatchString(f) {
			out = append(out, f)
		}
	}
	return out
}

// ComposeYAMLFiles is the population the two body-reading scans walk: every
// tracked *.yml/*.yaml. Directory entries (which a Dagger glob names with a
// trailing slash) are dropped here rather than in each caller.
func ComposeYAMLFiles(files []string) []string {
	out := []string{}
	for _, f := range cleanPaths(files) {
		if strings.HasSuffix(f, ".yml") || strings.HasSuffix(f, ".yaml") {
			out = append(out, f)
		}
	}
	return out
}

// PinScanFiles narrows that population the way the ratchet's grep did with
// `--exclude-dir=.forgejo`: a workflow file may legitimately mention ${PIN_}
// while describing the era that closed, and the gate is about what the stack
// DEPLOYS. The directory is excluded at any depth, which is what grep's flag
// means.
func PinScanFiles(files []string) []string {
	out := []string{}
	for _, f := range ComposeYAMLFiles(files) {
		if f == ".forgejo" || strings.HasPrefix(f, ".forgejo/") || strings.Contains(f, "/.forgejo/") {
			continue
		}
		out = append(out, f)
	}
	return out
}

var envFileRE = regexp.MustCompile(`[./A-Za-z0-9_-]+\.env`)

// EnvFileRefs answers every env_file target the tracked YAML mentions, sorted
// and deduplicated, so the caller can stub them before the parse.
//
// THE STUB IS WHY THIS EXISTS. env_file targets are secrets and are correctly
// absent from the repository, but `compose config` hard-errors on a missing
// env_file before it ever reaches the schema. Nothing downstream reads a VALUE
// — the parse runs with --no-interpolate — so an empty file is enough.
//
// The pattern is the workflows' own (`[./A-Za-z0-9_-]+\.env`) and it is
// deliberately loose: it matches the path wherever it appears in the document,
// because a compose spec spells env_file half a dozen ways (a scalar, a list
// item, a mapping with `path:`) and a parser for that is a second place for
// this and compose to disagree. A path that is not an env_file gets an empty
// file nothing reads.
func EnvFileRefs(bodies map[string]string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, body := range bodies {
		for _, m := range envFileRE.FindAllString(body, -1) {
			p := strings.TrimPrefix(m, "./")
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

var credentialShapeRE = regexp.MustCompile(`(^|/)\.env$|\.env\.|(^|/)envs/|\.pem$|\.key$|_rsa$`)

// CredentialShaped answers which tracked files look like a credential.
//
// THE IGNORE RULE IS ASSERTED, NOT TRUSTED. Both host-stacks repos' .gitignore
// state the rule — "if it holds a credential, it is IGNORED" — and nothing
// enforced it. An ignore rule only protects files it was written before; this
// asserts the OUTCOME.
//
// THE POPULATION IS THE BARE TRACKED SET, deliberately NOT the gate population
// (GatePopulation) the fleet atoms grade. A .env that a repo's pre-commit
// `exclude:` keeps out of its hooks is still a tracked .env, and a credential
// does not stop being one because a config said not to look at it. The argument
// that carries the fleet exclude everywhere else — grade the population the
// hook graded — argues the other way here, because the hook is not what is
// being ported: the assertion is.
func CredentialShaped(files []string) []string {
	out := []string{}
	for _, f := range cleanPaths(files) {
		if credentialShapeRE.MatchString(f) {
			out = append(out, f)
		}
	}
	return out
}

var pinInterpolationRE = regexp.MustCompile(`image:.*\$\{PIN_`)

// PinInterpolations answers every `image: ...${PIN_...}` line in the scanned
// bodies, as `file:line:text` — grep -rn's own shape, because that is what a
// reader has to open.
//
// THE BP6b RATCHET (BDTH, Rob-ratified 2026-08-08) was fully tightened the
// night it landed: the literal lane took every third-party image off ${PIN_},
// the staged :stable conversion took all 32 first-party stars off it, and
// compose/pins.env is deleted. A COUNT gate stays closed where a list gate
// reopens — ANY ${PIN_} image interpolation is a red merge, and the pin era
// does not reopen (nas01-stacks validate.yml:149-176).
func PinInterpolations(bodies map[string]string) []string {
	names := make([]string, 0, len(bodies))
	for name := range bodies {
		names = append(names, name)
	}
	sort.Strings(names)

	out := []string{}
	for _, name := range names {
		for i, line := range strings.Split(bodies[name], "\n") {
			line = strings.TrimSuffix(line, "\r")
			if pinInterpolationRE.MatchString(line) {
				out = append(out, fmt.Sprintf("%s:%d:%s", name, i+1, line))
			}
		}
	}
	return out
}

// cleanPaths drops what a Dagger glob returns that a file list must not carry:
// the directory entries it names with a trailing slash, and the "./" prefix a
// pattern may leave on.
func cleanPaths(files []string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		f = strings.TrimPrefix(f, "./")
		if f == "" || strings.HasSuffix(f, "/") {
			continue
		}
		out = append(out, f)
	}
	return out
}
