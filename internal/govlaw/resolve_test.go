package govlaw

import (
	"errors"
	"testing"
)

// absorb-april-soap: a tree die sourced from another repository is looked up at
// its ref when the lane has a Resolve.

type asked struct{ source, ref, path string }

func resolving(t *testing.T, files map[string]string, got Presence, err error) (Tree, *[]asked) {
	t.Helper()
	tr := stocks(files)
	var log []asked
	tr.Resolve = func(source, ref, path string) (Presence, error) {
		log = append(log, asked{source, ref, path})
		return got, err
	}
	return tr, &log
}

func TestResolveFindsTheRefAndPathAndSaysSo(t *testing.T) {
	tr, log := resolving(t, nil, Found, nil)
	r := Lint(tr)
	want(t, r, StatePass, "1 die(s)", "0 die source(s) in other repositories were not resolved")
	if len(*log) != 1 || (*log)[0] != (asked{"https://git.example.test/rob/x.git", "main", "."}) {
		t.Errorf("asked %+v", *log)
	}
	want(t, r, StatePass, "1 more found at their ref")
}

func TestResolveAMissingRefIsAFinding(t *testing.T) {
	tr, _ := resolving(t, nil, NoRef, nil)
	want(t, Lint(tr), StateFindings, `die "tool/x": ref "main" does not exist in https://git.example.test/rob/x.git`)
}

func TestResolveAMissingPathIsAFinding(t *testing.T) {
	tr, _ := resolving(t, map[string]string{"dies.toml": "[dies.d]\nsource = \"https://h.test/o/r.git@v1\"\npath = \"policy\"\n"}, NoPath, nil)
	want(t, Lint(tr), StateFindings, `die "d": path "policy" is not in https://h.test/o/r.git at "v1"`)
}

func TestResolveAFailedLookupIsCouldNotRunNotAPassNotAFinding(t *testing.T) {
	tr, _ := resolving(t, nil, Found, errors.New("connection refused"))
	r := Lint(tr)
	want(t, r, StateCannotRun, "CANNOT RUN", "could not be read", "connection refused")
	wantNot(t, r, "FINDINGS", "are sound")
}

func TestResolveAFailedLookupOutranksAFinding(t *testing.T) {
	tr, _ := resolving(t, map[string]string{"governance/blocks/blank.md": " "}, Found, errors.New("503"))
	want(t, Lint(tr), StateCannotRun, "could not be read")
}

func TestResolveAForeignSourceIsCountedNotLookedUp(t *testing.T) {
	tr, _ := resolving(t, nil, Found, ErrForeign)
	want(t, Lint(tr), StatePass, "1 die source(s) in other repositories were not resolved", "0 more found")
}

func TestResolveNeverAsksForTheOwnTreeOrAKitDie(t *testing.T) {
	tr, log := resolving(t, map[string]string{
		"dies.toml": "[dies.a]\nsource = \"https://h.test/foundry/foundry-stocks.git@main\"\npath = \"governance\"\n[dies.b]\nkit = \"root\"\n",
	}, Found, nil)
	want(t, Lint(tr), StatePass)
	if len(*log) != 0 {
		t.Errorf("asked %+v", *log)
	}
}
