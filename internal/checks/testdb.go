package checks

import (
	"encoding/json"
	"regexp"
	"sort"
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

// BuildTags is the -tags word for the databases bound: comma-joined, in
// binding order, "" when none.
func BuildTags(dbs []TestDB) string {
	tags := make([]string, 0, len(dbs))
	for _, d := range dbs {
		tags = append(tags, d.Tag)
	}
	return strings.Join(tags, ",")
}

// UncompiledTags is what the tree carries that the vocabulary does not name.
func UncompiledTags(tags []string, dbs []TestDB) []string {
	named := map[string]bool{}
	for _, d := range dbs {
		named[d.Tag] = true
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
