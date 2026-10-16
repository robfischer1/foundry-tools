package atomkit

import (
	"context"
	"time"

	"dagger/foundry-tools/internal/atoms"
)

// Tree is tree@1, /work/tree.json: the population facts the logic atoms read.
// The field set is internal/atoms.Input's population fields.
type Tree struct {
	Version     int      `json:"version"` // 1
	Root        string   `json:"root"`
	Files       []string `json:"files"`
	Committable []string `json:"committable"`
	Tracked     []string `json:"tracked"`
	Changed     []string `json:"changed"`
	Base        string   `json:"base"`
	Origin      string   `json:"origin"`
	Fork        string   `json:"fork"`
	FilesErr    string   `json:"files_err"`
	TrackedErr  string   `json:"tracked_err"`
	ChangedErr  string   `json:"changed_err"`
}

// CollectTree is tree@1 for the checkout at root: the facts internal/atoms'
// Input builder (Collect, ChangeSet) computes, so a tree a pod's `lead` reads
// and a tree the in-process runner reads give an atom the same population
// (lld.md FN-036). A list that would not compute is [] and its reason rides in
// the matching *_err field.
func CollectTree(ctx context.Context, root, base, origin, fork string) Tree {
	return treeOf(atoms.Collect(ctx, root, base, origin, time.Now()), fork)
}

// treeOf is an Input's population as tree@1.
func treeOf(in In, fork string) Tree {
	return Tree{
		Version: 1, Root: in.Root, Base: in.Base, Origin: in.Origin, Fork: fork,
		Files: nonNil(in.Files), Committable: nonNil(in.Committable),
		Tracked: nonNil(in.Tracked), Changed: nonNil(in.Changed),
		FilesErr: errText(in.FilesErr), TrackedErr: errText(in.TrackedErr), ChangedErr: errText(in.ChangedErr),
	}
}

// inOf is tree@1 as an Input's population fields; an empty *_err is a nil error.
func inOf(t Tree) In {
	return In{
		Root: t.Root, Files: t.Files, Committable: t.Committable, Tracked: t.Tracked, Changed: t.Changed,
		Base: t.Base, Origin: t.Origin, Fork: t.Fork,
		FilesErr: errOf(t.FilesErr), TrackedErr: errOf(t.TrackedErr), ChangedErr: errOf(t.ChangedErr),
	}
}

func nonNil(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}
