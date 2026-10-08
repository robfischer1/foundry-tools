package castlane

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/buildlane"
)

// record is a record around a tools block.
func record(tools string) string {
	return `{"$schema":"https://forgejo.notusmi.com/rob/foundry-dies/schema/slag-v3.schema.json","meta":{"name":"cerberus","produces":["binary"]},"tools":` + tools + `}`
}

// The record says the star's name and nothing else: whatever it still carries
// of binaries, produces or a schema version is not read.
func TestFromRecordReadsTheName(t *testing.T) {
	c, err := FromRecord(record(`{"cast":{"binaries":["ignored"],"payload_extra":[".cerberus/hooks"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	want := Cast{Name: "cerberus", LegacyExtras: []string{".cerberus/hooks"}}
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("got %+v, want %+v", c, want)
	}
	if got := c.Artifact(); got != "app/cerberus:stable" {
		t.Errorf("artifact %q", got)
	}
	if got := c.Stage("foundry.notusmi.com", "g0123456789ab"); got != "foundry.notusmi.com/staging/app-cerberus:g0123456789ab" {
		t.Errorf("stage %q", got)
	}
	if got := Target(".cerberus/hooks/"); got != "hooks" {
		t.Errorf("target %q", got)
	}
	// No tools block, no produces, another schema: still a record with a name.
	if c, err := FromRecord(`{"meta":{"name":"naiad"}}`); err != nil || c.Name != "naiad" || c.LegacyExtras != nil {
		t.Errorf("a bare record: %+v %v", c, err)
	}
}

func TestFromRecordRefusesARecordWithNoName(t *testing.T) {
	for name, c := range map[string]struct{ slag, why string }{
		"not json": {`{`, "does not parse"},
		"no name":  {`{"meta":{}}`, "no meta.name"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := FromRecord(c.slag); err == nil || !strings.Contains(err.Error(), c.why) {
				t.Fatalf("want a refusal naming %q, got %v", c.why, err)
			}
		})
	}
}

// Every main package directly under cmd/ ships, by directory name and
// sorted; anything else go list prints does not.
func TestBinariesFromGoList(t *testing.T) {
	out := "main example.com/argus/cmd/gpx_control\nmain example.com/argus/cmd/argus\nargus example.com/argus/cmd/lib\n" +
		"main example.com/argus/cmd/x/nested\nmain example.com/argus/cmd/\nmain example.com/argus/other\nmain\nmain a b\n\n"
	got, err := BinariesFromGoList(out)
	if err != nil || strings.Join(got, ",") != "argus,gpx_control" {
		t.Errorf("got %v %v", got, err)
	}
	for _, none := range []string{"", "go: warning: \"./cmd/...\" matched no packages\n", "lib example.com/m/cmd/lib\n", "main\n", "main example.com/m/cmd/\n", "main example.com/m/elsewhere\n"} {
		if _, err := BinariesFromGoList(none); err == nil {
			t.Errorf("%q: a binary repo with no main is a finding", none)
		}
	}
}

// The bin targets of the DEFAULT members, and only those: gravity's members
// include wasm-guest examples its default members leave out.
func TestBinariesFromCargoMetadata(t *testing.T) {
	meta := `{"packages":[
		{"id":"p1","targets":[{"name":"cerberus-gauge","kind":["bin"]},{"name":"cerberus","kind":["bin"]},{"name":"cerberus_lib","kind":["lib"]}]},
		{"id":"p2","targets":[{"name":"guest","kind":["bin"]}]},
		{"id":"p3","targets":[{"name":"cerberus","kind":["bin"]}]}
	],"workspace_default_members":["p1","p3"]}`
	got, err := BinariesFromCargoMetadata(meta)
	if err != nil || strings.Join(got, ",") != "cerberus,cerberus-gauge" {
		t.Errorf("got %v %v", got, err)
	}
	for name, c := range map[string]struct{ doc, why string }{
		"not json":     {"warning: not json", "not JSON"},
		"no bins":      {`{"packages":[{"id":"p","targets":[{"name":"x","kind":["lib"]}]}],"workspace_default_members":["p"]}`, "no bin target"},
		"none default": {`{"packages":[{"id":"p","targets":[{"name":"x","kind":["bin"]}]}],"workspace_default_members":[]}`, "no bin target"},
	} {
		if _, err := BinariesFromCargoMetadata(c.doc); err == nil || !strings.Contains(err.Error(), c.why) {
			t.Errorf("%s: want a refusal naming %q, got %v", name, c.why, err)
		}
	}
}

// Every binary and every payload entry lands under one name; a second claim
// is refused, and so is a name that is not a file name.
func TestClaimsRefuseWhatWouldShipWhicheverWrittenLast(t *testing.T) {
	if err := Claims([]string{"cerberus", "cerberus-gauge"}, []string{"hooks", "bin"}); err != nil {
		t.Errorf("a clean payload: %v", err)
	}
	for name, c := range map[string]struct {
		bins, payload []string
		why           string
	}{
		"an empty binary":     {[]string{""}, nil, "is not a file name"},
		"a path":              {[]string{"target/release/x"}, nil, "is not a file name"},
		"a windows path":      {[]string{`release\x`}, nil, "is not a file name"},
		"hidden":              {[]string{".x"}, nil, "is not a file name"},
		"a flag":              {[]string{"-rf"}, nil, "is not a file name"},
		"a binary twice":      {[]string{"x", "x"}, nil, `both land at "x"`},
		"payload over binary": {[]string{"hooks"}, []string{"hooks"}, `both land at "hooks"`},
	} {
		if err := Claims(c.bins, c.payload); err == nil || !strings.Contains(err.Error(), c.why) {
			t.Errorf("%s: want a refusal naming %q, got %v", name, c.why, err)
		}
	}
}

func TestLegacyTargets(t *testing.T) {
	got, err := LegacyTargets([]string{".cerberus/hooks", "bin"})
	if err != nil || strings.Join(got, ",") != "hooks,bin" {
		t.Errorf("got %v %v", got, err)
	}
	for name, bad := range map[string]string{"empty": "", "absolute": "/etc", "climbs": "hooks/../../etc", "the repo": "."} {
		if _, err := LegacyTargets([]string{bad}); err == nil {
			t.Errorf("%s: want a refusal", name)
		}
	}
	if _, err := LegacyTargets([]string{"a/b..c"}); err != nil {
		t.Errorf("a/b..c is inside the repo: %v", err)
	}
}

// The golden vectors are hephaestus's (internal/bundle/pin_test.go), which were
// generated by the Python reference. A pin that differs from them is a pin mold
// refuses at mint.
var goldenPins = []struct {
	name  string
	tree  map[string][]byte
	pin   string
	files []string
}{
	{"simple", map[string][]byte{"a.txt": []byte("alpha\n"), "b/c.txt": []byte("see\n")}, "gd77bd1c7fa61", []string{"a.txt", "b/c.txt"}},
	{"ordering", map[string][]byte{"a-b/x": []byte("1"), "a/x": []byte("2"), "a.txt": []byte("3")}, "g5252ae1581b8", []string{"a/x", "a-b/x", "a.txt"}},
	{"binary", map[string][]byte{"bin": {0x00, 0x01, 0xff}, "empty": {}, "z/deep/file": []byte("d")}, "gf6f8f22e8b94", []string{"bin", "empty", "z/deep/file"}},
	{"unicode", map[string][]byte{"naïve.txt": []byte("u"), "zz.txt": []byte("z")}, "ga9fe2bba4745", []string{"naïve.txt", "zz.txt"}},
}

func buildTree(t *testing.T, spec map[string][]byte) string {
	t.Helper()
	root := t.TempDir()
	for rel, data := range spec {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestContentPinMatchesMoldsPin(t *testing.T) {
	for _, g := range goldenPins {
		pin, files, err := ContentPin(buildTree(t, g.tree))
		if err != nil {
			t.Fatalf("%s: %v", g.name, err)
		}
		if pin != g.pin {
			t.Errorf("%s: pin %q, mold derives %q", g.name, pin, g.pin)
		}
		if !reflect.DeepEqual(files, g.files) {
			t.Errorf("%s: files %v, want %v in pin order", g.name, files, g.files)
		}
	}
}

// A symlink to a file is pinned as the file (Python's is_file follows links),
// and a directory contributes only the files under it.
func TestContentPinFollowsLinksAndSkipsDirectories(t *testing.T) {
	root := buildTree(t, map[string][]byte{"a.txt": []byte("alpha\n"), "b/c.txt": []byte("see\n")})
	if err := os.MkdirAll(filepath.Join(root, "empty-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	pin, _, err := ContentPin(root)
	if err != nil || pin != "gd77bd1c7fa61" {
		t.Errorf("an empty directory moved the pin: %q %v", pin, err)
	}
	linked := buildTree(t, map[string][]byte{"b/c.txt": []byte("see\n")})
	outside := buildTree(t, map[string][]byte{"target.txt": []byte("alpha\n")})
	if err := os.Symlink(filepath.Join(outside, "target.txt"), filepath.Join(linked, "a.txt")); err != nil {
		t.Fatal(err)
	}
	if pin, _, err := ContentPin(linked); err != nil || pin != "gd77bd1c7fa61" {
		t.Errorf("a linked file did not pin as the file it names: %q %v", pin, err)
	}
	if _, _, err := ContentPin(filepath.Join(root, "missing")); err == nil {
		t.Error("a tree that is not there pinned")
	}
}

func TestRunPinPrintsThePinThenTheFiles(t *testing.T) {
	root := buildTree(t, goldenPins[1].tree)
	var out, errs bytes.Buffer
	if code := RunPin([]string{root}, &out, &errs); code != 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	if got, want := out.String(), "g5252ae1581b8\na/x\na-b/x\na.txt\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	pin, files, err := ParseListing(out.String())
	if err != nil || pin != "g5252ae1581b8" || !reflect.DeepEqual(files, []string{"a/x", "a-b/x", "a.txt"}) {
		t.Errorf("the listing does not read back: %q %v %v", pin, files, err)
	}

	out.Reset()
	errs.Reset()
	if code := RunPin(nil, &out, &errs); code != 2 || !strings.Contains(errs.String(), "usage") {
		t.Errorf("no argument: exit %d, %q", code, errs.String())
	}
	errs.Reset()
	if code := RunPin([]string{filepath.Join(root, "missing")}, &out, &errs); code != 2 {
		t.Errorf("a missing tree: exit %d", code)
	}
	errs.Reset()
	if code := RunPin([]string{t.TempDir()}, &out, &errs); code != 1 || !strings.Contains(errs.String(), "holds no files") {
		t.Errorf("an empty tree: exit %d, %q", code, errs.String())
	}
	if out.Len() != 0 {
		t.Errorf("a refused tree printed %q", out.String())
	}
}

// One binary and nothing else is the commonest payload in the fleet.
func TestParseListingReadsASingleFile(t *testing.T) {
	pin, files, err := ParseListing("g0123456789ab\ntongs\n")
	if err != nil || pin != "g0123456789ab" || !reflect.DeepEqual(files, []string{"tongs"}) {
		t.Errorf("one file does not read back: %q %v %v", pin, files, err)
	}
}

func TestParseListingRefusesWhatCastpinDidNotSay(t *testing.T) {
	for out, why := range map[string]string{
		"":                   "no pin",
		"g12\nbin\n":         "no pin",
		"gzzzzzzzzzzzz\nx":   "no pin",
		"g0123456789ab\n":    "listed no files",
		"g0123456789ab":      "listed no files",
		"x\ng0123456789ab\n": "no pin",
	} {
		if _, _, err := ParseListing(out); err == nil || !strings.Contains(err.Error(), why) {
			t.Errorf("%q: want %q, got %v", out, why, err)
		}
	}
}

// answer is hades's tool-call envelope around text.
func answer(isError bool, text string) string {
	b, _ := json.Marshal(map[string]any{"isError": isError, "content": []map[string]string{{"type": "text", "text": text}}})
	return string(b)
}

const minted = `{"channel":"foundry.notusmi.com/app/tongs:stable","kind":"app","name":"tongs","tag":"stable","version_index":7,"pin":"g0123456789ab","digest":"sha256:abc","signed":true,"source_sha":"` + "dddddddddddddddddddddddddddddddddddddddd" + `","noop":false}`

func TestMintedReadsTheCast(t *testing.T) {
	r, code, why := Minted(200, answer(false, minted), "g0123456789ab")
	if code != buildlane.Clean || why != "" {
		t.Fatalf("settled %d %q", code, why)
	}
	want := Result{Channel: "foundry.notusmi.com/app/tongs:stable", Index: 7, Pin: "g0123456789ab", Digest: "sha256:abc", Signed: true}
	if r != want {
		t.Errorf("got %+v, want %+v", r, want)
	}
	noop := strings.Replace(minted, `"noop":false`, `"noop":true`, 1)
	if r, code, _ := Minted(200, answer(false, noop), "g0123456789ab"); code != buildlane.Clean || !r.NoOp {
		t.Errorf("a no-op cast is clean and says so: %+v %d", r, code)
	}
}

func TestMintedSettlesEveryOtherAnswer(t *testing.T) {
	cases := map[string]struct {
		status   int
		raw, why string
		code     int
	}{
		"no principal":      {403, `{"detail":"unidentifiable caller"}`, "did not derive a principal", buildlane.CouldNotRun},
		"not granted":       {403, `{"detail":"denied"}`, "not granted layer_cast", buildlane.Findings},
		"no certificate":    {401, ``, "rejected the caller at the door", buildlane.CouldNotRun},
		"a server error":    {503, ``, "HTTP 503", buildlane.CouldNotRun},
		"not a tool answer": {200, `garbage`, "not a tool answer", buildlane.CouldNotRun},
		"an empty answer":   {200, `{"content":[]}`, "not a tool answer", buildlane.CouldNotRun},
		"mold refused":      {200, answer(true, "mold app/tongs:stable: staged payload pin mismatch"), "findings in layer_cast", buildlane.Findings},
		"mold lost the net": {200, answer(true, "dial tcp 10.0.0.1:443: connect: connection refused"), "could not run", buildlane.CouldNotRun},
		"not a result":      {200, answer(false, "stamped"), "not a cast result", buildlane.CouldNotRun},
		"no digest":         {200, answer(false, strings.Replace(minted, `"sha256:abc"`, `""`, 1)), "answered no digest", buildlane.CouldNotRun},
		"another pin":       {200, answer(false, strings.Replace(minted, `"g0123456789ab"`, `"gffffffffffff"`, 1)), "minted pin gffffffffffff, and the lane staged g0123456789ab", buildlane.CouldNotRun},
		"unsigned":          {200, answer(false, strings.Replace(minted, `"signed":true`, `"signed":false`, 1)), "did not sign it", buildlane.Findings},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r, code, why := Minted(c.status, c.raw, "g0123456789ab")
			if code != c.code || !strings.Contains(why, c.why) {
				t.Fatalf("settled %d %q, want %d naming %q", code, why, c.code, c.why)
			}
			if r != (Result{}) {
				t.Errorf("a cast that did not settle clean answered a result: %+v", r)
			}
		})
	}
}

func TestDoorbellCarriesWhatDeliveryReads(t *testing.T) {
	body := Doorbell(Cast{Name: "tongs"}, Result{Channel: "foundry.notusmi.com/app/tongs:stable", Index: 7, Pin: "g0123456789ab", Digest: "sha256:abc"})
	var got struct {
		Records []struct {
			Value map[string]any `json:"value"`
		} `json:"records"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil || len(got.Records) != 1 {
		t.Fatalf("not one record: %s %v", body, err)
	}
	want := map[string]any{"kind": "app", "name": "tongs", "channel": "foundry.notusmi.com/app/tongs:stable", "index": float64(7), "pin": "g0123456789ab", "digest": "sha256:abc"}
	if !reflect.DeepEqual(got.Records[0].Value, want) {
		t.Errorf("got %v, want %v", got.Records[0].Value, want)
	}
}

// The verdict names layer_cast, the one verb the lane asks.
func TestMintedNamesLayerCast(t *testing.T) {
	if _, _, why := Minted(403, `{"detail":"denied"}`, "g0123456789ab"); !strings.Contains(why, "not granted layer_cast") {
		t.Errorf("denied: %q", why)
	}
	if _, _, why := Minted(200, answer(true, "refused: payload pin mismatch"), "g0123456789ab"); !strings.Contains(why, "layer_cast") {
		t.Errorf("refused: %q", why)
	}
	if _, _, why := Minted(200, answer(false, "stamped"), "g0123456789ab"); !strings.Contains(why, "layer_cast's answer is not a cast result") {
		t.Errorf("not a result: %q", why)
	}
}
