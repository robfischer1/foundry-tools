package main

import (
	"errors"
	"slices"
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
		[]string{"withSecretVariable", `name:"TEST_KUBE_TOKEN"`},
		[]string{"withEnvVariable", `name:"TEST_KUBE_CA_FILE"`, `value:"/run/test-kube/ca.crt"`},
		[]string{"withMountedSecret", `path:"/k/auth-header"`},
		[]string{"withExec", `"curl","--fail"`, "https://kube:6443/readyz", "--cacert", "/run/test-kube/ca.crt", `"-H","@/k/auth-header"`, "%{onerror}kube-apiserver (lane shared) not ready after KubeBootSeconds=120s: GET /readyz"},
		[]string{"withExec", `expect:ANY`, `args:["/atom","/src"]`},
	)
	// ORDER: the variables and the CA file are in place BEFORE the wait reads them,
	// and the wait runs BEFORE the atom — a suite never meets a server that is not ready.
	url := lastCall(c, "withEnvVariable", `name:"TEST_KUBE_URL"`)
	tok := lastCall(c, "withSecretVariable", `name:"TEST_KUBE_TOKEN"`)
	ca := lastCall(c, "withEnvVariable", `name:"TEST_KUBE_CA_FILE"`)
	wait := lastCall(c, "withExec", `"curl","--fail"`, "/readyz")
	header := lastCall(c, "withMountedSecret", `path:"/k/auth-header"`)
	run := lastCall(c, "withExec", `args:["/atom","/src"]`)
	if !(url < wait && tok < wait && ca < wait && header < wait && wait < run) {
		t.Errorf("wrong order: url %d token %d ca %d header %d wait %d atom %d:\n%s", url, tok, ca, header, wait, run, c)
	}
	// NO CREDENTIAL ON ARGV: the wait names the header file, and no exec carries a bearer line
	if hasCall(c, "withExec", "Bearer") {
		t.Errorf("a credential is on an exec's argv:\n%s", c)
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
	// the three private files are mounted secrets, the three public ones plain files
	for _, f := range serviceFiles {
		call := "withNewFile"
		if slices.Contains(secretServiceFiles, f) {
			call = "withMountedSecret"
		}
		if !hasCall(api, call, `path:"/k/`+f+`"`) {
			t.Errorf("the API server is not handed /k/%s through %s:\n%s", f, call, api)
		}
	}
}

// secretServiceFiles are the files that hold a credential: the token the API
// server grants cluster-admin to, and the two private keys.
var secretServiceFiles = []string{"tokens.csv", "sa.key", "tls.key"}

// callsNamed is the argument text of every call of that name in the chain.
func callsNamed(chain, name string) []string {
	var out []string
	for i := 0; ; {
		k := strings.Index(chain[i:], name+"(")
		if k < 0 {
			return out
		}
		start := i + k + len(name)
		end := start + matching(chain[start:], '(', ')')
		out = append(out, chain[start:end+1])
		i = end + 1
	}
}

// NO CREDENTIAL IS A PLAIN VALUE IN THE RECORDED CALLS. Dagger logs a
// withEnvVariable's value and a withNewFile's contents, so a token set that way
// is in the CI transcript (gate run 2685 showed TEST_KUBE_TOKEN=<64 hex> once per
// lane). The token, the auth header and the API server's private keys are
// delivered as secrets; only the public halves may be plain files.
func TestNoKubeCredentialIsALoggedValue(t *testing.T) {
	var made checks.KubePKI
	was := newKubePKI
	t.Cleanup(func() { newKubePKI = was })
	newKubePKI = func(hosts []string, now time.Time) (checks.KubePKI, error) {
		p, err := was(hosts, now)
		made = p
		return p, err
	}
	c := raceRun(t, nil, kubeTag)
	api := engine.chain(checks.ImageKubeAPIServer, "asService")
	if made.Token == "" || api == "" {
		t.Fatalf("no credentials were made or no API server was built (token %q)", made.Token)
	}
	secrets := []string{made.Token, made.Key, made.SAKey}
	for name, chain := range map[string]string{"lane": c, "api": api} {
		for _, call := range []string{"withEnvVariable", "withNewFile", "withExec", "withDefaultArgs"} {
			for _, args := range callsNamed(chain, call) {
				for _, s := range secrets {
					if strings.Contains(args, s) {
						t.Errorf("the %s chain logs a credential in %s", name, call)
					}
				}
			}
		}
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
