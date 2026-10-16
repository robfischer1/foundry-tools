package checks

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestReadAllAnswersEveryFileOrTheFirstThatWouldNotRead(t *testing.T) {
	bodies := map[string]string{"a.go": "A", "b.go": "B"}
	read := func(p string) (string, error) {
		if body, ok := bodies[p]; ok {
			return body, nil
		}
		return "", errors.New("gone " + p)
	}
	got, bad := ReadAll(read, []string{"a.go", "b.go"})
	if bad != "" || got["a.go"] != "A" || got["b.go"] != "B" || len(got) != 2 {
		t.Errorf("got %v, %q", got, bad)
	}
	if got, bad := ReadAll(read, nil); bad != "" || len(got) != 0 {
		t.Errorf("no paths: %v, %q", got, bad)
	}
	// A file never read is a file never judged: the answer is the first failure,
	// sorted, and no map.
	got, bad = ReadAll(read, []string{"z.go", "a.go", "m.go"})
	if got != nil || bad != "m.go (gone m.go) would not read" {
		t.Errorf("got %v, %q", got, bad)
	}
}

func TestWithSuffixKeepsTheFilesInOrder(t *testing.T) {
	files := []string{"a.go", "b.py", "c.rs", "d.go", "e.txt"}
	if got := WithSuffix(files, ".go", ".py"); !slices.Equal(got, []string{"a.go", "b.py", "d.go"}) {
		t.Errorf("got %v", got)
	}
	if got := WithSuffix(files, ".md"); got != nil {
		t.Errorf("got %v", got)
	}
}

const stampedAnswers = "_commit: v0.23.0\n_src_path: https://forgejo.notusmi.com/rob/go-repo-template.git\nproject_name: X\n"

func TestAbsentRulesetIsReadAgainstIntent(t *testing.T) {
	a := AtomDef{ID: "fleet:opengrep-sast", Stage: StagePrecommit, Lane: LaneAny}
	for _, tc := range []struct {
		name, answers string
	}{
		{"a tree nobody stamped", ""},
		{"an answers file that is not one of ours", "_src_path: https://forgejo.notusmi.com/rob/speckit-overrides.git\n"},
		{"a template that ships no ruleset", "_src_path: https://git.notusmi.com/config-repo-template.git\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := AbsentRuleset(a, tc.answers)
			if v.State != int(StatePass) || v.Result != "absent" || !strings.Contains(v.Reason+strings.Join(v.Logs, "\n"), "no SAST-shipping fleet template stamped it") {
				t.Errorf("%+v", v)
			}
		})
	}
	t.Run("a tree a fleet template stamped never received its ruleset", func(t *testing.T) {
		v := AbsentRuleset(a, stampedAnswers)
		if v.State != int(StateCannotRun) || !strings.Contains(v.Reason, "go-repo-template stamped it") {
			t.Fatalf("%+v", v)
		}
		// The template name is sliced exactly: an off-by-one keeps "/go-repo-template".
		if !strings.Contains(v.Reason, "copy go-repo-template's template/rules/sast/dataflow.yml") || strings.Contains(v.Reason, "/go-repo-template's") {
			t.Errorf("the template name is not sliced cleanly:\n%s", v.Reason)
		}
	})
}
