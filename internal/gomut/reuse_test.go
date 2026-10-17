package gomut

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

var reuseKeys = []checks.UnitKey{{Unit: "internal/x", Hash: "hx", Ranges: "rx"}}

// A lookup over the door posts the keys under the run's token and believes only
// an answer about them; every other outcome is an error, which grades cold.
func TestALookupAsksTheDoorUnderTheRunsToken(t *testing.T) {
	var auth, body, path, kind string
	answer, status := `{"gradings":[{"lang":"go","unit":"internal/x","hash":"hx","ranges":"rx","run_number":7}],"misses":[]}`, http.StatusOK
	door := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		auth, body, path, kind = r.Header.Get("Authorization"), string(b), r.URL.Path, r.Header.Get("Content-Type")
		if strings.TrimSpace(auth) == "Bearer" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	}))
	defer door.Close()
	look := lookupVia(door.URL+"/rob/ares.git", "tok")
	got, err := look(context.Background(), "go", "E", reuseKeys)
	if err != nil || got["internal/x"].RunNumber != 7 {
		t.Fatalf("got %+v %v", got, err)
	}
	if auth != "Bearer tok" || path != "/ci/mutants" || kind != "application/json" || body != string(checks.LookupBody("go", "E", reuseKeys)) {
		t.Fatalf("the door saw %q %q %q %q", auth, path, kind, body)
	}

	status, answer = http.StatusUnauthorized, "that token names no mutation run tok"
	if _, err := look(context.Background(), "go", "E", reuseKeys); err == nil || !strings.Contains(err.Error(), "→ 401") ||
		strings.Contains(err.Error(), "tok\n") || !strings.Contains(err.Error(), "<token>") {
		t.Fatalf("a refusal: %v", err)
	}
	status, answer = http.StatusOK, `{"gradings":[{"lang":"go","unit":"elsewhere","hash":"hx","ranges":"rx"}]}`
	if _, err := look(context.Background(), "go", "E", reuseKeys); err == nil {
		t.Fatal("an answer about another unit was believed")
	}
	// The door answers a good grading from here on: each failure below must be the
	// client's own refusal, not the door's.
	status, answer = http.StatusOK, `{"gradings":[{"lang":"go","unit":"internal/x","hash":"hx","ranges":"rx","run_number":7}],"misses":[]}`
	for name, l := range map[string]gradingLookup{
		"an empty token, which the door refuses": lookupVia(door.URL+"/x.git", ""),
		"no repo URL":                            lookupVia("", "tok"),
		"a door that is gone":                    lookupVia("http://127.0.0.1:1/x.git", "tok"),
		"a bad URL":                              lookupVia("http://door\x7f/x.git", "tok"),
	} {
		if got, err := l(context.Background(), "go", "E", reuseKeys); err == nil || got != nil {
			t.Errorf("%s: %+v %v", name, got, err)
		}
	}
	if ep := mutantsEndpoint("https://git.notusmi.com/rob/ares.git"); ep != "https://git.notusmi.com/ci/mutants" {
		t.Fatalf("endpoint = %q", ep)
	}
	if ep := mutantsEndpoint("http://ourea:8215/rob/ares.git"); ep != "http://ourea:8215/ci/mutants" {
		t.Fatalf("the executor's door: endpoint = %q", ep)
	}
	if ep := mutantsEndpoint(""); ep != "/ci/mutants" {
		t.Fatalf("no repo URL: %q", ep)
	}
	// A reply that is a page, not a sentence, is cut to its first line.
	long := strings.Repeat("x", 300)
	status, answer = http.StatusBadGateway, long+"\nsecond line"
	if _, err := look(context.Background(), "go", "E", reuseKeys); err == nil || strings.Contains(err.Error(), "second line") || !strings.Contains(err.Error(), "…") {
		t.Fatalf("a long reply: %v", err)
	}
}

// The sample is keyed on the commit alone, so the shas below are fixtures of the
// hash, not of time: x6's sha256 starts with 5, s255's with exactly 13, "a"'s
// with 202.
func TestTheAuditSampleIsKeyedOnTheCommit(t *testing.T) {
	if !auditSampled("x6") || auditSampled("s255") || auditSampled("a") {
		t.Fatal("the sample is sha256(sha)[0] < 13 (s255's is exactly 13)")
	}
}

// The credential arms the lookup, and is gone from the environment before any
// program starts: the tree under grade is code the pull wrote.
func TestTheTokenArmsTheLookupAndLeavesTheEnvironment(t *testing.T) {
	p := newPull(t, ".")
	if g := newRun(p.in); g.lookup != nil || g.audit {
		t.Error("a run with no token reuses nothing")
	}
	t.Setenv(atoms.EnvMutantsToken, "tok")
	t.Setenv(atoms.EnvSha, "x6")
	g := newRun(p.in)
	if g.lookup == nil || !g.audit {
		t.Errorf("a token and a sampled commit: %+v", g)
	}
	if v, ok := os.LookupEnv(atoms.EnvMutantsToken); ok && v != "" {
		t.Error("the token is still in the environment the tree's tests would inherit")
	}
	// the commit is the tree's HEAD when the run names none
	t.Setenv(atoms.EnvMutantsToken, "tok")
	t.Setenv(atoms.EnvSha, "")
	p.first(rule{"rev-parse HEAD", "s255\n", 0, ""})
	if g := newRun(p.in); g.lookup == nil || g.audit {
		t.Errorf("an unsampled head: %+v", g)
	}
}

