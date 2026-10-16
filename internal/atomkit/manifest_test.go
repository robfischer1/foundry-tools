package atomkit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// repoRoot is the module root, two directories above this package.
const repoRoot = "../.."

// manifestFailures is TestManifests' walk over a tree rooted at root: one text
// per failure, in walk order (lld.md FN-090).
func manifestFailures(root string) []string {
	var out []string
	declared := map[string][]string{} // id -> the paths declaring it, sorted below
	var tomls []string
	_ = filepath.WalkDir(filepath.Join(root, "atoms"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Ext(p) == ".toml" {
			rel, _ := filepath.Rel(root, p)
			tomls = append(tomls, filepath.ToSlash(rel))
		}
		return nil
	})
	for _, rel := range tomls {
		raw, _ := os.ReadFile(filepath.Join(root, rel))
		m, err := ParseManifest(raw)
		if err != nil {
			out = append(out, rel+": "+err.Error())
			continue
		}
		ns, name, _ := strings.Cut(m.ID, ":")
		if want := "atoms/" + ns + "/" + name + ".toml"; rel != want {
			out = append(out, rel+" declares "+m.ID)
		}
		declared[m.ID] = append(declared[m.ID], rel)
	}
	for id, p := range logicDefs(root) {
		declared[id] = append(declared[id], p...)
	}
	ids := make([]string, 0, len(declared))
	for id := range declared {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if paths := declared[id]; len(paths) > 1 {
			sort.Strings(paths)
			out = append(out, id+" declared by "+paths[0]+" and "+paths[1])
		}
	}
	return out
}

// logicDefs are the ids the .go files under atoms/ declare, by path: the ID of
// every atomkit.Def composite literal.
func logicDefs(root string) map[string][]string {
	out := map[string][]string{}
	_ = filepath.WalkDir(filepath.Join(root, "atoms"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".go" || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		f, perr := parser.ParseFile(token.NewFileSet(), p, nil, 0)
		if perr != nil {
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok || !isDefType(lit.Type) {
				return true
			}
			for _, e := range lit.Elts {
				kv, ok := e.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if k, kok := kv.Key.(*ast.Ident); kok && k.Name == "ID" {
					if s, sok := kv.Value.(*ast.BasicLit); sok {
						id := strings.Trim(s.Value, `"`)
						out[id] = append(out[id], filepath.ToSlash(rel))
					}
				}
			}
			return true
		})
		return nil
	})
	return out
}

func isDefType(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Def"
}

// TestManifests is FN-090: every atoms/**/*.toml parses, sits where its id
// says, and declares an id nothing else declares.
func TestManifests(t *testing.T) {
	for _, f := range manifestFailures(repoRoot) {
		t.Error(f)
	}
}

// writeTree writes files (path -> body) under a temp dir and answers it.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, body := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestUT_508_AnUnknownKeyFailsTheWalk(t *testing.T) {
	root := writeTree(t, map[string]string{"atoms/go/vet.toml": manVet + "shell = true\n"})
	if got := manifestFailures(root); !slices.Equal(got, []string{"atoms/go/vet.toml: unknown key shell"}) {
		t.Fatalf("got %v", got)
	}
}

func TestUT_509_AnIdThePathDoesNotSayFailsTheWalk(t *testing.T) {
	root := writeTree(t, map[string]string{"atoms/go/vet.toml": strings.Replace(manVet, `id = "go:vet"`, `id = "go:build"`, 1)})
	if got := manifestFailures(root); !slices.Equal(got, []string{"atoms/go/vet.toml declares go:build"}) {
		t.Fatalf("got %v", got)
	}
}

