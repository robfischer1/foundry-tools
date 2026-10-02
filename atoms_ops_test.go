package main

import (
	"fmt"
	"strings"
	"testing"

	"dagger/foundry-tools/internal/checks"
)

var opsIDs = []string{"ops:shell", "ops:chezmoi", "ops:yaml", "ops:dup", "ops:declaration", "ops:specs", "ops:metrics", "ops:ansible", "ops:flux"}

// A star is not an ops tree: every ops atom stands down ABSENT without a
// container, whatever scripts and YAML the star happens to carry. The lane
// never gated stars, and turning it on across the fleet at once is not what
// porting it means.
func TestOpsAtomsStandDownOffTheOpsShape(t *testing.T) {
	engine.reset()
	engine.withTree(map[string]string{
		"go.mod": "module x\n", "main.go": "package main\n",
		"ci/run.sh": "#!/bin/sh\necho hi\n", "deploy/x.yaml": "a: 1\n",
	})
	for _, id := range opsIDs {
		wantState(t, runAtom(t, id, ""), 0, "ABSENT", "no ops shape")
	}
	if engine.chain(opsLsNeedle) != "" {
		t.Errorf("no ops shape means no container:\n%s", engine.chain(opsLsNeedle))
	}
}

// opsLsNeedle is the tracked-file listing every ops atom reads first.
const opsLsNeedle = `"git","ls-files","-s","-z"`

// opsTree is an ops tree: the files git lists, and their contents as the
// container reads them under /src.
func opsTree(files map[string]string, modes map[string]string) {
	engine.reset()
	tree := map[string]string{"flux/clusters/home/apps.yaml": files["flux/clusters/home/apps.yaml"]}
	var ls strings.Builder
	for p, content := range files {
		tree[p] = content
		tree["/src/"+p] = content
		mode := "100644"
		if m, ok := modes[p]; ok {
			mode = m
		}
		fmt.Fprintf(&ls, "%s %s 0\t%s\x00", mode, strings.Repeat("a", 40), p)
	}
	engine.withTree(tree)
	engine.stdout(opsLsNeedle, ls.String())
}

// The ops tree is the gitignore-filtered tracked tree with a fresh index, no
// foundry-stocks and no base; every atom lists what is tracked first.
func TestOpsAtomsRunOnTheTrackedTree(t *testing.T) {
	opsTree(map[string]string{"flux/x.yaml": "a: 1\n", "ci/run.sh": "#!/bin/sh\necho\n"}, nil)
	engine.stdout(`"git","grep","-I","-n","-z"`, "ci/run.sh\x001\x00#!/bin/sh\n")
	wantState(t, runAtom(t, "ops:shell", ""), 0)

	c := engine.chain(`"-S","error"`, "exitCode")
	if !strings.Contains(c, checks.ImageFleet) {
		t.Errorf("ops:shell must run in the fleet lane image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withMountedDirectory", `path:"/src"`},
		[]string{"withExec", `args:["git","init","-q","."]`},
		[]string{"withExec", `args:["git","config","--local","ca.snapshot","ops-tracked"]`},
		[]string{"withExec", `args:["git","add","-A"]`},
		[]string{"withEnvVariable", `name:"UV_INDEX_URL"`, `value:"` + checks.OpsUVIndex + `"`},
		[]string{"withExec", `args:["uvx","--from","shellcheck-py","shellcheck","--version"]`},
		[]string{"withExec", "expect:ANY", `args:["uvx","--from","shellcheck-py","shellcheck","-S","error","-f","gcc","ci/run.sh"]`},
	)
	if engine.chain(`filter(`, `gitignore:true`, `exclude:[".git"]`, "id") == "" {
		t.Errorf("ops:shell must mount the gitignore-filtered tree:\n%s", strings.Join(engine.chains(), "\n"))
	}
	if hasCall(c, "withExec", `"shellcheck","--version"`, "expect:ANY") {
		t.Errorf("the --version probe is provisioning and must run under the default Expect:\n%s", c)
	}
	for _, relic := range []string{`path:"/stocks"`, "ops.sh", "OPS_", "GATE_BASE", `"bash"`} {
		if strings.Contains(c, relic) {
			t.Errorf("the ported lane still carries %s:\n%s", relic, c)
		}
	}
}