// ---- the lookup, end to end: a door that answers, and what the atom does with it

// twoUnits is a pull that changed the root package and internal/x.
func twoUnits(p *pull) {
	root := p.in.Root
	p.first(rule{"diff --relative", "a.go\ninternal/x/x.go\n", 0, ""})
	p.first(rule{"ls-tree", "100644 blob b1\tgo.mod\x00100644 blob b2\ta.go\x00100644 blob b3\tinternal/x/x.go\x00", 0, ""})
	p.first(rule{"git -C " + root + " diff --unified=0", "+++ b/a.go\n@@ -1 +1 @@\n+x\n+++ b/internal/x/x.go\n@@ -1 +1 @@\n+y\n", 0, ""})
	p.first(rule{"-deps -test", "m\t" + root + "\t\nm/internal/x\t" + filepath.Join(root, "internal/x") + "\t\n", 0, ""})
	p.first(rule{"-f {{if or .GoFiles .CgoFiles}}", root + "\n" + filepath.Join(root, "internal/x") + "\n", 0, ""})
}

// door answers a lookup with a stored grading for the unit it names, at the key it
// was asked, and records what it was asked.
func door(t *testing.T, unit string, g map[string]any) (*httptest.Server, *[]byte) {
	t.Helper()
	var asked []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked, _ = io.ReadAll(r.Body)
		var req struct {
			Units []struct{ Lang, Unit, Hash, Ranges string }
		}
		_ = json.Unmarshal(asked, &req)
		out := []map[string]any{}
		for _, u := range req.Units {
			if u.Unit == unit {
				e := map[string]any{"lang": u.Lang, "unit": u.Unit, "hash": u.Hash, "ranges": u.Ranges}
				for k, v := range g {
					e[k] = v
				}
				out = append(out, e)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"gradings": out})
	}))
	t.Cleanup(srv.Close)
	return srv, &asked
}

// A unit a stored grading answers is not graded again: gomutants is handed only
// the misses, the verdict folds the reused unit in, and the run stores gradings
// only for what it graded.
func TestAReuseRunGradesOnlyItsMisses(t *testing.T) {
	p := newPull(t, ".")
	twoUnits(p)
	srv, asked := door(t, "internal/x", map[string]any{"lane": "mutation", "run_number": 9, "counts": map[string]int{"generated": 3, "killed": 3}})
	p.in.Origin = srv.URL + "/rob/x.git"
	t.Setenv(atoms.EnvMutantsToken, "tok")
	v := p.run()
	if v.State != 0 || !strings.Contains(said(v), "go:mutation: reused 1 of 2 unit(s), graded earlier at the same content, scope and engine: internal/x (mutation #9 @)") {
		t.Fatalf("%s", said(v))
	}
	if !strings.Contains(string(*asked), `"engine":"`+goMutationEngine()+`"`) {
		t.Errorf("the lookup was not asked as this engine: %s", *asked)
	}
	c, _ := p.call("-changed-since")
	if got := strings.Join(c.Args[len(c.Args)-2:], " "); got != "-changed-since "+sinceSha && !strings.HasSuffix(strings.Join(c.Args, " "), "-changed-since "+sinceSha+" .") {
		t.Errorf("gomutants was handed %q", c.Args)
	}
	if len(v.Gradings) != 1 || v.Gradings[0].Unit != "." || v.Audit != "" {
		t.Fatalf("the run stores only what it graded, and audits nothing unsampled: %+v %q", v.Gradings, v.Audit)
	}
}

// Every unit answered: nothing runs, and the verdict is the stored one's — a
// stored survivor is still a survivor.
func TestARunWhoseEveryUnitIsReusedRunsNothing(t *testing.T) {
	p := newPull(t, ".")
	lived := checks.ScoredMutant{File: "a.go", Line: 1, Col: 1, Op: "T", Outcome: checks.OutcomeLived, Status: "LIVED"}
	srv, _ := door(t, ".", map[string]any{"lane": "mutation", "run_number": 3,
		"counts": map[string]int{"generated": 2, "killed": 1, "lived": 1}, "mutants": []checks.ScoredMutant{lived}})
	p.in.Origin = srv.URL + "/rob/x.git"
	t.Setenv(atoms.EnvMutantsToken, "tok")
	v := p.run()
	if v.State != 1 || !strings.Contains(said(v), "1 mutant(s) survived") || !strings.Contains(said(v), "### Reused — 1 unit(s)") {
		t.Fatalf("%s", said(v))
	}
	if len(v.Findings) != 1 || v.Findings[0].Subject != "a.go:1" || len(v.Gradings) != 0 {
		t.Fatalf("findings %+v gradings %+v", v.Findings, v.Gradings)
	}
	if p.ran("gomutants") || p.ran("-cache=off") {
		t.Fatal("a run with nothing to grade ran the mutants or the controls")
	}
}

