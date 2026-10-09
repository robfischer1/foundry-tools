package checks

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// THE RECORD SAYS POSTGRES, THE TREE SAYS WHICH TAGS, THE LANE BRINGS THE
// DATABASE. A Go star's DB-gated suites sit behind a build tag and read a DSN
// from the environment; before this the lane compiled no tag and exported no
// DSN, so every DB-touching line in chaos's store read NOT COVERED by
// construction (foundry-tools#8608, chaos #36: six mutants in ScalarValues,
// each driven end-to-end by a real Postgres the lane never started). The
// fixture chaos declared in star.toml drove the Docker Engine API, which the
// engine does not expose, and star.toml is retired anyway (Rob, 2026-09-13).
//
// A REPO STILL HAS NO SAY. Nothing here is a knob: the star's slag.json —
// the fleet's own record in foundry-dies — says whether the star HAS a
// Postgres (backends.postgres), the tree says which tags its tests sit behind
// (`//go:build <tag>` in *_test.go), and this table is the fleet's closed
// vocabulary mapping a tag to the service the lane binds and the name the
// suite reads. A tag outside the vocabulary stays uncompiled and is named in
// the scope line, so a suite nobody can see is at least said.
//
// TWO SERVERS, NOT ONE. live_db_novector exists because one suite needs a
// Postgres WITHOUT pgvector installed (chaos's soft-vector mode); one DSN
// cannot serve both, and a database on the pgvector server without the
// extension created is not the same thing as a server that cannot create it.
type TestDB struct {
	// Tag is the `//go:build` tag the suite sits behind, and the -tags word.
	Tag string
	// Env is the variable the suite reads its DSN from — the fleet's name.
	Env string
	// Alias is the service's hostname inside the lane container.
	Alias string
	// Image is the pinned server image (images.go).
	Image string
}

// TestDBs is the vocabulary, in binding order.
var TestDBs = []TestDB{
	{Tag: "live_db", Env: "TEST_DATABASE_URL", Alias: "db", Image: ImagePgvector},
	{Tag: "live_db_novector", Env: "TEST_NOVECTOR_DATABASE_URL", Alias: "db-novector", Image: ImagePostgres},
}

// The one role and database every test server carries; the role's
// credential is its own name. Throwaway by construction: the service lives
// for the lane's session, holds nothing, and is reachable from nowhere else.
const (
	TestDBRole = "test"
	TestDBName = "test"
)

// DSN is the URL the suite is handed for this server, on the shared binding.
func (d TestDB) DSN() string { return d.DSNFor("") }

// AliasFor is the service's hostname for one LANE. The empty scope is the
// shared binding every lane used to get; a named scope gets a host of its own.
//
// WHY A LANE NEEDS ITS OWN HOST AT ALL. The push stage runs the complex checks
// in sequence WITH THE MUTATION GATE BESIDE THEM — concurrently, in separate
// containers. Both bound a server built from an identical definition, and dagger
// content-addresses services, so the two resolved to ONE running Postgres. Two
// lanes, one database, and the DB-gated suites reset its schema per test:
//
//	reset schema: ERROR: deadlock detected (SQLSTATE 40P01)
//	migrate: ERROR: duplicate key value violates unique constraint
//	         "pg_namespace_nspname_index" (SQLSTATE 23505)
//	tx clock: ERROR: could not open relation with OID 120007 (SQLSTATE XX000)
//
// MEASURED on chaos, three runs of one unchanged suite: 150, 50 and 72 distinct
// failures, a different set each time. `-p 1` cannot reach it — the atom already
// carries it, and it bounds packages within ONE `go test`, not two processes in
// two containers.
//
// A SEPARATE ALIAS IS NOT ENOUGH ON ITS OWN. Two aliases pointing at one
// content-addressed service are still one database; the service DEFINITION has
// to differ too, which is why the caller perturbs it (atoms_go.go).
func (d TestDB) AliasFor(scope string) string {
	if scope == "" {
		return d.Alias
	}
	return d.Alias + "-" + scope
}

// DSNFor is the URL for this server on one lane's binding.
func (d TestDB) DSNFor(scope string) string {
	return "postgres://" + TestDBRole + ":" + TestDBRole + "@" + d.AliasFor(scope) + ":5432/" + TestDBName + "?sslmode=disable"
}