// ops:shell — the gate at severity error over scripts with a shebang, the
// sourced fragments read as bash, the warning count reported; a fault in the
// output is could-not-run, anything else nonzero findings.
func TestOpsShell(t *testing.T) {
	files := map[string]string{"flux/x.yaml": "", "a.sh": "#!/bin/bash\n", "lib/frag.sh": "x=1\n", "bin/tool": "#!/usr/bin/env bash\n", "z.zsh": "#!/bin/zsh\n"}
	shebangs := "a.sh\x001\x00#!/bin/bash\nbin/tool\x001\x00#!/usr/bin/env bash\nz.zsh\x001\x00#!/bin/zsh\n"
	opsTree(files, nil)
	engine.stdout(`"git","grep","-I","-n","-z"`, shebangs)
	engine.stdout(`"-S","warning"`, "a.sh:1:1: warning: x\na.sh:2:1: note: y\n")
	wantState(t, runAtom(t, "ops:shell", ""), 0)
	var gated, fragments bool
	for _, q := range engine.chains() {
		if hasCall(q, "withExec", `"-S","error","-f","gcc"`) && strings.Contains(q, `"a.sh"`) && strings.Contains(q, `"bin/tool"`) {
			gated = true
		}
		if hasCall(q, "withExec", `"-S","error","-s","bash","-f","gcc","lib/frag.sh"]`) {
			fragments = true
		}
		if strings.Contains(q, `"z.zsh"]`) {
			t.Errorf("a zsh file reached shellcheck:\n%s", q)
		}
	}
	if !gated || !fragments {
		t.Errorf("shebang scripts gated %v, fragments as bash %v", gated, fragments)
	}

	opsTree(files, nil)
	engine.stdout(`"git","grep","-I","-n","-z"`, shebangs)
	engine.exitCode(`"-S","error","-f","gcc"`, 1)
	engine.stdout(`"-S","error","-f","gcc"`, "a.sh:3:1: error: Couldn't parse this if expression. [SC1046]\n")
	engine.stdout(`"-S","warning","-f","gcc"`, "a.sh:1:1: warning: x\na.sh:2:1: note: y\n")
	wantState(t, runAtom(t, "ops:shell", ""), 1, "shell: 3 script(s), gating at severity error", "SC1046", "shell failed (rc=1) — findings",
		"shellcheck -S warning: 2 finding(s) — reported, not gating")

	opsTree(files, nil)
	engine.stdout(`"git","grep","-I","-n","-z"`, shebangs)
	engine.exitCode(`"-S","error"`, 1)
	engine.stderr(`"-S","error"`, "error: failed to fetch shellcheck-py: dial tcp 10.0.0.1:443: connection refused")
	wantState(t, runAtom(t, "ops:shell", ""), 2, "fault of the substrate (rc=1)")

	opsTree(map[string]string{"flux/x.yaml": "", "README.md": "#!/bin/sh is how it starts\n", "t.tmpl": "#!/bin/sh\n"}, nil)
	engine.stdout(`"git","grep","-I","-n","-z"`, "t.tmpl\x001\x00#!/bin/sh\n")
	wantState(t, runAtom(t, "ops:shell", ""), 0, "ABSENT - no shell script in this tree")

	opsTree(files, nil)
	engine.exitCode(`"git","grep","-I","-n","-z"`, 2)
	wantState(t, runAtom(t, "ops:shell", ""), 2, "could not read the scripts' first lines")

	// git grep answers 1 when no file starts with #!: the fragments still run.
	opsTree(map[string]string{"flux/x.yaml": "", "lib/frag.sh": "x=1\n"}, nil)
	engine.exitCode(`"git","grep","-I","-n","-z"`, 1)
	wantState(t, runAtom(t, "ops:shell", ""), 0)
	if engine.chain(`"-s","bash","-f","gcc","lib/frag.sh"]`) == "" {
		t.Error("a grep that matched nothing must still check the fragments")
	}
	// One script with a shebang and one fragment is two scripts, not none.
	opsTree(map[string]string{"flux/x.yaml": "", "a.sh": "#!/bin/sh\n", "lib/frag.sh": "x=1\n"}, nil)
	engine.stdout(`"git","grep","-I","-n","-z"`, "a.sh\x001\x00#!/bin/sh\n")
	engine.exitCode(`"-S","error","-f","gcc"`, 1)
	wantState(t, runAtom(t, "ops:shell", ""), 1, "shell: 2 script(s)")
}

