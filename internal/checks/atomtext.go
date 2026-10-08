package checks

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"go.yaml.in/yaml/v3"
)

// THE WORDS AND THE PARSES THAT TWO READERS OF ONE TREE SHARE. Four fleet atoms
// run twice: as a Dagger chain in package main, reading the tree through the
// Directory API, and as an in-process atom in internal/atoms, reading it from
// disk. What differs between them is only HOW A FILE IS READ; the sentence a
// finding is written in, and the parse that turns a ConfigMap into the keys it
// sets, are the same, and a shadow comparison of the two is only meaningful if
// they cannot drift. They live here, once. (The rule logic proper already did:
// every other judgement is a function in this package.)

const (
	// CopierAnswersFile is copier's record of which template a repo was
	// stamped from and at what version. Renovate's copier manager edits it, so
	// it is also where that manager leaves evidence when it fails.
	CopierAnswersFile = ".copier-answers.yml"
	// CopierSentinel is the line renovate's copier manager appends to the
	// answers file. It is a CRASH RECEIPT, not a record that anything updated:
	// it survives exactly when copier crashed (renovatebot/renovate#36147),
	// and once committed it latches.
	CopierSentinel = "#copier updated"
)

// CopierMarkerReport is the finding for an answers file that carries the crash
// receipt.
func CopierMarkerReport() string {
	return fmt.Sprintf(
		"%s carries renovate's %q marker.\n\n"+
			"That line is not a record of a template update. Renovate appends it to make\n"+
			"the file look modified so `updateArtifacts` fires, and `updateArtifacts` is\n"+
			"what actually runs copier; a successful render rewrites the file and the line\n"+
			"goes with it. Its survival means COPIER CRASHED, and the render this repo's\n"+
			"last copier PR claimed to perform did not happen.\n\n"+
			"It also LATCHES: re-adding an identical line is not a change renovate can\n"+
			"detect, so the artifact step never fires again and this repo takes no further\n"+
			"template update at all until the line is removed (renovatebot/renovate#36147,\n"+
			"no upstream fix; removing it by hand is the sanctioned remedy).\n\n"+
			"FIX: delete the marker line and any blank lines above it, and make sure the\n"+
			"file ends in a newline — the marker is appended without one, which separately\n"+
			"reds end-of-file-fixer for the next PR here. `_commit` and every answer value\n"+
			"stay as they are; copier itself moves the pin on the next real render.",
		CopierAnswersFile, CopierSentinel)
}

// SastTemplates is the closed set of fleet repo templates that ship
// rules/sast/dataflow.yml. A template outside it (config-repo-template carries
// no code language, so no ruleset) stamps trees for which absence is the
// correct state. Variation is by template kind, never by repo.
var SastTemplates = map[string]bool{
	"python-repo-template":   true,
	"go-repo-template":       true,
	"rust-repo-template":     true,
	"frontend-repo-template": true,
}

// CopierTemplate names the SAST-shipping template an answers file's body was
// stamped from, or "" when it is not stamped from one of them. It reads
// `_src_path` rather than trusting the file's presence: speckit ships its own
// answers file and a tree can carry one pointing anywhere. An empty body (an
// absent or unreadable file) answers "" — this decides whether to make another
// atom STRICTER, so a failure to read must not invent strictness.
func CopierTemplate(answers string) string {
	for _, line := range strings.Split(answers, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "_src_path:")
		if !ok {
			continue
		}
		src := strings.Trim(strings.TrimSpace(rest), `"'`)
		name := strings.TrimSuffix(src[strings.LastIndex(src, "/")+1:], ".git")
		if SastTemplates[name] {
			return name
		}
		return ""
	}
	return ""
}

// SastAbsentStamped is the could-not-run for a tree with no rules/sast that one
// of the fleet's templates stamped (tpl names it): such a tree did not decline
// SAST, it never received the file, and a pass would be 0 findings from 0
// files (foundry-stocks#4415).
func SastAbsentStamped(id, tpl string) string {
	return fmt.Sprintf(
		"%s: CANNOT RUN - this tree has no rules/sast, and %s stamped it.\n\n"+
			"Every SAST-shipping fleet repo template ships rules/sast/dataflow.yml, so a stamped repo\n"+
			"without one did not decline SAST — it never received the file. `_skip_if_exists`\n"+
			"only skips a path that ALREADY exists, so a working `copier update` creates it;\n"+
			"a repo missing it has taken no successful render since the template began\n"+
			"shipping it. Check fleet:copier-answers-intact on this same tree.\n\n"+
			"NOTHING SCANNED THIS REPO. A pass here would mean 0 findings from 0 files, which\n"+
			"is not a clean scan — it is an unexamined repo, and it is why this is exit 2 and\n"+
			"not exit 0 (foundry-stocks#4415).\n\n"+
			"FIX: copy %s's template/rules/sast/dataflow.yml to rules/sast/dataflow.yml.",
		id, tpl, tpl)
}

