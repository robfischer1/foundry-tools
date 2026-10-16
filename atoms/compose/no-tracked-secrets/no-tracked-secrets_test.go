package composenotrackedsecrets

import (
	"context"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/atomlib/atomtest"
	"dagger/foundry-tools/internal/atoms"
	"dagger/foundry-tools/internal/checks"
)

// run is the atom's check over an input, as the runner calls it.
func run(in atoms.Input) checks.Verdict { return check(context.Background(), in) }

func TestTheDeclarationIsTheRowItReplaced(t *testing.T) {
	atomtest.DeclaresItsRow(t, Def, "precommit", "none")
	if Def.ID != id {
		t.Errorf("%+v", Def)
	}
}

func TestComposeNoTrackedSecrets(t *testing.T) {
	for _, tc := range []struct {
		name    string
		files   map[string]string
		state   int
		result  string
		needles []string
	}{
		{"no compose spec is absent", map[string]string{".env": "S=1"}, 0, atomtest.Absent, []string{"tracks no compose.yaml/compose.yml"}},
		{"a spec and no credential passes", map[string]string{"compose.yaml": "x"}, 0, atomtest.Pass, []string{"no credential-shaped file is tracked"}},
		{"a tracked credential file beside a spec is a finding", map[string]string{"compose.yaml": "x", "stack/.env": "S=1", "keys/id.pem": "k"}, 1, atomtest.Findings,
			[]string{"tracked files that must never be committed", "  stack/.env", "  keys/id.pem"}},
		// The compose atoms read the RAW tree: the chains never applied the
		// fleet exclude to this lane, so a vendored spec is still a spec.
		{"a spec under an excluded directory still counts", map[string]string{"vendor/compose.yaml": "x"}, 0, atomtest.Pass, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			atomtest.Expect(t, run(atomtest.TreeIn(t, tc.files)), atomtest.StateOf(tc.state), tc.result, tc.needles...)
		})
	}
	t.Run("a scan that failed is not an absence the repository declared", func(t *testing.T) {
		in := atomtest.TreeIn(t, map[string]string{"compose.yaml": "x"})
		in.FilesErr = atomtest.ErrBoom
		atomtest.Expect(t, run(in), atomtest.StateOf(2), atomtest.Cannot, "the compose-surface scan itself failed (boom)")
	})
}

// THE IGNORE RULE IS ASSERTED, NOT TRUSTED: an ignore rule only protects files
// it was written before. Every credential shape is named.
func TestComposeNoTrackedSecretsNamesEveryCredentialShape(t *testing.T) {
	in := atomtest.TreeIn(t, map[string]string{
		"compose.yaml":        "x",
		".env":                "A=1\n",
		"tls/site.pem":        "",
		"keys/id_rsa":         "",
		"deploy/deploy.key":   "",
		"envs/prod/values.md": "",
		".env.production":     "",
	})
	v := run(in)
	atomtest.Expect(t, v, atomtest.StateOf(1), atomtest.Findings, "must never be committed")
	for _, hit := range []string{".env", "tls/site.pem", "keys/id_rsa", "deploy/deploy.key", "envs/prod/values.md", ".env.production"} {
		if !strings.Contains(v.Reason+strings.Join(v.Logs, "\n"), hit) {
			t.Errorf("the findings must name %s:\n%s", hit, v.Reason)
		}
	}
}

// A tree that tracks no credential and no spec but the one: the lane runs no
// program at all, the question is the file list.
func TestComposeNoTrackedSecretsRunsNoProgram(t *testing.T) {
	in, f := atomtest.ToolTree(t, map[string]string{"compose.yaml": "x"}, nil)
	atomtest.Expect(t, run(in), atomtest.StateOf(0), atomtest.Pass)
	if len(f.Calls) != 0 {
		t.Errorf("a file-list question ran %v", f.Ran())
	}
}
