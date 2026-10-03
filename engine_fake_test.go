package main

// THE FAKE ENGINE. A Dagger module's package main cannot be unit-tested as
// generated: internal/dagger's own init() reads DAGGER_SESSION_PORT and
// DAGGER_SESSION_TOKEN and panics without them, and the client it builds
// speaks GraphQL over HTTP to 127.0.0.1:<port>. So the test binary IS given a
// session — internal/checks/session.go defaults the two variables before
// internal/dagger initialises — and TestMain answers on that port with an
// engine made of paper: it parses each query's chain of fields, synthesises a
// response of the same shape, and records the query so a test can read the
// chain an atom built.
//
// WHAT THIS TESTS, AND WHAT IT CANNOT. It tests the module's own decisions:
// which image, which env, which mounts, which exec runs under which Expect,
// what the atom does with an exit code, an output, a missing file, an engine
// error. That is every line in atoms_*.go, and it is why the mutation lane can
// see this package at all — MEASURED 2026-09-12 on PR #31: 370 mutants in
// atoms_*.go NOT COVERED because nothing could execute them. It cannot test
// whether `go vet` finds anything; the engine and the lane images do that, and
// the module is verified against them by `dagger call` before it lands.
//
// The paper engine is deliberately literal-minded: a leaf's value comes from
// the tree a test declared (entries, glob, contents), from a script the test
// wrote for this chain (exitCode, stdout, stderr, a failure), or from a
// default that reads as "nothing happened" (exit 0, empty output). A query it
// cannot answer is a test failure, never a guess.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// engine is the one fake, shared by every test in the package; reset() between
// tests. Tests in this package do not run in parallel.
var engine = &fakeEngine{}

// mergeBaseNeedle is run.changeBase's second exec for a pull based at abc123,
// and sinceSha is what every diff-scoped fixture has it answer: the change
// set's origin, distinct from the base the door named, so a test that reads
// the base where the merge base belongs fails to match.
const (
	mergeBaseNeedle = `"git","merge-base","abc123","HEAD"`
	sinceSha        = "since0"
)

type fakeEngine struct {
	mu      sync.Mutex
	tree    map[string]string // path -> contents; a directory exists when a file is under it
	scripts []script
	queries []string
}

type script struct {
	match string // substring of the query text; "" matches every query
	leaf  string // which leaf this answers: exitCode, stdout, stderr, sync, any ("" = any)
	value any
	fail  string // when set, the query answers a GraphQL error with this message
}

func (e *fakeEngine) reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.tree = map[string]string{}
	e.scripts = nil
	e.queries = nil
}

// withTree declares the repository under check. Directories are implied by
// the files beneath them; declare an empty directory with a trailing slash
// and any value.
func (e *fakeEngine) withTree(files map[string]string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for k, v := range files {
		e.tree[k] = v
	}
}

// exitCode scripts the exit code of every exec whose query text contains
// match. A test naming the tool's argv (`"go","vet"`) scripts that exec alone.
func (e *fakeEngine) exitCode(match string, code int) {
	e.script(script{match: match, leaf: "exitCode", value: code})
}
func (e *fakeEngine) stdout(match, out string) {
	e.script(script{match: match, leaf: "stdout", value: out})
}
func (e *fakeEngine) stderr(match, out string) {
	e.script(script{match: match, leaf: "stderr", value: out})
}

// contents scripts the body of a file an atom reads back OUT OF A CONTAINER, as
// opposed to out of the repository's tree — how a test hands the mutation lane a
// control's JSON report, which the container wrote and no tree holds.
func (e *fakeEngine) contents(match, out string) {
	e.script(script{match: match, leaf: "contents", value: out})
}

// label scripts an image label read off every chain whose query text contains
// match — how a test hands the build lane a :stable's revision.
func (e *fakeEngine) label(match, value string) {
	e.script(script{match: match, leaf: "label", value: value})
}

// fail makes every query whose text contains match answer an engine error —
// what a provisioning exec under the default Expect, an unpullable image or a
// dead engine look like to the SDK.
func (e *fakeEngine) fail(match, message string) { e.script(script{match: match, fail: message}) }

