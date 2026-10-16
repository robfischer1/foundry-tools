package pinsync

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Main is the command: pinsync [-C dir] [-n]. Exit 0 when the tree is in step
// or was brought into step, 1 when -n finds something to move, 2 when it could
// not run (and then nothing it had not finished is written).
func Main(ctx context.Context, args []string, getenv func(string) string, client *http.Client, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pinsync", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("C", ".", "the foundry-tools checkout")
	dry := fs.Bool("n", false, "name what would move; write and fetch nothing")
	if fs.Parse(args) != nil || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: pinsync [-C dir] [-n]")
		return 2
	}
	token := getenv("GITHUB_TOKEN")
	if token == "" {
		token = getenv("GITHUB_COM_TOKEN")
	}
	e := &Env{
		Root: *root, HTTP: client, GitHubAPI: "https://api.github.com", Token: token,
		Write: !*dry, Log: stderr, RunLock: ExecLock,
	}
	res, err := e.Run(ctx)
	for _, f := range res.Changed {
		fmt.Fprintln(stdout, f)
	}
	switch {
	case err != nil:
		fmt.Fprintf(stderr, "pinsync: %v\n", err)
		return 2
	case *dry && len(res.Changed) > 0:
		return 1
	}
	return 0
}

// Client is the HTTP client the command fetches with: a release asset is tens
// of megabytes, so the deadline is the download's, not a request's.
func Client() *http.Client { return &http.Client{Timeout: 10 * time.Minute} }
