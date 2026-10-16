package pinsync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// replaceIn edits one fixture file.
func replaceIn(name, old, repl string) func(map[string]string) {
	return func(f map[string]string) { f[name] = strings.Replace(f[name], old, repl, 1) }
}

const (
	witlane   = "internal/checks/witlane.go"
	images    = "internal/checks/images.go"
	toolpins  = "internal/checks/toolpins.go"
	reqIn     = "pytools/requirements.in"
	reqLock   = "pytools/requirements.txt"
	toolsTOML = "tools.toml"
)

// A RUN THAT CANNOT FINISH SAYS WHY, and a failure before the write phase
// leaves every file as it was.
func TestARunThatCannotFinishSaysWhyAndWritesNothing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		start  func() map[string]string
		mutate func(map[string]string)
		web    func(*fakeWeb)
		env    func(*Env)
		tables func()
		says   string
	}{
		{name: "a url pin whose version constant does not exist", tables: func() { URLPins[0].Version = "NoSuchVersion" }, says: "no constant NoSuchVersion"},
		{name: "no internal/checks", mutate: func(f map[string]string) {
			for k := range f {
				if strings.HasPrefix(k, "internal/") {
					delete(f, k)
				}
			}
		}, says: "no such file"},
		{name: "a .go that is not a file", mutate: func(f map[string]string) { f["internal/checks/dir.go/x"] = "" }, says: "is a directory"},
		{name: "source that does not parse", mutate: func(f map[string]string) { f["internal/checks/bad.go"] = "package checks\nconst (" }, says: "parsing bad.go"},
		{name: "no tools.toml", mutate: func(f map[string]string) { delete(f, toolsTOML) }, says: "tools.toml: no such file"},
		{name: "a follower that is a join", mutate: replaceIn(witlane, `WasmToolsMember = "wasm-tools-1.261.0-x86_64-linux/wasm-tools"`, `WasmToolsMember = "wasm-tools-" + WasmToolsVersion`), says: "WasmToolsMember is not a string literal"},
		{name: "a follower whose version is gone", mutate: replaceIn(witlane, `WasmToolsVersion = "1.261.0"`, `WasmToolVersion = "1.261.0"`), says: "no constant WasmToolsVersion"},
		{name: "a version that is not a string", mutate: replaceIn(witlane, `WasmToolsVersion = "1.261.0"`, `WasmToolsVersion = 1`), says: "1 is not a string"},
		{name: "a version spelled as a call", mutate: replaceIn(witlane, `WasmToolsVersion = "1.261.0"`, `WasmToolsVersion = string("1.261.0")`), says: "*ast.CallExpr is not a string constant"},
		{name: "a version that is not one", mutate: replaceIn(witlane, `WasmToolsVersion = "1.261.0"`, `WasmToolsVersion = "latest"`), says: "WasmToolsURL: version \"latest\""},
		{name: "a constant that refers to itself", mutate: replaceIn(images, `OpengrepVersion = "v1.30.2"`, `OpengrepVersion = OpengrepVersion + "x"`), says: "refers to itself"},
		{name: "a join whose left side is unknown", mutate: replaceIn(images, `"https://github.com/opengrep/opengrep/releases/download/" + OpengrepVersion`, `Nowhere + OpengrepVersion`), says: "no constant Nowhere"},
		{name: "a url pin whose URL is gone", mutate: replaceIn(images, "OpengrepURL     =", "OpengrepURI     ="), says: "no constant OpengrepURL"},
		{name: "a url pin whose version is gone", mutate: replaceIn(images, `OpengrepVersion = "v1.30.2"`, `OpengrepVer = "v1.30.2"`), says: "no constant OpengrepVersion"},
		{name: "no ToolSHA256", mutate: replaceIn(toolpins, "var ToolSHA256 =", "var ToolSums ="), says: "no var ToolSHA256"},
		{name: "a ToolSHA256 entry that is not key: value", mutate: replaceIn(toolpins, "var ToolSHA256 = map[string]string{", "var ToolSHA256 = []string{\"x\"}\nvar _ = map[string]string{"), says: "not key: value"},
		{name: "a ToolSHA256 entry keyed by a literal", mutate: replaceIn(toolpins, "WasmToolsURL:        ", `"x": `), says: "not Const: \"literal\""},
		{name: "a url pin with no ToolSHA256 line", mutate: replaceIn(toolpins, "WasmToolsURL:        \""+wasmOldSum+"\",", ""), says: "has no WasmToolsURL line"},
		{name: "a tools.toml with no table for a url pin", mutate: replaceIn(toolsTOML, "[tool.opengrep]", "[tool.grep]"), says: "tools.toml has no [tool.opengrep] spec"},
		{name: "a ToolSHA256 value that is not a string", mutate: replaceIn(toolpins, "WasmToolsURL:        \""+wasmOldSum+"\"", "WasmToolsURL: 1"), says: "1 is not a string"},
		{name: "a tools.toml url table with no sha256", mutate: replaceIn(toolsTOML, "sha256 = \""+wasmOldSum+"\"", ""), says: "tools.toml has no [tool.wasm-tools] sha256"},
		{name: "no PythonLaneTools", mutate: replaceIn(toolpins, "var PythonLaneTools", "var PythonTools"), says: "no var PythonLaneTools"},
		{name: "a lane tool with no Version", mutate: replaceIn(toolpins, "{Name: \"ruff\", Version: RuffVersion}", "{Name: \"ruff\"}"), says: "no Version: field"},
		{name: "a lane tool whose name is not a string", mutate: replaceIn(toolpins, "{Name: \"ruff\"", "{Name: 7"), says: "PythonLaneTools: 7 is not a string"},
		{name: "a lane tool whose version is gone", mutate: replaceIn(toolpins, "Version: RuffVersion", "Version: Ruff"), says: "PythonLaneTools ruff: no constant Ruff"},
		{name: "a lane tool that is not kind uv", mutate: replaceIn(toolsTOML, "kind = \"uv\"", "kind = \"pip\""), says: "[tool.ruff] is not kind = \"uv\""},
		{name: "a lane tool table with no version", mutate: replaceIn(toolsTOML, "version = \"0.16.3\"", ""), says: "tools.toml has no [tool.ruff] version"},
		{name: "a requirements.in that is not a file", mutate: func(f map[string]string) { delete(f, reqIn); f[reqIn+"/x"] = "" }, says: "is a directory"},
		{name: "no lock", mutate: func(f map[string]string) { delete(f, reqLock) }, says: "requirements.txt: no such file"},
		{name: "a coupled pin whose constant is not a string", mutate: replaceIn(images, `const CopierVersion = "9.18.2"`, `const CopierVersion = 9`), says: "9 is not a string"},
		{name: "a lock that records no command", start: bumped, mutate: replaceIn(reqLock, "uv pip compile", "pip-compile"), says: "records no `uv pip compile`"},

		{name: "the retag cannot reach GitHub", start: bumped, env: func(e *Env) { e.GitHubAPI = "::nowhere" }, says: "PythonStandaloneURL: parse"},
		{name: "a retag of a URL that is not a GitHub release", start: bumped, mutate: func(f map[string]string) {
			for _, n := range []string{toolpins, toolsTOML} {
				f[n] = strings.ReplaceAll(f[n], "https://github.com/astral-sh/python-build-standalone/releases/download/20260623/", "https://mirror.test/20260623/")
			}
		}, says: "is not a GitHub release asset"},
		{name: "no release publishes the new python", start: bumped, web: func(w *fakeWeb) {
			for page := 1; page <= releasePages; page++ {
				w.bodies["https://api.test/repos/astral-sh/python-build-standalone/releases?per_page=5&page="+string(rune('0'+page))] = "[]"
			}
		}, says: "none of the 30 newest releases of astral-sh/python-build-standalone publishes cpython-3.14.7+20260623"},
		{name: "the release list is not JSON", start: bumped, web: func(w *fakeWeb) {
			w.bodies["https://api.test/repos/astral-sh/python-build-standalone/releases?per_page=5&page=1"] = "<html>"
		}, says: "decoding https://api.test/repos/astral-sh/python-build-standalone/releases"},
		{name: "an asset that is not there", start: bumped, web: func(w *fakeWeb) { delete(w.bodies, grepNew) }, says: "opengrep: GET " + grepNew + ": Not Found"},
		{name: "an asset host that cannot be reached", start: bumped, web: func(w *fakeWeb) { w.bodies[grepNew] = unreachable }, says: "no route to host"},
		{name: "an asset that breaks off", start: bumped, web: func(w *fakeWeb) { w.bodies[grepNew] = brokenOff }, says: "reading " + grepNew + ": connection reset"},
		{name: "a publisher whose sidecar disagrees", start: bumped, web: func(w *fakeWeb) { w.bodies[grepNew+".sha256"] = sum("tampered") }, says: grepNew + " measures " + sum("grep-new") + " but opengrep_manylinux_x86.sha256 states " + sum("tampered")},
		{name: "a publisher whose SHA256SUMS disagrees", start: bumped, web: func(w *fakeWeb) { w.bodies["https://sums/pbs"] = sum("tampered") + "  " + pyAsset + "\n" }, says: "but SHA256SUMS states"},
		{name: "GitHub's digest disagrees", start: bumped, web: func(w *fakeWeb) {
			w.bodies["https://api.test/repos/bytecodealliance/wasm-tools/releases/tags/v1.262.0"] = releaseJSON(t, release{Tag: "v1.262.0", Assets: []asset{{Name: "wasm-tools-1.262.0-x86_64-linux.tar.gz", Digest: "sha256:" + sum("tampered")}}})
		}, says: "but GitHub's digest of wasm-tools-1.262.0-x86_64-linux.tar.gz states"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixturePins(t)
			if tc.tables != nil {
				tc.tables()
			}
			start := fixtureFiles
			if tc.start != nil {
				start = tc.start
			}
			files := start()
			if tc.mutate != nil {
				tc.mutate(files)
			}
			root := writeTree(t, files)
			web := publisherWeb(t)
			if tc.web != nil {
				tc.web(web)
			}
			env, log := newEnv(root, web, &lockRun{}, true)
			if tc.env != nil {
				tc.env(env)
			}
			_, err := env.Run(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.says) {
				t.Fatalf("error %v, want one that says %q; log:\n%s", err, tc.says, log)
			}
			for name, body := range files {
				if got, err := os.ReadFile(filepath.Join(root, name)); err == nil && string(got) != body {
					t.Errorf("%s was written by a run that failed", name)
				}
			}
		})
	}
}

