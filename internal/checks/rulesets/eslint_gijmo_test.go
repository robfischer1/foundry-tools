package rulesets

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// THE FLEET ESLINT CONFIG LOADS THE GIJMO PLUGIN ONLY WHERE @gijmo/ui IS
// INSTALLED. The real plugin cannot be installed in a unit test, so the config
// is evaluated by node against stub packages: enough to read which config
// objects it produced. (Rob's 2026-10-14 ruling on foundry-tools#18210; the
// door-level proof is the red/green ts:bun-gate runs recorded on the issue.)

const evalScript = `
const cfg = (await import(process.argv[1])).default;
const names = new Set();
for (const c of cfg) for (const k of Object.keys(c.rules ?? {})) names.add(k);
console.log(JSON.stringify({
  n: cfg.length,
  rules: [...names].filter((k) => k.startsWith("gijmo/")),
  restricted: names.has("no-restricted-imports"),
  testFiles: cfg.filter((c) => (c.files ?? []).includes("**/__tests__/**/*.{ts,tsx}")).length,
}));
`

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func evalConfig(t *testing.T, withGijmo bool) (out struct {
	N          int      `json:"n"`
	Rules      []string `json:"rules"`
	Restricted bool     `json:"restricted"`
	TestFiles  int      `json:"testFiles"`
}) {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, "package.json"), `{"type":"module"}`)
	write(t, filepath.Join(root, "eslint.config.mjs"), ESLint)
	nm := filepath.Join(root, "node_modules")
	write(t, filepath.Join(nm, "@eslint/js/package.json"), `{"name":"@eslint/js","type":"module","exports":"./i.js"}`)
	write(t, filepath.Join(nm, "@eslint/js/i.js"), `export default { configs: { recommended: { rules: {} } } };`)
	write(t, filepath.Join(nm, "typescript-eslint/package.json"), `{"name":"typescript-eslint","type":"module","exports":"./i.js"}`)
	write(t, filepath.Join(nm, "typescript-eslint/i.js"),
		`export default { config: (...a) => a.flat(), configs: { strictTypeChecked: [{}], stylisticTypeChecked: [{}] } };`)
	if withGijmo {
		write(t, filepath.Join(nm, "@gijmo/ui/package.json"), `{"name":"@gijmo/ui","type":"module","exports":{"./eslint":"./e.js"}}`)
		write(t, filepath.Join(nm, "@gijmo/ui/e.js"),
			`const rules = { "spacing-token": {}, "font-size-token": {}, "serif-face-scope": {}, "status-not-color-alone": {}, "other": {} };
export default { rules, configs: { recommended: [{ plugins: { gijmo: { rules } }, rules: Object.fromEntries(Object.keys(rules).map((r) => ["gijmo/" + r, "error"])) }] } };`)
	}
	b, err := exec.Command("node", "--input-type=module", "-e", evalScript, filepath.Join(root, "eslint.config.mjs")).CombinedOutput()
	if err != nil {
		t.Fatalf("node could not load the fleet config: %v\n%s", err, b)
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unreadable eval output %q: %v", b, err)
	}
	return out
}

func TestFleetESLintLoadsGijmoOnlyWhereInstalled(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		if os.Getenv("NODE_REQUIRED") != "" {
			t.Fatalf("NODE_REQUIRED is set and there is no node on PATH: %v", err)
		}
		t.Skip("no node on PATH (set NODE_REQUIRED=1 to make this a failure)")
	}
	with := evalConfig(t, true)
	if len(with.Rules) != 5 {
		t.Errorf("a @gijmo/ui consumer must load the plugin's rules, got %v", with.Rules)
	}
	if with.TestFiles != 1 {
		t.Errorf("the __tests__ exemption for the value-literal rules is missing: %+v", with)
	}

	without := evalConfig(t, false)
	if len(without.Rules) != 0 || without.TestFiles != 0 {
		t.Errorf("a repo without @gijmo/ui must not see gijmo config: %+v", without)
	}
	for name, c := range map[string]bool{"with": with.Restricted, "without": without.Restricted} {
		if !c {
			t.Errorf("%s @gijmo/ui: the no-import-from-__tests__ rule is fleet-wide and missing", name)
		}
	}
}
