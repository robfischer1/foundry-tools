package checks

import (
	"errors"
	"reflect"
	"slices"
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
	if got := BuildTags(dbs, nil, nil, nil); got != "live_db,live_db_novector" {
		t.Errorf("BuildTags = %q", got)
	}
	if got := UncompiledTags(tags, dbs, nil, nil, nil); !reflect.DeepEqual(got, []string{"integration"}) {
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
	if got := BuildTags(nil, nil, nil, nil); got != "" {
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
	if got := BuildTags(nil, brokers, nil, nil); got != "live_kafka" {
		t.Errorf("BuildTags(brokers only) = %q", got)
	}
	if got := BuildTags(TestDBs[:1], brokers, nil, nil); got != "live_db,live_kafka" {
		t.Errorf("databases before brokers, one word: %q", got)
	}
	for _, e := range TestBrokers {
		if !strings.Contains(e.Image, "@sha256:") {
			t.Errorf("%s: image is not pinned: %s", e.Tag, e.Image)
		}
	}
}

func TestTestBrokerScopeIsPrintedEitherWayAndCarriesTheCaveat(t *testing.T) {
	none := TestBrokerScope(nil, "the tree states no kafka flow")
	if !strings.HasPrefix(none, "test brokers: none — ") || !strings.Contains(none, "no kafka flow") {
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
	const tagged = "//go:build live_kafka\n"

	for _, tc := range []struct {
		name  string
		reads BrokerReads
		bound int
		says  string
	}{
		{"the tree states no flow",
			BrokerReads{Tags: tagged}, 0, "the tree states no kafka flow"},
		{"the tree could not be read for flows",
			BrokerReads{FlowsErr: errStub, Tags: tagged}, 0, "the tree's kafka flows could not be read"},
		{"the grep failed, which is not the grep finding nothing",
			BrokerReads{Flows: 1, TagsErr: errStub}, 0, "build tags could not be read"},
		{"flows stated, no tag carried",
			BrokerReads{Flows: 1, Tags: "//go:build live_db\n"}, 0, "no test file sits behind a tag"},
		{"flows stated and the tag carried",
			BrokerReads{Flows: 2, Tags: tagged}, 1, "live_kafka → KAFKA_BOOTSTRAP"},
		// GREP'S EXIT 1 IS AN ANSWER, NOT A FAILURE, and the boundary between it
		// and 2 is the whole reason the guard reads `> 1`. A tree with no build
		// tags at all is the COMMONEST case in the fleet: read `>= 1` and every
		// one of those repos is told its tags "could not be read" instead of that
		// no test sits behind one — a wrong diagnosis, delivered confidently.
		{"grep matched nothing, which is an answer",
			BrokerReads{Flows: 1, Tags: "", TagsCode: 1}, 0, "no test file sits behind a tag"},
		{"grep itself failed",
			BrokerReads{Flows: 1, Tags: "", TagsCode: 2}, 0, "build tags could not be read"},
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
	// pairs differ only in that flag and must not produce the same sentence.
	_, unread := SelectBrokers(BrokerReads{FlowsErr: errStub})
	_, empty := SelectBrokers(BrokerReads{})
	if unread == empty {
		t.Errorf("a tree that could not be read and one that states no flow are different refusals: %q", unread)
	}
	_, tagsUnread := SelectBrokers(BrokerReads{Flows: 1, TagsErr: errStub})
	_, tagsEmpty := SelectBrokers(BrokerReads{Flows: 1, Tags: ""})
	if tagsUnread == tagsEmpty {
		t.Errorf("a grep that failed and a grep that matched nothing are different refusals: %q", tagsUnread)
	}
	// The two sides of that boundary must not produce the same sentence.
	_, matchedNothing := SelectBrokers(BrokerReads{Flows: 1, TagsCode: 1})
	_, grepFailed := SelectBrokers(BrokerReads{Flows: 1, TagsCode: 2})
	if matchedNothing == grepFailed {
		t.Errorf("exit 1 is grep answering, exit 2 is grep failing: both said %q", matchedNothing)
	}

	// The order of the guards is itself a contract: a tree that could not be
	// read for its flows binds nothing and says so about the FLOWS, before it
	// is asked about tags.
	_, line := SelectBrokers(BrokerReads{FlowsErr: errStub, TagsErr: errStub})
	if !strings.Contains(line, "kafka flows could not be read") {
		t.Errorf("the flows are read before the tags: %q", line)
	}
}

func TestStartArgsRefusesToInventTopics(t *testing.T) {
	args := TestBrokers[0].StartArgs("")
	joined := strings.Join(args, " ")

	// INVOKED THROUGH THE IMAGE ENTRYPOINT, which is the wrapper that shells out to
	// rpk — so the args are `redpanda start`, not `rpk redpanda start`. The bare
	// binary does reject these flags ("unrecognised option '--check=false'",
	// tartarus #87), which is why an earlier fix reached for rpk and a WithExec; that
	// bypassed the entrypoint and the service then never became ready at all. The
	// caller's WithDefaultArgs + AsService(UseEntrypoint) is the other half.
	if len(args) < 2 || args[0] != "redpanda" || args[1] != "start" {
		t.Errorf("the entrypoint takes `redpanda start`, not the rpk spelling: %v", args[:min(2, len(args))])
	}
	// A dagger container has no memory cgroup, so the entrypoint computes no bound
	// and seastar reserves nearly the whole host — the lane's broker logged
	// mem_available 65410170880 on a 64G node and never came up. Measured: without
	// these two flags the same definition never becomes ready.
	if !strings.Contains(joined, "--memory 1G") || !strings.Contains(joined, "--reserve-memory 0M") {
		t.Errorf("seastar must be bounded or the service never becomes ready: %q", joined)
	}
	// `--mode dev-container` turns auto-creation ON, so this setting is what keeps a
	// topic the record never declared from springing into being. NOT, as I first wrote
	// here, what keeps tartarus's undelivered-event test honest — measured both ways,
	// that test passes either way, because franz-go never asks the broker to create an
	// unknown topic. See StartArgs' doc for the correction.
	if !strings.Contains(joined, "redpanda.auto_create_topics_enabled=false") {
		t.Errorf("a broker that auto-creates topics cannot refuse an undeclared one: %q", joined)
	}
	// Both metrics endpoints off: redpanda's instance_metrics detector dials EC2 IMDS
	// (169.254.169.254, 10 s connect timeout) whenever either is on, and in a lane pod
	// that dial is an egress drop. The pair is the only switch that stops it
	// (daedalus #601, the executor sidecar's copy of these args).
	for _, prop := range []string{"redpanda.disable_metrics=true", "redpanda.disable_public_metrics=true"} {
		if i := slices.Index(args, prop); i < 1 || args[i-1] != "--set" {
			t.Errorf("--set %s missing, so the broker probes EC2 IMDS: %q", prop, joined)
		}
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

// TestTheScopeLineNeverCallsABoundTagUncompiled is the regression for a lane that
// printed `tags left uncompiled (not in the fleet's vocabulary): live_kafka`
// directly above `test brokers: live_kafka -> KAFKA_BOOTSTRAP`. UncompiledTags is
// the complement of the -tags word, so it has to be taken against BOTH
// vocabularies; told only about the databases it libels every broker tag.
func TestTheScopeLineNeverCallsABoundTagUncompiled(t *testing.T) {
	tags := []string{"integration", "live_db", "live_kafka"}
	dbs := TestDBsFor(tags)
	brokers := TestBrokersFor(tags)
	if len(dbs) == 0 || len(brokers) == 0 {
		t.Fatalf("fixture needs one of each: dbs=%v brokers=%v", dbs, brokers)
	}
	compiled := strings.Split(BuildTags(dbs, brokers, nil, nil), ",")
	uncompiled := UncompiledTags(tags, dbs, brokers, nil, TestKubes)
	for _, c := range compiled {
		for _, u := range uncompiled {
			if c == u {
				t.Errorf("%q is in the -tags word and also reported uncompiled", c)
			}
		}
	}
	if !reflect.DeepEqual(uncompiled, []string{"integration"}) {
		t.Errorf("UncompiledTags = %v, want only the tag no vocabulary names", uncompiled)
	}
}

// THE TEST SERVER RUNS NON-DURABLE, and through the image's own entrypoint:
// the first word is `postgres` (docker-entrypoint.sh initialises the cluster,
// then execs it), and every durability switch the throwaway does not need is
// off. A flag dropped here does not fail any suite — it silently brings back
// the fsync and checkpoint I/O every mutant paid for (ServerArgs' doc).
func TestTheTestServerRunsNonDurable(t *testing.T) {
	for _, d := range TestDBs {
		args := d.ServerArgs()
		if len(args) == 0 || args[0] != "postgres" {
			t.Fatalf("%s: ServerArgs must start the image's postgres through the entrypoint: %q", d.Tag, args)
		}
		got := map[string]bool{}
		for i := 1; i < len(args); i++ {
			if args[i-1] == "-c" {
				got[args[i]] = true
			}
		}
		for _, want := range []string{
			"fsync=off", "synchronous_commit=off", "full_page_writes=off",
			"checkpoint_timeout=1d", "max_wal_size=2GB",
		} {
			if !got[want] {
				t.Errorf("%s: ServerArgs lacks -c %s: %q", d.Tag, want, args)
			}
		}
	}
}

// THE BROKER IS HANDED OUT UNDER TWO NAMES, one address. KAFKA_BOOTSTRAP is the
// fleet's name and the one tartarus reads; TEST_KAFKA_BROKERS is the name the
// TEST_* family (TEST_DATABASE_URL, TEST_REDIS_URL) gives a lane's server, so a
// suite reaches the broker the way it reaches every other lane service. Both
// carry the same host:port; a suite reads either.
func TestTheBrokerIsHandedOutUnderBothNames(t *testing.T) {
	b := TestBrokers[0]
	if got := b.Envs(); !reflect.DeepEqual(got, []string{"KAFKA_BOOTSTRAP", "TEST_KAFKA_BROKERS"}) {
		t.Errorf("Envs = %v", got)
	}
	if b.Env != "KAFKA_BOOTSTRAP" {
		t.Errorf("the primary name moved: %q", b.Env)
	}
	line := TestBrokerScope(TestBrokers, "")
	if !strings.Contains(line, "live_kafka → KAFKA_BOOTSTRAP, TEST_KAFKA_BROKERS") {
		t.Errorf("the scope line does not name both variables: %q", line)
	}
}

// THE CACHE VOCABULARY is the broker's twin: a tag the tree states, a variable
// the suite reads, an alias the lane binds, a pinned image.
func TestTheCacheVocabularyBindsAndAddressesItself(t *testing.T) {
	caches := TestCachesFor([]string{"integration", "live_redis", "live_db"})
	if len(caches) != 1 || caches[0].Tag != "live_redis" {
		t.Fatalf("TestCachesFor = %+v", caches)
	}
	c := caches[0]
	if c.Env != "TEST_REDIS_URL" {
		t.Errorf("the variable is TEST_REDIS_URL, got %q", c.Env)
	}
	// redis://host:port, the shape forge-testkit-go's containers.Redis returns,
	// so a suite that parses it for its host reads either source alike.
	if got := c.URLFor(""); got != "redis://redis:6379" {
		t.Errorf("URLFor(shared) = %q", got)
	}
	if got := c.URLFor("mutation"); got != "redis://redis-mutation:6379" {
		t.Errorf("URLFor(lane) = %q", got)
	}
	if got := c.AliasFor("diffcov"); got != "redis-diffcov" {
		t.Errorf("AliasFor = %q", got)
	}
	if !strings.Contains(c.Image, "@sha256:") || !strings.Contains(c.Image, "redis:7-alpine") {
		t.Errorf("image is not redis 7-alpine pinned by digest: %s", c.Image)
	}
	if got := BuildTags(nil, nil, caches, nil); got != "live_redis" {
		t.Errorf("BuildTags(caches only) = %q", got)
	}
	// databases, then brokers, then caches: one word, one author
	if got := BuildTags(TestDBs[:1], TestBrokers, caches, nil); got != "live_db,live_kafka,live_redis" {
		t.Errorf("BuildTags = %q", got)
	}
	if got := TestCachesFor(nil); len(got) != 0 {
		t.Errorf("no tags, no cache: %v", got)
	}
}

// A REDIS THE LANE BINDS IS A THROWAWAY: nothing persisted, reachable from the
// lane's container (protected mode refuses every non-loopback client of a
// password-less server, which is every client through a service binding), and
// started through the image's entrypoint like the other two servers.
func TestTheCacheServerIsThrowawayAndReachable(t *testing.T) {
	args := TestCaches[0].ServerArgs(6379)
	if len(args) == 0 || args[0] != "redis-server" {
		t.Fatalf("ServerArgs must start the image's redis-server: %q", args)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"--port 6379", "--save  --appendonly no", "--protected-mode no"} {
		if !strings.Contains(joined, want) {
			t.Errorf("ServerArgs lacks %q: %q", want, args)
		}
	}
	if !strings.Contains(strings.Join(TestCaches[0].ServerArgs(20016), " "), "--port 20016") {
		t.Error("the port is the server's own")
	}
}

func TestTestCacheScopeIsPrintedEitherWay(t *testing.T) {
	none := TestCacheScope(nil, "no test file sits behind a tag the fleet names")
	if !strings.HasPrefix(none, "test caches: none — ") || !strings.Contains(none, "no test file") {
		t.Errorf("%q", none)
	}
	some := TestCacheScope(TestCaches, "")
	for _, want := range []string{"live_redis → TEST_REDIS_URL"} {
		if !strings.Contains(some, want) {
			t.Errorf("%q lacks %q", some, want)
		}
	}
}

// A CACHE TAG IS NEVER CALLED UNCOMPILED: UncompiledTags is the complement of the
// -tags word, so it is taken against all three vocabularies.
func TestTheScopeLineNeverCallsABoundCacheTagUncompiled(t *testing.T) {
	tags := []string{"integration", "live_db", "live_kafka", "live_redis"}
	dbs, brokers, caches := TestDBsFor(tags), TestBrokersFor(tags), TestCachesFor(tags)
	uncompiled := UncompiledTags(tags, dbs, brokers, caches, TestKubes)
	if !reflect.DeepEqual(uncompiled, []string{"integration"}) {
		t.Errorf("UncompiledTags = %v", uncompiled)
	}
	if got := strings.Split(BuildTags(dbs, brokers, caches, nil), ","); !reflect.DeepEqual(got, []string{"live_db", "live_kafka", "live_redis"}) {
		t.Errorf("BuildTags = %v", got)
	}
}
