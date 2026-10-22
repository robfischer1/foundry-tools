package catalogue

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

// EVERY ATOM DEF DECLARES SURFACE AND TOOLCHAIN AS STRING LITERALS. daedalus
// reads a local-run atom's Def at the sha with go/ast and understands literals
// only; a Surface spelled "any:" + jobs made it return nil for the whole tree,
// so go:test-race planned no kube sidecars and could not run (RDL1, 2026-10-22).
// The package compiles and every unit test passes either way, so only this
// reads it.

// nonLiteralDefFields names the Surface and Toolchain fields of the file's Def
// that are not a plain string literal.
func nonLiteralDefFields(t *testing.T, name string, src any) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), name, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var bad []string
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok || len(spec.Names) != 1 || spec.Names[0].Name != "Def" || len(spec.Values) != 1 {
			return true
		}
		lit, ok := spec.Values[0].(*ast.CompositeLit)
		if !ok {
			return true
		}
		for _, el := range lit.Elts {
			kv, ok := el.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok || (key.Name != "Surface" && key.Name != "Toolchain") {
				continue
			}
			if v, ok := kv.Value.(*ast.BasicLit); !ok || v.Kind != token.STRING {
				bad = append(bad, key.Name)
			}
		}
		return false
	})
	return bad
}

func TestEveryAtomDefDeclaresSurfaceAndToolchainAsLiterals(t *testing.T) {
	atomDirs, _, err := scan(filepath.Join(root, "atoms"))
	if err != nil {
		t.Fatal(err)
	}
	if len(atomDirs) == 0 {
		t.Fatal("no atom directories found")
	}
	for _, d := range atomDirs {
		path := filepath.Join(root, "atoms", d, filepath.Base(d)+".go")
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bad := nonLiteralDefFields(t, path, src); len(bad) > 0 {
			t.Errorf("%s: %v is not a string literal; daedalus reads Defs with go/ast and understands literals only", path, bad)
		}
	}
}

func TestNonLiteralDefFieldsReadsTheShapeItGuards(t *testing.T) {
	const jobsConst = "package a\nconst jobs = \"x\"\nvar Def = atomkit.Def{ID: \"a:b\", Surface: \"any:\" + jobs, Toolchain: \"go\"}\n"
	if got := nonLiteralDefFields(t, "a.go", jobsConst); len(got) != 1 || got[0] != "Surface" {
		t.Errorf("a concatenated Surface: %v", got)
	}
	const named = "package a\nvar Def = atomkit.Def{Surface: \"any:**\", Toolchain: tc}\n"
	if got := nonLiteralDefFields(t, "a.go", named); len(got) != 1 || got[0] != "Toolchain" {
		t.Errorf("a named Toolchain: %v", got)
	}
	const ok = "package a\nvar Def = atomkit.Def{ID: \"a:b\", Surface: \"any:**\", Toolchain: \"none\"}\n"
	if got := nonLiteralDefFields(t, "a.go", ok); len(got) != 0 {
		t.Errorf("literals flagged: %v", got)
	}
	const other = "package a\nvar Other = 1\nvar Def = notAComposite()\n"
	if got := nonLiteralDefFields(t, "a.go", other); len(got) != 0 {
		t.Errorf("a non-literal Def value flagged: %v", got)
	}
}
