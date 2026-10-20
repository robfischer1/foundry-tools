package bundlekit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/bundlelane"
	"dagger/foundry-tools/internal/checks"
)

const derived = `{"keys":{"k":{"c":{"x":{"v":1}},"d":{}}},"prefix":{"a":1},"caps":{"b":1},"reasons":[]}`

func TestPolicyBuildsTheOneUnsignedBundleFromACopyNamedPolicy(t *testing.T) {
	w := newWorld(t, nil)
	if f := Policy(context.Background(), w.run()); f != nil {
		t.Fatal(f)
	}
	var builds [][]string
	for _, c := range w.calls {
		if len(c) > 1 && c[0] == "opa" && c[1] == "build" {
			builds = append(builds, c)
		}
	}
	if len(builds) != 1 {
		t.Fatalf("builds %v", builds)
	}
	plain := strings.Join(builds[0], " ")
	wantPlain := "opa build -b policy/ -o " + filepath.Join(w.out, "bundle.tar.gz") + " --revision " + sha + " --ignore *_test.rego"
	if plain != wantPlain {
		t.Errorf("plain build\n%s\nwant\n%s", plain, wantPlain)
	}
	if strings.Contains(plain, "--signing") {
		t.Errorf("the bundle is built unsigned: %s", plain)
	}
	// opa names a rego file by the path it was built from: the copy is policy/ in its parent.
	if filepath.Base(w.builtPolicyRoot) == "" || !strings.Contains(filepath.Base(w.builtPolicyRoot), "bundle-policy-") {
		t.Errorf("built from %q", w.builtPolicyRoot)
	}
	if _, err := os.Stat(w.builtPolicyRoot); err == nil {
		t.Error("the scratch copy was left behind")
	}
	m, err := ReadMeta("policy")
	if err != nil {
		t.Fatal(err)
	}
	if m.Die != bundlelane.PolicyDie || m.Pin != "g0123456" || m.Revision != sha || !m.Publish || m.PublishIfMissing || m.Kind != "bundle" ||
		len(m.Artifacts) != 1 || m.Artifacts[0] != (Artifact{"bundle.tar.gz", "", "stable"}) || m.Schema != MetaSchema {
		t.Errorf("meta %+v", m)
	}
	if b, _ := os.ReadFile(filepath.Join(w.out, "policy"+MetaSuffix)); strings.Contains(string(b), "signing") {
		t.Errorf("meta names a signing step: %s", b)
	}
	if entries, _ := filepath.Glob(filepath.Join(w.out, "*signed*")); len(entries) != 0 {
		t.Errorf("signed leftovers: %v", entries)
	}
}

func TestPolicyPublishFollowsThePathsTheLandingTouched(t *testing.T) {
	for name, tc := range map[string]struct {
		diff    string
		parent  int
		publish bool
	}{
		"policy moved":                {"policy/admission/a.rego\n", 0, true},
		"only docs moved":             {"README.md\n.forgejo/w.yml\nschema/x.json\n", 0, false},
		"the previous tip is unknown": {"", 1, true},
	} {
		w := newWorld(t, nil)
		w.diff, w.parent = tc.diff, tc.parent
		if f := Policy(context.Background(), w.run()); f != nil {
			t.Fatalf("%s: %v", name, f)
		}
		if m, _ := ReadMeta("policy"); m.Publish != tc.publish {
			t.Errorf("%s: publish = %v", name, m.Publish)
		}
	}
}

func TestPolicyDerivesTheDecisionTableAndLaysItInTheCopy(t *testing.T) {
	w := newWorld(t, map[string]string{bundlelane.DecisionsDerive: "package t\n", "fleet/derive.rego": "package fleet\n"})
	w.table = derived
	w.dataJSON = `{"authz_audience":{"star_only":{"chaos":["graph_nodes"]}},"grants":{"decisions":` + derived + `},"principals":{"p":1},"capabilities":{"c":1}}`
	if f := Policy(context.Background(), w.run()); f != nil {
		t.Fatal(f)
	}
	var argv string
	for _, c := range w.calls {
		if len(c) > 2 && c[1] == "eval" && strings.HasSuffix(c[len(c)-1], "data.authz_v2_table.decisions") {
			argv = strings.Join(c, " ")
		}
	}
	if !strings.HasPrefix(argv, "opa eval -d policy -d tools/authz_table -d ") || !strings.Contains(argv, "decisions-stage --format raw") {
		t.Errorf("derive argv %q", argv)
	}
}

