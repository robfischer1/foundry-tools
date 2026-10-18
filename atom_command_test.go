package main

import (
	"context"
	"slices"
	"testing"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
)

// TestCommandInRunsTheComposedArgvAndHonoursThePreflightOptOut: the Dagger lane
// reads a command manifest as the pod executor does (atomkit.Command): the head
// defaults are in the argv, and preflight = false skips the toolchain's preflight.
func TestCommandInRunsTheComposedArgvAndHonoursThePreflightOptOut(t *testing.T) {
	yes, no := true, false
	m := atomkit.Manifest{
		Argv: []string{"tool", "run"}, Args: "append", Judge: "exit",
		Default: []atomkit.Default{{Args: []string{"-q"}}},
	}
	for _, tc := range []struct {
		name      string
		preflight *bool
		want      [][]string
	}{
		{"unset", nil, [][]string{{"pre", "fetch"}, {"tool", "run", "-q"}}},
		{"true", &yes, [][]string{{"pre", "fetch"}, {"tool", "run", "-q"}}},
		{"false", &no, [][]string{{"tool", "run", "-q"}}},
	} {
		var ran [][]string
		exec := func(_ context.Context, argv []string, _ string) atomkit.ExecResult {
			ran = append(ran, slices.Clone(argv))
			return atomkit.ExecResult{}
		}
		m.Preflight = tc.preflight
		v := commandIn(t.Context(), checks.AtomDef{ID: "dev:t"}, m, []string{"pre", "fetch"}, exec, "/src")
		if v.State != 0 || !slices.EqualFunc(ran, tc.want, slices.Equal) {
			t.Errorf("preflight %s: state %d, ran %v, want %v", tc.name, v.State, ran, tc.want)
		}
	}
}
