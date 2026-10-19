package atomkit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

// goldenNames are the seven files testdata/SHA256SUMS lists (UT-446).
var goldenNames = []string{"run-plan", "plan", "tree", "atom-verdict", "command-atom", "atom-index", "surface-cases"}

// roundTripNames are the goldenNames and lane, which SHA256SUMS does not list.
var roundTripNames = append(append([]string{}, goldenNames...), "lane")

func readGolden(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// roundTrip decodes golden into a T, re-marshals it, and answers the first
// differing key's path when the bytes differ.
func roundTrip[T any](golden []byte) error {
	var v T
	if err := json.Unmarshal(golden, &v); err != nil {
		return err
	}
	got, err := Marshal(v)
	if err != nil {
		return err
	}
	if bytes.Equal(got, golden) {
		return nil
	}
	return fmt.Errorf("re-encoding differs at key %q", firstDiff(golden, got))
}

// firstDiff names the first key (depth first, keys sorted) at which two JSON
// documents differ: a key present in one and not the other, or unequal values.
func firstDiff(a, b []byte) string {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return "(unparsable)"
	}
	return diffAt("", x, y)
}

func diffAt(path string, x, y any) string {
	mx, okx := x.(map[string]any)
	my, oky := y.(map[string]any)
	if okx && oky {
		keys := map[string]bool{}
		for k := range mx {
			keys[k] = true
		}
		for k := range my {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			vx, inx := mx[k]
			vy, iny := my[k]
			if inx != iny {
				return path + k
			}
			if d := diffAt(path+k+".", vx, vy); d != "" {
				return d
			}
		}
		return ""
	}
	ax, okx := x.([]any)
	ay, oky := y.([]any)
	if okx && oky {
		if len(ax) != len(ay) {
			return strings.TrimSuffix(path, ".")
		}
		for i := range ax {
			if d := diffAt(fmt.Sprintf("%s%d.", path, i), ax[i], ay[i]); d != "" {
				return d
			}
		}
		return ""
	}
	if fmt.Sprint(x) != fmt.Sprint(y) {
		return strings.TrimSuffix(path, ".")
	}
	return ""
}

func goldenCases() map[string]func([]byte) error {
	return map[string]func([]byte) error{
		"run-plan":      roundTrip[RunPlan],
		"plan":          roundTrip[ContainerPlan],
		"tree":          roundTrip[Tree],
		"atom-verdict":  roundTrip[goldenVerdict],
		"command-atom":  roundTrip[Manifest],
		"lane":          roundTrip[LaneDef],
		"atom-index":    roundTrip[AtomIndex],
		"surface-cases": roundTrip[surfaceCases],
	}
}