// ServerArgs is the test server's start command: the image's own `postgres`,
// through its entrypoint, with durability switched off.
//
// DURABILITY BUYS A THROWAWAY DATABASE NOTHING. The server lives for one lane,
// holds nothing anyone reads afterwards, and a crash loses a run that is re-run
// anyway. Stock settings still paid for it on every commit and every mutant: a
// WAL fsync per commit, full-page images after each checkpoint, and timed
// checkpoints fsyncing the cluster's files. Measured on chaos's mutation lane
// (forge/mutation-chaos-dd40855-rp2fb): a timed checkpoint of 269.7s writing
// 13,254 buffers and fsyncing 51,057 files, others of 26-40s, under a lane that
// runs against a 75-minute deadline and a 10% mutant-timeout budget.
//
//	fsync=off                 no fsync of WAL or data files at all
//	synchronous_commit=off    a commit does not wait on its WAL flush
//	full_page_writes=off      no torn-page insurance after a checkpoint
//	checkpoint_timeout=1d     no timed checkpoint inside any run
//	max_wal_size=2GB          the size trigger, bounded: WAL is disk the lane holds
//
// IT GOES THROUGH THE ENTRYPOINT, on TestBroker.StartArgs' rule: the caller sets
// these with WithDefaultArgs and AsService(UseEntrypoint), never WithExec — a
// WithExec service never reads ready. docker-entrypoint.sh initialises the
// cluster (POSTGRES_USER/PASSWORD/DB) and then execs exactly this command, so
// the leading word is `postgres` and the settings are server flags, not
// postgresql.conf edits the initdb would overwrite.
func (d TestDB) ServerArgs() []string {
	return []string{
		"postgres",
		"-c", "fsync=off",
		"-c", "synchronous_commit=off",
		"-c", "full_page_writes=off",
		"-c", "checkpoint_timeout=1d",
		"-c", "max_wal_size=2GB",
	}
}

var serviceNameLine = regexp.MustCompile(`^service_name:\s*`)

// ServiceName is the star's name as the answers file declares it — the
// directory its record lives under in foundry-dies (fleet/stars/<name>/).
func ServiceName(yaml string) string {
	return answersValue(yaml, serviceNameLine)
}

// PostgresBackend answers whether a slag record declares a Postgres backend:
// backends.postgres present and an object. Anything unparseable is no.
func PostgresBackend(slag string) bool {
	var doc struct {
		Backends map[string]json.RawMessage `json:"backends"`
	}
	if err := json.Unmarshal([]byte(slag), &doc); err != nil {
		return false
	}
	raw, ok := doc.Backends["postgres"]
	return ok && strings.HasPrefix(strings.TrimSpace(string(raw)), "{")
}

var buildTagLine = regexp.MustCompile(`^//go:build ([A-Za-z0-9_]+)$`)

// GoBuildTags reads the single-tag `//go:build` lines out of the lane's
// recursive constraint read — one line per match, as grep's -rhoE prints
// them — sorted and unique. A compound constraint (`a && b`, `!x`) is not a
// tag the lane can name and is not read.
func GoBuildTags(out string) []string {
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		m := buildTagLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		seen[m[1]] = true
	}
	tags := make([]string, 0, len(seen))
	for t := range seen {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	return tags
}

// TestDBsFor is the vocabulary's entries for the tags a tree carries, in
// binding order.
func TestDBsFor(tags []string) []TestDB {
	has := map[string]bool{}
	for _, t := range tags {
		has[t] = true
	}
	var out []TestDB
	for _, d := range TestDBs {
		if has[d.Tag] {
			out = append(out, d)
		}
	}
	return out
}

// BuildTags is the -tags word for every service bound: comma-joined, databases
// before brokers, "" when none.
//
// ONE FUNCTION FOR BOTH KINDS, deliberately. A second way to build this word is
// how it ends up incomplete — and an incomplete -tags word does not fail, it
// compiles the un-named suites OUT and the lane reports green over tests that
// never built. That is the exact shape this file exists to end, so the word has
// one author.
func BuildTags(dbs []TestDB, brokers []TestBroker) string {
	tags := make([]string, 0, len(dbs)+len(brokers))
	for _, d := range dbs {
		tags = append(tags, d.Tag)
	}
	for _, b := range brokers {
		tags = append(tags, b.Tag)
	}
	return strings.Join(tags, ",")
}