// THE LOCK IS REGENERATED AFTER THE WRITE, so a lock that fails leaves the
// moved requirement on disk for the next run, which regenerates again.
func TestALockThatFailsIsReportedAndRetriedByTheNextRun(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lock  *lockRun
		says  string
		retry bool // the lock it leaves still records its command
	}{
		{"uv fails", &lockRun{fail: errors.New("exit status 2")}, "regenerating the lock (uv pip compile requirements.in --generate-hashes --python-version 3.14 -o requirements.txt): exit status 2", true},
		{"uv writes a lock without the pin", &lockRun{fail: errLockWithoutPin}, "the regenerated lock does not hold copier==9.19.1", false},
		{"uv removes the lock", &lockRun{fail: errLockRemoved}, "requirements.txt: no such file", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixturePins(t)
			root := writeTree(t, bumped())
			env, _ := newEnv(root, publisherWeb(t), tc.lock, true)
			if _, err := env.Run(context.Background()); err == nil || !strings.Contains(err.Error(), tc.says) {
				t.Fatalf("error %v, want %q", err, tc.says)
			}
			if !strings.Contains(read(t, root, reqIn), "copier==9.19.1") {
				t.Error("the requirement did not move")
			}
			if !tc.retry {
				return
			}
			again := &lockRun{}
			env.RunLock = again.run
			if _, err := env.Run(context.Background()); err != nil || again.args == nil {
				t.Errorf("the next run: %v, lock ran %v", err, again.args)
			}
		})
	}
}