func TestOpsChezmoi(t *testing.T) {
	files := map[string]string{".chezmoiroot": "home\n", "dot_bashrc.tmpl": "{{ .chezmoi.os }}\n", "dot_vimrc.tmpl": "{{ include \"x\" }}\n"}
	opsTree(files, nil)
	wantState(t, runAtom(t, "ops:chezmoi", ""), 0)
	c := engine.chain(`"execute-template"`, `{{ .chezmoi.os }}`, "exitCode")
	wantCalls(t, c,
		[]string{"withFile", `path:"/usr/local/bin/chezmoi"`, `permissions:493`},
		[]string{"withExec", `args:["chezmoi","--version"]`},
		[]string{"withExec", "expect:ANY", `args:["chezmoi","--source",".","execute-template"]`, `stdin:"{{ .chezmoi.os }}\n"`},
	)
	if engine.chain(`http(url:"`+checks.ChezmoiURL+`")`, "sync") == "" {
		t.Errorf("ops:chezmoi must fetch chezmoi from its release URL: %s", checks.ChezmoiURL)
	}

	opsTree(files, nil)
	engine.exitCode(`{{ include`, 1)
	engine.stderr(`{{ include`, "chezmoi: template: stdin:1: error calling include: open x\nsecond line")
	wantState(t, runAtom(t, "ops:chezmoi", ""), 1, "FAIL dot_vimrc.tmpl\n    chezmoi: template: stdin:1: error calling include: open x\n    second line", "chezmoi: 2 template(s) checked")

	// A tree with templates but no chezmoi source marker renders nothing.
	opsTree(map[string]string{"flux/x.yaml": "", "tmpl/a.tmpl": "x"}, nil)
	wantState(t, runAtom(t, "ops:chezmoi", ""), 0, "ABSENT - no chezmoi template in this tree")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`http(url:"`+checks.ChezmoiURL+`")`, "upstream down")
	wantState(t, runAtom(t, "ops:chezmoi", ""), 2, "CANNOT RUN", "could not be provisioned")
	if engine.chain(`"execute-template"`) != "" {
		t.Error("a tool that did not arrive must not run the phase")
	}
}

func TestOpsYAML(t *testing.T) {
	opsTree(map[string]string{"flux/a.yaml": "a: 1\nb: 2\n", "compose.yml": "x: 1\nx: 2\n"}, nil)
	wantState(t, runAtom(t, "ops:yaml", ""), 1, "compose.yml: ", "already defined", "2 yaml file(s), 1 with a duplicate key or a parse error")

	opsTree(map[string]string{"flux/a.yaml": "a: 1\n---\nb: [1, 2]\n"}, nil)
	wantState(t, runAtom(t, "ops:yaml", ""), 0)

	// infra's own checker, when the tree carries it.
	opsTree(map[string]string{"flux/a.yaml": "x: 1\nx: 2\n", "tools/yaml-strict": "#!/usr/bin/env python3\n"}, nil)
	engine.exitCode(`"tools/yaml-strict"`, 1)
	engine.stdout(`"tools/yaml-strict"`, "flux/a.yaml: duplicate key 'x'")
	wantState(t, runAtom(t, "ops:yaml", ""), 1, "duplicate key 'x'")
	wantCalls(t, engine.chain(`"tools/yaml-strict"`, "exitCode"),
		[]string{"withExec", "expect:ANY", `args:["uv","run","--isolated","--no-project","--with","pyyaml","python","tools/yaml-strict","flux"]`})

	opsTree(map[string]string{"policy/a.rego": "package a\n"}, nil)
	wantState(t, runAtom(t, "ops:yaml", ""), 0, "ABSENT - no yaml in this tree")
}

// infra's own checkers run when the tree carries them and are ABSENT
// otherwise; dup-check must be tracked executable; console-specs' own 2 is
// could-not-run.
func TestOpsRepoTools(t *testing.T) {
	tools := map[string]string{"flux/a.yaml": "", "tools/dup-check": "", "tools/declaration-integrity": "", "tools/console-specs": "", "tools/metric-allowlist": ""}
	exec := map[string]string{"tools/dup-check": "100755"}
	for _, c := range []struct {
		id, needle string
		argv       string
	}{
		{"ops:dup", `"tools/dup-check"`, `args:["python3","tools/dup-check","--blocking"]`},
		{"ops:declaration", `"tools/declaration-integrity"`, `args:["uv","run","--isolated","--no-project","--with","pyyaml","python","tools/declaration-integrity"]`},
		{"ops:specs", `"tools/console-specs"`, `args:["uv","run","--isolated","--no-project","--with","pyyaml","python","tools/console-specs","--check"]`},
		{"ops:metrics", `"tools/metric-allowlist"`, `args:["uv","run","--isolated","--no-project","--with","pyyaml","python","tools/metric-allowlist"]`},
	} {
		opsTree(tools, exec)
		wantState(t, runAtom(t, c.id, ""), 0)
		wantCalls(t, engine.chain(c.needle, "exitCode"), []string{"withExec", "expect:ANY", c.argv})

		opsTree(tools, exec)
		engine.exitCode(c.needle, 1)
		engine.stdout(c.needle, "drifted: x")
		wantState(t, runAtom(t, c.id, ""), 1, "drifted: x", "failed (rc=1) — findings")

		opsTree(map[string]string{"flux/a.yaml": ""}, nil)
		wantState(t, runAtom(t, c.id, ""), 0, "ABSENT - no tools/")
	}
	opsTree(tools, nil) // dup-check not executable
	wantState(t, runAtom(t, "ops:dup", ""), 0, "ABSENT - no tools/dup-check in this tree")

	opsTree(tools, exec)
	engine.exitCode(`"tools/console-specs"`, 2)
	engine.stdout(`"tools/console-specs"`, "could not clone nas01-stacks")
	wantState(t, runAtom(t, "ops:specs", ""), 2, "could not clone nas01-stacks", "specs: could not read the source — did not look")

	opsTree(tools, exec)
	engine.exitCode(`"tools/metric-allowlist"`, 2)
	engine.stdout(`"tools/metric-allowlist"`, "could not read — no metricAllowlist")
	wantState(t, runAtom(t, "ops:metrics", ""), 2, "could not read — no metricAllowlist", "metrics: could not read a keep-list or a source — did not look")

	// The tool's own exec failing in the engine is could-not-run, not a pass.
	opsTree(tools, exec)
	engine.fail(`"tools/metric-allowlist"`, "engine gone")
	wantState(t, runAtom(t, "ops:metrics", ""), 2, "never ran", "engine gone")
}

