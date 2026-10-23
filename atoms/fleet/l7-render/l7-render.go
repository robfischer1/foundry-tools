// Package fleetl7render is the atom fleet:l7-render, the gate on flux's L7 policy
// for the REST doors (prime/first-l7-enforcement.yaml, nyx#22257).
//
// nyx's render worker re-renders the hades-sa paths of that file whenever a star's
// served verbs change, and publishes what it read beside it:
// prime/first-l7-enforcement.openapi/<star>.json (the routes hades loaded) and
// grants.json (the verbs the compiled grants let hades-sa call, per star). This
// atom is the backstop on those three:
//
//   - CONSISTENCY. Every verb grants.json lists for a star has its published
//     routes, and every one of those routes is matched by a rule of that star's
//     hades-sa ingress in the committed file. A verb a star serves and authz
//     allows with no path is a 403 at the network (daedalus ci_canary, flux#5839).
//     A route the verb's OWN rule carves out is not a missing path: grants.json
//     is verb-level, but the compiled grants' rob_tier names the sub-routes of a
//     verb only Charon may call (ourea merge force, retire confirm, ...), and the
//     renderer writes that verb's rule as every sub-path except a segment that
//     is one of those words (nyx verbPath/segmentNotIn, fed only by robDeny).
//     So a route the rules refuse, but admit once its words are neutral, and
//     refuse again with any one carved word put back, is that declaration read
//     off the committed rule (flux#5952), and is counted, not refused.
//   - SCOPE. The door lets nyx push anywhere (the git_edge row is unscoped), so
//     a pull carrying a commit authored or committed as nyx may change only the
//     file and the published copies.
//
// It reads committed files and the pull's own history; it dials nothing.
package fleetl7render

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Def is the atom.
var Def = atomkit.Def{
	ID: "fleet:l7-render", Stage: checks.StagePrepush, Lane: checks.LaneAny,
	Group:     checks.GroupBasic,
	Desc:      "flux's prime/first-l7-enforcement.yaml gives every verb a star serves and the grants allow (the published prime/first-l7-enforcement.openapi/) a matching hades-sa path, and a pull with a commit by nyx's render worker changes only that file and its published copies.",
	Surface:   "any:**",
	Toolchain: "none",
	Check:     check,
}

const id = "fleet:l7-render"

// row is the part of the catalogue row a verdict reads: its identity.
var row = checks.AtomDef{ID: id, Stage: checks.StagePrepush, Lane: checks.LaneAny}

const (
	// File is the policy; Published the directory of copies beside it.
	File      = "prime/first-l7-enforcement.yaml"
	Published = "prime/first-l7-enforcement.openapi/"
	// Nyx is the address nyx's render worker commits as.
	Nyx     = "nyx@pantheon.foundry"
	hadesSA = "hades-sa"
)

type route struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

type publishedStar struct {
	Routes map[string][]route `json:"routes"`
}

type publishedGrants struct {
	Stars map[string][]string `json:"stars"`
}

func check(ctx context.Context, in atomkit.In) checks.Verdict {
	settle := func(state checks.State, reason string) checks.Verdict {
		return checks.VerdictOf(row, int(state), id+": "+reason)
	}
	file, err := os.ReadFile(filepath.Join(in.Root, File))
	if os.IsNotExist(err) {
		return settle(checks.StatePass, "ABSENT - this tree does not hold "+File+", so it holds no L7 policy for the REST doors")
	}
	if err != nil {
		return settle(checks.StateCannotRun, "CANNOT RUN - "+File+" would not read: "+err.Error())
	}
	var findings []string
	gaps, carved, err := unpathed(in.Root, string(file))
	if err != nil {
		return settle(checks.StateCannotRun, "CANNOT RUN - "+err.Error())
	}
	findings = append(findings, gaps...)
	outside, err := scope(ctx, in)
	if err != nil {
		return settle(checks.StateCannotRun, "CANNOT RUN - "+err.Error())
	}
	findings = append(findings, outside...)
	if len(findings) > 0 {
		return settle(checks.StateFindings, "FINDINGS -\n  "+strings.Join(findings, "\n  "))
	}
	reason := "PASS - every published verb has its hades-sa path, and no nyx commit reaches outside the render"
	if carved > 0 {
		reason += fmt.Sprintf("; %d Rob-tier sub-routes are carved out by their verb's own hades-sa rule", carved)
	}
	return settle(checks.StatePass, reason)
}

