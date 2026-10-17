package atomtest

import (
	"strings"

	"dagger/foundry-tools/internal/atomkit"
)

// The fleet bases a star's Dockerfile FROMs, by the toolchain they carry.
var (
	BunBase    = "registry.notusmi.com/foundry/base-images/bun:stable@sha256:" + strings.Repeat("b", 64)
	PythonBase = "registry.notusmi.com/foundry/base-images/python:stable@sha256:" + strings.Repeat("c", 64)
	GoBase     = "registry.notusmi.com/foundry/base-images/go:stable@sha256:" + strings.Repeat("d", 64)
	RustBase   = "registry.notusmi.com/foundry/base-images/rust:stable@sha256:" + strings.Repeat("e", 64)
)

// Star is a star whose Dockerfile copies from release/, as the release atoms
// read it: the Dockerfile text and the answers file that names the star.
func Star(name, dockerfile string, extra map[string]string) map[string]string {
	files := map[string]string{
		"Dockerfile":          dockerfile,
		".copier-answers.yml": "service_name: " + name + "\n",
	}
	for k, v := range extra {
		files[k] = v
	}
	return files
}

// WithDies gives the input a foundry-dies checkout holding the records
// (name -> JSON), at fleet/repos/<name>.json, and names the clone it was
// fetched from.
func WithDies(t TB, in atomkit.In, origin string, records map[string]string) atomkit.In {
	t.Helper()
	dir := t.TempDir()
	for name, body := range records {
		Put(t, dir, "fleet/repos/"+name+".json", body)
	}
	in.Dies, in.Origin = dir, origin
	return in
}