func TestOpsAnsible(t *testing.T) {
	files := map[string]string{"ansible/playbooks/site.yml": "", "ansible/playbooks/b.yml": "", "ansible/playbooks/roles/x.yml": "",
		"ansible/inventory/hosts.yml": "", "ansible/requirements.yml": ""}
	opsTree(files, nil)
	engine.stdout(`"--profile","basic"`, "yaml[truthy]: Truthy value\nname[missing]: All tasks should be named\nnot a finding\n")
	wantState(t, runAtom(t, "ops:ansible", ""), 0)
	c := engine.chain(`"--profile","min"`, "exitCode")
	wantCalls(t, c,
		[]string{"withWorkdir", `path:"/src/ansible"`},
		[]string{"withMountedDirectory", `path:"/opt/ansible-collections"`},
		[]string{"withEnvVariable", `name:"ANSIBLE_COLLECTIONS_PATH"`, `value:"/opt/ansible-collections"`},
		[]string{"withExec", "expect:ANY", `args:["uv","run","--isolated","--no-project","--with","ansible-core","--with","ansible-lint","ansible-lint","--offline","-q","--profile","min","."]`},
	)
	// The collections install in their own step, keyed on the declaration
	// alone: the only tree it mounts is requirements.yml and ansible.cfg, and
	// it expects success, so a proxy that failed is never remembered failing.
	inst := engine.chain(`"collection","install"`, "stdout")
	wantCalls(t, inst,
		[]string{"withMountedDirectory", `path:"/src"`},
		[]string{"withWorkdir", `path:"/src/ansible"`},
		[]string{"withEnvVariable", `name:"UV_INDEX_URL"`},
		[]string{"withExec", `args:["uv","run","--isolated","--no-project","--with","ansible-core","ansible-galaxy","collection","install","-r","requirements.yml","-p","/opt/ansible-collections"]`},
	)
	if strings.Contains(inst, "expect:ANY") {
		t.Errorf("the install must expect success, or a failed resolution is cached:\n%s", inst)
	}
	if n := strings.Count(inst, "withMountedDirectory("); n != 1 {
		t.Errorf("the install mounts the declaration and nothing else, got %d mounts:\n%s", n, inst)
	}
	if engine.chain(`filter(include:["ansible/requirements.yml","ansible/ansible.cfg"])`) == "" {
		t.Error("the install's /src is not the requirements.yml + ansible.cfg filter of the tree")
	}
	if engine.chain("--clear-response-cache") != "" {
		t.Error("an install that succeeded is not retried")
	}
	for _, pb := range []string{"playbooks/b.yml", "playbooks/site.yml"} {
		if engine.chain(`"ansible-playbook","--syntax-check","-i","inventory/hosts.yml","`+pb+`"]`) == "" {
			t.Errorf("no syntax-check of %s against the inventory", pb)
		}
	}
	if engine.chain(`"playbooks/roles/x.yml"`) != "" {
		t.Error("only ansible/playbooks/*.yml are playbooks")
	}

	// The debt is counted from the report profile, and gates nothing.
	opsTree(files, nil)
	engine.exitCode(`"--profile","min"`, 2)
	engine.stdout(`"--profile","min"`, "risky-file-permissions: File permissions unset")
	engine.stdout(`"--profile","basic"`, "yaml[truthy]: a\nname[missing]: b\n")
	wantState(t, runAtom(t, "ops:ansible", ""), 1, "ansible-lint --profile min (gates)", "risky-file-permissions", "ansible-lint --profile basic: 2 finding(s) — reported, not gating")

	// A syntax error stops at that playbook.
	opsTree(files, nil)
	engine.exitCode(`"playbooks/b.yml"]`, 4)
	engine.stdout(`"playbooks/b.yml"]`, "ERROR! couldn't resolve module/action 'vyos.vyos.vyos_facts'")
	wantState(t, runAtom(t, "ops:ansible", ""), 1, "syntax-check playbooks/b.yml", "couldn't resolve module")
	if engine.chain(`"playbooks/site.yml"]`) != "" || engine.chain(`"--profile","min"`) != "" {
		t.Error("a failed syntax-check must stop the phase")
	}

	// Collections that would not install, twice: could-not-run, and nothing
	// checked. The retry clears galaxy's response cache.
	opsTree(files, nil)
	engine.fail(`"collection","install"`, "exit code: 1")
	wantState(t, runAtom(t, "ops:ansible", ""), 2, "declares collections that would not install — did not look",
		"install -r requirements.yml -p /opt/ansible-collections", "install --clear-response-cache -r requirements.yml")
	if engine.chain(`"--syntax-check"`) != "" {
		t.Error("an install that failed must stop the phase")
	}

	// One failed resolution, then an answer: the retry's collections are used.
	opsTree(files, nil)
	engine.fail(`"install","-r","requirements.yml"`, "exit code: 1")
	wantState(t, runAtom(t, "ops:ansible", ""), 0)
	if engine.chain(`"install","--clear-response-cache","-r","requirements.yml","-p","/opt/ansible-collections"`) == "" {
		t.Error("a failed install is retried once with --clear-response-cache")
	}

	// A tree declaring no collections and no inventory.
	opsTree(map[string]string{"ansible/playbooks/site.yml": ""}, nil)
	wantState(t, runAtom(t, "ops:ansible", ""), 0)
	if engine.chain(`"collection","install"`) != "" || engine.chain(`"-i","inventory/hosts.yml"`) != "" {
		t.Error("no requirements, no install; no inventory, no -i")
	}

	opsTree(map[string]string{"flux/a.yaml": ""}, nil)
	wantState(t, runAtom(t, "ops:ansible", ""), 0, "ABSENT - no ansible/playbooks in this tree")
}

