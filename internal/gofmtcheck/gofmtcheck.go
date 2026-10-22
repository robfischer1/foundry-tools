// Package gofmtcheck is the question go:gofmt and dev:go-fmt-check both ask:
// every Go file is gofmt-clean. The two atoms hold a declaration each and call
// Check with their own row, so the verdict says the id that was asked and the
// logic exists once. It only lists: nothing is written to the tree.
package gofmtcheck

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// Check lists the files gofmt would change.
//
// THE POPULATION IS THE GATE'S, NOT A find(1) WALK: the files the repository
// would commit, less the fleet exclude, which already carries vendor/. A
// generated or gitignored .go a developer happens to have on disk is not in it.
//
// NO `go mod download`. gofmt parses and prints; it never resolves an import,
// so a proxy's bad hour cannot reach this atom at all.
//
// GOFMT'S EXIT CODE IS NOT THE VERDICT, ITS STDOUT IS. `gofmt -l` exits 0
// whether it listed every file or none, so the answer is read off the listing.
// A non-zero gofmt is a 2: a file it could not parse is a file it did not
// check. The list goes in as arguments, in as many runs as the argv budget
// takes (checks.ArgvChunks), one program and no shell.
func Check(ctx context.Context, in atomkit.In, row checks.AtomDef) checks.Verdict {
	if in.FilesErr != nil {
		return checks.CannotEnumerate(row, in.FilesErr)
	}
	var files []string
	for _, f := range in.Files {
		if strings.HasSuffix(f, ".go") {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return checks.VerdictOf(row, int(checks.StatePass), row.ID+": no Go files")
	}
	run := in.Exec
	if run == nil {
		run = atoms.RunProgram
	}
	var listed []string
	for _, chunk := range checks.ArgvChunks(files) {
		if err := ctx.Err(); err != nil {
			return checks.Interrupted(row, err)
		}
		out, code := run(ctx, atoms.Cmd{Dir: in.Root, Name: "gofmt", Args: append([]string{"-l"}, chunk...)})
		if code != 0 {
			return checks.VerdictOf(row, int(checks.StateCannotRun), fmt.Sprintf(
				"%s: CANNOT RUN - gofmt exited %s; a file it could not parse is a file it did not check.\n%s",
				row.ID, strconv.Itoa(code), strings.Join(append(listed, out), "\n")))
		}
		if out != "" {
			listed = append(listed, out)
		}
	}
	if len(listed) > 0 {
		return checks.VerdictOf(row, int(checks.StateFindings), row.ID+": FINDINGS - gofmt needed on:\n"+strings.Join(listed, "\n"))
	}
	return checks.VerdictOf(row, int(checks.StatePass), row.ID+": clean")
}