func TestPolicyRefusals(t *testing.T) {
	derive := map[string]string{bundlelane.DecisionsDerive: "package t\n", "fleet/derive.rego": "package fleet\n"}
	merge := func(maps ...map[string]string) map[string]string {
		out := map[string]string{}
		for _, m := range maps {
			for k, v := range m {
				out[k] = v
			}
		}
		return out
	}
	noStars := `{"authz_audience":{"star_only":{}},"grants":{"g":1},"principals":{"p":1},"capabilities":{"c":1}}`
	for _, tc := range []struct {
		name     string
		files    map[string]string
		edit     func(*world)
		findings bool
		needles  []string
	}{
		{"git cannot read history", nil, func(w *world) { w.gitDead = true; t.Setenv("ATOM_SHA", sha) }, false, []string{"the history could not be read"}},
		{"a commit too short", nil, func(w *world) { t.Setenv("ATOM_SHA", "abc") }, false, []string{"too short for a pin"}},
		{"opa build fails", nil, func(w *world) { w.buildCode = 1 }, true, []string{"opa build over policy/ failed", "build error"}},
		{"data.json is empty of stars", nil, func(w *world) { w.dataJSON = noStars }, true, []string{"empty star_only"}},
		{"data.json is not JSON", nil, func(w *world) { w.dataJSON = "x" }, true, []string{"not JSON"}},
		{"no chaos row", nil, func(w *world) {
			w.dataJSON = `{"authz_audience":{"star_only":{"x":["v"]}},"grants":{"g":1},"principals":{"p":1},"capabilities":{"c":1}}`
		}, true, []string{"chaos has no star_only row"}},
		{"the probe does not evaluate", nil, func(w *world) { w.failEval["data.authz.callable.allowed"] = 1 }, true, []string{"session-principal probe did not evaluate"}},
		{"the probe answers a shape we cannot read", nil, func(w *world) { w.allowed = "x" }, false, []string{"shape this lane cannot read"}},
		{"a curated verb is visible", nil, func(w *world) {
			w.allowed = `{"result":[{"expressions":[{"value":["search","graph_nodes"]}]}]}`
		}, true, []string{"curated verb graph_nodes is visible"}},
		{"a table someone committed", merge(derive, map[string]string{bundlelane.DecisionsPath: "{}"}), nil, true, []string{"is committed, and the lane derives it"}},
		{"no fleet data for the table", merge(derive, map[string]string{"fleet/data.json": ""}), nil, true, []string{"fleet/data.json is absent"}},
		{"a catalog entry that does not parse", merge(derive, map[string]string{"fleet/repos/bad.json": "{"}), nil, true, []string{"bad"}},
		{"the derive fails", derive, func(w *world) { w.failEval["data.authz_v2_table.decisions"] = 2 }, true, []string{"did not derive"}},
		{"the table is not one object", derive, func(w *world) { w.table = "x" }, true, []string{"not one JSON object"}},
		{"the die lacks the table", derive, func(w *world) { w.table = derived }, true, []string{"the decision table was derived but the built die carries no data.grants.decisions"}},
	} {
		w := newWorld(t, tc.files)
		if tc.edit != nil {
			tc.edit(w)
		}
		expect(t, Policy(context.Background(), w.run()), tc.findings, tc.needles...)
	}
}

func TestPolicyFilesystemFaults(t *testing.T) {
	// The out directory is a file: nothing can be built into it.
	w := newWorld(t, nil)
	bad := filepath.Join(t.TempDir(), "file")
	os.WriteFile(bad, []byte("x"), 0o644)
	t.Setenv(OutEnv, filepath.Join(bad, "sub"))
	expect(t, Policy(context.Background(), w.run()), false, "bundle directory could not be made")
	// No policy/ to copy.
	w = newWorld(t, map[string]string{"policy/.manifest": "", "policy/admission/a.rego": ""})
	expect(t, Policy(context.Background(), w.run()), false, "policy/ could not be copied")
	// A tree whose commit cannot be read at all.
	w = newWorld(t, nil)
	t.Setenv("ATOM_SHA", "")
	w.gitDead = true
	expect(t, Policy(context.Background(), w.run()), false, "git rev-parse HEAD never ran")
}

func TestShaFallsBackToTheCheckoutsHead(t *testing.T) {
	w := newWorld(t, nil)
	t.Setenv("ATOM_SHA", "")
	if got, f := w.run().Sha(context.Background()); f != nil || got != sha {
		t.Errorf("%q %v", got, f)
	}
	w.headEmpty = true // an empty answer is no commit
	if _, f := w.run().Sha(context.Background()); f == nil || !strings.Contains(f.Msg, "the commit could not be read") {
		t.Errorf("%v", f)
	}
}