func TestOpsFlux(t *testing.T) {
	crs := "apiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nspec:\n  path: ./flux/apps\n---\napiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nspec:\n  path: flux/infrastructure/\n"
	opsTree(map[string]string{"flux/clusters/home/apps.yaml": crs, "flux/apps/kustomization.yaml": ""}, nil)
	engine.script(script{match: `"kubectl","kustomize"`, leaf: "contents", value: "kind: A\n---\nkind: B"})
	wantState(t, runAtom(t, "ops:flux", ""), 0)
	c := engine.chain(`"kustomize","flux/apps","-o"`, "exitCode")
	wantCalls(t, c,
		[]string{"withFile", `path:"/usr/local/bin/kubectl"`, `permissions:493`},
		[]string{"withExec", `args:["kubectl","version","--client=true"]`},
		// kubectl writes the file itself (-o): dagger tees an exec's stdout —
		// even a RedirectStdout one, measured — into the progress log, and
		// the rendered tree must never reach it. No shell either (rule 7).
		[]string{"withExec", "expect:ANY", `args:["kubectl","kustomize","flux/apps","-o","/tmp/kustomize.0.yaml"]`},
	)
	if engine.chain(`file(path:"/tmp/kustomize.0.yaml")`, "contents") == "" {
		t.Error("the built stream must be read back from the file, not from stdout")
	}
	if engine.chain(`"/tmp/kustomize.0.yaml"]){stdout}`) != "" {
		t.Error("the build's stdout must not be read — that is what echoes the tree into the log")
	}
	if engine.chain(`"kustomize","flux/apps"`, "redirectStdout") != "" {
		t.Error("RedirectStdout is not the mechanism: dagger still tees it into the log")
	}
	if engine.chain(`"kustomize","flux/infrastructure","-o"`, "exitCode") == "" {
		t.Error("the second CR's tree was not built")
	}
	if engine.chain(`http(url:"`+checks.KubectlURL+`")`, "sync") == "" {
		t.Errorf("ops:flux must fetch kubectl from %s", checks.KubectlURL)
	}
	// The built stream — both trees, a separator after each — is validated by
	// kubeconform copied out of its pinned image.
	v := engine.chain(`"kubeconform","-strict"`, "exitCode")
	wantCalls(t, v,
		[]string{"withFile", `path:"/usr/local/bin/kubeconform"`, `permissions:493`},
		[]string{"withExec", `args:["kubeconform","-v"]`},
		// kubeconform reads the files kubectl wrote, one per tree, in place:
		// the joined stream must never travel back in as a withNewFile
		// argument (dagger renders that argument whole into the log — 3.4 MB
		// of a 4.1 MB gate log, measured).
		[]string{"withExec", "expect:ANY", `args:["kubeconform","-strict","-summary","-ignore-missing-schemas","-skip","CustomResourceDefinition","-schema-location","default","/tmp/kustomize.0.yaml","/tmp/kustomize.1.yaml"]`},
	)
	if engine.chain(`withNewFile(path:"/tmp/ops/flux.built.yaml"`) != "" {
		t.Error("the built stream was sent back through withNewFile — that argument is rendered whole into the lane's log")
	}
	if hasCall(v, "withExec", `args:["kubeconform","-v"]`, "expect:ANY") {
		t.Errorf("the kubeconform probe is provisioning and must run under the default Expect:\n%s", v)
	}
	if engine.chain(`from(address:"`+checks.ImageKubeconform+`")`) == "" {
		t.Errorf("kubeconform must come from its pinned image %s", checks.ImageKubeconform)
	}

	// A schema violation in what was built is findings; a schema that would
	// not fetch is could-not-run.
	opsTree(map[string]string{"flux/clusters/home/apps.yaml": crs}, nil)
	engine.script(script{match: `"kubectl","kustomize"`, leaf: "contents", value: "kind: A\n---\nkind: B"})
	engine.exitCode(`"kubeconform","-strict"`, 1)
	engine.stdout(`"kubeconform","-strict"`, "flux.built.yaml - Deployment web is invalid: spec.replicas: Invalid type\nSummary: 4 resources found - Valid: 3, Invalid: 1")
	wantState(t, runAtom(t, "ops:flux", ""), 1, "4 object(s) built from 2 tree(s)", "spec.replicas: Invalid type", "flux failed (rc=1) — findings")

	opsTree(map[string]string{"flux/clusters/home/apps.yaml": crs}, nil)
	engine.script(script{match: `"kubectl","kustomize"`, leaf: "contents", value: "kind: A\n---\nkind: B"})
	engine.exitCode(`"kubeconform","-strict"`, 1)
	engine.stdout(`"kubeconform","-strict"`, "could not download schema: no such host")
	wantState(t, runAtom(t, "ops:flux", ""), 2, "fault of the substrate")

	// A duplicate resource id is findings, naming the build.
	opsTree(map[string]string{"flux/clusters/home/apps.yaml": crs}, nil)
	engine.script(script{match: `"kubectl","kustomize"`, leaf: "contents", value: "kind: A\n---\nkind: B"})
	engine.exitCode(`"flux/infrastructure","-o"`, 1)
	engine.stderr(`"flux/infrastructure","-o"`, "Error: may not add resource with an already registered id: IngressRoute.v1alpha1.traefik.io/git")
	wantState(t, runAtom(t, "ops:flux", ""), 1, "kustomize build flux/infrastructure", "already registered id", "build FAILED: flux/infrastructure")

	// A base that would not fetch is could-not-run.
	opsTree(map[string]string{"flux/clusters/home/apps.yaml": crs}, nil)
	engine.script(script{match: `"kubectl","kustomize"`, leaf: "contents", value: "kind: A\n---\nkind: B"})
	engine.exitCode(`"flux/apps","-o"`, 1)
	engine.stderr(`"flux/apps","-o"`, "Error: accumulating resources: failed to fetch github.com/x: dial tcp: i/o timeout")
	wantState(t, runAtom(t, "ops:flux", ""), 2, "fault of the substrate")

	// A cluster manifest that does not parse is named, and the build goes on.
	opsTree(map[string]string{"flux/clusters/home/apps.yaml": crs, "flux/clusters/home/bad.yaml": ": [bad\n"}, nil)
	engine.script(script{match: `"kubectl","kustomize"`, leaf: "contents", value: "kind: A\n---\nkind: B"})
	engine.exitCode(`"kubeconform","-strict"`, 1)
	wantState(t, runAtom(t, "ops:flux", ""), 1, "flux/clusters/home/bad.yaml: ", "kustomize build flux/apps")

	// No CR: every flux/<dir> with a kustomization.yaml.
	opsTree(map[string]string{"flux/hemera/kustomization.yaml": "", "flux/hemera/x/kustomization.yaml": ""}, nil)
	engine.script(script{match: `"kubectl","kustomize"`, leaf: "contents", value: "kind: A\n---\nkind: B"})
	wantState(t, runAtom(t, "ops:flux", ""), 0)
	if engine.chain(`"kustomize","flux/hemera","-o"`) == "" || engine.chain(`"flux/hemera/x","-o"`) != "" {
		t.Error("the fallback builds flux/<dir> only")
	}

	opsTree(map[string]string{"flux/README.md": ""}, nil)
	engine.script(script{match: `"kubectl","kustomize"`, leaf: "contents", value: "kind: A\n---\nkind: B"})
	wantState(t, runAtom(t, "ops:flux", ""), 0, "ABSENT - the Flux tree carries no Kustomization CR and no kustomization.yaml")
	opsTree(map[string]string{"ansible/playbooks/a.yml": ""}, nil)
	engine.script(script{match: `"kubectl","kustomize"`, leaf: "contents", value: "kind: A\n---\nkind: B"})
	wantState(t, runAtom(t, "ops:flux", ""), 0, "ABSENT - no Flux tree here")
}

