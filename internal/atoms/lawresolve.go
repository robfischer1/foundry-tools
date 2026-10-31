package atoms

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strings"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/govlaw"
)

// THE DIE SOURCES IN OTHER REPOSITORIES are looked up through the door's /tree
// manifest at the die's ref, not cloned: a ref the door does not hold answers
// 404, and a path with no file under it answers an empty manifest. A source on
// any other host is the door's no business (govlaw.ErrForeign).

// treeManifest is the part of the door's /tree answer a lookup reads.
type treeManifest struct {
	Entries []struct {
		Path string `json:"path"`
	} `json:"entries"`
	Truncated bool `json:"truncated"`
}

// LawResolver is govlaw.Tree.Resolve over the door.
func LawResolver(ctx context.Context, door checks.Door) func(source, ref, p string) (govlaw.Presence, error) {
	base, _ := url.Parse(door.Base)
	return func(source, ref, p string) (govlaw.Presence, error) {
		u, err := url.Parse(source)
		if err != nil || base == nil || u.Host != base.Host {
			return govlaw.Found, govlaw.ErrForeign
		}
		repo := strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), ".git")
		want := path.Clean(p)
		prefix := want
		if want == "." {
			prefix = ""
		}
		status, body, err := door.Tree(ctx, repo, ref, prefix)
		switch {
		case err != nil:
			return govlaw.Found, err
		case status == 404:
			return govlaw.NoRef, nil
		case status != 200:
			return govlaw.Found, fmt.Errorf("the door answered HTTP %d", status)
		}
		var m treeManifest
		if err := json.Unmarshal(body, &m); err != nil {
			return govlaw.Found, fmt.Errorf("the door's manifest does not parse: %v", err)
		}
		for _, e := range m.Entries {
			if prefix == "" || e.Path == prefix || strings.HasPrefix(e.Path, prefix+"/") {
				return govlaw.Found, nil
			}
		}
		if m.Truncated {
			return govlaw.Found, fmt.Errorf("the door's manifest was truncated before it showed a file under %q", p)
		}
		return govlaw.NoPath, nil
	}
}