// unpathed lists the published routes the committed file has no hades-sa rule
// for, and counts the ones their verb's rule deliberately carves out.
func unpathed(root, file string) ([]string, int, error) {
	raw, err := os.ReadFile(filepath.Join(root, Published, "grants.json"))
	if os.IsNotExist(err) {
		return nil, 0, nil // nothing published yet: the worker's first run is the baseline
	}
	if err != nil {
		return nil, 0, fmt.Errorf("%sgrants.json would not read: %w", Published, err)
	}
	var grants publishedGrants
	if err := json.Unmarshal(raw, &grants); err != nil {
		return nil, 0, fmt.Errorf("%sgrants.json does not parse: %w", Published, err)
	}
	rules := hadesRules(file)
	var out []string
	carved := 0
	for _, star := range sortedStars(grants.Stars) {
		doc, ok := rules[star]
		if !ok {
			out = append(out, fmt.Sprintf("%s lists %s but %s has no rest-mode grants-%s document with a hades-sa item", Published+"grants.json", star, File, star))
			continue
		}
		var pub publishedStar
		raw, err := os.ReadFile(filepath.Join(root, Published, star+".json"))
		if err == nil {
			err = json.Unmarshal(raw, &pub)
		}
		if err != nil {
			return nil, 0, fmt.Errorf("%s%s.json would not read: %w", Published, star, err)
		}
		for _, verb := range grants.Stars[star] {
			routes := pub.Routes[verb]
			if len(routes) == 0 {
				out = append(out, fmt.Sprintf("%s: verb %s is allowed but %s%s.json publishes no route for it", star, verb, Published, star))
			}
			for _, r := range routes {
				switch {
				case doc.match(r):
				case doc.carvedOut(r):
					carved++
				default:
					out = append(out, fmt.Sprintf("%s: verb %s is served on %s %s and allowed, but grants-%s has no hades-sa path for it", star, verb, r.Method, r.Path, star))
				}
			}
		}
	}
	return out, carved, nil
}

// neutral stands in for a word segment: no Rob-tier word spells it, and the
// renderer's segment class admits it.
const neutral = "_"

// word is a literal path segment that could be a Rob-tier word (an action, or
// force/confirm/live); a template variable is not one.
var word = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// carvedOut is whether the rules refuse r only for words the rule carves out:
// with every word segment after /v1/<verb> made neutral the route is admitted,
// and putting back at least one of them, alone, refuses it again. A route
// refused for any other reason (no rule for the verb, a rule that admits only
// the bare path, the wrong method) is admitted by no neutral form, so it stays
// a finding.
func (s ruleSet) carvedOut(r route) bool {
	segs := strings.Split(r.Path, "/")
	if len(segs) < 4 || segs[0] != "" || segs[1] != "v1" {
		return false
	}
	var at []int
	for i := 3; i < len(segs); i++ {
		if word.MatchString(segs[i]) {
			at = append(at, i)
		}
	}
	keeping := func(keep int) route {
		out := slices.Clone(segs)
		for _, i := range at {
			if i != keep {
				out[i] = neutral
			}
		}
		return route{Method: r.Method, Path: strings.Join(out, "/")}
	}
	if len(at) == 0 || !s.match(keeping(-1)) {
		return false
	}
	return slices.ContainsFunc(at, func(i int) bool { return !s.match(keeping(i)) })
}

// ruleSet is the http rules of a document's ingress items that admit hades-sa.
type ruleSet struct {
	rules []struct {
		method string
		re     *regexp.Regexp
	}
}

// match is whether a rule admits the route: the method (empty: any) and the
// path regex Cilium would evaluate, against the route with its template
// variables filled.
func (s ruleSet) match(r route) bool {
	path := templateVar.ReplaceAllString(r.Path, "x")
	return slices.ContainsFunc(s.rules, func(h struct {
		method string
		re     *regexp.Regexp
	}) bool {
		return (h.method == "" || strings.EqualFold(h.method, r.Method)) && h.re.MatchString(path)
	})
}

var (
	templateVar = regexp.MustCompile(`\{[^}/]*\}`)
	nameRE      = regexp.MustCompile(`^  name: "?grants-([a-z0-9][a-z0-9-]*)"?\s*$`)
	restRE      = regexp.MustCompile(`render-mode: "?rest"?\s*$`)
	saRE        = regexp.MustCompile(`io\.cilium\.k8s\.policy\.serviceaccount"?:\s*"?([a-z0-9-]+)"?\s*$`)
	entryRE     = regexp.MustCompile(`^\s*- (method|path):\s*(.*?)\s*$`)
	pathRE      = regexp.MustCompile(`^\s*path:\s*(.*?)\s*$`)
)