// Every ops atom files an engine that would not answer as could-not-run: the
// listing, and a phase's own exec.
func TestOpsAtomsCannotRunWhenTheEngineDoesNotAnswer(t *testing.T) {
	for _, id := range opsIDs {
		opsTree(map[string]string{"flux/a.yaml": ""}, nil)
		engine.fail(opsLsNeedle, "engine gone")
		wantState(t, runAtom(t, id, ""), 2, "never ran", "engine gone")

		opsTree(map[string]string{"flux/a.yaml": ""}, nil)
		engine.exitCode(opsLsNeedle, 128-1)
		wantState(t, runAtom(t, id, ""), 2, "the tracked tree could not be listed")
	}
	for id, needle := range map[string]string{
		"ops:shell": `"git","grep","-I"`, "ops:yaml": `file(path:"/src/flux/a.yaml")`,
	} {
		opsTree(map[string]string{"flux/a.yaml": "a: 1\n", "a.sh": "#!/bin/sh\n"}, nil)
		engine.stdout(`"git","grep","-I","-n","-z"`, "a.sh\x001\x00#!/bin/sh\n")
		engine.fail(needle, "engine gone")
		wantState(t, runAtom(t, id, ""), 2, "never ran")
	}
}

// The catalogue carries all eight on the fleet lane, prepush; none needs the
// foundry-stocks mount any more.
func TestOpsAtomsAreCatalogued(t *testing.T) {
	for _, id := range opsIDs {
		a := checks.AtomByID(id)
		if a.ID != id {
			t.Fatalf("%s is not in the catalogue", id)
		}
		if a.Stage != checks.StagePrecommit || a.Lane != checks.LaneAny || a.Image != checks.ImageFleet {
			t.Errorf("%s: stage=%s lane=%v image=%s", id, a.Stage, a.Lane, a.Image)
		}
	}
}