// UncompiledTags is what the tree carries that the vocabulary does not name.
//
// IT HAS TO SEE BOTH VOCABULARIES. Told only about the databases, it reported
// every broker tag as "not in the fleet's vocabulary" — on the same lane whose
// very next line announced it had bound a broker for that tag. The word is built
// from both (BuildTags), so the complement has to be taken against both, or the
// scope line calls a compiled suite uncompiled. Measured on tartarus #87, whose
// gate printed `tags left uncompiled ...: live_kafka` directly above
// `test brokers: live_kafka -> KAFKA_BOOTSTRAP`.
func UncompiledTags(tags []string, dbs []TestDB, brokers []TestBroker) []string {
	named := map[string]bool{}
	for _, d := range dbs {
		named[d.Tag] = true
	}
	for _, b := range brokers {
		named[b.Tag] = true
	}
	var out []string
	for _, t := range tags {
		if !named[t] {
			out = append(out, t)
		}
	}
	return out
}

// TestDBScope is the line an atom prints about the databases it brought, and
// it is printed EITHER WAY: a lane that bound nothing says why, because a
// suite that silently never compiled is the shape this file exists to end.
func TestDBScope(dbs []TestDB, uncompiled []string, why string) string {
	if len(dbs) == 0 {
		return "test databases: none — " + why
	}
	parts := make([]string, 0, len(dbs))
	for _, d := range dbs {
		parts = append(parts, d.Tag+" → "+d.Env)
	}
	line := "test databases: " + strings.Join(parts, ", ")
	if len(uncompiled) > 0 {
		line += "; tags left uncompiled (not in the fleet's vocabulary): " + strings.Join(uncompiled, ", ")
	}
	return line
}

// THE RECORD SAYS KAFKA, THE TREE SAYS WHICH TAG, THE LANE BRINGS THE BROKER.
// The broker half of the same contract, and deliberately a SEPARATE table from
// TestDBs rather than a row in it: TestDB.DSNFor builds a postgres URL and the
// service the caller constructs sets POSTGRES_USER/PASSWORD/DB on port 5432.
// A broker shares none of that, and overloading one table would have put a
// `if kind == kafka` branch in the middle of the path every repo's gate runs.
//
// ISOLATION IS THE TOPIC, NOT THE SERVER, and that is why this needs no helper
// where Postgres needed one. forge-testkit-go's containers.Kafka says it in its
// own doc: "this hands every test in the process an address for one broker,
// because Kafka's isolation unit is the topic plus the consumer group and not
// the broker process. Callers MUST mint unique topic and group names per test."
// One lane-bound broker has exactly those semantics, so a suite already obeying
// that rule needs nothing but the address. A suite that HARDCODES a topic name
// was already broken under the fixture and is broken louder here, across
// packages rather than within one process — see the caveat on TestBrokerScope.
//
// THE PER-LANE DEFINITION DIFFERS FOR FREE. The Postgres path has to perturb
// its service (FOUNDRY_TEST_DB_LANE) because dagger content-addresses services
// and two lanes built byte-identical definitions got handed ONE database. A
// broker's advertised address embeds its own alias — a scoped lane advertises
// broker-<scope>:9092 — so the definition already differs and no artificial
// perturbation is needed.
type TestBroker struct {
	// Tag is the `//go:build` tag the suite sits behind, and the -tags word.
	Tag string
	// Env is the variable the suite reads its bootstrap address from.
	Env string
	// Alias is the service's hostname inside the lane container, and the host
	// the broker advertises to clients.
	Alias string
	// Image is the pinned broker image (images.go).
	Image string
	// Port is the kafka wire, exposed and advertised.
	Port int
}

// TestBrokers is the vocabulary, in binding order.
var TestBrokers = []TestBroker{
	{Tag: "live_kafka", Env: "KAFKA_BOOTSTRAP", Alias: "broker", Image: ImageRedpanda, Port: 9092},
}

// AliasFor is the broker's hostname for one LANE, on the same rule as TestDB's.
func (b TestBroker) AliasFor(scope string) string {
	if scope == "" {
		return b.Alias
	}
	return b.Alias + "-" + scope
}

// AddrFor is the bootstrap address the suite is handed — host:port, the form
// every kafka client takes as a seed broker.
func (b TestBroker) AddrFor(scope string) string {
	return b.AliasFor(scope) + ":" + strconv.Itoa(b.Port)
}

