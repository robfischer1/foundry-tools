package checks

import (
	"os"
	"strings"
	"testing"
)

const (
	tsServed = `{"schema":1,"star":"a","verbs":["x","y"]}`
	tsBody   = `{"type":"object","properties":{"q":{"type":"string"}},"additionalProperties":false}`
	// The committed file wraps the served body with the dies' root keys.
	tsFile = `{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://foundry.notusmi.com/schema/tool-input/pa_x/v1","title":"pa_x","x-ops":{"":"read"},` +
		`"type":"object","properties":{"q":{"type":"string"}},"additionalProperties":false}`
)

// toolTree is a clean die tree: star a (prefix pa) serves x and y; y owes its
// input, and one edge verb has files and no star.
func toolTree(over map[string]string) map[string]string {
	out := map[string]string{
		"fleet/repos/a.json":                `{"verb_prefix":"pa"}`,
		CapabilitiesServedDir + "/a.json":   tsServed,
		ServedInputsDir + "/a.json":         `{"schema":1,"star":"a","verbs":{"x":` + tsBody + `}}`,
		ToolInputDir + "/pa_x.schema.json":  tsFile,
		ToolOutputDir + "/pa_x.schema.json": `{}`,
		ToolOutputDir + "/pa_y.schema.json": `{}`,
		ToolInputDir + "/edge.schema.json":  `{}`,
		ToolOutputDir + "/edge.schema.json": `{}`,
		ToolSchemasLockPath:                 `{"format":"tool-schemas-lock/1","pending_keywords":{},"owed_input":{"pa_y":"no tools/list"},"owed_output":{},"owed_served":{"edge":"edge verb"}}`,
	}
	for k, v := range over {
		if v == "<del>" {
			delete(out, k)
			continue
		}
		out[k] = v
	}
	return out
}

