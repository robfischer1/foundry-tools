package casttarget

import (
	"context"
	"dagger/foundry-tools/internal/castcache"
	"flag"
	"fmt"
	"io"
)

const usage = `usage: casttarget creds
       casttarget restore|save --repo URL --toolchain KEY --dir DIR

creds reads CASTCACHE_SOCKET (unix:///run/spire/agent.sock), CASTCACHE_ENDPOINT
and CASTCACHE_STAMP (which only keys the engine's exec cache), and leaves STS credentials at ` + castcache.CredsOut + `.
restore and save read the credentials from ` + castcache.SecretDir + `.`

// Main is the casttarget process. creds exits 1 when it holds no credentials,
// which the lane reads as "no cache this cast". restore and save exit 0
// whatever the cache did: their line says what happened, and the lane's
// verdict is not theirs to change. Only a malformed command exits 2.
func Main(ctx context.Context, args []string, env func(string) string, stdout io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stdout, usage)
		return 2
	}
	switch args[0] {
	case "creds":
		return mainCreds(ctx, env, stdout)
	case "restore", "save":
		return mainArchive(ctx, args[0], args[1:], env, stdout)
	}
	fmt.Fprintln(stdout, usage)
	return 2
}

// defaultSocket is the SPIRE agent's socket in the lane's creds container.
var defaultSocket = "unix:///run/spire/agent.sock"

// credsOut is where creds leaves its answer; a variable so a test can aim it.
var credsOut = castcache.CredsOut

func or(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func mainCreds(ctx context.Context, env func(string) string, stdout io.Writer) int {
	creds, id, err := FetchCreds(ctx, CredsConfig{
		Socket:   or(env("CASTCACHE_SOCKET"), defaultSocket),
		Endpoint: env("CASTCACHE_ENDPOINT"),
	})
	if err != nil {
		fmt.Fprintf(stdout, "credentials unavailable: %v\n", err)
		return 1
	}
	if err := WriteCreds(credsOut, creds); err != nil {
		fmt.Fprintf(stdout, "credentials unavailable: they could not be left for the lane: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "credentials issued to %s by role %s\n", id, castcache.RoleARN)
	return 0
}

func mainArchive(ctx context.Context, verb string, args []string, env func(string) string, stdout io.Writer) int {
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var c Config
	fs.StringVar(&c.Repo, "repo", "", "")
	fs.StringVar(&c.Toolchain, "toolchain", "", "")
	fs.StringVar(&c.Dir, "dir", "", "")
	if err := fs.Parse(args); err != nil || c.Dir == "" {
		fmt.Fprintln(stdout, usage)
		return 2
	}
	c.Endpoint = env("CASTCACHE_ENDPOINT")
	var err error
	if c.Creds, err = ReadCreds(); err != nil {
		fmt.Fprintln(stdout, skip(verb, err.Error()))
		return 0
	}
	if verb == "restore" {
		fmt.Fprintln(stdout, Restore(ctx, c))
	} else {
		fmt.Fprintln(stdout, Save(ctx, c))
	}
	return 0
}
