package lawlint

import (
	"context"
	"net/http"
	"testing"

	"dagger/foundry-tools/internal/atomtest"
	"dagger/foundry-tools/internal/checks"
)

// absorb-april-soap: the atom hands law:lint a lookup over the door's /tree.

func lawTree(source, p string) map[string]string {
	return atomtest.StocksTree(map[string]string{
		"dies.toml": "[dies.d]\nsource = \"" + source + "\"\npath = \"" + p + "\"\n",
	})
}

func TestTheAtomGradesASourceInAnotherRepository(t *testing.T) {
	door := atomtest.TreeDoor(t, http.StatusOK, map[string][]string{"rob/x@main": {"policy/a.toml"}})
	run := func(d checks.Door, source, p string) checks.Verdict {
		in := atomtest.TreeIn(t, lawTree(d.Base+source, p))
		in.Door = d
		return check(context.Background(), in)
	}
	atomtest.Expect(t, run(door, "/rob/x.git@main", "policy"), checks.StatePass, atomtest.Pass, "1 more found at their ref")
	atomtest.Expect(t, run(door, "/rob/x.git@nope", "policy"), checks.StateFindings, atomtest.Findings, `ref "nope" does not exist`)
	atomtest.Expect(t, run(door, "/rob/x.git@main", "absent"), checks.StateFindings, atomtest.Findings, `path "absent" is not in`)
	down := atomtest.TreeDoor(t, http.StatusBadGateway, nil)
	atomtest.Expect(t, run(down, "/rob/x.git@main", "policy"), checks.StateCannotRun, atomtest.Cannot, "could not be read")
}