// ---- ops:kube-linter ----

func TestOpsKubeLinterBuildsItsChainAndMapsItsExitCodes(t *testing.T) {
	engine.reset()
	engine.withTree(everyLaneTree)
	wantState(t, runAtom(t, "ops:kube-linter", ""), 0)

	c := engine.chain(`"/kube-linter"`, "exitCode")
	if !strings.Contains(c, checks.ImageKubeLinter) {
		t.Errorf("ops:kube-linter must run in the kube-linter image:\n%s", c)
	}
	wantCalls(t, c,
		[]string{"withMountedDirectory", `path:"/src"`},
		[]string{"withWorkdir", `path:"/src"`},
		[]string{"withExec", `expect:ANY`, `args:["/kube-linter","lint","--fail-if-no-objects-found","flux/"]`},
	)
	if strings.Contains(c, "GATE_BASE") {
		t.Errorf("ops:kube-linter must not read GATE_BASE:\n%s", c)
	}
	// The refusal arrives on stderr and the findings on stdout, and the code
	// is the same 1 for both — so both streams are read before anything is
	// decided.
	if engine.chain(`"/kube-linter"`, "stdout") == "" || engine.chain(`"/kube-linter"`, "stderr") == "" {
		t.Error("both streams must be read before the state is decided")
	}

	// Findings: the linter's own count leads (it is the line a human reads
	// first, and kube-linter prints it last), then the findings themselves.
	engine.exitCode(`"/kube-linter"`, 1)
	engine.stdout(`"/kube-linter"`, "flux/a.yaml: (object: app apps/v1, Deployment) container \"app\" does not have a read-only root file system\n")
	engine.stderr(`"/kube-linter"`, "Error: found 1 lint errors\n")
	v := runAtom(t, "ops:kube-linter", "")
	wantState(t, v, 1, "Error: found 1 lint errors", "read-only root file system")
	if !strings.Contains(v.Reason, "Error: found 1 lint errors\nflux/a.yaml:") {
		t.Errorf("the tail must lead the head:\n%s", v.Reason)
	}

	// --fail-if-no-objects-found IS kube-linter's own zero-population refusal,
	// and it exits 1 for it — the same code it uses for a finding. Reading
	// that as findings would be wrong in the direction that still looks like
	// the check worked.
	engine.reset()
	engine.withTree(everyLaneTree)
	engine.exitCode(`"/kube-linter"`, 1)
	engine.stderr(`"/kube-linter"`, "Error: no valid objects found\n")
	wantState(t, runAtom(t, "ops:kube-linter", ""), 2, "CANNOT RUN", "parsed no object under flux/")
}