func TestUT_510_AnIdDeclaredTwiceFailsTheWalk(t *testing.T) {
	root := writeTree(t, map[string]string{"atoms/go/vet.toml": manVet, "atoms/go/vet2.toml": manVet})
	got := manifestFailures(root)
	want := []string{"atoms/go/vet2.toml declares go:vet", "go:vet declared by atoms/go/vet.toml and atoms/go/vet2.toml"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
	root = writeTree(t, map[string]string{
		"atoms/go/vet.toml":        manVet,
		"atoms/go/vet/vet.go":      "package govet\n\nimport \"dagger/foundry-tools/internal/atomkit\"\n\nvar Def = atomkit.Def{ID: \"go:vet\"}\n",
		"atoms/go/vet/helper.go":   "package govet\n\nvar x = struct{ ID string }{ID: \"go:vet\"}\n",
		"atoms/go/vet/vet_test.go": "package govet\n\nvar y = atomkit.Def{ID: \"go:vet\"}\n",
		"atoms/go/vet/broken.go":   "package govet\n\nfunc (\n",
	})
	if got := manifestFailures(root); !slices.Equal(got, []string{"go:vet declared by atoms/go/vet.toml and atoms/go/vet/vet.go"}) {
		t.Fatalf("got %v", got)
	}
}

func TestUT_511_AShellScriptManifestFailsTheWalk(t *testing.T) {
	src := strings.Replace(manVet, `argv = ["go", "vet", "./..."]`, `argv = ["sh", "-c", "z"]`, 1)
	src = strings.Replace(src, `id = "go:vet"`, `id = "x:y"`, 1)
	root := writeTree(t, map[string]string{"atoms/x/y.toml": src})
	if got := manifestFailures(root); !slices.Equal(got, []string{"atoms/x/y.toml: argv runs sh with an inline script (-c)"}) {
		t.Fatalf("got %v", got)
	}
}

func TestUT_512_TheLandedManifestsPass(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(repoRoot, "atoms", "*", "*.toml"))
	if len(files) != 7 {
		t.Fatalf("%d manifests, want 7: %v", len(files), files)
	}
	if got := manifestFailures(repoRoot); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

// manifestRow is a row of lld.md SYS-004-MOD-002's table.
type manifestRow struct {
	file, id, stage, lane, surface, toolchain string
	argv                                      []string
	perModule                                 bool
	needs                                     []string
	env                                       map[string]string
	forkStyle                                 bool
}

var staticcheckArgv = []string{"staticcheck", "-checks", "all,-ST1000,-ST1003,-ST1016,-ST1020,-ST1021,-ST1022,-ST1023", "./..."}

var manifestRows = []manifestRow{
	{"atoms/go/vet.toml", "go:vet", "precommit", "go", "any:**/go.mod", "go", []string{"go", "vet", "./..."}, true, nil, nil, true},
	{"atoms/go/build.toml", "go:build", "prepush", "go", "any:**/go.mod", "go", []string{"go", "build", "./..."}, true, nil, nil, false},
	{"atoms/go/staticcheck.toml", "go:staticcheck", "prepush", "go", "any:**/go.mod", "go", staticcheckArgv, true, []string{"tool:staticcheck"}, nil, true},
	{"atoms/rust/cargo-fmt.toml", "rust:cargo-fmt", "precommit", "rust", "root:Cargo.toml", "rust", []string{"cargo", "fmt", "--all", "--check"}, false, nil, nil, false},
	{"atoms/rust/cargo-audit.toml", "rust:cargo-audit", "prepush", "rust", "root:Cargo.toml", "rust", []string{"cargo", "audit"}, false, []string{"tool:cargo-audit"}, nil, false},
	{"atoms/python/pip-audit.toml", "python:pip-audit", "prepush", "python", "any:**/*.py", "python", []string{"uv", "run", "--with", checks.PythonWith("pip-audit"), "pip-audit"}, false, []string{"tool:uv"}, nil, false},
	{"atoms/ts/bun-audit.toml", "ts:bun-audit", "prepush", "ts", "root:package.json", "bun", []string{"bun", "audit", "--audit-level=high"}, false, nil, map[string]string{"BUN_CONFIG_REGISTRY": "https://registry.npmjs.org/"}, false},
}

// TestUT_513_TheSevenManifestsAreTheTable: each file equals its row of the
// design's table, and copies desc, group and not_on_forks from the atom's row
// in checks.Atoms, so the Dagger path and the manifest cannot disagree.
func TestUT_513_TheSevenManifestsAreTheTable(t *testing.T) {
	for _, r := range manifestRows {
		raw, err := os.ReadFile(filepath.Join(repoRoot, r.file))
		if err != nil {
			t.Fatal(err)
		}
		m, err := ParseManifest(raw)
		if err != nil {
			t.Fatalf("%s: %v", r.file, err)
		}
		row := checks.AtomByID(r.id)
		got := []any{m.Schema, m.ID, m.Stage, m.Lane, m.Surface, m.Toolchain, m.PerModule, m.Judge, m.Args, m.Group, m.Desc, m.NotOnForks, m.WritesTree, m.SubsumedBy}
		want := []any{"command-atom@1", r.id, r.stage, r.lane, r.surface, r.toolchain, r.perModule, "exit", "none", checks.GroupOf(r.id), row.Desc, row.NotOnForks, false, row.SubsumedBy}
		if !slices.Equal(got, want) {
			t.Errorf("%s:\n got %v\nwant %v", r.file, got, want)
		}
		if m.Stage != row.Stage || m.Lane != string(row.Lane) {
			t.Errorf("%s: stage %s lane %s differ from the atom's row (%s, %s)", r.file, m.Stage, m.Lane, row.Stage, row.Lane)
		}
		if !slices.Equal(m.Argv, r.argv) || !slices.Equal(m.Needs, r.needs) || !mapsEqual(m.Env, r.env) {
			t.Errorf("%s: argv %v needs %v env %v", r.file, m.Argv, m.Needs, m.Env)
		}
		if r.forkStyle != (m.NotOnForks == checks.ForkFleetStyle) {
			t.Errorf("%s: not_on_forks %q", r.file, m.NotOnForks)
		}
		if strings.Contains(string(raw), "prepare") {
			t.Errorf("%s: names prepare", r.file)
		}
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