// A store that does not answer grades every unit, and says so.
func TestAStoreThatDoesNotAnswerGradesCold(t *testing.T) {
	p := newPull(t, ".")
	twoUnits(p)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	t.Cleanup(srv.Close)
	p.in.Origin = srv.URL + "/rob/x.git"
	t.Setenv(atoms.EnvMutantsToken, "tok")
	v := p.run()
	if v.State != 0 || !strings.Contains(said(v), "reuse: the store did not answer, so every unit was graded cold — POST "+srv.URL+"/ci/mutants → 502") {
		t.Fatalf("%s", said(v))
	}
	c, _ := p.call("-changed-since")
	if !strings.HasSuffix(strings.Join(c.Args, " "), "-changed-since "+sinceSha+" . ./internal/x") {
		t.Errorf("a cold run grades both units: %q", c.Args)
	}
	if len(v.Gradings) != 2 {
		t.Fatalf("a cold run stores every unit it graded: %+v", v.Gradings)
	}
}

// A sampled share of reuse runs grades every unit cold anyway and audits what the
// lookup answered; the verdict is the cold one.
func TestASampledReuseRunGradesColdAndAudits(t *testing.T) {
	p := newPull(t, ".")
	twoUnits(p)
	srv, _ := door(t, "internal/x", map[string]any{"lane": "mutation", "run_number": 9, "counts": map[string]int{"generated": 3, "killed": 3}})
	p.in.Origin = srv.URL + "/rob/x.git"
	t.Setenv(atoms.EnvMutantsToken, "tok")
	t.Setenv(atoms.EnvSha, "x6")
	v := p.run()
	if v.State != 0 || !strings.Contains(said(v), "go:mutation: audit — the 1 unit(s) a lookup answered were graded cold again: mismatch: internal/x") {
		t.Fatalf("%s", said(v))
	}
	c, _ := p.call("-changed-since")
	if !strings.HasSuffix(strings.Join(c.Args, " "), "-changed-since "+sinceSha+" . ./internal/x") {
		t.Errorf("an audited run grades both units cold: %q", c.Args)
	}
	if v.Audit != "mismatch: internal/x" || len(v.Gradings) != 2 {
		t.Fatalf("audit %q gradings %+v", v.Audit, v.Gradings)
	}
}

// EVERY RUN IS SCOPED TO THE UNITS IT GRADES, not only a reuse run: with no
// lookup at all gomutants is handed the changed packages and never ./..., so its
// coverage, baseline and per-test map run their suites and no other. A changed
// unit the module's `go list` does not name as a package with source — a
// test-only package, a nested module's directory — is not handed over, because
// `go test` would refuse the pattern and the run with it. Nothing left at all is
// ./..., the shape every run had before scoping.
func TestTheScopeIsTheUnitsItGradesThatTheModuleCanMutate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		listed func(p *pull) rule
		want   string
	}{
		{"both packages have source", func(p *pull) rule {
			return rule{"-f {{if or .GoFiles .CgoFiles}}", p.in.Root + "\n" + filepath.Join(p.in.Root, "internal/x") + "\n", 0, ""}
		}, ". ./internal/x"},
		{"a test-only package is left out", func(p *pull) rule {
			return rule{"-f {{if or .GoFiles .CgoFiles}}", p.in.Root + "\n", 0, ""}
		}, "."},
		{"nothing listed is the whole module", func(p *pull) rule { return rule{"-f {{if or .GoFiles .CgoFiles}}", "", 0, ""} }, "./..."},
		{"a listing that failed is the whole module", func(p *pull) rule { return rule{"-f {{if or .GoFiles .CgoFiles}}", "boom", 1, ""} }, "./..."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPull(t, ".")
			twoUnits(p)
			p.first(tc.listed(p))
			if v := p.run(); v.State != 0 {
				t.Fatalf("%s", said(v))
			}
			c, _ := p.call("-changed-since")
			if !strings.HasSuffix(strings.Join(c.Args, " "), "-changed-since "+sinceSha+" "+tc.want) {
				t.Errorf("gomutants was handed %q, want ...%s", c.Args, tc.want)
			}
		})
	}
}

// A tree whose HEAD cannot be read names no commit, and so is never sampled for
// the audit.
func TestAnUnreadableHeadIsNoCommit(t *testing.T) {
	p := newPull(t, ".")
	p.first(rule{"rev-parse HEAD", "fatal: no HEAD", 128, ""})
	if got := commit(p.in); got != "" {
		t.Errorf("commit = %q", got)
	}
}
