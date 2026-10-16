package pinsync

import (
	"bytes"
	"context"
	"go/ast"
	"go/token"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The repository this package is in: the tables are written for it, and these
// tests hold them to it.
var repoRoot = filepath.Join("..", "..")

// THE LANDED TREE IS IN STEP: a dry run over it finds nothing to move. A pin
// moved by hand without its followers fails here before Renovate ever runs.
func TestTheLandedTreeIsInStep(t *testing.T) {
	var log bytes.Buffer
	e := &Env{Root: repoRoot, HTTP: &http.Client{Transport: &fakeWeb{}}, Log: &log}
	res, err := e.Run(context.Background())
	if err != nil || len(res.Changed) != 0 {
		t.Fatalf("changed %v, err %v; log:\n%s", res.Changed, err, log.String())
	}
}

// URLPins is every url tool in tools.toml, and every ToolSHA256 key is one of
// them, or WasmTools1250URL: the regen lane's fixed asset, which has no
// renovate marker and so never moves.
func TestTheTablesCoverTheTree(t *testing.T) {
	src, err := loadGoSource(filepath.Join(repoRoot, "internal", "checks"))
	if err != nil {
		t.Fatal(err)
	}
	doc := parseTOML(mustRead(t, filepath.Join(repoRoot, "tools.toml")))
	var inTOML []string
	for i, line := range doc.lines {
		if m := tomlString.FindStringSubmatch(line); m != nil && m[2] == "kind" && m[4] == "url" {
			for j := i; j >= 0; j-- {
				if h := tomlHeader.FindStringSubmatch(doc.lines[j]); h != nil {
					inTOML = append(inTOML, strings.TrimPrefix(h[1], "tool."))
					break
				}
			}
		}
	}
	var tools, urls []string
	for _, p := range URLPins {
		tools, urls = append(tools, p.Tool), append(urls, p.URL)
	}
	slices.Sort(tools)
	slices.Sort(inTOML)
	if !slices.Equal(tools, inTOML) {
		t.Errorf("URLPins names %v; tools.toml's url tools are %v", tools, inTOML)
	}
	_, sums, err := src.mapEntries(SumsVar)
	if err != nil {
		t.Fatal(err)
	}
	for key := range maps.Keys(sums) {
		if !slices.Contains(urls, key) && key != "WasmTools1250URL" {
			t.Errorf("ToolSHA256[%s] is no url pin's", key)
		}
	}
}

// EVERY LITERAL THAT CARRIES A RENOVATE-MOVED VERSION FOLLOWS IT: a string
// literal constant holding a marked version as a whole token is a Follower of
// that version. A new literal URL added without its line here fails, rather
// than going stale on the first bump.
func TestEveryLiteralThatCarriesAVersionFollowsIt(t *testing.T) {
	src, err := loadGoSource(filepath.Join(repoRoot, "internal", "checks"))
	if err != nil {
		t.Fatal(err)
	}
	listed := map[[2]string]bool{}
	for _, f := range Followers {
		listed[[2]string{f.Const, f.Version}] = true
	}
	found := map[[2]string]bool{}
	for vname, vd := range src.consts {
		if !markerRe.MatchString(vd.doc) || !strings.HasSuffix(vname, "Version") {
			continue
		}
		v, err := src.eval(vname)
		if err != nil {
			continue // a marked constant that is not a string is not a URL's version
		}
		v = strings.TrimPrefix(v, "v")
		for cname, cd := range src.consts {
			lit, ok := cd.expr.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING || cname == vname || strings.HasSuffix(cname, "Version") {
				continue
			}
			s, _ := strconv.Unquote(lit.Value)
			if slices.Contains(versionToken.FindAllString(s, -1), v) {
				found[[2]string{cname, vname}] = true
			}
		}
	}
	for k := range found {
		if !listed[k] {
			t.Errorf("%s carries %s and is not a Follower of it", k[0], k[1])
		}
	}
	for k := range listed {
		if !found[k] {
			t.Errorf("Followers lists %s of %s, which does not carry it", k[0], k[1])
		}
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