// TestGoldenRoundTrip is UT-446 and IT-121: each golden decodes and
// re-encodes byte for byte.
func TestGoldenRoundTrip(t *testing.T) {
	cases := goldenCases()
	for _, name := range roundTripNames {
		t.Run(name, func(t *testing.T) {
			if err := cases[name](readGolden(t, name)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// verdictExtra is Verdict with a field one side added (UT-447, IT-122).
type verdictExtra struct {
	goldenVerdict
	Extra string `json:"extra"`
}

func TestUT_447_IT_122_GoldenDriftNamesTheField(t *testing.T) {
	err := roundTrip[verdictExtra](readGolden(t, "atom-verdict"))
	if err == nil || !strings.Contains(err.Error(), "extra") {
		t.Fatalf("want a failure naming extra, got %v", err)
	}
}

func TestUT_446_IT_221_SHA256SUMSListsTheSeven(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, ln := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		sum, file, ok := strings.Cut(ln, "  ")
		if !ok {
			t.Fatalf("bad SHA256SUMS line %q", ln)
		}
		listed = append(listed, file)
		got := sha256.Sum256(readGolden(t, strings.TrimSuffix(file, ".json")))
		if hex.EncodeToString(got[:]) != sum {
			t.Errorf("%s: sum does not match", file)
		}
	}
	var want []string
	for _, n := range goldenNames {
		want = append(want, n+".json")
	}
	sort.Strings(listed)
	sort.Strings(want)
	if strings.Join(listed, ",") != strings.Join(want, ",") {
		t.Fatalf("SHA256SUMS lists %v, want %v", listed, want)
	}
}

// TestFixturesMatchGoldens holds the Go fixtures and the goldens together.
func TestFixturesMatchGoldens(t *testing.T) {
	cp := ContainerPlan{Version: 1, Container: "go", MaxParallel: 2, Preflight: []string{}, Atoms: []PlanEntry{PEVET()}}
	for name, v := range map[string]any{"run-plan": RP1(), "tree": TF1()} {
		got, err := Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if want := readGolden(t, name); !bytes.Equal(got, want) {
			t.Errorf("%s: fixture differs from golden at %q", name, firstDiff(want, got))
		}
	}
	// plan.json also holds a dev entry; PE-VET must be its last atom verbatim.
	var plan ContainerPlan
	if err := json.Unmarshal(readGolden(t, "plan"), &plan); err != nil {
		t.Fatal(err)
	}
	a, _ := Marshal(plan.Atoms[len(plan.Atoms)-1])
	b, _ := Marshal(cp.Atoms[0])
	if !bytes.Equal(a, b) {
		t.Errorf("PE-VET differs from plan.json's go:vet entry at %q", firstDiff(a, b))
	}
}

// TestIT_221_Sums: every testdata/*.json that SHA256SUMS lists matches; the
// byte-equal comparison against foundry-tools' copy is the other repo's run.
func TestIT_121_GoldensArePresent(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("testdata", "*.json"))
	have := map[string]bool{}
	for _, f := range files {
		have[filepath.Base(f)] = true
	}
	for _, n := range goldenNames {
		if !have[n+".json"] {
			t.Errorf("golden %s.json missing", n)
		}
	}
}

// goldenVerdict is daedalus wire.Verdict, the shape atom-verdict.json is
// written in. Its findings are checks.Finding, which has no JSON tags, so they
// carry the Go field names (DEF-B3-007; FN-081 is authoritative); its gradings
// are passed through as raw JSON.
type goldenVerdict struct {
	Atom          string            `json:"atom"`
	Stage         string            `json:"stage"`
	Lane          string            `json:"lane"`
	State         int               `json:"state"`
	Result        string            `json:"result"`
	Reason        string            `json:"reason,omitempty"`
	Logs          []string          `json:"logs"`
	Truncated     bool              `json:"truncated,omitempty"`
	OriginalBytes int               `json:"original_bytes,omitempty"`
	Findings      []checks.Finding  `json:"findings,omitempty"`
	StartedAt     string            `json:"started_at,omitempty"`
	FinishedAt    string            `json:"finished_at,omitempty"`
	Gradings      []json.RawMessage `json:"gradings,omitempty"`
	Audit         string            `json:"audit,omitempty"`
	CPUMs         *int64            `json:"cpu_ms,omitempty"`
	MemPeakBytes  *int64            `json:"mem_peak_bytes,omitempty"`
}

// DEF-B3-007: the golden's findings are the bytes checks.Verdict writes, so a
// record from the Dagger gates and one from the pod's executor agree.
func TestDEF_B3_007_GoldenFindingsAreChecksFindings(t *testing.T) {
	v := checks.Verdict{Findings: []checks.Finding{{Verdict: "violated", Subject: "main.go:3", Cause: "go:vet", Detail: "a<b && c", Probe: "go:vet"}}}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	want := `"findings":[{"Verdict":"violated","Subject":"main.go:3","Cause":"go:vet","Detail":"a<b && c","Probe":"go:vet"}]`
	if !strings.Contains(buf.String(), want) || !strings.Contains(string(readGolden(t, "atom-verdict")), want) {
		t.Errorf("checks.Verdict wrote %s; golden %s", buf.String(), readGolden(t, "atom-verdict"))
	}
}

// surfaceCases is testdata/surface-cases.json, the table both repos run
// against their surface matcher; atomkit only round-trips it.
type surfaceCases struct {
	Cases []struct {
		Surface string   `json:"surface"`
		Files   []string `json:"files"`
		Want    bool     `json:"want"`
	} `json:"cases"`
}
