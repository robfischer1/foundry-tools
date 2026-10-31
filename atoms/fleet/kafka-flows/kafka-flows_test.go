package fleetkafkaflows

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/atomkit"
	"dagger/foundry-tools/internal/checks"
)

// put writes a file, making its directories.
func put(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// treeIn writes files to a directory and answers the In an atom is handed over
// it, WITHOUT git: the population is the file list, as Collect would build it
// for a tree with nothing ignored.
func treeIn(t *testing.T, files map[string]string) atomkit.In {
	t.Helper()
	dir := t.TempDir()
	var names []string
	for name, body := range files {
		put(t, dir, name, body)
		names = append(names, name)
	}
	sort.Strings(names)
	return atomkit.In{
		Root: dir, Files: checks.GatePopulation(names), Committable: names, Tracked: names,
		Now: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC),
	}
}

// run runs the atom over an input.
func run(t *testing.T, in atomkit.In) checks.Verdict {
	t.Helper()
	return check(context.Background(), in)
}

// expect holds a verdict to its state, its result word (an ABSENT pass reads
// "absent") and the words it must carry. A pass's Reason is only "<id>: PASS" —
// what the atom said rides in its Logs — so the words are looked for in both.
func expect(t *testing.T, v checks.Verdict, state checks.State, result string, needles ...string) {
	t.Helper()
	if v.State != int(state) || v.Result != result {
		t.Errorf("%s: state %d (%s), want %d (%s)\n%s", v.Atom, v.State, v.Result, state, result, v.Reason)
	}
	said := v.Reason + "\n" + strings.Join(v.Logs, "\n")
	for _, n := range needles {
		if !strings.Contains(said, n) {
			t.Errorf("%s: reason and logs lack %q:\n%s", v.Atom, n, said)
		}
	}
}

var pass = checks.StatePass.String()

var cannot = checks.StateCannotRun.String()

// stateOf is a table's plain int as the catalogue's State.
func stateOf(n int) checks.State { return checks.State(n) }

// doorOf answers for the git door: "<repo> <path>" to the body it serves; any
// other file is a 404, which is what the door says for a path it does not hold.
func doorOf(t *testing.T, answers map[string]string) checks.Door {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := answers[r.URL.Query().Get("repo")+" "+r.URL.Query().Get("path")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return checks.Door{Base: srv.URL}
}

// deadDoor is a door nothing answers at: connection refused.
func deadDoor(t *testing.T) checks.Door {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	return checks.Door{Base: base, NoRetry: true}
}

// missingRoot is an input over a directory that is not there: the atoms that
// open with the root's listing settle 2.
func missingRoot(t *testing.T) atomkit.In {
	t.Helper()
	return atomkit.In{Root: t.TempDir() + "/not-there", Now: time.Now()}
}

var errBoom = errors.New("boom")

func TestKafkaFlows(t *testing.T) {
	t.Run("a tree's flows are findings on a pass", func(t *testing.T) {
		in := treeIn(t, map[string]string{
			"go.mod": "module example.com/x\n",
			"a.go":   "package a\nimport (\"github.com/twmb/franz-go/pkg/kgo\"; \"git.notusmi.com/rob/stellar-core-go/kafkatopics\")\nfunc p() { kgo.NewClient(kgo.ConsumerGroup(\"g\"), kgo.ConsumeTopics(\"t\", kafkatopics.TopicGone)) }\n",
		})
		in.Door = deadDoor(t)
		v := run(t, in)
		expect(t, v, stateOf(0), pass, "2 Kafka flow(s), 1 unresolved", "could not be read")
		if len(v.Findings) != 2 {
			t.Fatalf("findings %+v", v.Findings)
		}
	})
	t.Run("a tree with no flow says so in a finding", func(t *testing.T) {
		v := run(t, treeIn(t, map[string]string{"README.md": "x"}))
		expect(t, v, stateOf(0), pass, "no Kafka flow")
		if len(v.Findings) != 1 || v.Findings[0].Cause != checks.FlowNone {
			t.Fatalf("findings %+v", v.Findings)
		}
	})
	t.Run("a source file that will not read is a 2", func(t *testing.T) {
		in := treeIn(t, map[string]string{"a": "x"})
		in.Files = append(in.Files, "gone.go")
		expect(t, run(t, in), stateOf(2), cannot, "gone.go (", "would not read")
	})
	t.Run("a tree that would not enumerate", func(t *testing.T) {
		in := missingRoot(t)
		in.FilesErr = errBoom
		expect(t, run(t, in), stateOf(2), cannot, "the tree would not enumerate")
	})
}

func TestKafkaFlowsRecordsTheTreesFlows(t *testing.T) {
	in := treeIn(t, map[string]string{
		"go.mod": "module example.com/x\n",
		"a.go":   "package a\nimport \"git.notusmi.com/rob/stellar-core-go/kafkatopics\"\nfunc p() { kafkatopics.Record(kafkatopics.TopicSessionTurns, nil) }\n",
	})
	in.Door = doorOf(t, map[string]string{
		// the door serves the constants where stellar-core-go went: the monorepo's go/ (rob/stellar-core-go 404s)
		"rob/stellar-core go/kafkatopics/topics.go":  "package kafkatopics\nconst TopicSessionTurns = \"session-turns\"\n",
		"rob/stellar-core go/heartbeat/heartbeat.go": "package heartbeat\n",
	})
	v := run(t, in)
	expect(t, v, stateOf(0), pass, "1 Kafka flow(s), 0 unresolved", "session-turns produce")
	if len(v.Findings) != 1 || v.Findings[0].Subject != "session-turns" {
		t.Errorf("findings %+v", v.Findings)
	}
}

// The declaration is the catalogue row it replaced: the same stage, group, text and
// fork lane, and the needs the split table gives it.
func TestTheDeclarationIsTheRowItReplaced(t *testing.T) {
	if err := atomkit.Validate(Def); err != nil {
		t.Fatal(err)
	}
	if Def.ID != id || Def.Stage != checks.StagePrecommit || Def.Lane != checks.LaneAny || Def.Group != checks.GroupOf(Def.ID) ||
		Def.Toolchain != "none" || Def.NotOnForks != "" || Def.SubsumedBy != "" || Def.NeedsManifest {
		t.Errorf("%+v", Def)
	}
	if Def.Desc != "Records the Kafka topics this tree produces to and consumes from (and each consumer group) as findings, for nyx; never fails a tree for having flows or an unresolved name." {
		t.Errorf("the description moved: %q", Def.Desc)
	}
	if !slices.Equal(Def.Needs, nil) {
		t.Errorf("needs %v", Def.Needs)
	}
}