func TestOpsKubeLinterIsAbsentWithoutFluxAndRefusesAnUnreadableRoot(t *testing.T) {
	engine.reset()
	engine.withTree(templateTree([]string{"flux/x.yaml"}, nil))
	v := runAtom(t, "ops:kube-linter", "")
	wantState(t, v, 0, "no Flux tree here: neither flux/ nor clusters/<name>/kustomization.yaml")
	if v.Result != "absent" {
		t.Errorf("result %q, want absent:\n%s", v.Result, v.Reason)
	}
	wantNoContainer(t, "no Flux tree is decided in Go")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`glob(pattern:"**")`, "mount evaporated")
	wantState(t, runAtom(t, "ops:kube-linter", ""), 2, "the tree would not enumerate", "mount evaporated")

	engine.reset()
	engine.withTree(everyLaneTree)
	engine.fail(`"/kube-linter"`, "failed to resolve image")
	wantState(t, runAtom(t, "ops:kube-linter", ""), 2, "never ran", "failed to resolve image")
}

// A REPOSITORY THAT IS THE FLUX TREE (foundry/flux since 2026-09-29): its
// clusters/ and one directory per Kustomization sit at the root. ops:flux
// builds every path its CRs apply, and ops:kube-linter lints exactly those
// paths plus clusters/ — not the whole root, which carries docs and tooling.
// Both read ABSENT on this shape until 2026-10-02.
func TestOpsFluxAndKubeLinterRunOnARootFluxTree(t *testing.T) {
	cr := "apiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\nspec:\n  path: ./prime\n  sourceRef: {name: flux-system}\n"
	rootTree := map[string]string{
		"clusters/pantheon/kustomization.yaml": "resources: [prime.yaml]\n",
		"clusters/pantheon/prime.yaml":         cr,
		"prime/kustomization.yaml":             "resources: []\n",
		"docs/notes.md":                        "",
	}
	opsTree(rootTree, nil)
	engine.script(script{match: `"kubectl","kustomize"`, leaf: "contents", value: "kind: A\n---\nkind: B"})
	wantState(t, runAtom(t, "ops:flux", ""), 0)
	if engine.chain(`"kustomize","prime","-o"`) == "" {
		t.Error("the root tree's Kustomization path was not built")
	}

	// kube-linter: clusters/ and the CR paths, from EVERY cluster manifest,
	// and not a kustomization.yaml no CR applies (data/ here).
	lintTree := map[string]string{
		"clusters/pantheon/kustomization.yaml": "resources: [prime.yaml, spire.yaml]\n",
		"clusters/pantheon/prime.yaml":         cr,
		"clusters/pantheon/spire.yaml":         strings.Replace(cr, "./prime", "./spire", 1),
		"prime/kustomization.yaml":             "resources: []\n",
		"spire/kustomization.yaml":             "resources: []\n",
		"data/kustomization.yaml":              "resources: []\n",
	}
	engine.reset()
	engine.withTree(lintTree)
	wantState(t, runAtom(t, "ops:kube-linter", ""), 0)
	if engine.chain(`"/kube-linter","lint","--fail-if-no-objects-found","clusters/","prime","spire"]`) == "" {
		t.Error("kube-linter must lint clusters/ and the CR paths of every cluster manifest, nothing else")
	}

	// No CR at the root: every <dir>/kustomization.yaml but clusters/.
	engine.reset()
	engine.withTree(map[string]string{
		"clusters/pantheon/kustomization.yaml": "resources: []\n",
		"prime/kustomization.yaml":             "resources: []\n",
	})
	wantState(t, runAtom(t, "ops:kube-linter", ""), 0)
	if engine.chain(`"/kube-linter","lint","--fail-if-no-objects-found","clusters/","prime"]`) == "" {
		t.Error("with no CR, kube-linter lints the fallback trees")
	}

	// A cluster manifest that cannot be read is a CANNOT RUN, never a lint of
	// a guessed population.
	engine.reset()
	engine.withTree(rootTree)
	engine.fail(`file(path:"clusters/pantheon/prime.yaml")`, "read evaporated")
	wantState(t, runAtom(t, "ops:kube-linter", ""), 2, "CANNOT RUN - clusters/pantheon/prime.yaml could not be read", "read evaporated")
	if engine.chain(`"/kube-linter","lint"`) != "" {
		t.Error("no lint may run on an unreadable cluster manifest")
	}
}
