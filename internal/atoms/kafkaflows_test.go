package atoms

import (
	"testing"

	"dagger/foundry-tools/internal/checks"
)

func TestKafkaFlows(t *testing.T) {
	const id = checks.KafkaFlowsID
	t.Run("a tree's flows are findings on a pass", func(t *testing.T) {
		in := treeIn(t, map[string]string{
			"go.mod": "module example.com/x\n",
			"a.go":   "package a\nimport (\"github.com/twmb/franz-go/pkg/kgo\"; \"git.notusmi.com/rob/stellar-core-go/kafkatopics\")\nfunc p() { kgo.NewClient(kgo.ConsumerGroup(\"g\"), kgo.ConsumeTopics(\"t\", kafkatopics.TopicGone)) }\n",
		})
		in.Door = deadDoor(t)
		v := runAtom(t, id, in)
		expect(t, v, stateOf(0), pass, "2 Kafka flow(s), 1 unresolved", "could not be read")
		if len(v.Findings) != 2 {
			t.Fatalf("findings %+v", v.Findings)
		}
	})
	t.Run("a tree with no flow says so in a finding", func(t *testing.T) {
		v := runAtom(t, id, treeIn(t, map[string]string{"README.md": "x"}))
		expect(t, v, stateOf(0), pass, "no Kafka flow")
		if len(v.Findings) != 1 || v.Findings[0].Cause != checks.FlowNone {
			t.Fatalf("findings %+v", v.Findings)
		}
	})
	t.Run("a source file that will not read is a 2", func(t *testing.T) {
		in := treeIn(t, map[string]string{"a": "x"})
		in.Files = append(in.Files, "gone.go")
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "gone.go (", "would not read")
	})
	t.Run("a tree that would not enumerate", func(t *testing.T) {
		in := missingRoot(t)
		in.FilesErr = errBoom
		expect(t, runAtom(t, id, in), stateOf(2), cannot, "the tree would not enumerate")
	})
}
