package main

import (
	"context"
	"fmt"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/dagger"
)

// WHAT IS LEFT OF THE DIES LANE'S CHAINS. The dies atoms are files of their own
// now (atoms/dies/<name>) and run through the adapter (atom_adapter.go), their
// environments in atom_env_dies.go. What stays here is what other chains still
// call: the bundle lane provisions the pinned opa (opaClient), and fleet:wit-topics
// still asks for its checker with requirePaths and stdlibpy.

// present answers whether the tree carries a path, WITHOUT conflating "not
// there" with "could not look". Glob returns an empty list for the first and an
// error for the second, which is the distinction every guard on this page needs
// and the one a `[ -f x ]` in shell could never make.
func present(ctx context.Context, r *run, path string) (bool, error) {
	matches, err := r.src.Glob(ctx, path)
	if err != nil {
		return false, err
	}
	return len(matches) > 0, nil
}

// opaClient provisions opa AT THE PINNED VERSION, and the pin is load-bearing.
//
// The rego language version is a property of the binary: a suite written for v1
// semantics graded by a different major answers a different question, and "the
// policy suite passed" would then be a true statement about the wrong language.
// The workflow this ports pinned 1.18.0 by hand (foundry-dies's
// retired CI workflow); the pin lives in checks.OpaVersion now.
//
// THE VERSION IS CHECKED RATHER THAN ASSUMED, so a mirror that served something
// else — or an image that starts shipping its own opa on PATH — cannot silently
// supply a different language. The old body used the check to SKIP the download
// when the right binary was already on disk; with the file placed by the engine
// there is nothing to skip, and the same probe now asserts what was fetched.
func (r *run) opaClient(ctx context.Context) (*dagger.Container, error) {
	f, err := fetchTool(ctx, checks.OpaURL)
	if err != nil {
		return nil, fmt.Errorf("opa %s could not be fetched: %w", checks.OpaVersion, err)
	}
	ctr := r.lane(checks.ImageFleet).
		WithFile("/usr/local/bin/opa", f, dagger.ContainerWithFileOpts{Permissions: 0o755})

	out, code, err := output(ctx, ctr.WithExec([]string{"opa", "version"}, anyExit))
	if err != nil {
		return nil, fmt.Errorf("the opa version probe never ran: %w", err)
	}
	if code != 0 {
		return nil, fmt.Errorf("opa is on disk but does not run (exit %d): %s", code, out)
	}
	if !checks.OpaVersionOK(out, checks.OpaVersion) {
		return nil, fmt.Errorf("the binary on disk does not answer %q, so the rego semantics are not the pinned ones: %s", "Version: "+checks.OpaVersion, out)
	}
	return ctr, nil
}