// failLeaf is fail() narrowed to ONE leaf: the chain answers normally until the
// atom reads that field. It is how "the exec ran and its output could not be
// read" is written — output() and outputBoth() read two and three leaves off
// one container, and each read is its own chance for the engine to go away.
func (e *fakeEngine) failLeaf(match, leaf, message string) {
	e.script(script{match: match, leaf: leaf, fail: message})
}

func (e *fakeEngine) script(s script) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.scripts = append(e.scripts, s)
}

// chains answers every query recorded since reset, newest last.
func (e *fakeEngine) chains() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.queries...)
}

// chain answers the newest recorded query whose text contains every needle,
// or "" — the way a test reads back the chain an atom built.
func (e *fakeEngine) chain(needles ...string) string {
	for _, q := range reverse(e.chains()) {
		ok := true
		for _, n := range needles {
			if !strings.Contains(q, n) {
				ok = false
				break
			}
		}
		if ok {
			return q
		}
	}
	return ""
}

func reverse(s []string) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[len(s)-1-i] = v
	}
	return out
}

// field is one step of a query chain: name and the raw argument text.
type field struct {
	name string
	args string
}

// parseChain reads a querybuilder query — `query Query {a(x:1){b{leaf}}}` —
// into its fields, leaf last. Braces inside string literals do not nest.
func parseChain(q string) []field {
	var fields []field
	i := strings.Index(q, "{")
	if i < 0 {
		return nil
	}
	rest := q[i+1:]
	for {
		rest = strings.TrimSpace(rest)
		if rest == "" || rest[0] == '}' {
			return fields
		}
		j := 0
		for j < len(rest) && (rest[j] == '_' || rest[j] >= 'a' && rest[j] <= 'z' || rest[j] >= 'A' && rest[j] <= 'Z' || rest[j] >= '0' && rest[j] <= '9') {
			j++
		}
		f := field{name: rest[:j]}
		rest = strings.TrimSpace(rest[j:])
		if strings.HasPrefix(rest, "(") {
			end := matching(rest, '(', ')')
			f.args = rest[1:end]
			rest = strings.TrimSpace(rest[end+1:])
		}
		fields = append(fields, f)
		if strings.HasPrefix(rest, "{") {
			rest = rest[1:]
			continue
		}
		return fields
	}
}

