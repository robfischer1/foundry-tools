package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"dagger/foundry-tools/internal/checks"
)

// THE KUBE-GATED SUITES' ENVIRONMENT, against the paper engine: which services
// the tree's live_kube tag binds, to which alias, with which variables and files,
// and what holds the lane until the API server is ready. The credentials are
// random, so these assertions read the SHAPE of the chain — never a value.

const kubeTag = "//go:build live_kube\n"

// serviceFiles are the six files the API server service is handed.
var serviceFiles = []string{"ca.crt", "tls.crt", "tls.key", "sa.key", "sa.pub", "tokens.csv"}

// A TREE THAT SITS BEHIND live_kube GETS A REAL API SERVER over a real etcd,
// bound to the lane container under the shared alias, with the three variables,
// the CA file, and the wait that holds the lane until /readyz answers.
func TestGoTestRaceBindsAKubeAPIServerForALiveKubeTree(t *testing.T) {
	c := raceRun(t, nil, kubeTag)
	k := checks.TestKubes[0]
	wantCalls(t, c,
		[]string{"withServiceBinding", `alias:"kube"`},
		[]string{"withNewFile", `path:"/run/test-kube/ca.crt"`, "BEGIN CERTIFICATE"},
		[]string{"withEnvVariable", `name:"TEST_KUBE_URL"`, `value:"` + k.URLFor("") + `"`},
		[]string{"withEnvVariable", `name:"TEST_KUBE_TOKEN"`},
		[]string{"withEnvVariable", `name:"TEST_KUBE_CA_FILE"`, `value:"/run/test-kube/ca.crt"`},
		[]string{"withExec", `"curl","--fail"`, "https://kube:6443/readyz", "--cacert", "/run/test-kube/ca.crt", "Authorization: Bearer "},
		[]string{"withExec", `expect:ANY`, `args:["/atom","/src"]`},
	)
	// the token the suite is handed is 64 hex characters, and not the empty string
	i := strings.Index(c, `name:"TEST_KUBE_TOKEN"`)
	if i < 0 || !strings.Contains(c[i:i+200], `value:"`) {
		t.Fatalf("no token value in the chain:\n%s", c)
	}
	// ORDER: the variables and the CA file are in place BEFORE the wait reads them,
	// and the wait runs BEFORE the atom — a suite never meets a server that is not ready.
	url := lastCall(c, "withEnvVariable", `name:"TEST_KUBE_URL"`)
	tok := lastCall(c, "withEnvVariable", `name:"TEST_KUBE_TOKEN"`)
	ca := lastCall(c, "withEnvVariable", `name:"TEST_KUBE_CA_FILE"`)
	wait := lastCall(c, "withExec", `"curl","--fail"`, "/readyz")
	run := lastCall(c, "withExec", `args:["/atom","/src"]`)
	if !(url < wait && tok < wait && ca < wait && wait < run) {
		t.Errorf("wrong order: url %d token %d ca %d wait %d atom %d:\n%s", url, tok, ca, wait, run, c)
	}
	if hasCall(c, "withServiceBinding", `alias:"db"`) || hasCall(c, "withServiceBinding", `alias:"broker"`) {
		t.Errorf("only the tags the tree carries are bound:\n%s", c)
	}
}

