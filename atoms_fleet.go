package main

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"golang.org/x/sync/errgroup"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// THE FLEET LANE. The atoms that run in every repository in custody, whatever
// it is written in, live in their own files (atoms/fleet/<name>/<name>.go) and
// the Dagger adapter (atom_adapter.go) runs them. What stays here is what the
// chains of the other lanes still read.

// cannotEnumerate is the verdict when the TREE ITSELF would not answer. It is a
// could-not-run about the repository rather than a finding about it: the atom
// never got as far as having an opinion.
func cannotEnumerate(a checks.AtomDef, err error) checks.Verdict {
	return checks.VerdictOf(a, 2, fmt.Sprintf("%s: CANNOT RUN - the tree would not enumerate: %v", a.ID, err))
}

// neverRan is the verdict when the ENGINE would not answer — a mount that did
// not evaluate, an image that would not pull, a provisioning exec that failed
// under the default Expect. It is verdict()'s own wording, because it is
// verdict()'s own case: output() separates the engine's error from the tool's
// exit code precisely so an atom that reads the code itself still files the
// error the way every other atom does.
func neverRan(a checks.AtomDef, err error) checks.Verdict {
	return checks.VerdictOf(a, 2, "the atom never ran: "+err.Error())
}

// oureaDoor is where dies:contracts reads contracts. A variable so the tests
// can point it at a fake door.
var oureaDoor = checks.NewDoor()

// fileRead is one file's contents, or why it would not read.
type fileRead struct {
	body string
	err  error
}

// readFiles reads every path under src at once, sixteen at a time — one query
// each, and a tree of six hundred scanned files is theia's. A read that fails
// is kept, not raised: which file failed is the scan's to report.
func readFiles(ctx context.Context, src *dagger.Directory, paths []string) map[string]fileRead {
	out := make(map[string]fileRead, len(paths))
	var mu sync.Mutex
	var g errgroup.Group
	g.SetLimit(16)
	for _, p := range paths {
		g.Go(func() error {
			body, err := src.File(p).Contents(ctx)
			mu.Lock()
			out[p] = fileRead{body: body, err: err}
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait()
	return out
}

// readOK folds readFiles into path -> body, or the first path that would not
// read: a file never read is a file never judged.
func readOK(in map[string]fileRead) (map[string]string, string) {
	out := make(map[string]string, len(in))
	var failed []string
	for p, f := range in {
		if f.err != nil {
			failed = append(failed, p+" ("+f.err.Error()+")")
			continue
		}
		out[p] = f.body
	}
	if len(failed) == 0 {
		return out, ""
	}
	sort.Strings(failed)
	return nil, failed[0] + " would not read"
}