// ListenFor is the address the broker binds inside its own container: every
// interface, so the binding can reach it. Paired with AdvertiseFor — a broker
// that listens on localhost is unreachable through a service binding, and one
// that advertises 0.0.0.0 hands the client an address it cannot dial.
func (b TestBroker) ListenFor() string {
	return "PLAINTEXT://0.0.0.0:" + strconv.Itoa(b.Port)
}

// AdvertiseFor is what the broker must advertise to be reachable through its
// binding. A broker that advertises anything else answers the first connection
// and then hands the client an address it cannot dial, which reads as a hang
// rather than as a refusal.
func (b TestBroker) AdvertiseFor(scope string) string {
	return "PLAINTEXT://" + b.AddrFor(scope)
}

// StartArgs is the broker's own start command.
//
// IT LIVES HERE BECAUSE IT IS A CONTRACT, NOT PLUMBING. The caller builds a dagger
// container, which no unit test can inspect; this is a []string, which one can.
//
// IT GOES THROUGH THE IMAGE'S ENTRYPOINT, and the caller must build the service
// with WithDefaultArgs + AsService(UseEntrypoint), never WithExec. Both halves are
// load-bearing and each was measured on its own against the pinned image:
//
//	WithExec(...)            + AsService()                  -> never becomes ready
//	WithDefaultArgs(...)     + AsService(UseEntrypoint:true) -> ready in seconds
//
// A WithExec is an operation with a snapshot to COMMIT. A service command never
// exits, so the commit never lands and dagger never marks the service ready: the
// client exec that binds it is simply never started. The engine says so —
// `commit output ref ...: context canceled` after the run is killed — and the
// symptom upstream is a lane that emits nothing at all and dies on the silence
// limit, with the broker's own log showing a perfectly healthy broker.
//
// THE ARGS ARE `redpanda start`, NOT `rpk redpanda start`, because the entrypoint
// is the wrapper that shells out to rpk. The bare `redpanda` executable refuses
// these flags with "unrecognised option '--check=false'" (tartarus #87), which is
// true and is why an earlier fix reached for rpk directly — but bypassing the
// entrypoint is what broke readiness. Through the entrypoint, `redpanda start` is
// the spelling that works.
//
// --memory IS NOT OPTIONAL. A dagger container carries no memory cgroup limit, so
// the entrypoint computes no bound and seastar reserves nearly the whole host:
// the lane's broker logged mem_available 65410170880 on a 64G node. That alone
// keeps the service from ever coming up. Measured: the same definition with these
// flags and no --memory never becomes ready; with them it is ready in seconds.
//
// AUTO-CREATION IS OFF, and the reason I first gave for it was WRONG. `--mode
// dev-container` does turn auto_create_topics_enabled ON — that part is measured,
// `rpk cluster config get` answering true. What I claimed followed from it does not:
// I wrote that tartarus's TestSessionEvent_AnUndeliveredEventIsAnError, which points
// its producer at an undeclared topic and requires the write to FAIL, would flip to
// failing under an auto-creating broker.
//
// IT DOES NOT. Measured against the pinned image both ways, that test PASSES with
// auto-creation on, and `rpk topic list` afterwards shows the undeclared topic was
// never created — only the two Connect declared. franz-go does not ask the broker to
// create an unknown topic, so the broker-side setting never enters that path. The
// mechanism was plausible and I shipped it as established before checking it.
//
// WHAT THE SETTING IS ACTUALLY FOR, on its own and smaller merit: a topic the record
// never declared should not spring into being because a suite misspelled one. The
// record names the topics; the broker inventing more of them turns a typo into a
// passing test somewhere else. That is hygiene, not a measured defect — no suite in
// the fleet depends on it today, and it is cheap enough to keep on that basis alone.
// Struck rather than deleted so the next reader does not re-derive the wrong theory.
func (b TestBroker) StartArgs(scope string) []string {
	return []string{
		"redpanda", "start",
		"--smp", "1", "--overprovisioned", "--node-id", "0", "--check=false",
		"--memory", "1G", "--reserve-memory", "0M",
		"--mode", "dev-container",
		"--set", "redpanda.auto_create_topics_enabled=false",
		"--kafka-addr", b.ListenFor(),
		"--advertise-kafka-addr", b.AdvertiseFor(scope),
	}
}

