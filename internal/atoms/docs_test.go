package atoms

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/checks"
)

func TestCheckYAML(t *testing.T) {
	a := checks.AtomByID("fleet:check-yaml")
	for _, tc := range []struct {
		name      string
		files     map[string]string
		order     []string
		wantState int
		// wantLogs are substrings every one of which the logs must contain.
		wantLogs []string
	}{
		{"no YAML at all", nil, nil, 0, []string{"fleet:check-yaml: no YAML in this repository"}},
		{
			"files that are not YAML are not read, even when they are not YAML",
			map[string]string{"a.md": "a: b: c\n", "b.txt": "[\n", "c.YAML": "a: b: c\n"},
			[]string{"a.md", "b.txt", "c.YAML"}, 0, []string{"fleet:check-yaml: no YAML in this repository"},
		},
		{"a valid .yml", map[string]string{"a.yml": "a: 1\nb: [1, 2]\n"}, []string{"a.yml"}, 0, nil},
		{"a valid .yaml in a directory", map[string]string{"d/a.yaml": "- x\n- y\n"}, []string{"d/a.yaml"}, 0, nil},
		{"an empty file has no documents and parses", map[string]string{"a.yml": ""}, []string{"a.yml"}, 0, nil},
		{
			// A k3s manifest: pre-commit refuses the second document by default.
			"multiple documents are valid",
			map[string]string{"a.yaml": "---\na: 1\n---\nb: 2\n---\nc: 3\n"},
			[]string{"a.yaml"}, 0, nil,
		},
		{
			// Home Assistant's !include: --unsafe parses, it does not construct.
			"a custom tag is not a constructor error",
			map[string]string{"a.yaml": "homeassistant:\n  packages: !include_dir_named packages\n  secret: !secret x\n"},
			[]string{"a.yaml"}, 0, nil,
		},
		{
			"a python object tag parses",
			map[string]string{"a.yaml": "x: !!python/object:foo.Bar {a: 1}\n"},
			[]string{"a.yaml"}, 0, nil,
		},
		{
			"anchors and merge keys parse",
			map[string]string{"a.yaml": "base: &b {x: 1}\nuse:\n  <<: *b\n  y: 2\n"},
			[]string{"a.yaml"}, 0, nil,
		},
		{
			// yaml.v3 leaves the line out of an error on a file's FIRST line
			// (its parser reports line 0 as no line); every later line is named.
			"a mapping value where none is allowed names the file and the line",
			map[string]string{"bad.yaml": "ok: 1\nbad: b: c\n"},
			[]string{"bad.yaml"}, 1, []string{"bad.yaml: yaml: line 2:"},
		},
		{
			"an unclosed flow sequence",
			map[string]string{"bad.yml": "a: [1, 2\n"},
			[]string{"bad.yml"}, 1, []string{"bad.yml: yaml: "},
		},
		{
			"a tab where indentation belongs",
			map[string]string{"bad.yml": "a:\n\tb: 1\n"},
			[]string{"bad.yml"}, 1, []string{"bad.yml: yaml: "},
		},
		{
			"an error in the SECOND document is found",
			map[string]string{"bad.yaml": "---\na: 1\n---\nb: c: d\n"},
			[]string{"bad.yaml"}, 1, []string{"bad.yaml: yaml: line "},
		},
		{
			"every bad file is one line, in the population's order, and good files are not named",
			map[string]string{"z.yml": "a: b: c\n", "a.yml": "ok: 1\n", "m.yaml": "[\n"},
			[]string{"a.yml", "m.yaml", "z.yml"}, 1, []string{"m.yaml: yaml: ", "z.yml: yaml: mapping values are not allowed"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, body := range tc.files {
				put(t, dir, rel, body)
			}
			v := checkYAML(context.Background(), a, Input{Root: dir, Files: tc.order})
			if v.State != tc.wantState {
				t.Fatalf("state %d, want %d\n%s", v.State, tc.wantState, v.Reason)
			}
			logs := strings.Join(v.Logs, "\n")
			for _, want := range tc.wantLogs {
				if !strings.Contains(logs, want) {
					t.Errorf("logs lack %q:\n%s", want, logs)
				}
			}
			if tc.wantState == 1 {
				for _, line := range v.Logs {
					if strings.HasPrefix(line, "a.yml") {
						t.Errorf("a good file is named as bad: %q", line)
					}
				}
			}
		})
	}
}

