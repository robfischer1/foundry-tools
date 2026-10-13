package orbitlane

import (
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/orbitcompose"
)

const body = "version = \"1\"\nstatus = \"approved\"\nverbs = [\"neighbors\", \"shape_for\"]\n"

func parse(t *testing.T, name, raw string) orbitcompose.Contract {
	t.Helper()
	c, err := orbitcompose.ParseContract(name, []byte(raw), nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func verdicts(found []checks.Finding) []string {
	var out []string
	for _, f := range found {
		out = append(out, f.Verdict+" "+f.Cause+" "+f.Subject)
	}
	return out
}

func TestSettleIsReportOnlyAndSaysSo(t *testing.T) {
	if Enforce {
		t.Fatal("the lane starts report-only; flipping Enforce is Rob's call, made in its own pull with this test")
	}
	in := []checks.Finding{
		finding(checks.VerdictViolated, "a", "x", "broke"),
		finding(checks.VerdictHolds, "b", "y", "fine"),
		finding(checks.VerdictUnanalyzable, "c", "z", "unknown"),
		finding(checks.VerdictInert, "d", "w", "nothing"),
	}
	state, why, out := Settle("orbit:repo", in)
	if state != 0 {
		t.Errorf("state %d", state)
	}
	if out[0].Verdict != checks.VerdictDrifted || out[0].Detail != "[report-only: would be violated] broke" {
		t.Errorf("violated was not demoted and marked: %+v", out[0])
	}
	if in[0].Verdict != checks.VerdictViolated {
		t.Error("the caller's slice was rewritten")
	}
	for _, f := range out {
		if f.Probe != "orbit:repo" {
			t.Errorf("probe %q", f.Probe)
		}
	}
	want := "orbit:repo: 1 unanalyzable, 1 drifted, 1 holds, 1 inert" +
		"\n  drifted  a — [report-only: would be violated] broke" +
		"\n  unanalyzable  c — unknown"
	if why != want {
		t.Errorf("reason\n%q\nwant\n%q", why, want)
	}
	if _, why, _ := Settle("orbit:repo", nil); why != "orbit:repo: nothing to check" {
		t.Errorf("empty: %q", why)
	}
}

func TestEnforcedAViolationIsRedAndStaysViolated(t *testing.T) {
	state, why, out := settle("orbit:surface", []checks.Finding{finding(checks.VerdictViolated, "a", "x", "broke")}, true)
	if state != 1 || out[0].Verdict != checks.VerdictViolated || out[0].Detail != "broke" || why != "orbit:surface: 1 violated\n  violated  a — broke" {
		t.Errorf("state %d %+v %q", state, out[0], why)
	}
	if state, _, _ := settle("orbit:surface", []checks.Finding{finding(checks.VerdictDrifted, "a", "x", "moved")}, true); state != 0 {
		t.Errorf("a drift is never red: %d", state)
	}
}

func TestContractsJudgesEveryFile(t *testing.T) {
	roster := map[string]bool{"urania": true, "themis": true, "chaos": true}
	generated := strings.Replace(body, "approved", "generated", 1)
	files := map[string][]byte{
		"README.md":          []byte("not a contract"),
		"urania-themis.toml": []byte(body),
		"chaos-themis.toml":  []byte(strings.Replace(body, "approved", "proposed", 1)),
		// The ladder is retired: generated is no longer a status a fact renders.
		"themis-chaos.toml":   []byte(generated),
		"chaos-ghost.toml":    []byte(body),
		"ghost-chaos.toml":    []byte(body),
		"chaos-urania.toml":   []byte("version = 1"),
		"urania-chaos.toml":   []byte(body),
		"themis-themis2.toml": []byte(body),
		// A CI lane consumer is on no roster and needs none; its producer does.
		"chaos-job.gate.toml": []byte(body),
		"ghost-job.gate.toml": []byte(body),
		"chaos-ci.gate.toml":  []byte(body),
		"ghost-ci.gate.toml":  []byte(body),
	}
	got := verdicts(Contracts(files, roster))
	want := []string{
		"holds contract orbits/chaos-ci.gate.toml",
		"violated party-not-on-roster orbits/chaos-ghost.toml",
		"holds contract orbits/chaos-job.gate.toml",
		"violated status-unknown orbits/chaos-themis.toml",
		"violated contract-unparseable orbits/chaos-urania.toml",
		"violated party-not-on-roster orbits/ghost-chaos.toml",
		"violated party-not-on-roster orbits/ghost-ci.gate.toml",
		"violated party-not-on-roster orbits/ghost-job.gate.toml",
		"violated status-unknown orbits/themis-chaos.toml",
		"violated party-not-on-roster orbits/themis-themis2.toml",
		"holds contract orbits/urania-chaos.toml",
		"holds contract orbits/urania-themis.toml",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, f := range Contracts(files, roster) {
		switch f.Cause {
		case "contract-unparseable":
			if !strings.Contains(f.Detail, "does not parse") || !strings.HasSuffix(f.Detail, Teach) {
				t.Errorf("unparseable detail %q", f.Detail)
			}
		case "party-not-on-roster":
			if !strings.HasSuffix(f.Detail, "is not on the fleet roster (fleet/repos/"+strings.Fields(f.Detail)[0]+".json)") {
				t.Errorf("roster detail %q", f.Detail)
			}
		case "status-unknown":
			if !strings.HasPrefix(f.Detail, "status ") || !strings.Contains(f.Detail, " is not approved (the only status") || !strings.HasSuffix(f.Detail, Teach) {
				t.Errorf("status detail %q", f.Detail)
			}
		case "approved-verbs-moved":
			t.Errorf("the version rule is retired: %q", f.Detail)
		case "contract":
			if f.Subject == "orbits/urania-themis.toml" && f.Detail != "approved v1, neighbors,shape_for" {
				t.Errorf("holds detail %q", f.Detail)
			}
		}
	}
}

// Teach points at the facts, not at a hand edit.
func TestTeachNamesTheDeclareVerbAndTheRender(t *testing.T) {
	for _, want := range []string{"nyx_declare_contract", "nyx_retire_contract", "nyx facts render"} {
		if !strings.Contains(Teach, want) {
			t.Errorf("Teach lacks %q: %s", want, Teach)
		}
	}
	if strings.Contains(Teach, "edit it in a foundry-dies pull") {
		t.Errorf("Teach still sends a session to hand-edit: %s", Teach)
	}
}

func TestContractsOfAnEmptyDirectoryIsAViolation(t *testing.T) {
	got := verdicts(Contracts(map[string][]byte{"README.md": nil}, nil))
	if !slices.Equal(got, []string{"violated no-contracts orbits/"}) {
		t.Errorf("%v", got)
	}
}

func contractsFor(t *testing.T) []orbitcompose.Contract {
	return []orbitcompose.Contract{
		parse(t, "urania-themis.toml", body),
		parse(t, "chaos-themis.toml", strings.Replace(body, `"neighbors", "shape_for"`, `"quads_to"`, 1)),
		parse(t, "themis-athena.toml", strings.Replace(body, `"neighbors", "shape_for"`, `"close_node"`, 1)),
	}
}

func TestRepoComparesTheEdgeSetAndEachPin(t *testing.T) {
	cs := contractsFor(t)
	ut, ct, ta := cs[0], cs[1], cs[2]
	laid := "[[produces]]\nto = \"athena\"\ncontract = \"themis-athena\"\nversion = \"1\"\ndigest = \"" + ta.Digest + "\"\n" +
		"\n[[consumes]]\nfrom = \"urania\"\ncontract = \"urania-themis\"\nversion = \"1\"\ndigest = \"sha256:old\"\nverbs = [\"x\"]\n" +
		"\n[[consumes]]\nfrom = \"eros\"\ncontract = \"eros-themis\"\nversion = \"0\"\ndigest = \"sha256:e\"\n"
	found := Repo("themis", []byte(laid), cs)
	got := verdicts(found)
	want := []string{
		"drifted edge-missing consumes chaos",
		"drifted edge-uncontracted consumes eros",
		"drifted edge-stale consumes urania",
		"holds edge produces athena",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	details := []string{
		"contract chaos-themis exists and orbit.toml does not name it; " + RelayHint,
		"orbit.toml names \"eros-themis\" and no contract in foundry-dies/orbits backs this edge; " + RelayHint,
		"orbit.toml pins urania-themis v1 sha256:old; the contract is urania-themis v1 " + ut.Digest + " — the contract moved and this file did not; " + RelayHint,
		"themis-athena v1 " + ta.Digest,
	}
	for i, f := range found {
		if f.Detail != details[i] {
			t.Errorf("%d: %q\nwant %q", i, f.Detail, details[i])
		}
	}
	_ = ct
	// A file that agrees on every key holds throughout, whatever else it inlines.
	good := string(orbitcompose.RenderReference(orbitcompose.Compose(cs)[2]))
	for _, f := range Repo("themis", []byte(good), cs) {
		if f.Verdict != checks.VerdictHolds {
			t.Errorf("the composed file does not hold: %+v", f)
		}
	}
}

func TestRepoTellsAMovedContractAndVersionApart(t *testing.T) {
	cs := contractsFor(t)
	ta := cs[2]
	for name, edge := range map[string]string{
		"contract": "contract = \"other\"\nversion = \"1\"\ndigest = \"" + ta.Digest + "\"",
		"version":  "contract = \"themis-athena\"\nversion = \"2\"\ndigest = \"" + ta.Digest + "\"",
	} {
		laid := "[[consumes]]\nfrom = \"themis\"\n" + edge + "\n"
		if got := verdicts(Repo("athena", []byte(laid), cs)); !slices.Equal(got, []string{"drifted edge-stale consumes themis"}) {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestRepoRefusesAFileThatDoesNotParseAndSaysNothingForNoSeams(t *testing.T) {
	got := Repo("themis", []byte("[[produces]\n"), nil)
	if len(got) != 1 || got[0].Verdict != checks.VerdictViolated || got[0].Cause != "orbit-toml-unparseable" || !strings.HasSuffix(got[0].Detail, RelayHint) {
		t.Errorf("%+v", got)
	}
	if got := verdicts(Repo("eros", []byte("# nothing\n"), contractsFor(t))); !slices.Equal(got, []string{"inert no-seams orbit.toml"}) {
		t.Errorf("%v", got)
	}
}

// A HYPHENATED STAR IS ONE PARTY: the roster splits the name.
func TestContractsSplitsAHyphenatedStarAgainstTheRoster(t *testing.T) {
	roster := map[string]bool{"blade-runner": true, "poseidon": true}
	files := map[string][]byte{"blade-runner-poseidon.toml": []byte(body)}
	if got := verdicts(Contracts(files, roster)); !slices.Equal(got, []string{"holds contract orbits/blade-runner-poseidon.toml"}) {
		t.Errorf("with the roster: %v", got)
	}
	if got := verdicts(Contracts(files, map[string]bool{"poseidon": true})); !slices.Equal(got, []string{"violated contract-unparseable orbits/blade-runner-poseidon.toml"}) {
		t.Errorf("blade-runner off the roster: %v", got)
	}
}

// Every file the composer could not read is a contract-unparseable finding
// on that file, with the teaching line.
func TestUnreadableIsOneFindingPerFile(t *testing.T) {
	_, bad, err := orbitcompose.ParseAll(map[string][]byte{
		"urania-themis.toml": []byte(body), "b-bad.toml": []byte("version = 1"), "a-bad.toml": []byte("x"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := Unreadable(bad)
	if got := verdicts(found); !slices.Equal(got, []string{
		"violated contract-unparseable orbits/a-bad.toml", "violated contract-unparseable orbits/b-bad.toml"}) {
		t.Fatalf("got %v", got)
	}
	for i, f := range found {
		if !strings.HasPrefix(f.Detail, bad[i].File+": ") || !strings.HasSuffix(f.Detail, "; "+Teach) {
			t.Errorf("detail %q", f.Detail)
		}
	}
	if len(Unreadable(nil)) != 0 {
		t.Error("nothing unreadable, nothing found")
	}
}
