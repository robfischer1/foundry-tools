package orbitlane

import (
	"slices"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
	"dagger/foundry-tools/internal/orbitcompose"
)

const body = "version = \"1\"\nstatus = \"generated\"\nwire_form = \"native\"\nverbs = [\"neighbors\", \"shape_for\"]\n"

func parse(t *testing.T, name, raw string) orbitcompose.Contract {
	t.Helper()
	c, err := orbitcompose.ParseContract(name, []byte(raw))
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
	approved := strings.Replace(body, "generated", "approved", 1)
	files := map[string][]byte{
		"README.md":           []byte("not a contract"),
		"urania-themis.toml":  []byte(body),
		"chaos-themis.toml":   []byte(strings.Replace(body, "generated", "proposed", 1)),
		"chaos-ghost.toml":    []byte(body),
		"ghost-chaos.toml":    []byte(body),
		"chaos-urania.toml":   []byte("version = 1"),
		"themis-urania.toml":  []byte(strings.Replace(approved, `"shape_for"`, `"shapes"`, 1)),
		"themis-chaos.toml":   []byte(strings.Replace(strings.Replace(approved, `"shape_for"`, `"shapes"`, 1), `"1"`, `"2"`, 1)),
		"urania-chaos.toml":   []byte(approved),
		"themis-themis2.toml": []byte(body),
	}
	base := map[string][]byte{
		"themis-urania.toml": []byte(approved),
		"themis-chaos.toml":  []byte(approved),
		"urania-chaos.toml":  []byte(approved),
	}
	got := verdicts(Contracts(files, base, roster))
	want := []string{
		"violated party-not-on-roster orbits/chaos-ghost.toml",
		"violated status-unknown orbits/chaos-themis.toml",
		"violated contract-unparseable orbits/chaos-urania.toml",
		"violated party-not-on-roster orbits/ghost-chaos.toml",
		"holds contract orbits/themis-chaos.toml",
		"violated party-not-on-roster orbits/themis-themis2.toml",
		"violated approved-verbs-moved orbits/themis-urania.toml",
		"holds contract orbits/urania-chaos.toml",
		"holds contract orbits/urania-themis.toml",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	found := Contracts(files, base, roster)
	for _, f := range found {
		switch f.Cause {
		case "contract-unparseable":
			if !strings.Contains(f.Detail, "does not parse") || !strings.HasSuffix(f.Detail, Teach) {
				t.Errorf("unparseable detail %q", f.Detail)
			}
		case "approved-verbs-moved":
			if f.Detail != "an approved contract's verbs changed (neighbors,shape_for -> neighbors,shapes) and its version stayed 1 — raise version in the same pull" {
				t.Errorf("approved detail %q", f.Detail)
			}
		case "party-not-on-roster":
			if !strings.HasSuffix(f.Detail, "is not on the fleet roster (fleet/stars/"+strings.Fields(f.Detail)[0]+"/data.json)") {
				t.Errorf("roster detail %q", f.Detail)
			}
		case "status-unknown":
			if f.Detail != "status proposed is not one of generated, approved" {
				t.Errorf("status detail %q", f.Detail)
			}
		case "contract":
			if f.Subject == "orbits/urania-themis.toml" && f.Detail != "generated v1, neighbors,shape_for" {
				t.Errorf("holds detail %q", f.Detail)
			}
		}
	}
}

func TestContractsOfAnEmptyDirectoryIsAViolation(t *testing.T) {
	got := verdicts(Contracts(map[string][]byte{"README.md": nil}, nil, nil))
	if !slices.Equal(got, []string{"violated no-contracts orbits/"}) {
		t.Errorf("%v", got)
	}
}

func TestAnApprovedContractWithNoBaseOrAnUnparseableBaseHolds(t *testing.T) {
	approved := strings.Replace(body, "generated", "approved", 1)
	roster := map[string]bool{"urania": true, "themis": true}
	for name, base := range map[string]map[string][]byte{
		"new":         nil,
		"broken base": {"urania-themis.toml": []byte("version = 1")},
		"same verbs":  {"urania-themis.toml": []byte(approved)},
	} {
		got := verdicts(Contracts(map[string][]byte{"urania-themis.toml": []byte(approved)}, base, roster))
		if !slices.Equal(got, []string{"holds contract orbits/urania-themis.toml"}) {
			t.Errorf("%s: %v", name, got)
		}
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
