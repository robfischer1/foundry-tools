package checks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
)

// fleet:orbit-drift's judgement, in Go. It was fleet_orbit_drift.py, run under
// uv in the lane container; it is the same ladder — 0 agreement, 1 drift or an
// unpinned edge, 2 CANNOT RUN — with one change, the host: the contracts are
// read from the Ourea door (Door), not Forgejo's raw API.
//
// Every edge in orbit.toml names a contract and pins its digest. The canonical
// copy lives in foundry-dies/orbits; this fetches each and compares. A digest
// that disagrees is drift: the contract moved and the repo's declaration did
// not.

// orbitContractsDir is where the canonical contracts sit in OrbitsRepo.
const orbitContractsDir = "orbits"

// OrbitDrift judges one orbit.toml against the door and answers the atom's
// state with its report.
//
// A DOOR THAT COULD NOT ANSWER IS 2, NEVER 1. Unreachable, any status but 2xx
// and 404, a body that is not TOML, a TOML body with no `verbs` key: each is a
// contract that was not read, and a contract that was not read is not a
// contract that disagrees. A body that is not a contract hashes to a confident
// digest mismatch pointing at the wrong thing entirely (a proxy's login page, a
// "Moved Permanently"), which is why the shape is checked before the hash.
func OrbitDrift(ctx context.Context, orbitToml string, door Door) (int, string) {
	var doc map[string]any
	if _, err := toml.Decode(orbitToml, &doc); err != nil {
		return 2, fmt.Sprintf("fleet:orbit-drift: CANNOT RUN - orbit.toml did not parse: %v", err)
	}

	type edge struct{ direction, peer, contract, digest string }
	var edges []edge
	for _, direction := range []string{"consumes", "produces"} {
		for _, e := range tables(doc[direction]) {
			peer := firstText(e["from"], e["to"], "?")
			edges = append(edges, edge{direction, peer, text(e["contract"]), text(e["digest"])})
		}
	}
	if len(edges) == 0 {
		return 0, "fleet:orbit-drift: orbit.toml declares no seams"
	}

	var agree, drift, unpinned []string
	for _, e := range edges {
		if e.contract == "" {
			unpinned = append(unpinned, fmt.Sprintf("%s %s names no contract", e.direction, e.peer))
			continue
		}
		file := orbitContractsDir + "/" + e.contract + ".toml"
		at := door.URL(OrbitsRepo, file)
		status, body, err := door.Get(ctx, OrbitsRepo, file)
		switch {
		case err != nil:
			return 2, fmt.Sprintf("fleet:orbit-drift: CANNOT RUN - the door is unreachable (%s): %v", at, err)
		case status == http.StatusNotFound:
			drift = append(drift, fmt.Sprintf("%s: names contract '%s', which is not in foundry-dies/orbits", e.peer, e.contract))
			continue
		case status < 200 || status >= 300:
			return 2, fmt.Sprintf("fleet:orbit-drift: CANNOT RUN - the door answered HTTP %d for %s. A contract that could not be fetched is not a contract that agrees.", status, at)
		}
		parsed, err := parseContract(body)
		if err != nil {
			return 2, fmt.Sprintf("fleet:orbit-drift: CANNOT RUN - %s did not answer a TOML document (%v). A body that is not a contract is not a contract that disagrees.", at, err)
		}
		if _, ok := parsed["verbs"]; !ok {
			return 2, fmt.Sprintf("fleet:orbit-drift: CANNOT RUN - %s parsed as TOML but carries no verbs key, so it is not a seam contract.", at)
		}
		sum := sha256.Sum256(body)
		got := "sha256:" + hex.EncodeToString(sum[:])
		switch {
		case e.digest == "":
			unpinned = append(unpinned, fmt.Sprintf("%s: contract '%s' carries no digest, so nothing was compared", e.peer, e.contract))
		case e.digest != got:
			drift = append(drift, fmt.Sprintf("%s: contract '%s' hashes to %s, orbit.toml records %s", e.peer, e.contract, got, e.digest))
		default:
			agree = append(agree, e.peer)
		}
	}

	if len(drift) == 0 && len(unpinned) == 0 {
		return 0, fmt.Sprintf("fleet:orbit-drift: %d seam(s) agree with foundry-dies/orbits", len(agree))
	}
	lines := make([]string, 0, len(drift)+len(unpinned)+2)
	for _, l := range append(drift, unpinned...) {
		lines = append(lines, "orbit-drift: "+l)
	}
	if len(drift) > 0 {
		lines = append(lines, "The canonical contract moved and this repo's declaration did not. "+
			"Re-lay orbit.toml from the die, in a worktree at the repo root: "+
			"furnace die --for . --dest DIR && gavel order . --from DIR, then commit orbit.toml.")
	}
	if len(unpinned) > 0 {
		lines = append(lines, "An edge with no digest pins nothing, so this atom compared nothing "+
			"for it - and nothing to check is not checked and clean.")
	}
	return 1, strings.Join(lines, "\n")
}

// parseContract decodes a fetched body as TOML, refusing bytes that are not
// UTF-8 first.
func parseContract(body []byte) (map[string]any, error) {
	if !utf8.Valid(body) {
		return nil, fmt.Errorf("not valid UTF-8")
	}
	var parsed map[string]any
	if _, err := toml.Decode(string(body), &parsed); err != nil {
		return nil, err
	}
	return parsed, nil
}

// tables is a TOML array of tables as its tables. The decoder hands back
// []map[string]any for `[[consumes]]` and []any of maps for an inline
// `consumes = [{...}]`; both are the same declaration, and anything else in the
// array is not an edge.
func tables(v any) []map[string]any {
	switch t := v.(type) {
	case []map[string]any:
		return t
	case []any:
		var out []map[string]any
		for _, e := range t {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

// text is a TOML value as the string it holds, or "" for anything else —
// which is how an absent key, an empty string and a wrong type all read as
// "not declared".
func text(v any) string {
	s, _ := v.(string)
	return s
}

// firstText is the first of vs that is a non-empty string.
func firstText(vs ...any) string {
	for _, v := range vs {
		if s := text(v); s != "" {
			return s
		}
	}
	return ""
}