// matching finds the index of the close bracket balancing s[0], skipping
// string literals.
func matching(s string, open, close byte) int {
	depth, inStr := 0, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			if c == '\\' {
				i++
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(s) - 1
}

var strArgRE = regexp.MustCompile(`(\w+):\s*"((?:[^"\\]|\\.)*)"`)

// arg reads a string argument out of a field's argument text.
func (f field) arg(name string) (string, bool) {
	for _, m := range strArgRE.FindAllStringSubmatch(f.args, -1) {
		if m[1] == name {
			var s string
			if err := json.Unmarshal([]byte(`"`+m[2]+`"`), &s); err != nil {
				return m[2], true
			}
			return s, true
		}
	}
	return "", false
}

// dirOf answers the directory a chain is standing in: the last directory(path)
// step, or the root.
// gitMount is where an atom mounts the repository a git field names —
// runtime.go's withDies — so a test can place that checkout in
// the tree under the same path the atoms read it at.
func gitMount(git field) string {
	url, _ := git.arg("url")
	switch {
	case strings.Contains(url, "foundry-dies"):
		return "/dies"
	case strings.Contains(url, "foundry-stocks"):
		return "/stocks"
	case strings.Contains(url, "foundry/flux"):
		return "/flux"
	case strings.Contains(url, "forge-testkit-go"):
		return "/testkit"
	case strings.Contains(url, "/ourea"):
		return "/ourea"
	}
	return ""
}

// hasDir answers whether the tree holds anything under dir.
func (e *fakeEngine) hasDir(dir string) bool {
	for k := range e.tree {
		if strings.HasPrefix(k, dir+"/") {
			return true
		}
	}
	return false
}

func dirOf(fields []field) string {
	dir := ""
	for _, f := range fields {
		if f.name == "directory" {
			if p, ok := f.arg("path"); ok {
				dir = path.Join(dir, p)
			}
		}
	}
	return dir
}

func (e *fakeEngine) entries(dir string) []string {
	seen := map[string]bool{}
	var out []string
	prefix := ""
	if dir != "" {
		prefix = strings.TrimSuffix(dir, "/") + "/"
	}
	for p := range e.tree {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		rest := strings.TrimPrefix(p, prefix)
		if rest == "" {
			continue
		}
		if k := strings.IndexByte(rest, '/'); k >= 0 {
			rest = rest[:k+1] // a directory, named with the trailing slash the engine uses
		}
		if !seen[rest] {
			seen[rest] = true
			out = append(out, rest)
		}
	}
	sort.Strings(out)
	return out
}

// globRE turns a Dagger glob into a regexp: ** crosses directories, * and ?
// do not.
func globRE(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch {
		case c == '*' && i+1 < len(pattern) && pattern[i+1] == '*':
			i++
			if i+1 < len(pattern) && pattern[i+1] == '/' {
				i++
				b.WriteString(`(?:.*/)?`)
			} else {
				b.WriteString(`.*`)
			}
		case c == '*':
			b.WriteString(`[^/]*`)
		case c == '?':
			b.WriteString(`[^/]`)
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

func (e *fakeEngine) glob(dir, pattern string, gitless bool) []string {
	re := globRE(pattern)
	var out []string
	prefix := ""
	if dir != "" {
		prefix = strings.TrimSuffix(dir, "/") + "/"
	}
	for p := range e.tree {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		rel := strings.TrimPrefix(p, prefix)
		if gitless && (rel == ".git" || strings.HasPrefix(rel, ".git/")) {
			continue
		}
		// A DIRECTORY MATCHES ITS OWN NAME, measured against the cluster engine
		// 2026-09-15: glob(".forgejo") over a tree holding .forgejo/workflows/ci.yml
		// answers [".forgejo/"], and glob("**") lists every directory with its
		// trailing separator alongside the files. A matrix pins a whole tree
		// ABSENT by naming it, so reading the name as no match would pass a
		// guard that the engine fails.
		if re.MatchString(rel) || (strings.HasSuffix(rel, "/") && re.MatchString(strings.TrimSuffix(rel, "/"))) {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	if out == nil {
		out = []string{}
	}
	return out
}

// answer builds the response for one query.
func (e *fakeEngine) answer(q string) (data any, errMsg string) {
	fields := parseChain(q)
	if len(fields) == 0 {
		return nil, "the paper engine could not parse: " + q
	}
	e.mu.Lock()
	tree := e.tree
	scripts := append([]script(nil), e.scripts...)
	e.mu.Unlock()
	_ = tree

	leaf := fields[len(fields)-1]
	for _, s := range scripts {
		if s.fail != "" && strings.Contains(q, s.match) && (s.leaf == "" || s.leaf == leaf.name) {
			return nil, s.fail
		}
	}
	scripted := func(name string) (any, bool) {
		var v any
		found := false
		for _, s := range scripts { // last matching script wins
			if s.fail == "" && s.leaf == name && strings.Contains(q, s.match) {
				v, found = s.value, true
			}
		}
		return v, found
	}

	// THE SIGNAL RANGE IS NOT AN EXIT. Every Expect the SDK has covers exit
	// codes 0-127 and 192-255 at most (internal/dagger: ReturnTypeAny), so the
	// cluster engine answers an exec that ends in 128-191 — git's fatal 128, an
	// OOM kill's 137 — with an error on every read off it, never with the code.
	// Answered literally, build.go's detect passed on a merge-base 128 the
	// cluster never returned (foundry-tools #64).
	if v, ok := scripted("exitCode"); ok && strings.Contains(q, "withExec") {
		if code, _ := v.(int); code >= 128 && code <= 191 {
			return nil, fmt.Sprintf("exit code: %d", code)
		}
	}

	var val any
	switch leaf.name {
	case "exitCode":
		val = 0
		if v, ok := scripted("exitCode"); ok {
			val = v
		}
	case "stdout", "stderr":
		val = ""
		if v, ok := scripted(leaf.name); ok {
			val = v
		}
	case "entries":
		if fields[0].name == "git" {
			val = []string{}
			break
		}
		val = e.entries(dirOf(fields))
	case "glob":
		p, _ := leaf.arg("pattern")
		gitless := strings.Contains(q, "filter(")
		dir := dirOf(fields)
		// A glob over a repository the atoms read at its one home (the dies,
		// the stocks) lists the checkout the test placed under its mount path,
		// the way `contents` reads one — so a test can hand an atom the fleet's
		// records and let it find the one it needs.
		if fields[0].name == "git" {
			if mount := gitMount(fields[0]); mount != "" && e.hasDir(mount) {
				dir = path.Join(mount, dir)
			} else {
				val = []string{}
				break
			}
		}
		val = e.glob(dir, p, gitless)
	case "contents":
		// A scripted answer first: a test may hand an atom the file a body
		// wrote inside the container (an rc file, a reason) without placing it
		// in the repository's tree.
		if v, ok := scripted("contents"); ok {
			val = v
			break
		}
		if fields[0].name == "git" {
			// foundry-stocks / foundry-dies: every script and ruleset the atoms
			// read at its one home is present, and says so — UNLESS the test
			// placed a checkout of that repository in the tree under its mount
			// path (/dies/…), in which case the checkout is what is
			// read, and a path it lacks is absent. That is how a test hands an
			// atom a star's RECORD (fleet/stars/<star>/slag.json) and how it
			// models a star the dies do not know.
			if mount := gitMount(fields[0]); mount != "" && e.hasDir(mount) {
				fp := ""
				for _, f := range fields {
					if f.name == "file" {
						if p, ok := f.arg("path"); ok {
							fp = path.Join(mount, p)
						}
					}
				}
				c, ok := e.tree[fp]
				if !ok {
					return nil, fmt.Sprintf("no such file or directory: %s", fp)
				}
				val = c
				break
			}
			val = "# the paper engine's copy\n"
			break
		}
		fp := ""
		for _, f := range fields {
			if f.name == "file" {
				if p, ok := f.arg("path"); ok {
					fp = path.Join(dirOf(fields), p)
				}
			}
		}
		c, ok := e.tree[fp]
		if !ok {
			return nil, fmt.Sprintf("no such file or directory: %s", fp)
		}
		val = c
	case "label":
		// An image label: what the test scripted for this chain, or none.
		val = ""
		if v, ok := scripted("label"); ok {
			val = v
		}
	case "size":
		val = 1
	case "plaintext":
		// A secret's value is the plaintext its setSecret was given: the one
		// way a test hands a lane a credential.
		val = ""
		if fields[0].name == "setSecret" {
			if p, ok := fields[0].arg("plaintext"); ok {
				val = p
			}
		}
	case "id", "sync":
		val = fakeID(q)
	case "publish":
		// The registry's answer to a push: the ref at the digest it minted.
		// Scripted by a test that wants a particular digest; otherwise the
		// address pushed, at a digest that is a function of the chain.
		val = "registry.notusmi.com/rob/x@sha256:" + strings.Repeat("d", 64)
		if v, ok := scripted("publish"); ok {
			val = v
		}
	case "export":
		p, _ := leaf.arg("path")
		val = p
	default:
		return nil, "the paper engine has no answer for leaf " + leaf.name + " in: " + q
	}
	out := map[string]any{leaf.name: val}
	for i := len(fields) - 2; i >= 0; i-- {
		out = map[string]any{fields[i].name: out}
	}
	return out, ""
}

// execError is the extensions the cluster engine attaches to a signal-range
// exec's error, which the SDK reads as an ExecError: the exit code and what the
// exec printed before it ended (measured 2026-10-02 against the cluster engine,
// v0.21.9: `sh -c 'echo out; echo err >&2; kill -9 $$'` under Expect ANY
// answered ExecError{ExitCode: 137, Stdout: "out", Stderr: "err"}). nil for
// every other query.
func (e *fakeEngine) execError(q string) map[string]any {
	if !strings.Contains(q, "withExec") {
		return nil
	}
	e.mu.Lock()
	scripts := append([]script(nil), e.scripts...)
	e.mu.Unlock()
	ext := map[string]any{"_type": "EXEC_ERROR", "stdout": "", "stderr": ""}
	code := -1
	for _, s := range scripts { // last matching script wins, as in answer
		if s.fail != "" || !strings.Contains(q, s.match) {
			continue
		}
		switch s.leaf {
		case "exitCode":
			code, _ = s.value.(int)
		case "stdout", "stderr":
			ext[s.leaf] = s.value
		}
	}
	if code < 128 || code > 191 {
		return nil
	}
	ext["exitCode"] = code
	return ext
}

// fakeID is the id this engine answers for a query, and it is A FUNCTION OF
// THE QUERY — which is what lets a test follow an object from the chain that
// BUILT it to the call that CONSUMED it. The querybuilder marshals an object
// argument as its id, so `withMountedDirectory(path:"/src", source:"id-...")`
// names a directory whose own chain was recorded separately; idOf is how a
// test says which one.
func fakeID(q string) string {
	sum := sha256.Sum256([]byte(q))
	return "id-" + hex.EncodeToString(sum[:4])
}

func (e *fakeEngine) serve(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	var req struct {
		Query string `json:"query"`
	}
	_ = json.Unmarshal(b, &req)
	e.mu.Lock()
	e.queries = append(e.queries, req.Query)
	e.mu.Unlock()
	data, errMsg := e.answer(req.Query)
	w.Header().Set("Content-Type", "application/json")
	if errMsg != "" {
		gqlErr := map[string]any{"message": errMsg}
		if ext := e.execError(req.Query); ext != nil {
			gqlErr["extensions"] = ext
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]any{gqlErr}})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func TestMain(m *testing.M) {
	port := os.Getenv("DAGGER_SESSION_PORT")
	if _, err := strconv.Atoi(port); err != nil {
		// Unreachable in practice: internal/dagger's init already panicked.
		fmt.Fprintln(os.Stderr, "DAGGER_SESSION_PORT must be set to run the module's tests (see engine_fake_test.go)")
		os.Exit(2)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "the paper engine could not listen on %s: %v\n", port, err)
		os.Exit(2)
	}
	srv := &http.Server{Handler: http.HandlerFunc(engine.serve)}
	go func() { _ = srv.Serve(ln) }()
	code := m.Run()
	_ = srv.Close()
	os.Exit(code)
}

// TestThePaperEngineErrorsInTheSignalRange pins the edges the cluster engine
// keeps: 127 and 192 are codes an atom reads; 128 through 191 are errors.
func TestThePaperEngineErrorsInTheSignalRange(t *testing.T) {
	const q = `query Query {container{withExec(args:["git","rev-parse"], expect:ANY){exitCode}}}`
	for _, tc := range []struct {
		code    int
		errored bool
	}{{127, false}, {128, true}, {137, true}, {191, true}, {192, false}} {
		engine.reset()
		engine.exitCode(`"git","rev-parse"`, tc.code)
		data, errMsg := engine.answer(q)
		if tc.errored {
			if want := fmt.Sprintf("exit code: %d", tc.code); errMsg != want || data != nil {
				t.Errorf("exit %d must answer the engine's error %q, got %v / %q", tc.code, want, data, errMsg)
			}
			continue
		}
		if errMsg != "" {
			t.Errorf("exit %d is a code the atom reads, got the error %q", tc.code, errMsg)
			continue
		}
		got := data.(map[string]any)["container"].(map[string]any)["withExec"].(map[string]any)["exitCode"]
		if got != tc.code {
			t.Errorf("exit %d must answer as the code, got %v", tc.code, got)
		}
	}
	engine.reset()
}

// ---- helpers every lane's tests use ----

// goTree is a tree that declares every lane, so an atom's happy path has a
// surface to stand on.
// NO ci-matrix.toml HERE, since 2026-09-23 (CA F18). template:render-matrix
// was sweep:template-render-matrix, on a clock and never in a pull; it is
// prepush now, so a ci-matrix.toml in the shared tree would make it run — and
// FAIL, for want of a scripted copier render — inside every push-stage test
// that is about something else. The template lane supplies its own surface;
// see templateTree.
var everyLaneTree = map[string]string{
	"go.mod":       "module x\n\ngo 1.26\n",
	"main.go":      "package main\n",
	"main_test.go": "package main\n",
	// The dependency array is spelled one entry per line because that is what
	// checks.DeclaresForgeTestkit reads — a quoted entry at the start of a
	// line, never the bare word. On one line the tree LOOKED like it took the
	// dependency and the three forge-testkit atoms stood down ABSENT against
	// it, so their happy path had no surface here.
	"pyproject.toml":                   "[project]\nname = \"x\"\ndependencies = [\n    \"forge-testkit>=1\",\n]\n",
	"src/x.py":                         "",
	"tests/test_x.py":                  "",
	"Cargo.toml":                       "[package]\nname = \"x\"\n",
	"package.json":                     "{}",
	"a.test.ts":                        "",
	"rules/sast/go.yml":                "rules:\n  - languages: [go, python, rust, typescript]\n",
	"orbit.toml":                       "",
	"policy/.manifest":                 "",
	"policy/admission/x.rego":          "",
	"fleet/stars/x/x.slag":             "{}",
	"tests/fixtures/ouranos-self.json": "{}",
	"tools/check_contracts.py":         "",
	"tests/contracts/fixtures.toml":    "",
	"contracts/contracts.toml":         "",
	"schema/slag.schema.json":          "{}",
	"schema/slag-v2.schema.json":       "{}",
	"compose.yaml":                     "services: {}\n",
	"Dockerfile":                       "FROM scratch\n",
	".forgejo/workflows/ci.yml":        "uses: foundry/foundry-stocks/.forgejo/workflows/build.yml@main\nimage: x@sha256:" + strings.Repeat("a", 64) + "\n",
	"flux/x.yaml":                      "kind: Deployment\n",
	// The matrix the render-matrix atom reads: one case, so a test can seed
	// its rendered tree at /out/<name>.
	".git/HEAD":           "ref: refs/heads/main\n",
	".copier-answers.yml": "critical_modules: src/x.py\n",
}

// runAtom runs one atom against the fake engine and answers its verdict.
func runAtom(t *testing.T, id string, base string) checks.Verdict {
	t.Helper()
	fn, ok := registry[id]
	if !ok {
		t.Fatalf("%s has no runner", id)
	}
	return fn(context.Background(), newRun(dag.Directory(), "", base))
}

// hasCall reports whether the chain carries a call to name whose argument
// text contains every needle. THE QUERYBUILDER DOES NOT ORDER ARGUMENTS —
// `withEnvVariable(name:"X", value:"y")` and `withEnvVariable(value:"y",
// name:"X")` are the same call on the wire — so a test that greps for one
// spelling flickers. This looks inside each call's parentheses instead.
func hasCall(chain, name string, needles ...string) bool {
	for i := 0; ; {
		k := strings.Index(chain[i:], name+"(")
		if k < 0 {
			return false
		}
		start := i + k + len(name)
		end := start + matching(chain[start:], '(', ')')
		args := chain[start : end+1]
		ok := true
		for _, n := range needles {
			if !strings.Contains(args, n) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
		i = end + 1
	}
}

// lastCall answers the offset of the LAST call to name whose arguments carry
// every needle, or -1 — hasCall's search, for the tests that pin ORDER between
// calls. The same argument-order rule applies: `withMountedDirectory(path:"/src"`
// is one of two spellings the querybuilder emits for one call, and a LastIndex
// on that spelling read -1 on foundry-tools main 2d881a5, where the chain was
// spelled `withMountedDirectory(source:…, path:"/src")` — a red tip on a test
// that pins nothing about arguments.
func lastCall(chain, name string, needles ...string) int {
	last := -1
	for i := 0; ; {
		k := strings.Index(chain[i:], name+"(")
		if k < 0 {
			return last
		}
		start := i + k
		argStart := start + len(name)
		end := argStart + matching(chain[argStart:], '(', ')')
		args := chain[argStart : end+1]
		ok := true
		for _, n := range needles {
			if !strings.Contains(args, n) {
				ok = false
				break
			}
		}
		if ok {
			last = start
		}
		i = end + 1
	}
}

// wantCalls fails the test for every call the chain lacks; each want is a
// call name followed by needles that must all sit inside its arguments.
func wantCalls(t *testing.T, chain string, wants ...[]string) {
	t.Helper()
	for _, w := range wants {
		if !hasCall(chain, w[0], w[1:]...) {
			t.Errorf("chain lacks %s(%s):\n%s", w[0], strings.Join(w[1:], " "), chain)
		}
	}
}

// wantState fails the test unless the verdict carries the state and its
// reason contains every needle.
func wantState(t *testing.T, v checks.Verdict, state int, needles ...string) {
	t.Helper()
	if v.State != state {
		t.Fatalf("%s: state %d (%s), want %d\n%s", v.Atom, v.State, v.Result, state, v.Reason)
	}
	for _, n := range needles {
		if !strings.Contains(v.Reason, n) {
			t.Errorf("%s: reason lacks %q:\n%s", v.Atom, n, v.Reason)
		}
	}
}
