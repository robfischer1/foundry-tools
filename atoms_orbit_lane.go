package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"dagger/foundry-tools/internal/dagger"
	"dagger/foundry-tools/internal/fluxpin"
)

// THE ORBIT LANE'S ATOMS (stage `orbit`) live in atoms/orbit/<name>; what stays
// here is what the module itself still reads: narcissus's analyzer image (the
// atoms binary's per-run mount of narc) and the roster foundry-dies carries (the
// orbit bundle's star set).

// NarcissusImage names the image that carries `narc`, narcissus's own
// analyzer. The lane runs the star's reader, never a port of it: `narc scan` is
// the table code_query answers on the wire, and it takes no store and no door.
//
// IT IS RUN AT THE BUILD FLUX HAS PINNED LIVE, NOT AT :stable. A star image's
// :stable tag is frozen (nothing moves it), so it is a stale narc; the cluster
// runs what prime/images/kustomization.yaml pins, and Rob's ruling is "whatever
// flux has pinned live" (2026-10-03). narcissusRef resolves the pin by digest.
const NarcissusImage = "registry.notusmi.com/rob/narcissus"

// narcissusRef is NarcissusImage at the tag and digest flux pins live, read
// from foundry/flux main when the lane runs. A pin that cannot be read is an
// error naming the file and why: there is no fallback to :stable.
func narcissusRef(ctx context.Context, r *run) (string, error) {
	body, err := r.flux.File(fluxpin.File).Contents(ctx)
	if err != nil {
		return "", fmt.Errorf("foundry/flux %s could not be read: %v", fluxpin.File, err)
	}
	return fluxpin.Ref([]byte(body), NarcissusImage)
}

// roster answers each repo on dir's fleet catalog (fleet/repos/<name>.json) and
// its verb prefix: a contract may name any repo the catalog holds.
func roster(ctx context.Context, dir *dagger.Directory) (map[string]string, error) {
	paths, err := dir.Glob(ctx, "fleet/repos/*.json")
	if err != nil {
		return nil, fmt.Errorf("the roster could not be listed: %v", err)
	}
	out := map[string]string{}
	for _, p := range paths {
		body, err := dir.File(p).Contents(ctx)
		if err != nil {
			return nil, fmt.Errorf("%s could not be read: %v", p, err)
		}
		var star struct {
			VerbPrefix string `json:"verb_prefix"`
		}
		// A shard that is not JSON still names a repo by its file name; its
		// prefix is unknown, so no gateway name is attributed through it.
		_ = json.Unmarshal([]byte(body), &star)
		out[strings.TrimSuffix(path.Base(p), ".json")] = star.VerbPrefix
	}
	return out, nil
}

// starSet is a roster as the set of its star names; none at all is nil, the
// two-part file-name rule alone.
func starSet(prefixes map[string]string) map[string]bool {
	if len(prefixes) == 0 {
		return nil
	}
	out := make(map[string]bool, len(prefixes))
	for s := range prefixes {
		out[s] = true
	}
	return out
}