const (
	// OureaConfigDir and OureaConfigFile are the ConfigMap that overrides
	// ourea's defaults in the cluster, and the only file
	// fleet:ourea-config-retired-keys has an opinion about. prime/ is the
	// fleet's flux tree and nothing else's.
	OureaConfigDir  = "prime"
	OureaConfigFile = "ourea-config.yaml"
	// OureaConfigPath is the two joined.
	OureaConfigPath = OureaConfigDir + "/" + OureaConfigFile
	// OureaConfigKey is the ConfigMap data key holding the door's TOML.
	OureaConfigKey = "config.toml"
	// OureaRef / OureaRetiredKeysFile locate the list the atom grades against:
	// generated from ourea's own `retiredKeys` map, at a MOVING ref (a key
	// retired after a pinned sha would be graded by nothing).
	OureaRef             = "main"
	OureaRetiredKeysFile = "internal/config/retired-keys.json"
	// OureaDoorRepo is ourea as the door names it: owner-qualified, read at
	// the door's default ref, which is OureaRef.
	OureaDoorRepo = "rob/ourea"
)

// OureaConfigKeys pulls the door's TOML out of the ConfigMap and decodes it to
// the keys it sets. It DECODES RATHER THAN GREPS: measured on flux's live
// ConfigMap, 7 of the 11 retired names appear in its text anyway, every one
// inside a comment explaining why that key went. Only the top level is
// compared. The errors are `%v`, not `%w`: nothing unwraps them, the callers
// format them into a verdict.
func OureaConfigKeys(configMap string) (map[string]any, error) {
	var cm struct {
		Data map[string]string `yaml:"data"`
	}
	if err := yaml.Unmarshal([]byte(configMap), &cm); err != nil {
		return nil, fmt.Errorf("the ConfigMap is not YAML: %v", err)
	}
	body, ok := cm.Data[OureaConfigKey]
	if !ok {
		return nil, fmt.Errorf("it carries no data[%q], so it sets no door config", OureaConfigKey)
	}
	var cfg map[string]any
	if _, err := toml.Decode(body, &cfg); err != nil {
		return nil, fmt.Errorf("data[%q] is not TOML: %v", OureaConfigKey, err)
	}
	return cfg, nil
}

// OureaRetiredList decodes the door's published list of keys it stopped
// reading. Every caller must refuse on an error: a check that cannot learn its
// own criteria has not checked anything, and an empty list grades a ConfigMap
// against no keys.
func OureaRetiredList(body string) (map[string]string, error) {
	var keys map[string]string
	if err := json.Unmarshal([]byte(body), &keys); err != nil {
		return nil, fmt.Errorf("it did not decode as an object of key -> reason: %v", err)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("it decoded empty, and grading a ConfigMap against no keys is not a check")
	}
	return keys, nil
}

// OureaRetiredNamed is the retired keys a decoded ConfigMap sets, sorted.
func OureaRetiredNamed(cfg map[string]any, retired map[string]string) []string {
	var named []string
	for k := range cfg {
		if _, ok := retired[k]; ok {
			named = append(named, k)
		}
	}
	sort.Strings(named)
	return named
}

// OureaRetiredReport is the finding for a ConfigMap that sets the named keys.
func OureaRetiredReport(named []string, retired map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s carries %d key(s) the door no longer reads.\n\n", OureaConfigPath, len(named))
	for _, k := range named {
		fmt.Fprintf(&b, "  %s\n    %s\n\n", k, retired[k])
	}
	b.WriteString(
		"Each line is INERT, not dangerous — the code that read it is gone, so it cannot\n" +
			"restore the old behaviour. The door boots on it and says so in a WARN. This is\n" +
			"the same fact read a second time, at the moment the fix is one deleted line.\n\n" +
			"FIX: delete the named key(s) from the `" + OureaConfigKey + "` block. Every other key,\n" +
			"and every comment around them, stays.\n\n" +
			"The list comes from ourea's own " + OureaRetiredKeysFile + " at " + OureaRef + ", which is\n" +
			"generated from its `retiredKeys` map and pinned to it by a test there. If a key\n" +
			"here looks live, that map is what disagrees with you.")
	return b.String()
}