// TestBrokersFor is the vocabulary's entries for the tags a tree carries, in
// binding order.
func TestBrokersFor(tags []string) []TestBroker {
	has := map[string]bool{}
	for _, t := range tags {
		has[t] = true
	}
	var out []TestBroker
	for _, b := range TestBrokers {
		if has[b.Tag] {
			out = append(out, b)
		}
	}
	return out
}

// KafkaBackend answers whether a slag record declares a kafka backend.
//
// THE SHAPE IS NOT POSTGRES'. backends.postgres is an OBJECT (cluster, database,
// owner); backends.kafka is the ARRAY OF TOPICS the star produces or consumes,
// and a star with no broker carries `false`. So an empty array is no more a
// declaration than `false` is: a star that names no topic has no broker seam for
// a suite to exercise.
func KafkaBackend(slag string) bool {
	var doc struct {
		Backends map[string]json.RawMessage `json:"backends"`
	}
	if err := json.Unmarshal([]byte(slag), &doc); err != nil {
		return false
	}
	raw, ok := doc.Backends["kafka"]
	if !ok {
		return false
	}
	var topics []string
	if err := json.Unmarshal(raw, &topics); err != nil {
		return false
	}
	return len(topics) > 0
}

// TestBrokerScope is the line an atom prints about the brokers it brought, and
// it is printed EITHER WAY, on the same rule as TestDBScope: a lane that bound
// nothing says why.
//
// IT CARRIES THE TOPIC CAVEAT, because the lane cannot enforce it. One broker
// serves every package in the run, so two suites that hardcode the same topic
// read each other's records — intermittently, which looks like flake rather than
// like the ordering dependency it is.
func TestBrokerScope(brokers []TestBroker, why string) string {
	if len(brokers) == 0 {
		return "test brokers: none — " + why
	}
	parts := make([]string, 0, len(brokers))
	for _, b := range brokers {
		parts = append(parts, b.Tag+" → "+b.Env)
	}
	return "test brokers: " + strings.Join(parts, ", ") +
		"; one broker serves every package — mint unique topic and group names per test"
}

// BrokerReads is the three reads withTestBrokers makes, handed over as data.
//
// THE DECISION IS PURE, THE I/O IS THE CALLER'S, and that split is not tidiness.
// The first cut put all four guards inside the dagger method, where no unit test
// can reach them: the mutation lane graded them NOT COVERED and LIVED on the very
// pull that introduced them (foundry-tools #196, six survivors at atoms_go.go
// 287-304). A guard no test can execute is a guard that is not there — so the
// decision moved here, where every arm is a table row.
//
// An error is not the same fact as an empty read, for either one: a record that could not
// be fetched and a record that names no topic are different refusals, and the
// scope line has to say which.
type BrokerReads struct {
	// Answers is .copier-answers.yml's contents; "" when it could not be read.
	Answers string
	// Slag is the record's contents, SlagErr the error fetching it. The error
	// distinguishes "could not fetch" from "fetched and says nothing".
	Slag    string
	SlagErr error
	// Tags is the tree's `//go:build` grep output; TagsErr and TagsCode are the
	// grep's own outcome. grep exits 1 for no match, which is an ANSWER; 2 and up
	// is grep failing, which is not the same as the grep finding nothing.
	Tags     string
	TagsErr  error
	TagsCode int
}

// SelectBrokers answers which brokers the lane binds and the scope line to print,
// from the three reads. The empty slice is a decision, never an oversight — the
// line always says which read refused.
func SelectBrokers(r BrokerReads) ([]TestBroker, string) {
	star := ServiceName(r.Answers)
	if star == "" {
		return nil, TestBrokerScope(nil, "no service_name in .copier-answers.yml, so no record to read")
	}
	if r.SlagErr != nil {
		return nil, TestBrokerScope(nil, "no record at fleet/stars/"+star+"/slag.json")
	}
	if !KafkaBackend(r.Slag) {
		return nil, TestBrokerScope(nil, "the record names no kafka topics")
	}
	if r.TagsErr != nil || r.TagsCode > 1 {
		return nil, TestBrokerScope(nil, "the tree's build tags could not be read")
	}
	brokers := TestBrokersFor(GoBuildTags(r.Tags))
	if len(brokers) == 0 {
		return nil, TestBrokerScope(nil, "the record names kafka topics but no test file sits behind a tag the fleet names")
	}
	return brokers, TestBrokerScope(brokers, "")
}