// BOTH SERVICES ARE THE PINNED IMAGES, started the way a service becomes ready
// (default args through the entrypoint, never a WithExec), on their ports, the
// API server reaching its etcd under the etcd alias and holding the six files.
func TestTheKubeServicesAreBuiltTheWayAServiceBecomesReady(t *testing.T) {
	raceRun(t, nil, kubeTag)
	k := checks.TestKubes[0]
	etcd := engine.chain(checks.ImageEtcd, "asService")
	api := engine.chain(checks.ImageKubeAPIServer, "asService")
	if etcd == "" || api == "" {
		t.Fatalf("a kube service chain is missing:\n%v", engine.chains())
	}
	for name, svc := range map[string]string{"etcd": etcd, "kube-apiserver": api} {
		if strings.Contains(svc, "withExec") {
			t.Errorf("the %s service carries a withExec and will never read ready:\n%s", name, svc)
		}
		if !hasCall(svc, "asService", "useEntrypoint:true") {
			t.Errorf("the %s service must start through the image's entrypoint:\n%s", name, svc)
		}
		if hasCall(svc, "withEnvVariable", `name:"FOUNDRY_TEST_KUBE_LANE"`) {
			t.Errorf("the shared %s service carries a lane word:\n%s", name, svc)
		}
	}
	if !hasCall(etcd, "withDefaultArgs", `args:["`+strings.Join(k.EtcdArgs(""), `","`)+`"]`) ||
		!hasCall(etcd, "withExposedPort", "2379") {
		t.Errorf("etcd is not started with EtcdArgs on 2379:\n%s", etcd)
	}
	if !hasCall(api, "withDefaultArgs", `args:["`+strings.Join(k.APIServerArgs(""), `","`)+`"]`) ||
		!hasCall(api, "withExposedPort", "6443") {
		t.Errorf("the API server is not started with APIServerArgs on 6443:\n%s", api)
	}
	if !hasCall(api, "withServiceBinding", `alias:"kube-etcd"`) {
		t.Errorf("the API server does not reach etcd under its alias:\n%s", api)
	}
	for _, f := range serviceFiles {
		if !hasCall(api, "withNewFile", `path:"/k/`+f+`"`) {
			t.Errorf("the API server is not handed /k/%s:\n%s", f, api)
		}
	}
	// the files are what the argv reads: the token file carries the admin line
	if !hasCall(api, "withNewFile", `path:"/k/tokens.csv"`, `,admin,admin,system:masters\n`) {
		t.Errorf("tokens.csv does not grant the admin identity:\n%s", api)
	}
}

// THE DIFF-COVERAGE LANE GETS ITS OWN PAIR: a scoped alias for each, and a lane
// word in BOTH service definitions — dagger content-addresses services, so two
// lanes that built identical definitions were handed one server.
func TestKubeServicesAreScopedPerLane(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.withTree(map[string]string{".copier-answers.yml": "service_name: x\n"})
	engine.stdout(goTagsNeedle, kubeTag)
	engine.contents(goCovNeedle, goVerdict("go:diff-coverage", "prepush"))
	runAtom(t, "go:diff-coverage", "abc123")
	c := engine.chain(goCovNeedle, "contents")
	k := checks.TestKubes[0]
	wantCalls(t, c,
		[]string{"withServiceBinding", `alias:"kube-diffcov"`},
		[]string{"withEnvVariable", `name:"TEST_KUBE_URL"`, `value:"` + k.URLFor("diffcov") + `"`},
	)
	if hasCall(c, "withServiceBinding", `alias:"kube"`) {
		t.Errorf("go:diff-coverage took the shared API server:\n%s", c)
	}
	for _, img := range []string{checks.ImageEtcd, checks.ImageKubeAPIServer} {
		svc := engine.chain(img, "asService")
		if !hasCall(svc, "withEnvVariable", `name:"FOUNDRY_TEST_KUBE_LANE"`, `value:"diffcov"`) {
			t.Errorf("the lane's %s is defined identically to the shared one:\n%s", img, svc)
		}
	}
	etcd := engine.chain(checks.ImageEtcd, "asService")
	if !hasCall(etcd, "withDefaultArgs", "http://kube-etcd-diffcov:2379") {
		t.Errorf("the lane's etcd advertises the shared alias:\n%s", etcd)
	}
	if api := engine.chain(checks.ImageKubeAPIServer, "asService"); !hasCall(api, "withServiceBinding", `alias:"kube-etcd-diffcov"`) {
		t.Errorf("the lane's API server dials the shared etcd:\n%s", api)
	}
}