// PARITY WITH THE CHAIN. pre-commit --unsafe reads parse events and passes an
// alias with no anchor; yaml.v3 rejects it while parsing, so the atom undefines
// the alias and parses again. A real syntax error is still a finding, with or
// without an undefined alias beside it.
func TestCheckYAMLAcceptsWhatTheChainAccepts(t *testing.T) {
	a := checks.AtomByID("fleet:check-yaml")
	for _, tc := range []struct {
		name      string
		body      string
		wantState int
		wantLog   string
	}{
		{"an alias with no anchor anywhere", "a: *b\n", 0, ""},
		{"a forward alias: the anchor comes after", "a: *b\nb: &b 1\n", 0, ""},
		{"an anchor defined earlier is fine", "b: &b 1\na: *b\n", 0, ""},
		{"an undefined alias in a flow sequence, twice", "a: [*b, *b]\n", 0, ""},
		{"an undefined alias as a merge key's value", "a:\n  <<: *base\n  c: 1\n", 0, ""},
		{"two different undefined aliases", "a: *x\nb: *y-1\n", 0, ""},
		{"an undefined alias in the second document", "---\na: 1\n---\nb: *c\n", 0, ""},
		{"a star inside a plain scalar is not an alias", "a: x*b\n", 0, ""},
		{"an undefined alias beside a real syntax error", "a: *b\nc: [1, 2\n", 1, "a.yaml: yaml: "},
		{"a syntax error alone", "a: [1, 2\n", 1, "a.yaml: yaml: "},
		{"an undefined alias after a tab indent", "a:\n\tb: *c\n", 1, "a.yaml: yaml: "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			put(t, dir, "a.yaml", tc.body)
			v := checkYAML(context.Background(), a, Input{Root: dir, Files: []string{"a.yaml"}})
			if v.State != tc.wantState {
				t.Fatalf("state %d, want %d\n%s", v.State, tc.wantState, v.Reason)
			}
			if logs := strings.Join(v.Logs, "\n"); !strings.Contains(logs, tc.wantLog) {
				t.Errorf("logs lack %q:\n%s", tc.wantLog, logs)
			}
		})
	}
}

// The rewrites are bounded: with none allowed, the parser's own complaint about
// the alias is the answer; with one, the alias is gone and the file parses.
func TestSyntaxErrorRewritesAreBounded(t *testing.T) {
	body := []byte("a: *b\n")
	if err := syntaxErrorWithin(body, 0); err == nil || !strings.Contains(err.Error(), "unknown anchor 'b'") {
		t.Errorf("no rewrite allowed: %v", err)
	}
	if err := syntaxErrorWithin(body, 1); err != nil {
		t.Errorf("one rewrite allowed: %v", err)
	}
	if err := syntaxError([]byte("a: *b\nc: *d\ne: *f\n")); err != nil {
		t.Errorf("three aliases, three rewrites: %v", err)
	}
}

// A finding never names the null the alias was rewritten to: the error is the
// file's own text.
func TestCheckYAMLFindingKeepsTheFilesOwnLine(t *testing.T) {
	err := syntaxError([]byte("a: *b\nc: d: e\n"))
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error %v, want one on line 2", err)
	}
}

// A pass is silent, as the hook it replaces is: the chain's pass carries no
// output, so a count printed here would make the two vectors differ in logs.
func TestCheckYAMLPassesSilently(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "a.yml", "a: 1\n")
	v := checkYAML(context.Background(), checks.AtomByID("fleet:check-yaml"), Input{Root: dir, Files: []string{"a.yml"}})
	if v.State != 0 || v.Result != "pass" || len(v.Logs) != 0 || v.Reason != "fleet:check-yaml: PASS" {
		t.Errorf("settled %+v", v)
	}
}

func TestCheckYAMLUnreadable(t *testing.T) {
	a := checks.AtomByID("fleet:check-yaml")
	dir := t.TempDir()
	put(t, dir, "bad.yml", "a: b: c\n")
	put(t, dir, "ok.yml", "a: 1\n")
	for _, tc := range []struct {
		name      string
		files     []string
		wantState int
	}{
		{"a YAML file that cannot be read is a could-not-run", []string{"gone.yml", "ok.yml"}, 2},
		{"a finding outranks it", []string{"gone.yml", "bad.yml"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if v := checkYAML(context.Background(), a, Input{Root: dir, Files: tc.files}); v.State != tc.wantState {
				t.Errorf("state %d, want %d\n%s", v.State, tc.wantState, v.Reason)
			}
		})
	}
}

func TestCheckYAMLRefusesATreeThatWouldNotEnumerate(t *testing.T) {
	v := checkYAML(context.Background(), checks.AtomByID("fleet:check-yaml"), Input{FilesErr: errors.New("boom")})
	if v.State != 2 || !strings.Contains(v.Reason, "the tree would not enumerate: boom") {
		t.Errorf("settled %d: %s", v.State, v.Reason)
	}
}

func TestCheckYAMLStopsWhenItsContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := t.TempDir()
	put(t, dir, "a.yml", "a: 1\n")
	if v := checkYAML(ctx, checks.AtomByID("fleet:check-yaml"), Input{Root: dir, Files: []string{"a.yml"}}); v.State != 2 {
		t.Errorf("a stopped scan is not a verdict about the tree: state %d", v.State)
	}
}

// The directive is assembled from fragments: written whole, the gate's own
// stop-justifications scan of THIS file would read it as a suppression.
const sjNoqa = "no" + "qa"

func TestStopJustifications(t *testing.T) {
	a := checks.AtomByID("fleet:stop-justifications")
	drive := "import subprocess\np = subprocess.Popen([x])  # " + sjNoqa + ": S603\n"
	now := time.Date(2026, 10, 8, 3, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name      string
		files     map[string]string
		origin    string
		wantState int
		wantLog   string
	}{
		{"a clean tree", map[string]string{"a.py": "x = 1\n"}, "", 0, "stop-justifications: 1 file(s) scanned, no undocumented suppressions."},
		{"an undocumented suppression", map[string]string{"a.py": "x = 1  # " + sjNoqa + ": BLE001\n"}, "", 1, "a.py"},
		{"the origin names the exemption: a path-style URL", map[string]string{"probes/seam/drive.py": drive}, "http://door/cerberus.git", 0, "excused by a DIRECTORY exemption in cerberus"},
		{"the origin names the exemption: an scp-style URL", map[string]string{"probes/seam/drive.py": drive}, "git@host:rob/cerberus.git", 0, "excused by a DIRECTORY exemption in cerberus"},
		{"another repository's exemption is not this one's", map[string]string{"probes/seam/drive.py": drive}, "http://door/notcerberus.git", 1, "probes/seam/drive.py"},
		{"no origin names no exemption", map[string]string{"probes/seam/drive.py": drive}, "", 1, "probes/seam/drive.py"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			var tracked []string
			for rel, body := range tc.files {
				put(t, dir, rel, body)
				tracked = append(tracked, rel)
			}
			v := stopJustifications(context.Background(), a, Input{Root: dir, Tracked: tracked, Origin: tc.origin, Now: now})
			if v.State != tc.wantState || !strings.Contains(strings.Join(v.Logs, "\n"), tc.wantLog) {
				t.Errorf("state %d, want %d, wanting %q:\n%s", v.State, tc.wantState, tc.wantLog, v.Reason)
			}
		})
	}
}

// Justification expiry is the reason this atom is whole-tree scope. The grant
// below ends 2026-10-20 (checks.DirectoryExemption); Now is converted to UTC
// before the date is taken, so 23:30 on the 20th in New York — already the
// 21st in UTC — is past it.
func TestStopJustificationsReadsTheUTCDate(t *testing.T) {
	a := checks.AtomByID("fleet:stop-justifications")
	dir := t.TempDir()
	put(t, dir, "bases/blade-go/Dockerfile", "FROM x\n# hado"+"lint ignore=DL3002\n")
	newYork := time.FixedZone("EST", -5*3600)
	for _, tc := range []struct {
		name      string
		now       time.Time
		wantState int
	}{
		{"the grant's last day, UTC", time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC), 0},
		{"the day after, UTC", time.Date(2026, 10, 21, 0, 0, 1, 0, time.UTC), 1},
		{"the 20th in New York is the 21st in UTC", time.Date(2026, 10, 20, 23, 30, 0, 0, newYork), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := Input{Root: dir, Tracked: []string{"bases/blade-go/Dockerfile"}, Origin: "http://door/foundry-stocks.git", Now: tc.now}
			if v := stopJustifications(context.Background(), a, in); v.State != tc.wantState {
				t.Errorf("state %d, want %d\n%s", v.State, tc.wantState, v.Reason)
			}
		})
	}
}

func TestStopJustificationsCannotRun(t *testing.T) {
	a := checks.AtomByID("fleet:stop-justifications")
	for _, tc := range []struct {
		name     string
		in       Input
		wantText string
	}{
		{"a tree that will not list", Input{TrackedErr: errors.New("git ls-files failed")}, "CANNOT RUN — git ls-files failed"},
		{"a tracked file that will not read", Input{Root: t.TempDir(), Tracked: []string{"gone.py"}}, "could not read gone.py"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := stopJustifications(context.Background(), a, tc.in)
			if v.State != 2 || !strings.Contains(strings.Join(v.Logs, "\n"), tc.wantText) {
				t.Errorf("state %d:\n%s", v.State, v.Reason)
			}
		})
	}
}
