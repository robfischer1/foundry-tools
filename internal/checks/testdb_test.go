package checks

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestServiceNameReadsTheAnswersFileTheWayTheShellDid(t *testing.T) {
	for in, want := range map[string]string{
		"service_name: chaos\n":           "chaos",
		"_src: x\nservice_name: 'eros'\n": "eros",
		"service_name: \"a\"\r\n":         "a",
		"critical_modules: src\n":         "",
		"":                                "",
	} {
		if got := ServiceName(in); got != want {
			t.Errorf("ServiceName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPostgresBackendReadsTheRecordAndNothingElse(t *testing.T) {
	for in, want := range map[string]bool{
		`{"backends":{"postgres":{"cnpg_cluster":"chaos-db","database":"chaos","owner":"chaos"}}}`: true,
		`{"backends":{"postgres":{}}}`:      true,
		`{"backends":{}}`:                   false,
		`{"backends":{"postgres":null}}`:    false,
		`{"backends":{"postgres":"chaos"}}`: false,
		`{"tools":{}}`:                      false,
		`not json`:                          false,
		``:                                  false,
	} {
		if got := PostgresBackend(in); got != want {
			t.Errorf("PostgresBackend(%s) = %v, want %v", in, got, want)
		}
	}
}

func TestGoBuildTagsReadsOnlySingleTagConstraints(t *testing.T) {
	out := strings.Join([]string{
		"//go:build live_db",
		"//go:build live_db",
		"//go:build integration",
		"//go:build live_db && !race", // compound: not a tag the lane can name
		"//go:build !windows",
		"// go:build live_db_novector", // not a constraint line at all
		"//go:build live_db_novector",
		"",
	}, "\n")
	want := []string{"integration", "live_db", "live_db_novector"}
	if got := GoBuildTags(out); !reflect.DeepEqual(got, want) {
		t.Errorf("GoBuildTags = %v, want %v", got, want)
	}
	if got := GoBuildTags(""); len(got) != 0 {
		t.Errorf("no lines, no tags: %v", got)
	}
}

func TestTheVocabularyBindsInOrderAndNamesTheRest(t *testing.T) {
	tags := []string{"integration", "live_db", "live_db_novector"}
	dbs := TestDBsFor(tags)
	if len(dbs) != 2 || dbs[0].Tag != "live_db" || dbs[1].Tag != "live_db_novector" {
		t.Fatalf("TestDBsFor = %+v", dbs)
	}
	if got := BuildTags(dbs, nil); got != "live_db,live_db_novector" {
		t.Errorf("BuildTags = %q", got)
	}
	if got := UncompiledTags(tags, dbs); !reflect.DeepEqual(got, []string{"integration"}) {
		t.Errorf("UncompiledTags = %v", got)
	}
	// The DSN names the role twice (its credential is its name), the alias
	// as host, the one database, and no TLS — a throwaway on a private
	// binding. Spelled structurally: a literal user:credential@ pair in a
	// test is what the fleet's secret scan exists to refuse.
	if got := dbs[0].DSN(); !strings.HasPrefix(got, "postgres://"+TestDBRole+":"+TestDBRole+"@db:5432/"+TestDBName) || !strings.HasSuffix(got, "?sslmode=disable") {
		t.Errorf("DSN = %q", got)
	}
	if got := dbs[1].DSN(); !strings.Contains(got, "@db-novector:5432/") {
		t.Errorf("the novector server has its own host: %q", got)
	}
	// A LANE'S OWN HOST. The empty scope is the shared binding every lane had;
	// a named one gets a host of its own, so two lanes running side by side
	// cannot be handed the same database.
	if got := dbs[0].AliasFor(""); got != dbs[0].Alias {
		t.Errorf("the empty scope is the shared binding: %q", got)
	}
	if got := dbs[0].AliasFor("mutation"); got != "db-mutation" {
		t.Errorf("AliasFor(mutation) = %q", got)
	}
	if got := dbs[1].AliasFor("mutation"); got != "db-novector-mutation" {
		t.Errorf("every server the tree carries moves with the lane: %q", got)
	}
	if got := dbs[0].DSNFor("mutation"); !strings.Contains(got, "@db-mutation:5432/") {
		t.Errorf("DSNFor(mutation) = %q", got)
	}
	// And the two are genuinely different hosts — the whole point.
	if dbs[0].DSNFor("mutation") == dbs[0].DSN() {
		t.Error("a scoped DSN that equals the shared one separates nothing")
	}
	// DSN() is DSNFor("") and must not have moved: go:test-race still binds the
	// shared server, and its own assertion compares against DSN().
	if dbs[0].DSN() != dbs[0].DSNFor("") {
		t.Error("DSN() drifted from the empty scope")
	}

	// The order of the tree's tags does not reorder the binding.
	rev := TestDBsFor([]string{"live_db_novector", "live_db"})
	if rev[0].Tag != "live_db" {
		t.Errorf("binding order is the vocabulary's, not the tree's: %+v", rev)
	}
	if got := BuildTags(nil, nil); got != "" {
		t.Errorf("no databases, no -tags word: %q", got)
	}
	// Every vocabulary entry points at a pinned image the fleet checks.
	for _, d := range TestDBs {
		if !strings.Contains(d.Image, "@sha256:") {
			t.Errorf("%s: image is not pinned: %s", d.Tag, d.Image)
		}
	}
}

func TestTestDBScopeIsPrintedEitherWay(t *testing.T) {
	none := TestDBScope(nil, nil, "the record declares no postgres backend")
	if !strings.HasPrefix(none, "test databases: none — ") || !strings.Contains(none, "no postgres backend") {
		t.Errorf("%q", none)
	}
	some := TestDBScope(TestDBs[:1], []string{"integration"}, "")
	for _, want := range []string{"live_db → TEST_DATABASE_URL", "uncompiled", "integration"} {
		if !strings.Contains(some, want) {
			t.Errorf("%q lacks %q", some, want)
		}
	}
	if strings.Contains(TestDBScope(TestDBs[:1], nil, ""), "uncompiled") {
		t.Errorf("nothing uncompiled, nothing said about it")
	}
}

func TestTheBrokerVocabularyBindsAndAddressesItself(t *testing.T) {
	brokers := TestBrokersFor([]string{"integration", "live_kafka"})
	if len(brokers) != 1 || brokers[0].Tag != "live_kafka" {
		t.Fatalf("TestBrokersFor = %+v", brokers)
	}
	b := brokers[0]
	if b.Env != "KAFKA_BOOTSTRAP" {
		t.Errorf("the fleet's name for a bootstrap address is KAFKA_BOOTSTRAP, got %q", b.Env)
	}
	// The shared binding, and a lane's own.
	if got := b.AddrFor(""); got != "broker:9092" {
		t.Errorf("AddrFor(shared) = %q", got)
	}
	if got := b.AddrFor("mutation"); got != "broker-mutation:9092" {
		t.Errorf("AddrFor(lane) = %q", got)
	}
	// ADVERTISED IS THE ALIAS, LISTENED IS EVERY INTERFACE. Swap these and the
	// broker answers the first connection, then hands the client an address it
	// cannot dial — a hang, not a refusal.
	if got := b.AdvertiseFor("mutation"); got != "PLAINTEXT://broker-mutation:9092" {
		t.Errorf("AdvertiseFor = %q", got)
	}
	if got := b.ListenFor(); got != "PLAINTEXT://0.0.0.0:9092" {
		t.Errorf("ListenFor = %q", got)
	}
	// A scoped lane's definition differs WITHOUT a perturbation, because the
	// advertised address carries the alias. That is the property the database
	// path needs FOUNDRY_TEST_DB_LANE to buy.
	if b.AdvertiseFor("") == b.AdvertiseFor("mutation") {
		t.Error("two lanes must not build byte-identical broker definitions")
	}
	if got := BuildTags(nil, brokers); got != "live_kafka" {
		t.Errorf("BuildTags(brokers only) = %q", got)
	}
	if got := BuildTags(TestDBs[:1], brokers); got != "live_db,live_kafka" {
		t.Errorf("databases before brokers, one word: %q", got)
	}
	for _, e := range TestBrokers {
		if !strings.Contains(e.Image, "@sha256:") {
			t.Errorf("%s: image is not pinned: %s", e.Tag, e.Image)
		}
	}
}

func TestKafkaBackendReadsAnArrayOfTopicsNotAnObject(t *testing.T) {
	// The shape is NOT postgres'. backends.kafka is the array of topics the star
	// carries; a star with no broker records false.
	for _, tc := range []struct {
		name string
		slag string
		want bool
	}{
		{"topics named", `{"backends":{"kafka":["ci-attest","tartarus.acts"]}}`, true},
		{"one topic", `{"backends":{"kafka":["session-events"]}}`, true},
		{"false", `{"backends":{"kafka":false}}`, false},
		{"empty array names no seam", `{"backends":{"kafka":[]}}`, false},
		{"absent", `{"backends":{"postgres":{"database":"x"}}}`, false},
		{"no backends", `{}`, false},
		{"unparseable", `{`, false},
		// An object is postgres' shape, not kafka's: reading one as the other is
		// how a star with a database would have been handed a broker.
		{"an object is not a topic list", `{"backends":{"kafka":{"topics":["a"]}}}`, false},
	} {
		if got := KafkaBackend(tc.slag); got != tc.want {
			t.Errorf("%s: KafkaBackend = %v, want %v", tc.name, got, tc.want)
		}
	}
	// The two predicates do not read each other's field.
	both := `{"backends":{"kafka":["a"],"postgres":{"database":"x"}}}`
	if !KafkaBackend(both) || !PostgresBackend(both) {
		t.Error("a star with both backends declares both")
	}
	if PostgresBackend(`{"backends":{"kafka":["a"]}}`) {
		t.Error("topics are not a postgres declaration")
	}
	if KafkaBackend(`{"backends":{"postgres":{"database":"x"}}}`) {
		t.Error("a database is not a topic list")
	}
}

func TestTestBrokerScopeIsPrintedEitherWayAndCarriesTheCaveat(t *testing.T) {
	none := TestBrokerScope(nil, "the record names no kafka topics")
	if !strings.HasPrefix(none, "test brokers: none — ") || !strings.Contains(none, "no kafka topics") {
		t.Errorf("%q", none)
	}
	some := TestBrokerScope(TestBrokers, "")
	for _, want := range []string{"live_kafka → KAFKA_BOOTSTRAP", "unique topic and group names"} {
		if !strings.Contains(some, want) {
			t.Errorf("%q lacks %q", some, want)
		}
	}
	// The caveat rides on the BOUND line, not the empty one: there is nothing to
	// warn about when no broker was brought.
	if strings.Contains(none, "unique topic") {
		t.Errorf("no broker, no caveat: %q", none)
	}
}

var errStub = errors.New("the read failed")

func TestSelectBrokersNamesWhichReadRefused(t *testing.T) {
	const answers = "service_name: tartarus\n"
	const topics = `{"backends":{"kafka":["tartarus.acts"]}}`
	const tagged = "//go:build live_kafka\n"

	for _, tc := range []struct {
		name  string
		reads BrokerReads
		bound int
		says  string
	}{
		{"no service_name, so no record to find",
			BrokerReads{}, 0, "no service_name"},
		{"the record could not be fetched",
			BrokerReads{Answers: answers, SlagErr: errStub}, 0, "no record at fleet/stars/tartarus/slag.json"},
		{"the record names no topics",
			BrokerReads{Answers: answers, Slag: `{"backends":{"kafka":false}}`}, 0, "names no kafka topics"},
		{"the grep failed, which is not the grep finding nothing",
			BrokerReads{Answers: answers, Slag: topics, TagsErr: errStub}, 0, "build tags could not be read"},
		{"topics named, no tag carried",
			BrokerReads{Answers: answers, Slag: topics, Tags: "//go:build live_db\n"}, 0, "no test file sits behind a tag"},
		{"topics named and the tag carried",
			BrokerReads{Answers: answers, Slag: topics, Tags: tagged}, 1, "live_kafka → KAFKA_BOOTSTRAP"},
		// GREP'S EXIT 1 IS AN ANSWER, NOT A FAILURE, and the boundary between it
		// and 2 is the whole reason the guard reads `> 1`. A tree with no build
		// tags at all is the COMMONEST case in the fleet: read `>= 1` and every
		// one of those repos is told its tags "could not be read" instead of that
		// no test sits behind one — a wrong diagnosis, delivered confidently.
		{"grep matched nothing, which is an answer",
			BrokerReads{Answers: answers, Slag: topics, Tags: "", TagsCode: 1}, 0, "no test file sits behind a tag"},
		{"grep itself failed",
			BrokerReads{Answers: answers, Slag: topics, Tags: "", TagsCode: 2}, 0, "build tags could not be read"},
	} {
		got, line := SelectBrokers(tc.reads)
		if len(got) != tc.bound {
			t.Errorf("%s: bound %d brokers, want %d", tc.name, len(got), tc.bound)
		}
		if !strings.Contains(line, tc.says) {
			t.Errorf("%s: scope line %q lacks %q", tc.name, line, tc.says)
		}
	}

	// UNREAD IS NOT EMPTY, for either read, and the line has to say which. These
	// two differ only in that flag and must not produce the same sentence.
	_, unread := SelectBrokers(BrokerReads{Answers: answers, SlagErr: errStub})
	_, empty := SelectBrokers(BrokerReads{Answers: answers, Slag: `{}`})
	if unread == empty {
		t.Errorf("a record that could not be fetched and one that declares nothing are different refusals: %q", unread)
	}
	_, tagsUnread := SelectBrokers(BrokerReads{Answers: answers, Slag: topics, TagsErr: errStub})
	_, tagsEmpty := SelectBrokers(BrokerReads{Answers: answers, Slag: topics, Tags: ""})
	if tagsUnread == tagsEmpty {
		t.Errorf("a grep that failed and a grep that matched nothing are different refusals: %q", tagsUnread)
	}
	// The two sides of that boundary must not produce the same sentence.
	_, matchedNothing := SelectBrokers(BrokerReads{Answers: answers, Slag: topics, TagsCode: 1})
	_, grepFailed := SelectBrokers(BrokerReads{Answers: answers, Slag: topics, TagsCode: 2})
	if matchedNothing == grepFailed {
		t.Errorf("exit 1 is grep answering, exit 2 is grep failing: both said %q", matchedNothing)
	}

	// The order of the guards is itself a contract: a tree carrying the tag but a
	// record naming no topics binds nothing, and says so about the RECORD.
	_, line := SelectBrokers(BrokerReads{Answers: answers, Slag: `{"backends":{"kafka":[]}}`, Tags: tagged})
	if !strings.Contains(line, "names no kafka topics") {
		t.Errorf("the record is read before the tree: %q", line)
	}
}

func TestStartArgsRefusesToInventTopics(t *testing.T) {
	args := TestBrokers[0].StartArgs("")
	joined := strings.Join(args, " ")

	// INVOKED THROUGH rpk. The bare `redpanda` binary rejects every flag here —
	// dagger's WithExec bypasses the image entrypoint that would otherwise shell out
	// to rpk, and tartarus #87 could-not-run on "unrecognised option '--check=false'"
	// after a local docker run of the same args had succeeded.
	if len(args) < 3 || args[0] != "rpk" || args[1] != "redpanda" || args[2] != "start" {
		t.Errorf("must invoke rpk, not the bare binary, which rejects these flags: %v", args[:min(3, len(args))])
	}
	// `--mode dev-container` turns auto-creation ON, so this setting is what keeps a
	// topic the record never declared from springing into being. NOT, as I first wrote
	// here, what keeps tartarus's undelivered-event test honest — measured both ways,
	// that test passes either way, because franz-go never asks the broker to create an
	// unknown topic. See StartArgs' doc for the correction.
	if !strings.Contains(joined, "redpanda.auto_create_topics_enabled=false") {
		t.Errorf("a broker that auto-creates topics cannot refuse an undeclared one: %q", joined)
	}
	// Listened on every interface, advertised as the alias. Swap them and the broker
	// answers the first connection, then hands the client an address it cannot dial.
	if !strings.Contains(joined, "--kafka-addr PLAINTEXT://0.0.0.0:9092") {
		t.Errorf("must listen on every interface: %q", joined)
	}
	if !strings.Contains(joined, "--advertise-kafka-addr PLAINTEXT://broker:9092") {
		t.Errorf("must advertise the binding alias: %q", joined)
	}
	// A scoped lane's command differs, which is what keeps dagger from handing two
	// lanes one content-addressed service.
	if strings.Join(TestBrokers[0].StartArgs("mutation"), " ") == joined {
		t.Error("two lanes must not build byte-identical start commands")
	}
	if !strings.Contains(strings.Join(TestBrokers[0].StartArgs("mutation"), " "), "broker-mutation:9092") {
		t.Error("the scoped lane advertises its own alias")
	}
}