// THE MUTATION LANE BINDS IT THE SAME WAY bindTestDatabases is bound for it: its
// own pair, the tag compiled into every listing and the run, and the wait ahead of
// the first mutant.
func TestGoMutationBindsAKubeAPIServerOfItsOwn(t *testing.T) {
	scriptGoMutation(map[string]string{".copier-answers.yml": "service_name: x\n"})
	engine.stdout(`"grep","-rhoE"`, "//go:build live_db\n"+kubeTag)
	wantState(t, runAtom(t, "go:mutation", "abc123"), 0, "live_kube → TEST_KUBE_URL")
	c := engine.chain(goMutantsNeedle, "exitCode")
	wantCalls(t, c,
		[]string{"withServiceBinding", `alias:"kube-mutation"`},
		[]string{"withEnvVariable", `name:"TEST_KUBE_URL"`, `value:"` + checks.TestKubes[0].URLFor("mutation") + `"`},
		[]string{"withEnvVariable", `name:"TEST_KUBE_CA_FILE"`, `value:"/run/test-kube/ca.crt"`},
		[]string{"withExec", `"curl","--fail"`, "https://kube-mutation:6443/readyz"},
		[]string{"withExec", `"-changed-since","since0","-tags","live_db,live_kube","./..."`},
	)
	if hasCall(c, "withServiceBinding", `alias:"kube"`) {
		t.Errorf("the mutation gate bound the shared API server it races:\n%s", c)
	}
	for _, img := range []string{checks.ImageEtcd, checks.ImageKubeAPIServer} {
		if svc := engine.chain(img, "asService"); !hasCall(svc, "withEnvVariable", `name:"FOUNDRY_TEST_KUBE_LANE"`, `value:"mutation"`) {
			t.Errorf("the mutation lane's %s is defined identically to the shared one:\n%s", img, svc)
		}
	}
	// the listings carry the tag too: a tagged package is only in the set under it
	for _, needle := range []string{goMainListNeedle, goPackagesNeedle, goSourceNeedle} {
		if list := engine.chain(needle, "stdout"); !strings.Contains(list, `"-tags","live_db,live_kube"`) {
			t.Errorf("a listing dropped the kube tag:\n%s", list)
		}
	}
}

// A TREE WITH NO live_kube BINDS NO API SERVER, and the mutation lane's argv
// carries no tags for a tree with no tagged suite.
func TestNoKubeIsBoundWithoutTheTag(t *testing.T) {
	c := raceRun(t, nil, "//go:build live_db\n")
	if hasCall(c, "withNewFile", `path:"/run/test-kube/ca.crt"`) || strings.Contains(c, "TEST_KUBE") {
		t.Errorf("an API server was bound for a tree that does not ask:\n%s", c)
	}
	if engine.chain(checks.ImageKubeAPIServer, "asService") != "" {
		t.Errorf("the API server service was built for a tree that does not ask")
	}
}

// A LANE THAT CANNOT MAKE ITS CREDENTIALS RUNS NOTHING: every exec after the
// refusal fails, naming why, instead of a suite meeting a server it cannot trust.
func TestAKubeLaneWithoutCredentialsFailsLoudly(t *testing.T) {
	was := newKubePKI
	t.Cleanup(func() { newKubePKI = was })
	newKubePKI = func([]string, time.Time) (checks.KubePKI, error) {
		return checks.KubePKI{}, errors.New("entropy exhausted")
	}
	c := raceRun(t, nil, kubeTag)
	if !hasCall(c, "withExec", `args:["false","test-kube: live_kube: the lane could not make its credentials: entropy exhausted"]`) {
		t.Errorf("the lane does not refuse:\n%s", c)
	}
	if hasCall(c, "withServiceBinding", `alias:"kube"`) {
		t.Errorf("an API server was bound with no credentials:\n%s", c)
	}
}

// THE UNION OF EVERY MODULE'S API SERVERS, each once.
func TestGoTestRaceBindsOneAPIServerForManyKubeModules(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{
		"a/go.mod": "module a\n", "a/a_test.go": "package a\n",
		"b/go.mod": "module b\n", "b/b_test.go": "package b\n",
	})
	engine.stdoutAll(kubeTag, goTagsNeedle, `path:"/src/a"`)
	engine.stdoutAll(kubeTag, goTagsNeedle, `path:"/src/b"`)
	engine.contents(goRaceNeedle, goVerdict("go:test-race", "prepush"))
	runAtom(t, "go:test-race", "")
	c := engine.chain(goRaceNeedle, "contents")
	if n := strings.Count(c, "withServiceBinding("); n != 1 {
		t.Errorf("want the API server once, got %d bindings:\n%s", n, c)
	}
}
