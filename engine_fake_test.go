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
		if re.MatchString(rel) {
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
		val = e.glob(dirOf(fields), p, gitless)
	case "contents":
		if fields[0].name == "git" {
			// foundry-stocks / foundry-dies: every script and ruleset the atoms
			// read at its one home is present, and says so.
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
	case "size":
		val = 1
	case "id", "sync":
		sum := sha256.Sum256([]byte(q))
		val = "id-" + hex.EncodeToString(sum[:4])
	default:
		return nil, "the paper engine has no answer for leaf " + leaf.name + " in: " + q
	}
	out := map[string]any{leaf.name: val}
	for i := len(fields) - 2; i >= 0; i-- {
		out = map[string]any{fields[i].name: out}
	}
	return out, ""
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
		_ = json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]any{{"message": errMsg}}})
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

// ---- helpers every lane's tests use ----

// goTree is a tree that declares every lane, so an atom's happy path has a
// surface to stand on.
var everyLaneTree = map[string]string{
	"go.mod":                           "module x\n\ngo 1.26\n",
	"main.go":                          "package main\n",
	"main_test.go":                     "package main\n",
	"pyproject.toml":                   "[project]\nname = \"x\"\ndependencies = [\"forge-testkit>=1\"]\n",
	"src/x.py":                         "",
	"tests/test_x.py":                  "",
	"Cargo.toml":                       "[package]\nname = \"x\"\n",
	"package.json":                     "{}",
	"a.test.ts":                        "",
	".secrets.baseline":                "{}",
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
	"ci-matrix.toml":                   "",
	".git/HEAD":                        "ref: refs/heads/main\n",
	".copier-answers.yml":              "critical_modules: src/x.py\n",
}

// runAtom runs one atom against the fake engine and answers its verdict.
func runAtom(t *testing.T, id string, base string) checks.Verdict {
	t.Helper()
	fn, ok := registry[id]
	if !ok {
		t.Fatalf("%s has no runner", id)
	}
	return fn(context.Background(), newRun(dag.Directory(), base))
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
