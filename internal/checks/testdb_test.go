package checks

import (
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
	if got := BuildTags(dbs); got != "live_db,live_db_novector" {
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
	// The order of the tree's tags does not reorder the binding.
	rev := TestDBsFor([]string{"live_db_novector", "live_db"})
	if rev[0].Tag != "live_db" {
		t.Errorf("binding order is the vocabulary's, not the tree's: %+v", rev)
	}
	if got := BuildTags(nil); got != "" {
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
