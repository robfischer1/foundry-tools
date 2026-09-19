package checks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The form, stated as goldens: sorted keys at every depth, two-space indent,
// ints kept as ints, non-ASCII escaped as python's ensure_ascii does, empty
// containers on one line, and a trailing LF. Each golden is what
// json.dumps(doc, sort_keys=True, indent=2) + "\n" prints for the same doc.
func TestCanonicalJSONIsPythonsSortedIndentedEnsureASCIIForm(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"keys sort at every depth": {
			`{"tools":{"cast":{"binaries":["b","a"]},"build":{"binaries":["x"]}},"apiVersion":3}`,
			"{\n  \"apiVersion\": 3,\n  \"tools\": {\n    \"build\": {\n      \"binaries\": [\n        \"x\"\n      ]\n    },\n    \"cast\": {\n      \"binaries\": [\n        \"b\",\n        \"a\"\n      ]\n    }\n  }\n}\n",
		},
		"an int stays an int and a float keeps its literal": {
			`{"port": 8201, "ratio": 1.50, "big": 12345678901234567890}`,
			"{\n  \"big\": 12345678901234567890,\n  \"port\": 8201,\n  \"ratio\": 1.50\n}\n",
		},
		"non-ascii is escaped, ascii punctuation is not": {
			`{"charter": "the door — every ref's home", "emoji": "🚀", "tab": "a\tb"}`,
			"{\n  \"charter\": \"the door \\u2014 every ref's home\",\n  \"emoji\": \"\\ud83d\\ude80\",\n  \"tab\": \"a\\tb\"\n}\n",
		},
		"an object inside an array nests one level deeper than the array": {
			`{"orbits":[{"name":"z","every":"5m"},["x",{"y":[]}]]}`,
			"{\n  \"orbits\": [\n    {\n      \"every\": \"5m\",\n      \"name\": \"z\"\n    },\n    [\n      \"x\",\n      {\n        \"y\": []\n      }\n    ]\n  ]\n}\n",
		},
		"empty containers and null": {
			`{"a": [], "b": {}, "c": null, "d": false}`,
			"{\n  \"a\": [],\n  \"b\": {},\n  \"c\": null,\n  \"d\": false\n}\n",
		},
	} {
		got, err := CanonicalJSON([]byte(tc.in))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(got) != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", name, got, tc.want)
		}
		// The form is a fixed point: canonicalizing the canonical form
		// changes nothing, which is what the dies atom relies on.
		again, err := CanonicalJSON(got)
		if err != nil || string(again) != string(got) {
			t.Errorf("%s: not a fixed point (%v)", name, err)
		}
	}
}

func TestCanonicalJSONRefusesWhatIsNotOneDocument(t *testing.T) {
	for name, in := range map[string]string{
		"not json":      `{"a": `,
		"trailing data": `{"a": 1} {"b": 2}`,
		"empty":         ``,
	} {
		if _, err := CanonicalJSON([]byte(in)); err == nil {
			t.Errorf("%s: canonicalized", name)
		}
	}
}

// Against the fleet itself, when it is mounted the way the lane mounts it:
// every committed record is already its own canonical form, so the emitter
// this atom carries agrees with the one hephaestus grades with. Skipped
// where no /dies is mounted (a laptop); the lane always has one.
func TestCanonicalJSONRoundTripsEveryMountedRecord(t *testing.T) {
	dies := os.Getenv("FOUNDRY_DIES")
	if dies == "" {
		t.Skip("FOUNDRY_DIES not set — the fleet's records are not mounted here")
	}
	records, err := filepath.Glob(filepath.Join(dies, "fleet", "stars", "*", "slag.json"))
	if err != nil || len(records) == 0 {
		t.Skipf("no records under %s (%v)", dies, err)
	}
	for _, p := range records {
		committed, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		got, err := CanonicalJSON(committed)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if string(got) != string(committed) {
			t.Errorf("%s: diverges from the committed form", strings.TrimPrefix(p, dies))
		}
	}
}
