package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
	"dagger/foundry-tools/internal/svidcred"
)

// REGISTRY LOGIN WITH A WORKLOAD IDENTITY (CI workload identity v2). With
// --registry-svid=<audience> and a --spire socket, a lane logs in to the
// registry with a JWT-SVID the SPIRE agent issues, instead of a static
// password. Without both, nothing here runs and the static path is untouched.
//
// THE TOKEN IS NEVER IN A LAYER. A docker config would be: the engine caches
// every file an exec or a mount writes, and a JWT lives 15 minutes. So the
// config a tool reads here (svidcred.Config) names a credential helper and
// holds nothing; oras and cosign run docker-credential-svid when they
// authenticate, which fetches the token through the forwarded socket at that
// moment (internal/svidcred). The token exists in the helper's and the tool's
// memory for the request, and a long push asks again when it needs to.
//
// THE ENGINE'S OWN PUSH (Container.Publish) cannot run a helper: the engine
// authenticates, not a container. login fetches the token in an exec just
// before the push, cache-busted so no run reuses an earlier token, and hands
// it to the push as a dagger.Secret (masked in logs, never in an env var or
// file). The exec's stdout is the one place the token is held beyond memory:
// the engine's exec metadata, keyed by an input nothing can repeat.

// svidLogin is what a lane was asked: the audience, and the socket the token
// is fetched through.
type svidLogin struct {
	audience string
	spire    *dagger.Socket
}

// on says whether the lane logs in with its identity: the flag AND a socket.
// Either missing leaves the lane exactly as it was.
func (s svidLogin) on() bool { return s.audience != "" && s.spire != nil }

const (
	svidSocketPath = "/run/spire/agent.sock"
	svidHelperPath = "/usr/local/bin/" + svidcred.Binary
	// svidOwner is the nonroot user the tool images run as: a forwarded socket
	// is root-owned and a nonroot exec cannot connect to it (hadesCaller).
	svidOwner = "65532:65532"
)

// config is the docker config that sends host's logins to the helper. It holds
// no credential.
func (s svidLogin) config(host string) *dagger.Secret {
	return dag.SetSecret("registry-svid-config-"+host, svidcred.Config(host))
}

// helper is ctr with the credential helper installed, the socket forwarded and
// the audience named. The helper is on the fleet base's PATH.
func (s svidLogin) helper(ctr *dagger.Container) *dagger.Container {
	return ctr.
		WithFile(svidHelperPath, helperBinary("svidcred"), dagger.ContainerWithFileOpts{Permissions: 0o755}).
		WithUnixSocket(svidSocketPath, s.spire, dagger.ContainerWithUnixSocketOpts{Owner: svidOwner}).
		WithEnvVariable(svidcred.EnvSocket, "unix://"+svidSocketPath).
		WithEnvVariable(svidcred.EnvAudience, s.audience)
}

// login is ctr set up to log in to host with the identity: the helper, and the
// docker config naming it where tools read one (/run/docker/config.json, and
// DOCKER_CONFIG for cosign). Off, it is ctr unchanged.
func (s svidLogin) login(ctr *dagger.Container, host string) *dagger.Container {
	if !s.on() {
		return ctr
	}
	return s.helper(ctr).
		WithMountedSecret("/run/docker/config.json", s.config(host), dagger.ContainerWithMountedSecretOpts{Owner: svidOwner}).
		WithEnvVariable("DOCKER_CONFIG", "/run/docker")
}

// withHelper is ctr with the credential helper, for a container whose docker
// config the lane mounts itself (registryConfig). Off, ctr unchanged.
func (s svidLogin) withHelper(ctr *dagger.Container) *dagger.Container {
	if !s.on() {
		return ctr
	}
	return s.helper(ctr)
}

// token fetches a JWT-SVID now, for a push the engine makes itself, and
// answers the user and the token as a secret. The nanosecond stamp keeps any
// run from reusing an earlier exec's token.
func (s svidLogin) token(ctx context.Context, host, name string) (string, *dagger.Secret, error) {
	out, err := s.helper(dag.Container().From(checks.ImageStatic)).
		WithEnvVariable("SVIDCRED_FETCHED_AT", strconv.FormatInt(time.Now().UnixNano(), 10)).
		WithExec([]string{svidHelperPath, "get"}, dagger.ContainerWithExecOpts{Stdin: host}).
		Stdout(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("the registry JWT-SVID did not fetch: %w", err)
	}
	user, secret, err := svidcred.Parse(strings.TrimSpace(out))
	if err != nil {
		return "", nil, err
	}
	return user, dag.SetSecret(name, secret), nil
}