func TestGradeToolSchemasServed(t *testing.T) {
	a := AtomDef{ID: ToolSchemasServedID, Stage: StagePrecommit, Lane: LaneAny}
	for _, tc := range []struct {
		name    string
		files   map[string]string
		state   int
		needles []string
	}{
		{"clean", toolTree(nil), 0, []string{"1 star(s)"}},
		{"nothing landed is absent", map[string]string{"fleet/repos/a.json": `{"verb_prefix":"pa"}`}, 0, []string{"ABSENT"}},
		{"star with no prefix and no verbs is skipped", toolTree(map[string]string{
			"fleet/repos/b.json": `{}`, CapabilitiesServedDir + "/b.json": `{"schema":1,"star":"b","verbs":[]}`,
			ServedInputsDir + "/b.json": `{"schema":1,"star":"b","verbs":{}}`,
		}), 0, nil},
		// 1
		{"1: served verb has no served-inputs entry", toolTree(map[string]string{
			CapabilitiesServedDir + "/a.json": `{"schema":1,"star":"a","verbs":["x","y","z"]}`,
		}), 1, []string{"a: pa_z is served but capabilities/served-inputs/a.json has no entry"}},
		{"1: served-inputs names a verb nobody serves", toolTree(map[string]string{
			ServedInputsDir + "/a.json": `{"schema":1,"star":"a","verbs":{"x":` + tsBody + `,"gone":{}}}`,
		}), 1, []string{"a: capabilities/served-inputs/a.json holds gone, which capabilities/served/a.json does not serve"}},
		{"1: served-inputs file missing for a star", toolTree(map[string]string{ServedInputsDir + "/a.json": "<del>"}), 1, []string{"a: capabilities/served-inputs/a.json is missing"}},
		{"1: served-inputs wrong schema", toolTree(map[string]string{ServedInputsDir + "/a.json": `{"schema":2,"star":"a","verbs":{}}`}), 1, []string{"capabilities/served-inputs/a.json: schema 2"}},
		{"1: served-inputs names another star", toolTree(map[string]string{ServedInputsDir + "/a.json": `{"schema":1,"star":"b","verbs":{}}`}), 1, []string{`names star "b" but is committed as "a"`}},
		{"1: served-inputs unknown field", toolTree(map[string]string{ServedInputsDir + "/a.json": `{"schema":1,"star":"a","verbs":{},"extra":1}`}), 1, []string{"capabilities/served-inputs/a.json:"}},
		// 2
		{"2: tool-input file missing", toolTree(map[string]string{ToolInputDir + "/pa_x.schema.json": "<del>"}), 1, []string{"a: pa_x has no schema/tool-input/pa_x.schema.json"}},
		// 3
		{"3: body mismatch", toolTree(map[string]string{
			ToolInputDir + "/pa_x.schema.json": strings.Replace(tsFile, `"string"`, `"integer"`, 1),
		}), 1, []string{"a: pa_x schema/tool-input/pa_x.schema.json differs from the served schema"}},
		// 4
		{"4: tool-output file missing", toolTree(map[string]string{ToolOutputDir + "/pa_x.schema.json": "<del>"}), 1, []string{"a: pa_x has no schema/tool-output/pa_x.schema.json"}},
		// 5
		{"5: orphan input", toolTree(map[string]string{ToolInputDir + "/pa_nope.schema.json": `{}`}), 1, []string{"schema/tool-input/pa_nope.schema.json: no star serves pa_nope"}},
		{"5: orphan output", toolTree(map[string]string{ToolOutputDir + "/pb_q.schema.json": `{}`}), 1, []string{"schema/tool-output/pb_q.schema.json: no star serves pb_q"}},
		// 6
		{"6: owed_input verb now has its file", toolTree(map[string]string{
			ToolInputDir + "/pa_y.schema.json": tsFile,
			ServedInputsDir + "/a.json":        `{"schema":1,"star":"a","verbs":{"x":` + tsBody + `,"y":` + tsBody + `}}`,
		}), 1, []string{"owed_input: pa_y now has schema/tool-input/pa_y.schema.json"}},
		{"6: owed_output verb now has its file", toolTree(map[string]string{
			ToolSchemasLockPath: `{"format":"tool-schemas-lock/1","owed_input":{"pa_y":"r"},"owed_output":{"pa_x":"r"},"owed_served":{"edge":"e"}}`,
		}), 1, []string{"owed_output: pa_x now has schema/tool-output/pa_x.schema.json"}},
		{"6: owed_output verb is not served", toolTree(map[string]string{
			ToolSchemasLockPath: `{"format":"tool-schemas-lock/1","owed_input":{"pa_y":"r"},"owed_output":{"pz_q":"r"},"owed_served":{"edge":"e"}}`,
		}), 1, []string{"owed_output: pz_q is not served"}},
		{"6: owed_input verb is not served", toolTree(map[string]string{
			ToolSchemasLockPath: `{"format":"tool-schemas-lock/1","owed_input":{"pa_y":"r","pz_q":"r"},"owed_served":{"edge":"e"}}`,
		}), 1, []string{"owed_input: pz_q is not served"}},
		{"6: owed_served verb now has an owner", toolTree(map[string]string{
			ToolSchemasLockPath: `{"format":"tool-schemas-lock/1","owed_input":{"pa_y":"r"},"owed_served":{"edge":"e","pa_x":"e"}}`,
		}), 1, []string{"owed_served: pa_x is now served by a"}},
		// lock shape
		{"lock wrong format", toolTree(map[string]string{ToolSchemasLockPath: `{"format":"x/9"}`}), 1, []string{`tools/tool_schemas.lock.json: format "x/9"`}},
		{"lock malformed", toolTree(map[string]string{ToolSchemasLockPath: `{`}), 1, []string{"tools/tool_schemas.lock.json:"}},
		{"served verbs without a prefix", toolTree(map[string]string{"fleet/repos/a.json": `{}`}), 1, []string{"a: serves 2 verb(s) but its record has no verb_prefix"}},
		// canonicalisation: key order and whitespace differ, still equal
		{"canonicalisation: order and whitespace are not drift", toolTree(map[string]string{
			ToolInputDir + "/pa_x.schema.json": "{\n \"x-ops\": {\"\": \"read\"},\n \"additionalProperties\": false,\n \"type\": \"object\",\n \"title\": \"t\",\n \"properties\": {\"q\": {\"type\": \"string\"}}\n}\n",
		}), 0, nil},
		{"canonicalisation: a served root $schema is dropped like the file's", toolTree(map[string]string{
			ServedInputsDir + "/a.json": `{"schema":1,"star":"a","verbs":{"x":{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","properties":{"q":{"type":"string"}},"additionalProperties":false}}}`,
		}), 0, nil},
		{"3: a served nested difference still counts", toolTree(map[string]string{
			ServedInputsDir + "/a.json": `{"schema":1,"star":"a","verbs":{"x":{"$schema":"s","type":"object","properties":{"q":{"type":"integer"}},"additionalProperties":false}}}`,
		}), 1, []string{"differs from the served schema"}},
		{"canonicalisation: html characters compare unescaped", toolTree(map[string]string{
			ServedInputsDir + "/a.json":        `{"schema":1,"star":"a","verbs":{"x":{"description":"a<b>&c"}}}`,
			ToolInputDir + "/pa_x.schema.json": `{"$id":"i","description":"a\u003cb\u003e\u0026c"}`,
		}), 0, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := GradeToolSchemasServed(a, fakeTree(tc.files))
			said := v.Reason + "\n" + strings.Join(v.Logs, "\n")
			if v.State != tc.state {
				t.Errorf("state %d, want %d:\n%s", v.State, tc.state, said)
			}
			for _, n := range tc.needles {
				if !strings.Contains(said, n) {
					t.Errorf("lacks %q:\n%s", n, said)
				}
			}
		})
	}
	// A file the glob lists but the read refuses is a 2, never a pass.
	full := toolTree(nil)
	for _, refused := range []string{ServedInputsDir + "/a.json", ToolInputDir + "/pa_x.schema.json", ToolSchemasLockPath, CapabilitiesServedDir + "/a.json", "fleet/repos/a.json"} {
		tr := fakeTree(full)
		tr.Read = func(p string) (string, error) {
			if p == refused {
				return "", os.ErrPermission
			}
			return full[p], nil
		}
		if v := GradeToolSchemasServed(a, tr); v.State != 2 || !strings.Contains(v.Reason, "could not be read") {
			t.Errorf("unreadable %s graded %d: %s", refused, v.State, v.Reason)
		}
	}
}