// A FILE THAT CANNOT BE WRITTEN is the run's error, not a silent skip. The
// file turns into a directory while the run fetches (a permission would not
// stop the gate's root).
func TestAFileThatCannotBeWrittenIsAnError(t *testing.T) {
	fixturePins(t)
	root := writeTree(t, bumped())
	web := publisherWeb(t)
	web.onAsk = func(string) {
		path := filepath.Join(root, witlane)
		if err := os.Remove(path); err == nil {
			_ = os.Mkdir(path, 0o755) // the test's own tree; the assertion below is the check
		}
	}
	env, _ := newEnv(root, web, &lockRun{}, true)
	if _, err := env.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("error %v, want is a directory", err)
	}
}

// A tree with no python lock has nothing to hold there, and a requirement no
// renovate marker names is left to uv.
func TestAPythonPinNothingCouplesIsLeftAlone(t *testing.T) {
	fixturePins(t)
	files := fixtureFiles()
	files[reqIn] = "foo==1.0\n" + files[reqIn]
	root := writeTree(t, files)
	env, _ := newEnv(root, &fakeWeb{}, &lockRun{}, true)
	if res, err := env.Run(context.Background()); err != nil || len(res.Changed) != 0 {
		t.Fatalf("changed %v, %v", res.Changed, err)
	}
	if err := os.RemoveAll(filepath.Join(root, "pytools")); err != nil {
		t.Fatal(err)
	}
	if res, err := env.Run(context.Background()); err != nil || len(res.Changed) != 0 {
		t.Fatalf("with no pytools: changed %v, %v", res.Changed, err)
	}
}

func TestExecLockRunsTheCommandInTheDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := ExecLock(context.Background(), dir, []string{"sh", "-c", "touch here"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "here")); err != nil {
		t.Errorf("the command did not run in %s: %v", dir, err)
	}
	if err := ExecLock(context.Background(), dir, []string{"sh", "-c", "exit 3"}); err == nil {
		t.Error("a command that failed was not an error")
	}
}
