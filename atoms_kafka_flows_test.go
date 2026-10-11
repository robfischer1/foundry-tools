package main

import (
	"net/http"
	"testing"
)

func TestFleetKafkaFlowsRecordsTheTreesFlows(t *testing.T) {
	engine.reset()
	engine.withTree(fleetTree(map[string]string{
		"go.mod": "module example.com/x\n",
		"a.go":   "package a\nimport \"git.notusmi.com/rob/stellar-core-go/kafkatopics\"\nfunc p() { kafkatopics.Record(kafkatopics.TopicSessionTurns, nil) }\n",
	}))
	fakeDoor(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("package kafkatopics\nconst TopicSessionTurns = \"session-turns\"\n"))
	})
	v := runAtom(t, "fleet:kafka-flows", "")
	wantReport(t, v, 0, "1 Kafka flow(s), 0 unresolved", "session-turns produce")
	if len(v.Findings) != 1 || v.Findings[0].Subject != "session-turns" {
		t.Errorf("findings %+v", v.Findings)
	}
	fleetNoContainer(t, "kafka-flows reads the tree from the module")
}

func TestFleetKafkaFlowsIsAPassOnATreeWithNone(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	wantReport(t, runAtom(t, "fleet:kafka-flows", ""), 0, "no Kafka flow")
}

func TestFleetKafkaFlowsCannotRunOnATreeItCannotRead(t *testing.T) {
	engine.reset()
	engine.withTree(fleetTree(map[string]string{"a.go": "package a\n"}))
	engine.fail(`file(path:"a.go")`, "i/o error")
	wantReport(t, runAtom(t, "fleet:kafka-flows", ""), 2, "CANNOT RUN", "a.go (", "would not read")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`glob(pattern:"**")`, "mount evaporated")
	wantReport(t, runAtom(t, "fleet:kafka-flows", ""), 2, "the tree would not enumerate", "mount evaporated")
}