// hadesRules reads the document's hades-sa rules per star. The file is emitted
// by nyx's renderer, so its layout is fixed: a document is the lines between
// "---", an ingress item starts at four spaces of indent.
func hadesRules(file string) map[string]ruleSet {
	out := map[string]ruleSet{}
	lines := strings.Split(file, "\n")
	from := 0
	for i := 0; i <= len(lines); i++ {
		if i < len(lines) && lines[i] != "---" {
			continue
		}
		star, set := document(lines[from:i])
		if star != "" && len(set.rules) > 0 {
			out[star] = set
		}
		from = i + 1
	}
	return out
}

func document(doc []string) (string, ruleSet) {
	var star string
	rest := false
	for _, l := range doc {
		if m := nameRE.FindStringSubmatch(l); m != nil {
			star = m[1]
		}
		rest = rest || restRE.MatchString(l)
	}
	var set ruleSet
	if !rest {
		return "", set
	}
	start := -1
	flush := func(end int) {
		if start >= 0 {
			set.add(doc[start:end])
		}
	}
	for i, l := range doc {
		if l == "    - fromEndpoints:" {
			flush(i)
			start = i
		}
	}
	flush(len(doc))
	return star, set
}

// add reads one ingress item into s when it admits hades-sa.
func (s *ruleSet) add(item []string) {
	admits, inHTTP := false, false
	for i := 0; i < len(item); i++ {
		l := item[i]
		switch {
		case strings.Contains(l, "toPorts:"):
			inHTTP = false
		case !inHTTP && saRE.MatchString(l):
			admits = admits || saRE.FindStringSubmatch(l)[1] == hadesSA
		case strings.TrimSpace(l) == "http:":
			inHTTP = true
		}
		m := entryRE.FindStringSubmatch(l)
		if !admits || !inHTTP || m == nil {
			continue
		}
		method, path := "", m[2]
		if m[1] == "method" {
			method, path = scalar(m[2]), ""
			if i+1 < len(item) {
				if p := pathRE.FindStringSubmatch(item[i+1]); p != nil {
					path = p[1]
					i++
				}
			}
		}
		re, err := regexp.Compile(scalar(path))
		if err != nil {
			continue // a rule Cilium could not compile admits nothing here either
		}
		s.rules = append(s.rules, struct {
			method string
			re     *regexp.Regexp
		}{method, re})
	}
}

// scalar reads a YAML scalar the emitter wrote: a JSON string, or a bare word.
func scalar(s string) string {
	if strings.HasPrefix(s, `"`) {
		var out string
		if json.Unmarshal([]byte(s), &out) == nil {
			return out
		}
	}
	return s
}

func sortedStars(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// scope lists the paths a pull with a nyx commit changes outside the render.
func scope(ctx context.Context, in atomkit.In) ([]string, error) {
	since := "HEAD~1"
	if in.Base != "" {
		out, err := git(ctx, in, "merge-base", in.Base, "HEAD")
		if err != nil || strings.TrimSpace(out) == "" {
			return nil, fmt.Errorf("the base %s is not in this history: %v", in.Base, err)
		}
		since = strings.TrimSpace(out)
	}
	who, err := git(ctx, in, "log", "--no-merges", "--format=%ae%x00%ce", since+"..HEAD")
	if err != nil {
		return nil, fmt.Errorf("the commits since %s would not list: %w", since, err)
	}
	if !nyxIn(who) {
		return nil, nil
	}
	diff, err := git(ctx, in, "diff", "-z", "--name-only", "--no-renames", since, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("the paths changed since %s would not list: %w", since, err)
	}
	var out []string
	for _, p := range checks.SplitNul(diff) {
		if p == File {
			continue
		}
		if name, ok := strings.CutPrefix(p, Published); ok && name != "" && !strings.Contains(name, "/") && strings.HasSuffix(name, ".json") {
			continue
		}
		out = append(out, fmt.Sprintf("a pull with a commit by %s may change only %s and %s*.json; this one also changes %s", Nyx, File, Published, p))
	}
	return out, nil
}

// nyxIn says whether any author or committer of a `%ae%x00%ce` listing is nyx.
func nyxIn(listing string) bool {
	for _, line := range strings.Split(listing, "\n") {
		for _, addr := range strings.Split(line, "\x00") {
			if strings.EqualFold(strings.TrimSpace(addr), Nyx) {
				return true
			}
		}
	}
	return false
}

// git runs one git command at the root; a non-zero exit is an error carrying its output.
func git(ctx context.Context, in atomkit.In, args ...string) (string, error) {
	out, code := in.Runner()(ctx, atoms.Cmd{Dir: in.Root, Name: "git", Args: append([]string{"-c", "safe.directory=*"}, args...)})
	if code != 0 {
		return out, fmt.Errorf("git %s exited %d: %.200s", args[0], code, strings.TrimSpace(out))
	}
	return out, nil
}
