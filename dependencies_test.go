// SPDX-FileCopyrightText: 2026 Rob Fischer
//
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

const depDigest = "sha256:ec818e859cb2f7af45f81377279e688f6c5313f862e4a019d2e48a76fec2b3ee"

// declaredBy runs the lane and answers the one ::ourea:: line it printed.
func declaredBy(t *testing.T, m *FoundryTools) string {
	t.Helper()
	out := sayings(t, func() { pull(t, m) })
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "::ourea::") {
			return line
		}
	}
	return ""
}

// TestATreeDeclaresWhatItsDockerfilesDependOn — the dependency half, end to
// end through the lane. A pull publishes nothing, so this declaration carries
// ONLY deps, which is the point: what a tree depends on is a fact about the
// TREE and does not wait for a build to succeed.
func TestATreeDeclaresWhatItsDockerfilesDependOn(t *testing.T) {
	m := buildOn(t, map[string]string{
		"Dockerfile": "FROM foundry.notusmi.com/foundry/base-images/go:stable@" + depDigest + "\n",
		// The same base in a second Dockerfile is ONE dependency.
		"tools/Dockerfile": "FROM foundry.notusmi.com/foundry/base-images/go:stable@" + depDigest + "\n",
		// Upstream is not ours to keep and must not be recorded.
		"other/Dockerfile": "FROM docker.io/library/alpine@sha256:" + strings.Repeat("c", 64) + "\n",
	})
	line := declaredBy(t, m)
	if line == "" {
		t.Fatal("the lane declared nothing; a tree with a digest-pinned FROM depends on something")
	}
	if !strings.Contains(line, `"artifact":"foundry.notusmi.com/foundry/base-images/go"`) ||
		!strings.Contains(line, `"role":"dep"`) {
		t.Errorf("the base was not declared as a dependency:\n%s", line)
	}
	if strings.Contains(line, "docker.io/library/alpine") {
		t.Errorf("an UPSTREAM image was declared; our retention cannot reap it:\n%s", line)
	}
	if n := strings.Count(line, `"foundry.notusmi.com/foundry/base-images/go"`); n != 1 {
		t.Errorf("the same base in two Dockerfiles made %d entries, want 1:\n%s", n, line)
	}
}

// TestADependencyReadThatFAILSDoesNotSinkTheBuild — bookkeeping about a build
// must not fail the build. Both reads are exercised because they fail in
// different places: the glob finds the files, the contents read opens one.
func TestADependencyReadThatFAILSDoesNotSinkTheBuild(t *testing.T) {
	// The root Dockerfile is what the BUILD reads; tools/Dockerfile is read by
	// the dependency scan alone. Failing the second isolates the bookkeeping
	// from the build, which is the distinction under test — failing the first
	// would break the build itself and prove nothing about this path.
	tree := map[string]string{
		"Dockerfile":       "FROM foundry.notusmi.com/foundry/base-images/go:stable@" + depDigest + "\n",
		"tools/Dockerfile": "FROM foundry.notusmi.com/foundry/base-images/go:stable@" + depDigest + "\n",
	}

	t.Run("the glob cannot run", func(t *testing.T) {
		m := buildOn(t, tree)
		engine.fail(`glob(pattern:"**/Dockerfile*"`, "filter: engine went away")
		out := sayings(t, func() { pull(t, m) })
		if !strings.Contains(out, "dependencies could not be read") {
			t.Errorf("a failed dependency read was not said:\n%s", out)
		}
		if strings.Contains(out, `"role":"dep"`) {
			t.Errorf("a dependency was declared from a read that failed:\n%s", out)
		}
		// AND THE BUILD STILL SETTLES. The whole contract: the artifacts are
		// the build's business and the dependency list is bookkeeping about
		// them, so a failure here is reported and stepped over.
		settledOn(t, "0", "clean")
	})

	t.Run("a file cannot be opened", func(t *testing.T) {
		m := buildOn(t, tree)
		engine.failLeaf(`file(path:"tools/Dockerfile"`, "contents", "no such file or directory")
		out := sayings(t, func() { pull(t, m) })
		if !strings.Contains(out, "dependencies could not be read") {
			t.Errorf("an unreadable Dockerfile was not said:\n%s", out)
		}
		settledOn(t, "0", "clean")
	})
}