func TestVerdictFoldsFaultsIntoTheAtomsStates(t *testing.T) {
	said := func(v checks.Verdict) string { return v.Reason + "\n" + strings.Join(v.Logs, "\n") }
	if v := Verdict(row, nil, "built"); v.State != int(checks.StatePass) || !strings.Contains(said(v), "bundle:test: built") {
		t.Errorf("%+v", v)
	}
	if v := Verdict(row, Findings("x"), ""); v.State != int(checks.StateFindings) || !strings.Contains(said(v), "FINDINGS - x") {
		t.Errorf("%+v", v)
	}
	if v := Verdict(row, CouldNotRun("y"), ""); v.State != int(checks.StateCannotRun) || !strings.Contains(said(v), "CANNOT RUN - y") {
		t.Errorf("%+v", v)
	}
	var f error = Findings("z")
	if f.Error() != "z" {
		t.Error("a Fault is its message")
	}
}

func TestMetaRoundTripsAndNamesWhatIsMissing(t *testing.T) {
	newWorld(t, nil)
	if err := WriteMeta("x", Meta{Die: "d"}); err != nil {
		t.Fatal(err)
	}
	m, err := ReadMeta("x")
	if err != nil || m.Die != "d" || m.Schema != MetaSchema {
		t.Fatalf("%+v %v", m, err)
	}
	if _, err := ReadMeta("nothing"); err == nil {
		t.Error("a missing meta was read")
	}
	os.WriteFile(filepath.Join(OutDir(), "bad"+MetaSuffix), []byte("{"), 0o644)
	if _, err := ReadMeta("bad"); err == nil || !strings.Contains(err.Error(), "is not JSON") {
		t.Errorf("%v", err)
	}
	bad := filepath.Join(t.TempDir(), "f")
	os.WriteFile(bad, []byte("x"), 0o644)
	t.Setenv(OutEnv, filepath.Join(bad, "sub"))
	if err := WriteMeta("y", Meta{}); err == nil {
		t.Error("a meta was written under a file")
	}
}

func TestDirsFollowTheirVariables(t *testing.T) {
	t.Setenv(OutEnv, "")
	t.Setenv(StageEnv, "")
	t.Setenv(FluxEnv, "")
	t.Setenv("ATOM_WORK", "/atom")
	if OutDir() != "/atom/bundle" || StageDir() != StageDefault || FluxDir() != FluxDefault {
		t.Errorf("%s %s %s", OutDir(), StageDir(), FluxDir())
	}
}

func TestPolicyWriteFaults(t *testing.T) {
	derive := map[string]string{bundlelane.DecisionsDerive: "package t\n", "fleet/derive.rego": "package fleet\n"}
	for _, tc := range []struct {
		needle  string
		files   map[string]string
		needles string
	}{
		{"/policy/admission/a.rego", nil, "policy/ could not be copied"},
		{"probe-session.json", nil, "probe could not be written"},
		{"policy" + MetaSuffix, nil, "policy.meta.json"},
		{"decisions-stage", derive, "roster could not be staged"},
		{"grants/decisions/data.json", derive, "decision table could not be laid"},
	} {
		w := newWorld(t, tc.files)
		if tc.files != nil {
			w.table = derived
			w.dataJSON = `{"authz_audience":{"star_only":{"chaos":["graph_nodes"]}},"grants":{"decisions":` + derived + `},"principals":{"p":1},"capabilities":{"c":1}}`
		}
		restore := failWrite(t, tc.needle)
		expect(t, Policy(context.Background(), w.run()), false, tc.needles)
		restore()
	}
	// The staged roster's declared document is its own write.
	w := newWorld(t, derive)
	defer failWrite(t, "decisions-stage/fleet/declared")()
	expect(t, Policy(context.Background(), w.run()), false, "roster could not be staged")
}

func TestPolicyNeedsAScratchDirectory(t *testing.T) {
	w := newWorld(t, nil)
	t.Setenv("ATOM_WORK", filepath.Join(t.TempDir(), "gone"))
	expect(t, Policy(context.Background(), w.run()), false, "scratch directory could not be made")
}

func TestPolicyDerivedTableReadFaults(t *testing.T) {
	// A catalog entry that cannot be read is the lane's: the glob lists it, the read fails.
	w := newWorld(t, map[string]string{bundlelane.DecisionsDerive: "package t\n", "fleet/derive.rego": "package fleet\n"})
	os.Remove(filepath.Join(w.in.Root, "fleet/repos/chaos.json"))
	if err := os.Symlink("/nonexistent/entry", filepath.Join(w.in.Root, "fleet/repos/chaos.json")); err != nil {
		t.Fatal(err)
	}
	expect(t, Policy(context.Background(), w.run()), false, "could not be read")
}
